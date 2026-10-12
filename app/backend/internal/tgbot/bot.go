// Package tgbot is the Telegram bot built into Umbrella. It reads updates by long polling, so
// it needs no public address, no webhook and no deployment of its own: it runs inside the
// Umbrella process with the token of the notification channel. People link their Telegram
// account from their profile and then acknowledge, resolve and comment on incidents with the
// buttons under notifications or with commands; replies to a notification become comments.
// With several Umbrella instances one of them polls (Telegram allows one reader per bot).
package tgbot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/notify"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/telegram"
)

const (
	pollTimeout = 25 * time.Second
	idlePause   = 5 * time.Second
	maxBackoff  = time.Minute
	listLimit   = 10
)

// Config is what the bot runs with.
type Config struct {
	On     bool
	Token  string
	API    string
	Locale string
}

// Person is the Umbrella user linked to a Telegram account.
type Person struct {
	ID, Username, Name string
	// CanAck: the user may acknowledge and resolve incidents; Scope limits which (nil: all).
	CanAck bool
	// CanView: the user may see incidents (the list).
	CanView bool
	Scope   []string
}

// Backend is what the bot needs of Umbrella.
type Backend interface {
	// Config reads the settings and resolves the token; an error when it cannot.
	Config() (Config, error)
	// User is the active user linked to a Telegram account.
	User(tg telegram.User) (Person, bool)
	// Link ties a Telegram account to the user a link token names; Unlink unties it.
	Link(ctx context.Context, token string, tg telegram.User) (Person, error)
	Unlink(ctx context.Context, p Person) error
	// Act acknowledges, resolves or comments on an incident in the name of the user.
	Act(ctx context.Context, p Person, id, action, text string) (alert.Alert, error)
	// Active lists the active incidents the user sees, open first.
	Active(ctx context.Context, p Person, limit int) ([]alert.Alert, error)
	// IncidentURL is the page of an incident; empty without a public address.
	IncidentURL(id string) string
}

// Lock lets one of several instances poll: it returns a release function once this instance
// holds the lock, or false while another one does.
type Lock func(ctx context.Context) (release func(), ok bool)

// Status is what the settings page shows of the bot.
type Status struct {
	// State: off, starting, polling, standby (another instance polls) or error.
	State        string     `json:"state"`
	Username     string     `json:"username,omitempty"`
	LastUpdateAt *time.Time `json:"last_update_at,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	LastErrorAt  *time.Time `json:"last_error_at,omitempty"`
	Handled      int        `json:"handled"`
}

type Bot struct {
	backend Backend
	client  *http.Client
	lock    Lock

	mu   sync.Mutex
	stat Status
}

func New(b Backend, lock Lock) *Bot {
	return &Bot{backend: b, lock: lock, client: &http.Client{Timeout: pollTimeout + 15*time.Second}, stat: Status{State: "off"}}
}

// SetHTTPClient replaces the HTTP client (tests).
func (b *Bot) SetHTTPClient(c *http.Client) { b.client = c }

func (b *Bot) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stat
}

// Username is the @name of the bot once it runs; empty before.
func (b *Bot) Username() string { return b.Status().Username }

func (b *Bot) set(f func(s *Status)) {
	b.mu.Lock()
	f(&b.stat)
	b.mu.Unlock()
}

func (b *Bot) fail(err error) {
	now := time.Now().UTC()
	b.set(func(s *Status) { s.State, s.LastError, s.LastErrorAt = "error", err.Error(), &now })
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// Run polls Telegram while the bot is on, until ctx ends. Turning the bot off or changing the
// token takes effect within one poll.
func (b *Bot) Run(ctx context.Context) {
	for ctx.Err() == nil {
		cfg, err := b.backend.Config()
		if err != nil || !cfg.On || cfg.Token == "" {
			if err != nil && cfg.On {
				b.fail(err)
			} else {
				b.set(func(s *Status) { s.State = "off" })
			}
			if !sleep(ctx, idlePause) {
				return
			}
			continue
		}
		release := func() {}
		if b.lock != nil {
			var ok bool
			if release, ok = b.lock(ctx); !ok {
				b.set(func(s *Status) { s.State = "standby" })
				if !sleep(ctx, 3*idlePause) {
					return
				}
				continue
			}
		}
		b.session(ctx, cfg)
		release()
	}
}

// session polls with one token until the settings change or ctx ends.
func (b *Bot) session(ctx context.Context, cfg Config) {
	c := telegram.New(b.client, cfg.API, cfg.Token)
	b.set(func(s *Status) { s.State = "starting" })
	me, err := c.Me(ctx)
	if err != nil {
		b.fail(err)
		sleep(ctx, idlePause)
		return
	}
	b.set(func(s *Status) { s.Username = me.Username })
	if err := c.DropWebhook(ctx); err != nil {
		slog.Warn("telegram webhook not removed", "err", err)
	}
	for _, lang := range []string{"", "ru", "en"} {
		if err := c.SetCommands(ctx, lang, commandMenu(lang)); err != nil {
			slog.Warn("telegram commands not set", "err", err)
			break
		}
	}
	offset, backoff := 0, idlePause
	for ctx.Err() == nil {
		now, err := b.backend.Config()
		if err != nil || !now.On || now.Token != cfg.Token || now.API != cfg.API {
			return
		}
		cfg = now
		b.set(func(s *Status) {
			if s.State != "error" {
				s.State = "polling"
			}
		})
		updates, err := c.Updates(ctx, offset, pollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			b.fail(err)
			wait := backoff
			var te *telegram.Error
			if errors.As(err, &te) && te.RetryAfter > 0 {
				wait = te.RetryAfter
			}
			if !sleep(ctx, wait) {
				return
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		backoff = idlePause
		b.set(func(s *Status) { s.State, s.LastError, s.LastErrorAt = "polling", "", nil })
		for _, u := range updates {
			offset = u.UpdateID + 1
			b.handle(ctx, c, cfg, u)
		}
	}
}

// handle answers one update; a panic in it does not stop the bot.
func (b *Bot) handle(ctx context.Context, c *telegram.Client, cfg Config, u telegram.Update) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("telegram update failed", "panic", r)
		}
	}()
	now := time.Now().UTC()
	b.set(func(s *Status) { s.LastUpdateAt, s.Handled = &now, s.Handled+1 })
	h := &handler{b: b, c: c, ctx: ctx, cfg: cfg}
	switch {
	case u.Callback != nil:
		h.callback(*u.Callback)
	case u.Message != nil && u.Message.From != nil && !u.Message.From.IsBot:
		h.message(*u.Message)
	}
}

// handler answers one update.
type handler struct {
	b   *Bot
	c   *telegram.Client
	ctx context.Context
	cfg Config
	w   map[string]string
}

func (h *handler) words(tg telegram.User) map[string]string {
	if h.w == nil {
		h.w = wordsFor(tg.Language, h.cfg.Locale)
	}
	return h.w
}

func (h *handler) say(chat telegram.Chat, replyTo int, text string) {
	if _, err := h.c.Send(h.ctx, telegram.Outgoing{Chat: telegram.ChatOf(chat.ID), HTML: text, ReplyTo: replyTo}); err != nil {
		slog.Warn("telegram answer not sent", "err", err)
	}
}

// actionWord is the word of the outcome of an action, or of why it failed.
func actionWord(w map[string]string, action string, err error) string {
	switch {
	case err == nil:
		return w["done."+action]
	case errors.Is(err, alert.ErrNotOpen):
		return w["err.not_open"]
	case errors.Is(err, alert.ErrNotActive):
		return w["err.not_active"]
	case errors.Is(err, alert.ErrNotFound):
		return w["err.not_found"]
	}
	return w["err.failed"]
}

// callback answers a press on a button under a notification.
func (h *handler) callback(q telegram.CallbackQuery) {
	w := h.words(q.From)
	action, id := "", ""
	switch {
	case strings.HasPrefix(q.Data, notify.CallbackAck):
		action, id = "ack", strings.TrimPrefix(q.Data, notify.CallbackAck)
	case strings.HasPrefix(q.Data, notify.CallbackResolve):
		action, id = "resolve", strings.TrimPrefix(q.Data, notify.CallbackResolve)
	default:
		_ = h.c.Answer(h.ctx, q.ID, "", false)
		return
	}
	p, ok := h.b.backend.User(q.From)
	switch {
	case !ok:
		_ = h.c.Answer(h.ctx, q.ID, w["err.unlinked"], true)
		return
	case !p.CanAck:
		_ = h.c.Answer(h.ctx, q.ID, w["err.forbidden"], true)
		return
	}
	a, err := h.b.backend.Act(h.ctx, p, id, action, "")
	_ = h.c.Answer(h.ctx, q.ID, actionWord(w, action, err), err != nil)
	if q.Message == nil {
		return
	}
	// The buttons follow the incident: resolve stays while it is acknowledged.
	status := a.Status
	switch {
	case errors.Is(err, alert.ErrNotOpen):
		status = alert.StatusAcknowledged
	case errors.Is(err, alert.ErrNotActive), errors.Is(err, alert.ErrNotFound):
		status = alert.StatusResolved
	}
	if status != "" {
		chat := telegram.ChatOf(q.Message.Chat.ID)
		_ = h.c.SetKeyboard(h.ctx, chat, q.Message.MessageID, keyboardFor(q.Message.ReplyMarkup, id, status))
	}
}

// keyboardFor keeps the link buttons of a message and the actions that still apply.
func keyboardFor(k *telegram.Keyboard, id, status string) *telegram.Keyboard {
	out := &telegram.Keyboard{}
	if k != nil {
		for _, row := range k.Rows {
			var keep []telegram.Button
			for _, btn := range row {
				switch {
				case btn.URL != "":
					keep = append(keep, btn)
				case btn.Data == notify.CallbackResolve+id && status == alert.StatusAcknowledged:
					keep = append(keep, btn)
				}
			}
			if len(keep) > 0 {
				out.Rows = append(out.Rows, keep)
			}
		}
	}
	return out
}

// incidentOf is the incident a notification is about: the data of its buttons or its links.
func incidentOf(m *telegram.Message) string {
	if m == nil || m.ReplyMarkup == nil {
		return ""
	}
	for _, row := range m.ReplyMarkup.Rows {
		for _, btn := range row {
			for _, p := range []string{notify.CallbackAck, notify.CallbackResolve} {
				if id, ok := strings.CutPrefix(btn.Data, p); ok {
					return id
				}
			}
			if _, q, ok := strings.Cut(btn.URL, "/incidents?id="); ok {
				id, _, _ := strings.Cut(q, "&")
				return id
			}
		}
	}
	return ""
}

// command splits "/ack@bot X-1 text" into ack, X-1 and text.
func command(text string) (cmd, arg, rest string) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", "", text
	}
	head, tail, _ := strings.Cut(text, " ")
	cmd, _, _ = strings.Cut(strings.TrimPrefix(head, "/"), "@")
	tail = strings.TrimSpace(tail)
	arg, rest, _ = strings.Cut(tail, " ")
	return strings.ToLower(cmd), arg, strings.TrimSpace(rest)
}

// message answers a command or a reply to a notification.
func (h *handler) message(m telegram.Message) {
	from := *m.From
	w := h.words(from)
	cmd, arg, rest := command(m.Text)
	private := m.Chat.Private()
	if cmd == "" {
		// A reply to a notification is a comment on its incident.
		if id := incidentOf(m.ReplyTo); id != "" && rest != "" {
			h.act(m, from, id, "comment", rest)
		} else if private {
			h.say(m.Chat, 0, w["help"])
		}
		return
	}
	switch cmd {
	case "start":
		if arg == "" {
			h.say(m.Chat, 0, w["welcome"]+"\n\n"+w["help"])
			return
		}
		if !private {
			h.say(m.Chat, m.MessageID, w["err.private"])
			return
		}
		p, err := h.b.backend.Link(h.ctx, arg, from)
		if err != nil {
			h.say(m.Chat, 0, w["err.link"])
			return
		}
		h.say(m.Chat, 0, format(w["linked"], "who", p.Name))
	case "help":
		h.say(m.Chat, 0, w["help"])
	case "me":
		if p, ok := h.b.backend.User(from); ok {
			h.say(m.Chat, m.MessageID, format(w["me"], "who", p.Name, "login", p.Username))
		} else {
			h.say(m.Chat, m.MessageID, w["err.unlinked"])
		}
	case "unlink":
		p, ok := h.b.backend.User(from)
		if !ok {
			h.say(m.Chat, m.MessageID, w["err.unlinked"])
			return
		}
		if err := h.b.backend.Unlink(h.ctx, p); err != nil {
			h.say(m.Chat, m.MessageID, w["err.failed"])
			return
		}
		h.say(m.Chat, m.MessageID, w["unlinked"])
	case "incidents", "list":
		h.list(m, from)
	case "ack", "resolve":
		if arg == "" {
			arg = incidentOf(m.ReplyTo)
		}
		if arg == "" {
			h.say(m.Chat, m.MessageID, w["err.no_id"])
			return
		}
		h.act(m, from, arg, cmd, "")
	case "comment":
		id, text := arg, rest
		if r := incidentOf(m.ReplyTo); r != "" && (id == "" || text == "") {
			id, text = r, strings.TrimSpace(arg+" "+rest)
		}
		if id == "" || text == "" {
			h.say(m.Chat, m.MessageID, w["err.no_comment"])
			return
		}
		h.act(m, from, id, "comment", text)
	default:
		if private {
			h.say(m.Chat, m.MessageID, w["help"])
		}
	}
}

func (h *handler) act(m telegram.Message, from telegram.User, id, action, text string) {
	w := h.words(from)
	p, ok := h.b.backend.User(from)
	switch {
	case !ok:
		h.say(m.Chat, m.MessageID, w["err.unlinked"])
		return
	case !p.CanAck:
		h.say(m.Chat, m.MessageID, w["err.forbidden"])
		return
	}
	id = incidentID(id)
	_, err := h.b.backend.Act(h.ctx, p, id, action, text)
	h.say(m.Chat, m.MessageID, html.EscapeString(id)+": "+actionWord(w, action, err))
}

func (h *handler) list(m telegram.Message, from telegram.User) {
	w := h.words(from)
	p, ok := h.b.backend.User(from)
	switch {
	case !ok:
		h.say(m.Chat, m.MessageID, w["err.unlinked"])
		return
	case !p.CanView:
		h.say(m.Chat, m.MessageID, w["err.forbidden_view"])
		return
	}
	list, err := h.b.backend.Active(h.ctx, p, listLimit)
	if err != nil {
		h.say(m.Chat, m.MessageID, w["err.failed"])
		return
	}
	if len(list) == 0 {
		h.say(m.Chat, m.MessageID, w["none"])
		return
	}
	var b strings.Builder
	b.WriteString("<b>" + w["active"] + "</b>\n")
	for _, a := range list {
		mark := "🔴"
		if a.Status == alert.StatusAcknowledged {
			mark = "🟡"
		}
		line := fmt.Sprintf("%s %s · %s · %s", mark, html.EscapeString(a.ID), html.EscapeString(w[a.Severity]), html.EscapeString(a.Title))
		if u := h.b.backend.IncidentURL(a.ID); u != "" {
			line = fmt.Sprintf("%s <a href=\"%s\">%s</a> · %s · %s", mark, html.EscapeString(u), html.EscapeString(a.ID), html.EscapeString(w[a.Severity]), html.EscapeString(a.Title))
		}
		b.WriteString("\n" + line)
	}
	b.WriteString("\n\n" + w["list.hint"])
	h.say(m.Chat, m.MessageID, b.String())
}

// incidentID is an incident number as people type it: «INC-12», «inc-12» or «12».
func incidentID(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	if _, err := strconv.Atoi(v); err == nil {
		return "INC-" + v
	}
	return v
}

// format fills {placeholders} of a word with HTML-escaped values.
func format(s string, kv ...string) string {
	for i := 0; i+1 < len(kv); i += 2 {
		s = strings.ReplaceAll(s, "{"+kv[i]+"}", html.EscapeString(kv[i+1]))
	}
	return s
}
