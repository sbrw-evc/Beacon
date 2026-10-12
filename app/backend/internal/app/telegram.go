package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/telegram"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/tgbot"
)

const (
	// tgLinkPurpose signs the start links that tie a Telegram account to a user.
	tgLinkPurpose = "telegram-link"
	tgLinkTTL     = 15 * time.Minute
	// tgLockKey is the advisory lock that lets one instance poll Telegram.
	tgLockKey = 0x75_6d_62_74_67 // "umbtg"
)

// botBackend is what the Telegram bot built into Umbrella sees of the application.
type botBackend struct{ a *App }

func (b botBackend) Config() (tgbot.Config, error) {
	var tg model.TelegramChannel
	var locale string
	b.a.deps.Store.Read(func(d *store.Data) {
		tg, locale = d.Settings.Alerting.Notify.Telegram, d.Settings.DefaultLocale
	})
	cfg := tgbot.Config{On: tg.Enabled && tg.Bot && tg.TokenRef != "", API: tg.APIURL, Locale: locale}
	if !cfg.On {
		return cfg, nil
	}
	if b.a.deps.Vault == nil {
		return cfg, errors.New("secrets are not available")
	}
	token, err := b.a.deps.Vault.Resolve(tg.TokenRef)
	cfg.Token = token
	return cfg, err
}

// telegramUser finds the active user whose Telegram chat is the private chat of tg (its ID) or
// its @name.
func telegramUser(d *store.Data, tg telegram.User) *model.User {
	id := telegram.ChatOf(tg.ID)
	var found *model.User
	for _, u := range d.Users {
		if u.Disabled || u.Telegram == "" {
			continue
		}
		if u.Telegram == id || (tg.Username != "" && strings.EqualFold(u.Telegram, "@"+tg.Username)) {
			if found == nil || u.ID < found.ID {
				found = u
			}
		}
	}
	return found
}

func (b botBackend) person(u model.User) tgbot.Person {
	perms := b.a.access.Permissions(u)
	return tgbot.Person{ID: u.ID, Username: u.Username, Name: u.Profile.DisplayName(u.Name), CanAck: perms.Has("incidents:ack"),
		CanView: perms.Has("incidents:view"), Scope: b.a.incidentScope(u)}
}

func (b botBackend) User(tg telegram.User) (tgbot.Person, bool) {
	var u *model.User
	b.a.deps.Store.Read(func(d *store.Data) {
		if found := telegramUser(d, tg); found != nil {
			cp := *found
			u = &cp
		}
	})
	if u == nil {
		return tgbot.Person{}, false
	}
	return b.person(*u), true
}

func (b botBackend) Link(ctx context.Context, token string, tg telegram.User) (tgbot.Person, error) {
	links := b.a.notifier.Links()
	if links == nil {
		return tgbot.Person{}, errors.New("links are not ready")
	}
	id, err := links.VerifyCompact(tgLinkPurpose, token, time.Now())
	if err != nil {
		return tgbot.Person{}, err
	}
	u, err := b.a.users.LinkTelegram(id, telegram.ChatOf(tg.ID))
	if err != nil {
		return tgbot.Person{}, err
	}
	if u.Disabled {
		return tgbot.Person{}, errors.New("the account is disabled")
	}
	return b.person(u), nil
}

func (b botBackend) Unlink(ctx context.Context, p tgbot.Person) error {
	_, err := b.a.users.SetTelegram(p.ID, "")
	return err
}

func (b botBackend) Act(ctx context.Context, p tgbot.Person, id, action, text string) (alert.Alert, error) {
	if !b.a.ingestReady() {
		return alert.Alert{}, errAlertsNotReady
	}
	return b.a.alerts.ActIn(ctx, id, action, p.Username, text, p.Scope)
}

func (b botBackend) Active(ctx context.Context, p tgbot.Person, limit int) ([]alert.Alert, error) {
	if !b.a.ingestReady() {
		return nil, errAlertsNotReady
	}
	page, err := b.a.alerts.List(ctx, alert.Filter{Status: "active", ScopeServiceIDs: p.Scope, Limit: limit})
	return page.Alerts, err
}

func (b botBackend) IncidentURL(id string) string {
	if pub := b.a.settings.Get().Alerting.PublicURL; pub != "" {
		return model.IncidentURL(pub, id)
	}
	return ""
}

// botLock lets one instance poll Telegram: a session-level advisory lock held on its own
// connection while it polls.
func (a *App) botLock(ctx context.Context) (func(), bool) {
	conn, err := a.deps.Backend.Pool().Acquire(ctx)
	if err != nil {
		return nil, false
	}
	var ok bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", int64(tgLockKey)).Scan(&ok); err != nil || !ok {
		conn.Release()
		return nil, false
	}
	return func() {
		if _, err := conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", int64(tgLockKey)); err != nil {
			slog.Warn("telegram lock not released", "err", err)
		}
		conn.Release()
	}, true
}

// telegramLinkView is how a user ties their Telegram account: a link that opens the bot with
// a one-time start parameter.
type telegramLinkView struct {
	Bot    string `json:"bot"`
	URL    string `json:"url"`
	Linked bool   `json:"linked"`
	// Expires is when the link stops working.
	Expires time.Time `json:"expires"`
}

func (a *App) registerTelegram(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/me/telegram-link", a.authed(a.telegramLink))
}

func (a *App) telegramLink(w http.ResponseWriter, r *http.Request) {
	links := a.notifier.Links()
	bot := ""
	if a.tgBot != nil {
		bot = a.tgBot.Username()
	}
	if links == nil || bot == "" {
		httpx.Error(w, http.StatusConflict, "bot_off", errors.New("the Telegram bot is not running"))
		return
	}
	u := current(r).user
	exp := time.Now().Add(tgLinkTTL).UTC()
	token := links.SignCompact(tgLinkPurpose, u.ID, exp)
	httpx.JSON(w, http.StatusOK, telegramLinkView{Bot: bot, URL: fmt.Sprintf("https://t.me/%s?start=%s", url.PathEscape(bot), token),
		Linked: u.Telegram != "" && isNumericChat(u.Telegram), Expires: exp})
}

func isNumericChat(v string) bool { _, err := strconv.ParseInt(v, 10, 64); return err == nil }
