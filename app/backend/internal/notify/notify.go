// Package notify is backup notification: when nobody has taken a severe enough incident
// (PagerDuty is off, or did not take it in time), the people of its route get it by e-mail and
// from a Telegram bot, team channels also in Microsoft Teams and Zoom, with a link that
// acknowledges the incident. Once the incident is
// acknowledged or resolved, the same addresses get a short follow-up.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sbrw-evc/beacon/app/backend/internal/alert"
	"github.com/sbrw-evc/beacon/app/backend/internal/model"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
)

const (
	ChannelEmail    = "email"
	ChannelTelegram = "telegram"
	ChannelTeams    = "teams"
	ChannelZoom     = "zoom"
)

// errPermanent marks a failure retrying will not fix.
type errPermanent struct{ error }

var (
	ErrDisabled = errors.New("the channel is turned off")
	// ErrBadAddress: the address is not one of the channel.
	ErrBadAddress = errors.New("the address is not valid for the channel")
	ErrNoSecret   = errors.New("secrets are not available")
)

type Resolver interface {
	Resolve(ref string) (string, error)
}

// Results records what was sent on the timeline of the incident, and that backup notification
// or a follow-up of the incident was attempted, so that it is not handed over again.
// FallbackDue is asked right before sending: an incident taken meanwhile is not sent.
type Results interface {
	Note(ctx context.Context, id, kind, code string, args map[string]string) error
	FallbackDue(ctx context.Context, id string) (bool, error)
	FallbackDone(ctx context.Context, id string, sent []alert.Notified) error
	FollowUpDone(ctx context.Context, id, event string) error
}

type nopResults struct{}

func (nopResults) Note(context.Context, string, string, string, map[string]string) error { return nil }
func (nopResults) FallbackDue(context.Context, string) (bool, error)                     { return true, nil }
func (nopResults) FallbackDone(context.Context, string, []alert.Notified) error          { return nil }
func (nopResults) FollowUpDone(context.Context, string, string) error                    { return nil }

// job is a queued notification: backup notification or the follow-up of an incident.
type job struct {
	a        alert.Alert
	followUp bool
}

func (j job) key() string {
	if j.followUp {
		return "f:" + j.a.ID
	}
	return "n:" + j.a.ID
}

type Service struct {
	st      *store.Store
	sec     Resolver
	client  *http.Client
	results Results
	queue   chan job

	// queued: incidents in the queue or being delivered; the engine hands a pending incident
	// over again until it is reported, and a second copy is not queued.
	qmu    sync.Mutex
	queued map[string]bool

	mu     sync.RWMutex
	links  *Links
	onCall OnCallSource

	Retries int
	Backoff time.Duration
	now     func() time.Time
}

func New(st *store.Store, sec Resolver) *Service {
	return &Service{st: st, sec: sec, client: &http.Client{Timeout: DefaultHTTPTimeout}, results: nopResults{},
		queue: make(chan job, DefaultQueueSize), queued: map[string]bool{}, Retries: DefaultRetries, Backoff: DefaultBackoff, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) SetResults(r Results) { s.results = r }

// OnCallSource gives the people on call for an alert (PagerDuty), who get the notifications
// of Beacon too.
type OnCallSource interface {
	OnCallPeople(a alert.Alert) []alert.Person
}

// SetOnCall adds the people on call to the recipients.
func (s *Service) SetOnCall(o OnCallSource) {
	s.mu.Lock()
	s.onCall = o
	s.mu.Unlock()
}

func (s *Service) OnCall() OnCallSource {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.onCall
}

// SetLinks turns on acknowledgement links.
func (s *Service) SetLinks(l *Links) {
	s.mu.Lock()
	s.links = l
	s.mu.Unlock()
}

func (s *Service) Links() *Links {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.links
}

// Fallback queues backup notification for an incident. The engine keeps the incident pending
// until Deliver reports it, and hands it over again later when the queue is full or the
// process restarted in between.
func (s *Service) Fallback(a alert.Alert) { s.enqueue(job{a: a}) }

// FollowUp queues the follow-up of an incident that was acknowledged or resolved
// (a.FollowUp) to the addresses backup notification reached; like Fallback, the engine hands
// it over again until DeliverFollowUp reports it.
func (s *Service) FollowUp(a alert.Alert) { s.enqueue(job{a: a, followUp: true}) }

func (s *Service) enqueue(j job) {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	if s.queued[j.key()] {
		return
	}
	select {
	case s.queue <- j:
		s.queued[j.key()] = true
	default:
		slog.Warn("backup notification queue is full: it is retried later", "alert", j.a.ID)
	}
}

func (s *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-s.queue:
			if j.followUp {
				s.DeliverFollowUp(ctx, j.a)
			} else {
				s.Deliver(ctx, j.a)
			}
			s.qmu.Lock()
			delete(s.queued, j.key())
			s.qmu.Unlock()
		}
	}
}

// target is one address a notification goes to.
type target struct {
	channel   string
	address   string
	recipient string
	tz        *time.Location
	// ref is the earlier message to this address the follow-up answers.
	ref string
}

// key identifies an address: a channel and an address are reached once.
func (t target) key() string { return t.channel + "|" + strings.ToLower(t.address) }

type config struct {
	set    model.Alerting
	locale string
	// secret is the resolved secret of each enabled channel (the SMTP password, the bot
	// token), secretErr why a channel has none it needs.
	secret    map[string]string
	secretErr map[string]error
	msgs      messages
}

// settings reads the settings of the config, without the secrets and the templates.
func (s *Service) settings() config {
	var c config
	s.st.Read(func(d *store.Data) {
		c.set = d.Settings.Alerting
		c.set.Notify.ExtraEmails = slices.Clone(d.Settings.Alerting.Notify.ExtraEmails)
		c.set.Notify.ExtraTelegram = slices.Clone(d.Settings.Alerting.Notify.ExtraTelegram)
		c.set.Notify.ExtraTeams = slices.Clone(d.Settings.Alerting.Notify.ExtraTeams)
		c.set.Notify.ExtraZoom = slices.Clone(d.Settings.Alerting.Notify.ExtraZoom)
		c.set.Notify.Templates = maps.Clone(d.Settings.Alerting.Notify.Templates)
		c.set.PagerDuty.Modes = maps.Clone(d.Settings.Alerting.PagerDuty.Modes)
		c.set.PagerDuty.Routes = nil
		c.locale = d.Settings.DefaultLocale
	})
	return c
}

func (s *Service) config() config {
	c := s.settings()
	c.msgs = newMessages(c.locale, c.set.Notify.Templates)
	c.secret, c.secretErr = map[string]string{}, map[string]error{}
	for _, ch := range channels {
		if !ch.Enabled(c.set.Notify) {
			continue
		}
		switch ref, err := ch.Secret(c.set.Notify); {
		case err != nil:
			c.secretErr[ch.Kind()] = err
		case ref != "":
			c.secret[ch.Kind()], c.secretErr[ch.Kind()] = s.resolve(ref)
		}
	}
	return c
}

func (s *Service) resolve(ref string) (string, error) {
	if s.sec == nil {
		return "", ErrNoSecret
	}
	v, err := s.sec.Resolve(ref)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoSecret, err)
	}
	return v, nil
}

// LoadTZ is the time zone of a name; UTC when it is empty or unknown.
func LoadTZ(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	if l, err := time.LoadLocation(name); err == nil {
		return l
	}
	return time.UTC
}

// zones are the time zones of messages: the default one and those of users.
type zones struct {
	def   *time.Location
	users map[string]*time.Location
}

// of is the time zone of a user (by ID); the default for anybody else.
func (z zones) of(userID string) *time.Location {
	if l := z.users[userID]; l != nil {
		return l
	}
	return z.def
}

// zones reads the default time zone and those of the users.
func (s *Service) zones(userIDs []string) zones {
	z := zones{users: map[string]*time.Location{}}
	def := ""
	names := map[string]string{}
	s.st.Read(func(d *store.Data) {
		def = d.Settings.DefaultTZ
		for _, id := range userIDs {
			if u := d.Users[id]; u != nil && u.Timezone != "" {
				names[id] = u.Timezone
			}
		}
	})
	z.def = LoadTZ(def)
	for id, n := range names {
		z.users[id] = LoadTZ(n)
	}
	return z
}

// recipients are the people of the route of an alert (the team, or the owners of the item when
// the team has nobody) and, when PagerDuty shares who is on call, those people.
func (s *Service) recipients(a alert.Alert) []alert.Person {
	people := a.Route.Recipients()
	if oc := s.OnCall(); oc != nil {
		people = append(people, oc.OnCallPeople(a)...)
	}
	return people
}

// targets: the people of the route with their contacts, the team channel, then the extra
// addresses; each address once.
func (s *Service) targets(c config, a alert.Alert) []target {
	n := c.set.Notify
	var out []target
	seen := map[string]bool{}
	add := func(t target) {
		if t.address == "" || seen[t.key()] {
			return
		}
		seen[t.key()] = true
		out = append(out, t)
	}
	people := s.recipients(a)
	ids := make([]string, 0, len(people))
	for _, p := range people {
		ids = append(ids, p.UserID)
	}
	z := s.zones(ids)
	on := enabled(n)
	for _, p := range people {
		for _, ch := range on {
			if addr := ch.Person(p); ch.Valid(addr) {
				recipient := UserRecipient(p.UserID)
				if p.UserID == "" {
					recipient = ch.Recipient(addr)
				}
				add(target{channel: ch.Kind(), address: addr, recipient: recipient, tz: z.of(p.UserID)})
			}
		}
	}
	if team := a.Route.Channel; team != nil {
		for _, ch := range on {
			if addr := ch.Team(*team); ch.Valid(addr) {
				add(target{channel: ch.Kind(), address: addr, recipient: ch.Recipient(addr), tz: z.def})
			}
		}
	}
	for _, ch := range on {
		for _, addr := range ch.Extra(n) {
			add(target{channel: ch.Kind(), address: addr, recipient: ch.Recipient(addr), tz: z.def})
		}
	}
	return out
}

// Target is an address backup notification goes to; a webhook URL is redacted.
type Target struct {
	Channel string `json:"channel"`
	Address string `json:"address"`
}

// PreviewTargets are the addresses backup notification about an incident of the route would
// go to now, by the same rules as delivery; none while every channel is off.
func (s *Service) PreviewTargets(r alert.Route) []Target {
	out := []Target{}
	for _, t := range s.targets(s.settings(), alert.Alert{Route: r}) {
		out = append(out, Target{Channel: t.channel, Address: ShowAddress(t.channel, t.address)})
	}
	return out
}

// On tells whether a channel is turned on and has what it needs to send (alert.Notifier).
func (s *Service) On() bool {
	n := s.settings().set.Notify
	return slices.ContainsFunc(channels, func(ch channel) bool { return ch.Ready(n) })
}

// On tells whether any channel is turned on.
func On(n model.Notify) bool { return len(enabled(n)) > 0 }

// enabled are the channels turned on, in their order.
func enabled(n model.Notify) []channel {
	var out []channel
	for _, ch := range channels {
		if ch.Enabled(n) {
			out = append(out, ch)
		}
	}
	return out
}

// outcome codes of one round of delivery (Deliver or DeliverFollowUp) on the timeline.
type outcome struct {
	sent, failed string
	args         map[string]string
}

// deliverAll sends a message to each target, records failures one by one and successes per
// channel on the timeline of the incident, and returns the addresses reached.
func (s *Service) deliverAll(ctx context.Context, c config, a alert.Alert, targets []target, compose func(target) composed, o outcome) []alert.Notified {
	var reached []alert.Notified
	sent := map[string][]string{}
	for _, t := range targets {
		ref, err := s.send(ctx, c, compose(t), t)
		shown := ShowAddress(t.channel, t.address)
		if err != nil {
			slog.Warn("notification not sent", "alert", a.ID, "kind", o.sent, "channel", t.channel, "to", shown, "err", err)
			args := map[string]string{"channel": t.channel, "to": shown, "error": err.Error()}
			maps.Copy(args, o.args)
			s.note(ctx, a.ID, o.failed, args)
			continue
		}
		sent[t.channel] = append(sent[t.channel], shown)
		reached = append(reached, alert.Notified{Channel: t.channel, Address: t.address, Recipient: t.recipient, Ref: ref})
	}
	for _, ch := range Channels() {
		if len(sent[ch]) > 0 {
			args := map[string]string{"channel": ch, "to": strings.Join(sent[ch], ", ")}
			maps.Copy(args, o.args)
			s.note(ctx, a.ID, o.sent, args)
		}
	}
	return reached
}

// Deliver sends backup notification for an incident, records the outcome on its timeline and
// then reports the attempt with the addresses reached. Delivery cut short by shutdown is not
// reported: the incident stays pending and is sent after the restart. An incident acknowledged
// or resolved since it was queued is not sent.
func (s *Service) Deliver(ctx context.Context, a alert.Alert) {
	due, err := s.results.FallbackDue(ctx, a.ID)
	if err != nil {
		if !errors.Is(err, alert.ErrNotFound) {
			slog.Warn("backup notification not checked: it is retried later", "alert", a.ID, "err", err)
		}
		return
	}
	if !due {
		return
	}
	c := s.config()
	targets := s.targets(c, a)
	var reached []alert.Notified
	defer func() {
		if ctx.Err() != nil {
			return
		}
		if err := s.results.FallbackDone(ctx, a.ID, reached); err != nil && !errors.Is(err, alert.ErrNotFound) {
			slog.Warn("backup notification not marked as sent", "alert", a.ID, "err", err)
		}
	}()
	if len(targets) == 0 {
		s.note(ctx, a.ID, "notify_none", nil)
		return
	}
	reached = s.deliverAll(ctx, c, a, targets, func(t target) composed { return s.compose(c, a, t) },
		outcome{sent: "notify_sent", failed: "notify_failed"})
}

func (s *Service) note(ctx context.Context, id, code string, args map[string]string) {
	if err := s.results.Note(ctx, id, alert.KindFallback, code, args); err != nil && !errors.Is(err, alert.ErrNotFound) {
		slog.Warn("backup notification not recorded", "alert", id, "err", err)
	}
}

// send delivers a message to a target with retries; it returns the reference of the message
// where the channel has one.
func (s *Service) send(ctx context.Context, c config, m composed, t target) (string, error) {
	ch := channelOf(t.channel)
	if ch == nil {
		return "", fmt.Errorf("%w: %s", ErrUnknownChannel, t.channel)
	}
	m.locale, m.replyTo = c.locale, t.ref
	attempt := func() (string, error) {
		if err := c.secretErr[t.channel]; err != nil {
			return "", errPermanent{err}
		}
		return ch.Send(ctx, s, c.set.Notify, c.secret[t.channel], t.address, m)
	}
	wait := s.Backoff
	var err error
	for i := 1; i <= max(1, s.Retries); i++ {
		var ref string
		if ref, err = attempt(); err == nil {
			return ref, nil
		}
		var perm errPermanent
		if errors.As(err, &perm) {
			return "", perm.error
		}
		if i < s.Retries {
			select {
			case <-ctx.Done():
				return "", err
			case <-time.After(wait):
			}
			wait *= 2
		}
	}
	return "", err
}

// composed is a message: the parts of its template and its links, which Teams and Telegram
// show as buttons.
type composed struct {
	subject, text, html string
	links               []link
	// incident is the incident the message is about: Telegram adds buttons that act on it.
	incident string
	// final: the incident was taken (a follow-up); no buttons act on it any more.
	final bool
	// locale and replyTo are filled in by send: the language of the buttons and the earlier
	// message to the same address the message answers.
	locale, replyTo string
}

type link struct {
	title, url string
	// ack: the link acknowledges the incident (a signed link).
	ack bool
}

// withLinks adds the links of a message in the language of the messages.
func (c composed) withLinks(locale string, m Message) composed {
	w := lang(locale)
	for _, l := range []link{{w["ack"], m.Ack, true}, {w["open"], m.Open, false}, {"Grafana", m.Grafana, false}, {w["traffic"], m.Flow, false}} {
		if l.url != "" {
			c.links = append(c.links, l)
		}
	}
	c.incident = m.ID
	return c
}

// messages of the config: the built-in templates of its language with the overrides.
func (c config) messages() messages {
	if c.msgs.builtin != nil {
		return c.msgs
	}
	return newMessages(c.locale, nil)
}

// ackURL is the signed link that acknowledges an incident in the name of a recipient; empty
// without a public address or links.
func (s *Service) ackURL(base, id, recipient string) string {
	if l := s.Links(); l != nil && base != "" {
		return base + "/ack/" + l.Sign(id, recipient, s.now().Add(LinkTTL))
	}
	return ""
}

// compose is backup notification about an incident for one address.
func (s *Service) compose(c config, a alert.Alert, t target) composed {
	m := s.incidentMessage(c, a, t.tz)
	m.Ack = s.ackURL(strings.TrimRight(c.set.PublicURL, "/"), a.ID, t.recipient)
	return c.messages().render("fallback", m).withLinks(c.locale, m)
}

// incidentMessage is what the templates see of an incident.
func (s *Service) incidentMessage(c config, a alert.Alert, tz *time.Location) Message {
	m := Message{ID: a.ID, Title: a.Title, Severity: a.Severity, CI: a.CIName, Signal: a.Signal, Team: a.Route.Team,
		Opened: formatTime(&a.OpenedAt, tz), PDState: a.PD.State, PDErrorCode: a.PD.ErrorCode, PDError: a.PD.Error,
		Event: a.FollowUp, AckedBy: a.AckedBy, AckedAt: formatTime(a.AckedAt, tz), ResolvedBy: a.ResolvedBy, ResolvedAt: formatTime(a.ResolvedAt, tz)}
	if c.set.PagerDuty.Enabled {
		m.PDMode = c.set.PagerDuty.ModeFor(a.Severity)
	}
	for _, r := range a.Route.Services {
		m.Services = append(m.Services, r.Name)
	}
	if base := strings.TrimRight(c.set.PublicURL, "/"); base != "" {
		m.Open = model.IncidentURL(base, a.ID)
		if c.set.Grafana.DashboardURL != "" && a.FollowUp == "" {
			m.Grafana = model.GrafanaHopURL(base, a.ID)
		}
		if c.set.Flow.URL != "" && a.FollowUp == "" {
			m.Flow = model.FlowHopURL(base, a.ID)
		}
	}
	return m
}

// composeFollowUp is the short message telling that the incident was taken (who and when) or
// resolved, with a link to it.
func (s *Service) composeFollowUp(c config, a alert.Alert, tz *time.Location) composed {
	m := s.incidentMessage(c, a, tz)
	out := c.messages().render("followup", m).withLinks(c.locale, m)
	out.final = true
	return out
}

// DeliverFollowUp tells the addresses backup notification reached that the incident was
// acknowledged or resolved, records the outcome on its timeline and reports the attempt.
// Addresses of a channel turned off since are skipped. A Telegram follow-up answers the first
// message, whose buttons are taken away.
func (s *Service) DeliverFollowUp(ctx context.Context, a alert.Alert) {
	if a.FollowUp == "" {
		return
	}
	c := s.config()
	defer func() {
		if ctx.Err() != nil {
			return
		}
		if err := s.results.FollowUpDone(ctx, a.ID, a.FollowUp); err != nil && !errors.Is(err, alert.ErrNotFound) {
			slog.Warn("follow-up not marked as sent", "alert", a.ID, "err", err)
		}
	}()
	var ids []string
	for _, n := range a.Notified {
		if kind, id, _ := strings.Cut(n.Recipient, ":"); kind == "u" {
			ids = append(ids, id)
		}
	}
	z := s.zones(ids)
	var targets []target
	for _, n := range a.Notified {
		if ch := channelOf(n.Channel); ch == nil || !ch.Enabled(c.set.Notify) {
			continue
		}
		_, id, _ := strings.Cut(n.Recipient, ":")
		targets = append(targets, target{channel: n.Channel, address: n.Address, recipient: n.Recipient, tz: z.of(id), ref: n.Ref})
	}
	s.deliverAll(ctx, c, a, targets, func(t target) composed { return s.composeFollowUp(c, a, t.tz) },
		outcome{sent: "notify_followup", failed: "notify_followup_failed", args: map[string]string{"event": a.FollowUp}})
}

// Test sends a test message to an address of a channel with the saved settings; the map tells
// more for the interface (the name of the Telegram bot).
func (s *Service) Test(ctx context.Context, kind, to string) (map[string]string, error) {
	ch := channelOf(kind)
	if ch == nil {
		return nil, ErrUnknownChannel
	}
	c := s.config()
	if !ch.Enabled(c.set.Notify) {
		return nil, ErrDisabled
	}
	if err := c.secretErr[kind]; err != nil {
		return nil, err
	}
	m := s.composeTest(c)
	m.locale = c.locale
	if t, ok := ch.(tester); ok {
		info, err := t.Test(ctx, s, c.set.Notify, c.secret[kind], to, m)
		return info, unwrap(err)
	}
	_, err := ch.Send(ctx, s, c.set.Notify, c.secret[kind], to, m)
	return nil, unwrap(err)
}

func (s *Service) composeTest(c config) composed {
	return c.messages().render("test", Message{})
}

// TestEmail sends a test message with the saved settings.
func (s *Service) TestEmail(ctx context.Context, to string) error {
	_, err := s.Test(ctx, ChannelEmail, to)
	return err
}

// TestTelegram sends a test message to a chat with the saved settings and returns the name of
// the bot.
func (s *Service) TestTelegram(ctx context.Context, chat string) (string, error) {
	info, err := s.Test(ctx, ChannelTelegram, chat)
	return info["bot"], err
}

func unwrap(err error) error {
	var perm errPermanent
	if errors.As(err, &perm) {
		return perm.error
	}
	return err
}
