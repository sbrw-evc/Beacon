package app

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/credentials"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/notify"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/tgbot"
)

const (
	notifySecretPath = "notify"
	maxExtraTargets  = 50
	// maxFallbackDelay bounds the wait of backup notification: an hour.
	maxFallbackDelay = 3600
)

// NotificationsService keeps the settings of backup notification; the SMTP password, the bot
// token and the Zoom verification token are kept in OpenBao. Webhook URLs of Teams and Zoom are
// settings like other addresses: administrators see them to edit them.
type NotificationsService struct {
	st      *store.Store
	secrets Secrets
	n       *notify.Service
}

func NewNotificationsService(st *store.Store, secrets Secrets, n *notify.Service) *NotificationsService {
	return &NotificationsService{st: st, secrets: secrets, n: n}
}

type NotifyView struct {
	model.Notify
	HasPassword bool `json:"has_password"`
	HasToken    bool `json:"has_token"`
	// HasZoomToken: the Zoom verification token is stored (it is never shown).
	HasZoomToken bool   `json:"has_zoom_token"`
	PublicURL    string `json:"public_url"`
	// Links: acknowledgement links are put in messages (the public address is set and the
	// signing key is ready).
	Links bool `json:"links"`
	// PDEnabled: PagerDuty is on; AutoDelaySeconds is the wait of backup notification when no
	// delay is set (DelaySeconds is null): 2 minutes with PagerDuty, none without it.
	PDEnabled        bool `json:"pd_enabled"`
	AutoDelaySeconds int  `json:"auto_delay_seconds"`
	// DefaultTemplates are the built-in message templates by name: what Templates replace.
	DefaultTemplates map[string]string `json:"default_templates"`
	// BotStatus is the state of the Telegram bot built into Umbrella.
	BotStatus *tgbot.Status `json:"bot_status,omitempty"`
}

func (s *NotificationsService) View() NotifyView {
	var n model.Notify
	var pub string
	var pdOn bool
	s.st.Read(func(d *store.Data) {
		n = d.Settings.Alerting.Notify
		n.ExtraEmails = slices.Clone(n.ExtraEmails)
		n.ExtraTelegram = slices.Clone(n.ExtraTelegram)
		n.ExtraTeams = slices.Clone(n.ExtraTeams)
		n.ExtraZoom = slices.Clone(n.ExtraZoom)
		n.Templates = maps.Clone(n.Templates)
		pub = d.Settings.Alerting.PublicURL
		pdOn = d.Settings.Alerting.PagerDuty.Enabled
	})
	auto := 0
	if pdOn {
		auto = int(alert.DefaultFallbackAfter.Seconds())
	}
	if n.ExtraEmails == nil {
		n.ExtraEmails = []string{}
	}
	if n.ExtraTelegram == nil {
		n.ExtraTelegram = []string{}
	}
	if n.ExtraTeams == nil {
		n.ExtraTeams = []string{}
	}
	if n.ExtraZoom == nil {
		n.ExtraZoom = []string{}
	}
	if n.Email.Security == "" {
		n.Email.Security = model.SMTPStartTLS
	}
	if n.MinSeverity == "" {
		n.MinSeverity = alert.DefaultFallbackSeverity
	}
	return NotifyView{Notify: n, HasPassword: n.Email.PasswordRef != "", HasToken: n.Telegram.TokenRef != "", HasZoomToken: n.Zoom.TokenRef != "", PublicURL: pub,
		Links: pub != "" && s.n.Links() != nil, PDEnabled: pdOn, AutoDelaySeconds: auto, DefaultTemplates: notify.DefaultTemplates()}
}

type EmailInput struct {
	Enabled       bool   `json:"enabled"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Security      string `json:"security"`
	SkipVerify    bool   `json:"skip_verify"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	ClearPassword bool   `json:"clear_password"`
	From          string `json:"from"`
}

type TelegramInput struct {
	Enabled bool   `json:"enabled"`
	Token   string `json:"token"`
	APIURL  string `json:"api_url"`
	// Bot turns on the bot built into Umbrella; nil keeps the saved value (older clients).
	Bot *bool `json:"bot,omitempty"`
}

type TeamsInput struct {
	Enabled bool `json:"enabled"`
}

// ZoomInput: Token is the verification token of the Incoming Webhook app; empty keeps the stored
// one.
type ZoomInput struct {
	Enabled bool   `json:"enabled"`
	Token   string `json:"token"`
}

type NotifyInput struct {
	Email         EmailInput    `json:"email"`
	Telegram      TelegramInput `json:"telegram"`
	Teams         TeamsInput    `json:"teams"`
	Zoom          ZoomInput     `json:"zoom"`
	ExtraEmails   []string      `json:"extra_emails"`
	ExtraTelegram []string      `json:"extra_telegram"`
	// ExtraTeams and ExtraZoom: webhook URLs (https).
	ExtraTeams []string `json:"extra_teams"`
	ExtraZoom  []string `json:"extra_zoom"`
	// DelaySeconds: null is automatic (2 minutes with PagerDuty, at once without it), 0 at once.
	DelaySeconds *int `json:"delay_seconds"`
	// MinSeverity: empty is error.
	MinSeverity string `json:"min_severity"`
	// Templates replace built-in message templates by name; absent (null) keeps the saved ones,
	// {} goes back to the built-in ones.
	Templates map[string]string `json:"templates,omitempty"`
}

// maxTemplateSize bounds one message template.
const maxTemplateSize = 16 << 10

// cleanTemplates checks the message templates of a form.
func cleanTemplates(in map[string]string) (map[string]string, error) {
	for n, v := range in {
		if len(v) > maxTemplateSize {
			return nil, invalid("template_invalid", fmt.Errorf("%s: longer than %d bytes", n, maxTemplateSize))
		}
	}
	out, err := notify.CleanTemplates(in)
	if err != nil {
		return nil, invalid("template_invalid", err)
	}
	return out, nil
}

func cleanList(in []string, valid func(string) bool, code string) ([]string, error) {
	out := []string{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || slices.Contains(out, v) {
			continue
		}
		if !valid(v) {
			return nil, invalid(code, fmt.Errorf("%q is not valid", v))
		}
		out = append(out, v)
	}
	if len(out) > maxExtraTargets {
		return nil, invalid("too_many_targets", nil)
	}
	return out, nil
}

func (s *NotificationsService) Save(ctx context.Context, actor string, in NotifyInput) (NotifyView, error) {
	var n model.Notify
	s.st.Read(func(d *store.Data) { n = d.Settings.Alerting.Notify })
	e := in.Email
	e.Host = strings.TrimSpace(e.Host)
	e.From = strings.TrimSpace(e.From)
	switch e.Security {
	case "", model.SMTPStartTLS, model.SMTPTLS, model.SMTPNone:
	default:
		return NotifyView{}, invalid("security_invalid", nil)
	}
	if e.Port < 0 || e.Port > 65535 {
		return NotifyView{}, invalid("port_invalid", nil)
	}
	if strings.ContainsAny(e.Host, " /\r\n") {
		return NotifyView{}, invalid("host_invalid", nil)
	}
	if e.From != "" && !validFrom(e.From) {
		return NotifyView{}, invalid("from_invalid", nil)
	}
	if e.Enabled && (e.Host == "" || e.From == "") {
		return NotifyView{}, invalid("email_incomplete", nil)
	}
	api, err := optionalURL(in.Telegram.APIURL)
	if err != nil {
		return NotifyView{}, invalid("url_invalid", err)
	}
	extraEmails, err := cleanList(in.ExtraEmails, notify.ValidEmail, "email_invalid")
	if err != nil {
		return NotifyView{}, err
	}
	extraTelegram, err := cleanList(in.ExtraTelegram, notify.ValidChat, "telegram_invalid")
	if err != nil {
		return NotifyView{}, err
	}
	extraTeams, err := cleanList(in.ExtraTeams, validWebhook, "teams_invalid")
	if err != nil {
		return NotifyView{}, err
	}
	extraZoom, err := cleanList(in.ExtraZoom, validWebhook, "zoom_invalid")
	if err != nil {
		return NotifyView{}, err
	}
	if in.DelaySeconds != nil && (*in.DelaySeconds < 0 || *in.DelaySeconds > maxFallbackDelay) {
		return NotifyView{}, invalid("delay_invalid", nil)
	}
	if in.MinSeverity != "" && alert.SeverityRank(in.MinSeverity) == 0 {
		return NotifyView{}, invalid("severity_invalid", nil)
	}
	templates := n.Templates
	if in.Templates != nil {
		if templates, err = cleanTemplates(in.Templates); err != nil {
			return NotifyView{}, err
		}
	}
	token := strings.TrimSpace(in.Telegram.Token)
	if in.Telegram.Enabled && token == "" && n.Telegram.TokenRef == "" {
		return NotifyView{}, invalid("token_required", nil)
	}
	zoomToken := strings.TrimSpace(in.Zoom.Token)
	if in.Zoom.Enabled && zoomToken == "" && n.Zoom.TokenRef == "" {
		return NotifyView{}, invalid("zoom_token_required", nil)
	}
	pwRef, tokenRef, zoomRef := n.Email.PasswordRef, n.Telegram.TokenRef, n.Zoom.TokenRef
	if in.Email.ClearPassword || strings.TrimSpace(e.Username) == "" {
		pwRef = ""
	}
	if e.Password != "" {
		if pwRef, err = putSecret(ctx, s.secrets, notifySecretPath, "smtp_password", e.Password); err != nil {
			return NotifyView{}, err
		}
	}
	if token != "" {
		if tokenRef, err = putSecret(ctx, s.secrets, notifySecretPath, "telegram_token", token); err != nil {
			return NotifyView{}, err
		}
	}
	if zoomToken != "" {
		if zoomRef, err = putSecret(ctx, s.secrets, notifySecretPath, "zoom_token", zoomToken); err != nil {
			return NotifyView{}, err
		}
	}
	n.Email = model.EmailChannel{Enabled: e.Enabled, Host: e.Host, Port: e.Port, Security: e.Security, SkipVerify: e.SkipVerify,
		Username: strings.TrimSpace(e.Username), PasswordRef: pwRef, From: e.From}
	if n.Email.Security == "" {
		n.Email.Security = model.SMTPStartTLS
	}
	bot := n.Telegram.Bot
	if in.Telegram.Bot != nil {
		bot = *in.Telegram.Bot
	}
	n.Telegram = model.TelegramChannel{Enabled: in.Telegram.Enabled, TokenRef: tokenRef, APIURL: api, Bot: bot}
	n.Teams = model.TeamsChannel{Enabled: in.Teams.Enabled}
	n.Zoom = model.ZoomChannel{Enabled: in.Zoom.Enabled, TokenRef: zoomRef}
	n.ExtraEmails, n.ExtraTelegram = extraEmails, extraTelegram
	n.ExtraTeams, n.ExtraZoom = extraTeams, extraZoom
	n.DelaySeconds, n.MinSeverity = in.DelaySeconds, in.MinSeverity
	n.Templates = templates
	now := time.Now().UTC()
	n.UpdatedAt, n.UpdatedBy = &now, actor
	detail := fmt.Sprintf("email=%v telegram=%v teams=%v zoom=%v delay=%s min_severity=%s", n.Email.Enabled, n.Telegram.Enabled, n.Teams.Enabled, n.Zoom.Enabled,
		delayText(n.DelaySeconds), n.MinSeverity)
	if len(n.Templates) > 0 {
		names := slices.Sorted(maps.Keys(n.Templates))
		detail += " templates=" + strings.Join(names, ",")
	}
	s.st.Write(func(d *store.Data) {
		d.Settings.Alerting.Notify = n
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.notify", Detail: detail})
	})
	return s.View(), nil
}

func delayText(v *int) string {
	if v == nil {
		return "auto"
	}
	return fmt.Sprintf("%ds", *v)
}

// validWebhook: an incoming webhook URL of Teams or Zoom; the API takes https only.
func validWebhook(v string) bool {
	return strings.HasPrefix(v, "https://") && notify.ValidWebhook(v)
}

// validAddress checks an address of a channel as the API takes it: webhook URLs are https.
func validAddress(kind, v string) bool {
	if kind == notify.ChannelTeams || kind == notify.ChannelZoom {
		return validWebhook(v)
	}
	return notify.ValidAddress(kind, v)
}

func validFrom(v string) bool {
	if strings.ContainsAny(v, "\r\n") {
		return false
	}
	i := strings.LastIndex(v, "<")
	if i >= 0 && strings.HasSuffix(v, ">") {
		v = v[i+1 : len(v)-1]
	}
	return notify.ValidEmail(v)
}

func (a *App) registerNotifications(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/notifications", a.authed(a.can("settings.alerting:view", a.notifyView)))
	mux.HandleFunc("PUT /api/notifications", a.authed(a.can("settings.alerting:edit", a.notifySave)))
	mux.HandleFunc("POST /api/notifications/test", a.authed(a.can("settings.alerting:test", a.notifyTest)))
	mux.HandleFunc("POST /api/notifications/preview", a.authed(a.can("settings.alerting:view", a.notifyPreview)))
	mux.HandleFunc("GET /ack/{token}", a.ackPage)
	mux.HandleFunc("POST /ack/{token}", a.ackPage)
}

func (a *App) notifyView(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, a.withBot(a.notifications.View()))
}

// withBot adds the state of the Telegram bot to the view.
func (a *App) withBot(v NotifyView) NotifyView {
	if a.tgBot != nil {
		st := a.tgBot.Status()
		v.BotStatus = &st
	}
	return v
}

func (a *App) notifySave(w http.ResponseWriter, r *http.Request) {
	var in NotifyInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.notifications.Save(r.Context(), current(r).user.Username, in)
	settingsRespond(w, a.withBot(out), err)
}

type notifyPreviewInput struct {
	// Templates: the overrides being edited; null previews the saved ones.
	Templates map[string]string `json:"templates"`
	// Locale: en or ru; empty is the default language.
	Locale string `json:"locale"`
}

// notifyPreview renders the messages of a sample incident with templates that are not saved yet.
func (a *App) notifyPreview(w http.ResponseWriter, r *http.Request) {
	var in notifyPreviewInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	set := a.settings.Get()
	templates := set.Alerting.Notify.Templates
	if in.Templates != nil {
		var err error
		if templates, err = cleanTemplates(in.Templates); err != nil {
			writeError(w, err)
			return
		}
	}
	locale := in.Locale
	if locale == "" {
		locale = set.DefaultLocale
	}
	out, err := notify.PreviewTemplates(locale, templates)
	if err != nil {
		writeError(w, invalid("template_invalid", err))
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"messages": out})
}

type notifyTestInput struct {
	Channel string `json:"channel"`
	To      string `json:"to"`
}

// notifyTest sends a test message with the saved settings, by default to the user who asks.
func (a *App) notifyTest(w http.ResponseWriter, r *http.Request) {
	var in notifyTestInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	u := current(r).user
	to := strings.TrimSpace(in.To)
	out := map[string]any{"ok": true}
	if !slices.Contains(notify.Channels(), in.Channel) {
		writeError(w, invalid("channel_invalid", nil))
		return
	}
	if to == "" {
		to = notify.UserAddress(in.Channel, u)
	}
	if !validAddress(in.Channel, to) {
		writeError(w, invalid(in.Channel+"_invalid", nil))
		return
	}
	info, err := a.notifier.Test(r.Context(), in.Channel, to)
	for k, v := range info {
		out[k] = v
	}
	shown := notify.ShowAddress(in.Channel, to)
	switch {
	case errors.Is(err, notify.ErrDisabled):
		writeError(w, invalid("channel_disabled", nil))
	case errors.Is(err, notify.ErrNoSecret):
		writeError(w, credentials.ErrUnavailable)
	case err != nil:
		httpx.Error(w, http.StatusBadGateway, "notify_failed", err)
	default:
		out["to"] = shown
		httpx.JSON(w, http.StatusOK, out)
	}
}

var ackTemplate = template.Must(template.New("ack").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex"><title>Umbrella · {{.ID}}</title>
<style>
:root{color-scheme:light dark;--bg:#f5f7fb;--card:#fff;--text:#111827;--muted:#6b7280;--accent:#2563eb;--border:#e5e7eb}
@media (prefers-color-scheme:dark){:root{--bg:#0b1020;--card:#151b2e;--text:#e5e7eb;--muted:#9ca3af;--accent:#60a5fa;--border:#273049}}
body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--bg);color:var(--text);font:15px/1.5 system-ui,sans-serif;padding:16px;box-sizing:border-box}
main{background:var(--card);border:1px solid var(--border);border-radius:14px;padding:24px;max-width:460px;width:100%;box-sizing:border-box}
h1{font-size:18px;margin:0 0 4px}p{margin:8px 0}.muted{color:var(--muted)}
button{margin-top:12px;background:var(--accent);color:#fff;border:0;border-radius:8px;padding:10px 18px;font:inherit;font-weight:600;cursor:pointer}
a{color:var(--accent)}
</style></head><body><main>
<p class="muted">Umbrella</p>
{{if .Title}}<h1>{{.ID}} · {{.Title}}</h1>{{end}}
<p>{{.Message}}</p>
{{if .Form}}<form method="post"><button type="submit">{{.Button}}</button></form>{{end}}
{{if .Open}}<p><a href="{{.Open}}">{{.OpenText}}</a></p>{{end}}
</main></body></html>`))

// ackPage acknowledges an incident from the link of a notification. GET only shows the page,
// because messengers open links to make previews; the button posts the form. Its words are
// the notification words (page.*) of the default language.
func (a *App) ackPage(w http.ResponseWriter, r *http.Request) {
	lang := "ru"
	if a.settings.Get().DefaultLocale == "en" {
		lang = "en"
	}
	words := map[string]string{}
	for _, k := range []string{"ask", "button", "done", "already", "resolved", "bad", "unavailable"} {
		words[k] = notify.Word(lang, "page."+k)
	}
	page := struct {
		Lang, ID, Title, Message, Button, Open, OpenText string
		Form                                             bool
	}{Lang: lang, Button: words["button"], OpenText: notify.Word(lang, "open")}
	render := func(status int) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_ = ackTemplate.Execute(w, page)
	}
	if a.alerts == nil || !a.ingestReady() {
		page.Message = words["unavailable"]
		render(http.StatusServiceUnavailable)
		return
	}
	id, who, err := a.notifier.Links().Verify(r.PathValue("token"), time.Now())
	if err != nil {
		page.Message = words["bad"]
		render(http.StatusForbidden)
		return
	}
	al, _, err := a.alerts.Get(r.Context(), id)
	if err != nil {
		page.Message = words["bad"]
		render(http.StatusNotFound)
		return
	}
	page.ID, page.Title = al.ID, al.Title
	if pub := a.settings.Get().Alerting.PublicURL; pub != "" {
		page.Open = model.IncidentURL(pub, al.ID)
	}
	switch {
	case al.Status == alert.StatusResolved:
		page.Message = words["resolved"]
		render(http.StatusOK)
		return
	case al.Status == alert.StatusAcknowledged:
		page.Message = words["already"]
		render(http.StatusOK)
		return
	case r.Method == http.MethodGet:
		page.Message, page.Form = words["ask"], true
		render(http.StatusOK)
		return
	}
	actor := a.linkActor(who)
	if _, err := a.alerts.Act(r.Context(), id, "ack", actor, ""); err != nil {
		if errors.Is(err, alert.ErrNotOpen) {
			page.Message = words["already"]
			render(http.StatusOK)
			return
		}
		page.Message = words["unavailable"]
		render(http.StatusInternalServerError)
		return
	}
	page.Message = words["done"]
	render(http.StatusOK)
}

// linkActor names who acknowledged by a link: the user's name, or the address the message
// went to.
func (a *App) linkActor(who string) string {
	kind, v, _ := strings.Cut(who, ":")
	switch kind {
	case "u":
		name := ""
		a.deps.Store.Read(func(d *store.Data) {
			if u := d.Users[v]; u != nil {
				name = u.Username
			}
		})
		if name != "" {
			return name
		}
	case "e":
		return v
	case "t":
		return "telegram " + v
	case "ms":
		return "teams " + v
	case "zm":
		return "zoom " + v
	}
	return "link"
}
