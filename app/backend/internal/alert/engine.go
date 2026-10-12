package alert

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/textx"
)

const (
	DefaultWindow        = 10 * time.Minute
	DefaultFallbackAfter = 2 * time.Minute
	DefaultRetryEvery    = time.Minute
	// DefaultFallbackRetry: a backup notification not reported as attempted this long after it
	// was handed to the notifier (a restart, a full queue) is handed over again.
	DefaultFallbackRetry = 5 * time.Minute
	// Retention: resolved alerts older than this are deleted with their timelines.
	Retention = 90 * 24 * time.Hour
	tickBatch = 1000
	// SendLease: a backup notification being sent is not handed to another notifier for this
	// long (a slow SMTP server, several targets), so it is not sent twice.
	SendLease = 15 * time.Minute
	// maxOldIncidents bounds PD.OldIncidents of an alert that reopens again and again.
	maxOldIncidents = 20
)

// ErrPDSkipped: the alert is below the PagerDuty severity threshold. The gateway reports it
// so the alert is not retried.
var ErrPDSkipped = errors.New("below the PagerDuty severity threshold")

// ErrPDOff: PagerDuty is turned off. The gateway reports it when it was turned off after the
// command was made; the alert is then "off", not failed.
var ErrPDOff = errors.New("PagerDuty is not enabled")

// DefaultFallbackSeverity is the lowest severity backup notification takes by default.
const DefaultFallbackSeverity = "error"

type Engine struct {
	db     *pgxpool.Pool
	st     *store.Store
	pd     Sender
	notify Notifier
	now    func() time.Time

	// Window: an alert resolved this long ago opens again on a new firing event instead of a
	// new alert, and RED and USE alerts of one service opened within it are linked.
	Window time.Duration
	// FallbackAfter: how long backup notification waits for PagerDuty to take an alert while
	// PagerDuty is on and the settings name no delay of their own (Notify.DelaySeconds).
	FallbackAfter time.Duration
	FallbackRetry time.Duration
	RetryEvery    time.Duration
	// Window, FallbackAfter and FallbackRetry are the defaults; the alerting policy of the
	// settings (model.AlertPolicy) replaces them, and Retention and TestLifetime, when it sets them.

	mu        sync.Mutex
	cached    *world
	cachedVer uint64
	purgedAt  time.Time
}

type nopSender struct{}

func (nopSender) Send(Command) {}

type nopNotifier struct{}

func (nopNotifier) Fallback(Alert) {}
func (nopNotifier) FollowUp(Alert) {}
func (nopNotifier) On() bool       { return false }

func New(db *pgxpool.Pool, st *store.Store) *Engine {
	return &Engine{db: db, st: st, pd: nopSender{}, notify: nopNotifier{}, now: func() time.Time { return time.Now().UTC() },
		Window: DefaultWindow, FallbackAfter: DefaultFallbackAfter, FallbackRetry: DefaultFallbackRetry, RetryEvery: DefaultRetryEvery}
}

func (e *Engine) SetSender(s Sender)            { e.pd = s }
func (e *Engine) SetNotifier(n Notifier)        { e.notify = n }
func (e *Engine) SetClock(now func() time.Time) { e.now = now }
func (e *Engine) DB() *pgxpool.Pool             { return e.db }

// policy is what the alerting settings say about delivery and the lifecycle right now.
type policy struct {
	pdOn bool
	// pd is the PagerDuty settings: the mode for each severity and the backup delay.
	pd model.PagerDuty
	// notifyOn: a notification channel of Umbrella is turned on.
	notifyOn bool
	// delay and minSeverity: when backup notification goes out and for which alerts; ownDelay
	// is the delay the settings name (nil: automatic).
	delay       time.Duration
	ownDelay    *time.Duration
	minSeverity string
	// window, fallbackRetry, retention and testLifetime: see model.AlertPolicy.
	window        time.Duration
	fallbackRetry time.Duration
	retention     time.Duration
	testLifetime  time.Duration
}

// orDefault is a number of units of the policy, the default when it is not set.
func orDefault(n int, unit, def, limit time.Duration) time.Duration {
	if n <= 0 {
		return def
	}
	return min(time.Duration(n)*unit, limit)
}

func (e *Engine) policy() policy {
	var p policy
	var delay *int
	var ap model.AlertPolicy
	e.st.Read(func(d *store.Data) {
		al := d.Settings.Alerting
		p.pdOn, p.minSeverity = al.PagerDuty.Enabled, al.Notify.MinSeverity
		p.pd = al.PagerDuty
		p.pd.Modes = maps.Clone(al.PagerDuty.Modes)
		p.pd.Routes = nil
		if al.Notify.DelaySeconds != nil {
			v := *al.Notify.DelaySeconds
			delay = &v
		}
		if al.Policy != nil {
			ap = *al.Policy
		}
	})
	p.window = orDefault(ap.ReopenWindowSeconds, time.Second, e.Window, MaxWindow)
	p.fallbackRetry = orDefault(ap.FallbackRetrySeconds, time.Second, e.FallbackRetry, MaxFallback)
	p.retention = orDefault(ap.RetentionDays, 24*time.Hour, Retention, MaxRetention)
	p.testLifetime = orDefault(ap.TestLifetimeSeconds, time.Second, TestLifetime, MaxTestLifetime)
	p.notifyOn = e.notify.On()
	switch {
	case delay != nil:
		p.delay = time.Duration(max(0, *delay)) * time.Second
		d := p.delay
		p.ownDelay = &d
	case p.pdOn:
		p.delay = orDefault(ap.FallbackDelaySeconds, time.Second, e.FallbackAfter, MaxFallback)
	}
	if SeverityRank(p.minSeverity) == 0 {
		p.minSeverity = DefaultFallbackSeverity
	}
	return p
}

// pdMode is the role of PagerDuty for alerts of a severity; off while PagerDuty is off.
func (p policy) pdMode(severity string) string {
	if !p.pdOn {
		return model.PDModeOff
	}
	return p.pd.ModeFor(severity)
}

// channelsFirst: the channels of Umbrella take the alert at once, without waiting for
// PagerDuty: the mode of its severity says so (backup, parallel), or keeps it from PagerDuty
// (off, while PagerDuty itself is on), or PagerDuty skipped it (below its threshold).
func (p policy) channelsFirst(a *Alert) bool {
	if !p.pdOn {
		return false
	}
	switch p.pdMode(a.Severity) {
	case model.PDModeBackup, model.PDModeParallel, model.PDModeOff:
		return true
	}
	return a.PD.State == PDSkipped
}

// standby: the alert waits for the channels of Umbrella before PagerDuty. That needs backup
// mode and channels that will take the alert; otherwise it goes to PagerDuty at once.
func (p policy) standby(a *Alert) bool {
	return p.pdMode(a.Severity) == model.PDModeBackup && p.notifyOn && SeverityRank(a.Severity) >= SeverityRank(p.minSeverity)
}

// fallbackDelay is how long an open alert waits before the channels of Umbrella.
func (p policy) fallbackDelay(a *Alert) time.Duration {
	if !p.channelsFirst(a) {
		return p.delay
	}
	if p.ownDelay != nil {
		return *p.ownDelay
	}
	return 0
}

func (e *Engine) world() *world {
	v := e.st.Version()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cached == nil || e.cachedVer != v {
		e.cached, e.cachedVer = snapshot(e.st), v
	}
	return e.cached
}

// change collects what happened to one alert in one transaction.
type change struct {
	a       *Alert
	entries []Entry
	dirty   bool
}

func (c *change) log(at time.Time, kind, code string, args map[string]string, author string) {
	c.entries = append(c.entries, Entry{At: at, Kind: kind, Code: code, Args: args, Author: author})
	c.dirty = true
}

func (c *change) save(ctx context.Context, tx pgx.Tx) error {
	if !c.dirty {
		return nil
	}
	return save(ctx, tx, c.a, c.entries)
}

func dedupKey(ci *model.ConfigItem, name, signal string) string {
	signal = strings.ToLower(strings.TrimSpace(signal))
	if ci != nil {
		return "ci:" + ci.ID + "|" + signal
	}
	return "name:" + strings.ToLower(strings.TrimSpace(name)) + "|" + signal
}

// Apply folds the events of one request into alerts inside the caller's transaction, so the
// events and the alerts are written together. after sends the PagerDuty commands and must be
// called once the transaction is committed.
func (e *Engine) Apply(ctx context.Context, tx pgx.Tx, events []Incoming) (after func(), err error) {
	if len(events) == 0 {
		return func() {}, nil
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(lockKey)); err != nil {
		return nil, err
	}
	w := e.world()
	now := e.now()
	out := &handover{}
	for _, in := range events {
		c, err := e.fold(ctx, tx, w, in, now, out)
		if err != nil {
			return nil, err
		}
		out.cmds = append(out.cmds, c...)
	}
	return out.send(e), nil
}

// Ingest folds events that do not come through the intake queue (rules) in a transaction of
// its own.
func (e *Engine) Ingest(ctx context.Context, events []Incoming) error {
	var after func()
	err := pgx.BeginFunc(ctx, e.db, func(tx pgx.Tx) error {
		var err error
		after, err = e.Apply(ctx, tx, events)
		return err
	})
	if err == nil && after != nil {
		after()
	}
	return err
}

// handover is what a transaction hands to PagerDuty and the notifier once it is committed.
type handover struct {
	cmds      []Command
	fallbacks []Alert
	followUps []Alert
}

// note remembers the backup notification and the follow-up of an alert the transaction made due.
func (h *handover) note(fallback, followUp bool, a *Alert) {
	if fallback {
		h.fallbacks = append(h.fallbacks, a.Clone())
	}
	if followUp {
		h.followUps = append(h.followUps, a.Clone())
	}
}

func (h *handover) send(e *Engine) func() {
	return func() {
		for _, c := range h.cmds {
			e.pd.Send(c)
		}
		for _, a := range h.fallbacks {
			e.notify.Fallback(a)
		}
		for _, a := range h.followUps {
			e.notify.FollowUp(a)
		}
	}
}

func (e *Engine) fold(ctx context.Context, tx pgx.Tx, w *world, in Incoming, now time.Time, out *handover) ([]Command, error) {
	ci, excluded := w.resolveCI(in.CI, in.Labels)
	name := eventCIName(in.CI, in.Labels)
	signal := in.Signal
	if strings.TrimSpace(signal) == "" {
		signal = in.Title
	}
	key := dedupKey(ci, name, signal)
	p := e.policy()
	a, err := current(ctx, tx, key, now.Add(-p.window))
	if err != nil {
		return nil, err
	}
	c := &change{a: a}
	if a == nil && ci != nil {
		// An alert opened while the item was not in the catalog is bound to it now.
		if a, err = activeByKey(ctx, tx, dedupKey(nil, name, signal)); err != nil {
			return nil, err
		}
		if a != nil {
			c.a = a
			a.DedupKey = key
			e.bind(c, w, ci, now)
		}
	}
	srcKey := in.ConnectorID + "/" + in.Key
	var cmds []Command
	add := func(cmd *Command) {
		if cmd != nil {
			cmds = append(cmds, *cmd)
		}
	}
	eventArgs := map[string]string{"connector": in.ConnectorID, "title": in.Title, "severity": in.Severity, "status": in.Status}
	if in.Value != "" {
		eventArgs["value"] = in.Value
	}

	if in.Status == SourceResolved {
		if a == nil {
			return nil, nil
		}
		src := a.Sources[srcKey]
		if src == nil || src.Status == SourceResolved {
			if src != nil {
				src.LastSeen = now
				c.dirty = true
			}
			return nil, c.save(ctx, tx)
		}
		src.Status, src.LastSeen, src.Value = SourceResolved, now, in.Value
		src.details(in)
		a.Count++
		a.LastSeen = now
		if !Active(a.Status) {
			c.dirty = true
			return nil, c.save(ctx, tx)
		}
		c.log(now, KindEvent, "event", eventArgs, "")
		a.Severity = firingSeverity(a, a.Severity)
		if allResolved(a) {
			out.note(false, e.resolve(c, now, "sources_resolved", ""), a)
			add(e.pdCmd(a, PDResolve, now))
		}
		return cmds, c.save(ctx, tx)
	}

	opened, reopened := false, false
	if a == nil {
		id, _, err := nextID(ctx, tx)
		if err != nil {
			return nil, err
		}
		a = &Alert{ID: id, DedupKey: key, Title: in.Title, CIName: name, Signal: signal, Method: in.Method, Severity: in.Severity,
			Status: StatusOpen, Sources: map[string]*Source{}, Labels: map[string]string{}, FirstSeen: now, OpenedAt: now, LastSeen: now,
			PD: PD{State: PDPending, Key: "umb-" + id}}
		if a.Method == "" {
			a.Method = model.MethodOther
		}
		a.EventCI = name
		c.a = a
		c.log(now, KindStatus, "opened", nil, "")
		if ci != nil {
			e.bind(c, w, ci, now)
		} else {
			a.Route = w.route(nil, now)
			c.log(now, KindRoute, "ci_unknown", map[string]string{"ci": name}, "")
		}
		if err := e.link(ctx, tx, c, now); err != nil {
			return nil, err
		}
		opened = true
	} else if !Active(a.Status) {
		a.reopen(now)
		c.log(now, KindStatus, "reopened", map[string]string{"window": p.window.String()}, "")
		reopened = true
	}

	src := a.Sources[srcKey]
	if src != nil && src.Status == SourceFiring && src.Severity == in.Severity && !opened && !reopened {
		// A repeated delivery: the alert is only touched.
		src.LastSeen, src.Value = now, in.Value
		src.details(in)
		a.Count++
		a.LastSeen = now
		c.dirty = true
		return nil, c.save(ctx, tx)
	}
	if src == nil {
		src = &Source{ConnectorID: in.ConnectorID, Key: in.Key, FirstSeen: now}
		a.Sources[srcKey] = src
	}
	src.Status, src.Severity, src.Title, src.Value, src.LastSeen = SourceFiring, in.Severity, in.Title, in.Value, now
	src.details(in)
	maps.Copy(a.Labels, in.Labels)
	a.Count++
	a.LastSeen = now
	c.log(now, KindEvent, "event", eventArgs, "")
	prev := a.Severity
	a.Severity = firingSeverity(a, in.Severity)
	raised := SeverityRank(a.Severity) > SeverityRank(prev)
	if raised && !opened {
		c.log(now, KindStatus, "severity_raised", map[string]string{"from": prev, "to": a.Severity}, "")
	}

	if e.exclude(c, excluded, now) {
		return nil, c.save(ctx, tx)
	}
	if m := w.maintenanceFor(a, now); m != nil {
		suppress(c, m, now)
		return nil, c.save(ctx, tx)
	}
	wasSuppressed := a.Suppressed
	unsuppress(c, now)
	if opened || reopened || raised || wasSuppressed || a.PD.State == PDSkipped || a.PD.State == PDFailed || a.PD.State == PDOff {
		before := a.PD
		if a.PD.State == PDSkipped || a.PD.State == PDOff {
			a.PD.State = PDPending
		}
		add(e.pdCmd(a, PDTrigger, now))
		e.logHold(c, before, p, now)
	}
	if e.fallbackDue(c, p, now) {
		out.note(true, false, a)
	}
	return cmds, c.save(ctx, tx)
}

// logHold records on the timeline that a trigger was held back: the alert is a test, the
// mode of its severity keeps it from PagerDuty, or PagerDuty waits in standby.
func (e *Engine) logHold(c *change, before PD, p policy, now time.Time) {
	a := c.a
	switch {
	case a.PD.State == PDStandby && before.State != PDStandby:
		after := p.pd.BackupAfter()
		c.log(now, KindPagerDuty, "pd_standby", map[string]string{"after": after.String(), "after_s": fmt.Sprint(int(after.Seconds()))}, "")
	case a.PD.State == PDSkipped && a.PD.ErrorCode != "" && (before.State != PDSkipped || before.ErrorCode != a.PD.ErrorCode):
		c.log(now, KindPagerDuty, "pd_skipped", map[string]string{"code": a.PD.ErrorCode}, "")
	}
}

// suppress puts an alert under a maintenance window: no trigger goes to PagerDuty (one waiting
// or failed waits for the end of the window) and no backup notification.
func suppress(c *change, m *model.Maintenance, now time.Time) {
	a := c.a
	if !a.Suppressed || a.MaintenanceID != m.ID {
		c.log(now, KindMaintenance, "suppressed", map[string]string{"window": m.Title, "id": m.ID}, "")
	}
	a.Suppressed, a.MaintenanceID = true, m.ID
	if a.PD.State == PDPending || a.PD.State == PDFailed {
		a.PD.State, a.PD.Retry = PDSkipped, ""
	}
	c.dirty = true
}

// unsuppress ends the maintenance window of an alert: a trigger held back is sent again.
func unsuppress(c *change, now time.Time) {
	a := c.a
	if !a.Suppressed || a.Excluded {
		return
	}
	a.Suppressed, a.MaintenanceID = false, ""
	t := now
	a.UnsuppressedAt = &t
	if a.PD.State == PDSkipped && a.PD.ErrorCode == "" {
		a.PD.State, a.PD.AttemptAt = PDPending, nil
	}
	c.log(now, KindMaintenance, "maintenance_over", nil, "")
}

// reopen opens a resolved alert again: acknowledgement, backup notification and the PagerDuty
// delivery start afresh.
func (a *Alert) reopen(now time.Time) {
	a.Status, a.ResolvedAt, a.ResolvedBy, a.AckedBy, a.AckedAt = StatusOpen, nil, "", "", nil
	a.OpenedAt, a.Fallback, a.FallbackAt, a.FallbackState, a.FallbackTry = now, false, nil, "", nil
	a.UnsuppressedAt = nil
	a.Notified, a.FollowUp, a.FollowUpTry = nil, "", nil
	a.PD.State, a.PD.Error, a.PD.ErrorCode, a.PD.Retry, a.PD.AttemptAt, a.PD.Escalated = PDPending, "", "", "", nil, false
	// PagerDuty opens a new incident for the trigger after a resolve: it goes by the current
	// route, and the old incident is remembered so that its late webhooks are ignored.
	a.PD.Route, a.PD.RouteID, a.PD.Queue, a.PD.QueueName = "", "", "", ""
	if a.PD.IncidentID != "" && !slices.Contains(a.PD.OldIncidents, a.PD.IncidentID) {
		a.PD.OldIncidents = append(a.PD.OldIncidents, a.PD.IncidentID)
		if len(a.PD.OldIncidents) > maxOldIncidents {
			a.PD.OldIncidents = a.PD.OldIncidents[len(a.PD.OldIncidents)-maxOldIncidents:]
		}
	}
	a.PD.IncidentID, a.PD.IncidentURL = "", ""
}

// firingSeverity is the highest severity of the sources that still fire.
func firingSeverity(a *Alert, fallback string) string {
	best := ""
	for _, s := range a.Sources {
		if s.Status == SourceFiring && SeverityRank(s.Severity) > SeverityRank(best) {
			best = s.Severity
		}
	}
	if best == "" {
		return fallback
	}
	return best
}

func allResolved(a *Alert) bool {
	for _, s := range a.Sources {
		if s.Status != SourceResolved {
			return false
		}
	}
	return true
}

// bind attaches the alert to a configuration item and routes it.
func (e *Engine) bind(c *change, w *world, ci *model.ConfigItem, now time.Time) {
	a := c.a
	a.CIID, a.CIName, a.CIKind = ci.ID, ci.Name, ci.Kind
	if len(a.Sources) > 0 {
		c.log(now, KindRoute, "ci_bound", map[string]string{"ci": ci.Name, "ci_id": ci.ID}, "")
	}
	e.reroute(c, w, ci, now)
}

func (e *Engine) reroute(c *change, w *world, ci *model.ConfigItem, now time.Time) {
	a := c.a
	r := w.route(ci, now)
	a.Route = r
	args := map[string]string{"via": r.Via, "people": fmt.Sprint(len(r.Recipients()))}
	if len(r.Services) > 0 {
		names := make([]string, 0, len(r.Services))
		for _, s := range r.Services {
			names = append(names, s.Name)
		}
		args["services"] = strings.Join(names, ", ")
	}
	if r.Team != nil {
		args["team"] = r.Team.Name
	}
	c.log(now, KindRoute, "routed", args, "")
}

// link marks RED and USE alerts of the same service opened within the window: the USE alert
// is the probable cause of the RED one.
func (e *Engine) link(ctx context.Context, tx pgx.Tx, c *change, now time.Time) error {
	a := c.a
	other := model.MethodCounterpart(a.Method)
	if len(a.Route.Services) == 0 || other == "" {
		return nil
	}
	o, err := scanAlert(tx.QueryRow(ctx, `SELECT doc FROM alerts WHERE `+sqlActive+` AND method = $1 AND service_ids && $2
		AND first_seen >= $3 AND id <> $4 ORDER BY first_seen DESC LIMIT 1 FOR UPDATE`, other, a.Route.ServiceIDs(), now.Add(-e.policy().window), a.ID))
	if err != nil || o == nil {
		return err
	}
	oc := &change{a: o}
	a.RelatedID, o.RelatedID = o.ID, a.ID
	red, use, redC, useC := a, o, c, oc
	if a.Method == model.MethodUSE {
		red, use, redC, useC = o, a, oc, c
	}
	redC.log(now, KindStatus, "probable_cause", map[string]string{"alert": use.ID, "ci": use.CIName}, "")
	useC.log(now, KindStatus, "affects_service", map[string]string{"alert": red.ID, "ci": red.CIName}, "")
	return oc.save(ctx, tx)
}

func (e *Engine) resolve(c *change, now time.Time, why, actor string) (followUp bool) {
	a := c.a
	a.Status = StatusResolved
	t := now
	a.ResolvedAt, a.ResolvedBy = &t, actor
	c.log(now, KindStatus, "resolved", map[string]string{"why": why}, actor)
	return e.taken(c, now)
}

// acknowledge marks the alert taken by actor.
func (e *Engine) acknowledge(c *change, now time.Time, actor string) (followUp bool) {
	a := c.a
	t := now
	a.Status, a.AckedBy, a.AckedAt = StatusAcknowledged, actor, &t
	c.log(now, KindStatus, "acknowledged", nil, actor)
	return e.taken(c, now)
}

// taken is called when the alert stops being open (acknowledged or resolved). Backup
// notification still waiting is not sent any more; the addresses it reached get a follow-up.
// It reports whether a follow-up is to be handed to the notifier now.
func (e *Engine) taken(c *change, now time.Time) bool {
	a := c.a
	if a.FallbackState == FallbackPending {
		a.Fallback, a.FallbackAt, a.FallbackState, a.FallbackTry = false, nil, "", nil
		c.log(now, KindFallback, "fallback_cancelled", map[string]string{"status": a.Status}, "")
	}
	if a.FallbackState != FallbackSent || len(a.Notified) == 0 {
		return false
	}
	a.FollowUp = a.Status
	t := now
	a.FollowUpTry = &t
	return true
}

// fallbackFrom is when the wait for PagerDuty started: the opening, or the end of the last
// maintenance window over the alert, so PagerDuty gets its full time after a window too.
func (a *Alert) fallbackFrom() time.Time {
	if a.UnsuppressedAt != nil && a.UnsuppressedAt.After(a.OpenedAt) {
		return *a.UnsuppressedAt
	}
	return a.OpenedAt
}

// fallbackDue starts backup notification of an open alert nobody has taken: PagerDuty is off,
// did not take it in time, or skipped it (below its threshold), and the alert is severe enough.
// Acknowledged and resolved alerts, alerts in maintenance and test alerts never go out. It
// reports whether the notification is to be handed to the notifier now.
func (e *Engine) fallbackDue(c *change, p policy, now time.Time) bool {
	a := c.a
	first := p.channelsFirst(a)
	delay := p.fallbackDelay(a)
	switch {
	case a.Fallback, a.Status != StatusOpen, a.Suppressed, a.IsTest(),
		SeverityRank(a.Severity) < SeverityRank(p.minSeverity),
		!first && (pdHas(a) || (p.pdOn && a.PD.State == PDOff)),
		now.Sub(a.fallbackFrom()) < delay:
		return false
	}
	reason := "pd_not_taken"
	switch m := p.pdMode(a.Severity); {
	case a.PD.State == PDOff:
		reason = "pd_off"
	case a.PD.State == PDSkipped:
		reason = "pd_skipped"
	case m == model.PDModeBackup, m == model.PDModeParallel:
		reason = "pd_" + m
	}
	t := now
	a.Fallback, a.FallbackAt, a.FallbackState, a.FallbackTry = true, &t, FallbackPending, &t
	c.log(now, KindFallback, "fallback", map[string]string{"after": delay.String(), "after_s": fmt.Sprint(int(delay.Seconds())),
		"reason": reason, "people": fmt.Sprint(len(a.Route.Recipients()))}, "")
	return true
}

// pdCmd makes a PagerDuty command and stamps the attempt. A maintenance window holds back
// triggers only: an incident PagerDuty already has is still acknowledged and resolved there,
// otherwise it would stay open and keep escalating. A window that starts while the incident is
// open in PagerDuty leaves it open there (nothing is resolved just because work began); the
// acknowledgement or resolution made during the window is what closes it.
//
// While PagerDuty is off nothing is sent: an alert it does not have is "off", and the
// acknowledgement or resolution of an incident it has waits in Retry until it is turned on
// again. A test alert is never sent.
func (e *Engine) pdCmd(a *Alert, action Action, now time.Time) *Command {
	if a.Suppressed && (action == PDTrigger || !pdHas(a)) {
		return nil
	}
	if action != PDTrigger && a.PD.State == PDStandby {
		// PagerDuty never got the alert: there is nothing to acknowledge or resolve there.
		return nil
	}
	if a.IsTest() {
		// A test event never reaches PagerDuty.
		if !pdHas(a) {
			a.PD.State, a.PD.Error, a.PD.ErrorCode, a.PD.Retry = PDSkipped, "", "test", ""
		}
		return nil
	}
	p := e.policy()
	if !p.pdOn {
		if !pdHas(a) {
			a.PD.State, a.PD.Error, a.PD.ErrorCode, a.PD.Retry = PDOff, "", "", ""
		} else if action != PDTrigger {
			a.PD.Retry = string(action)
		}
		return nil
	}
	if action == PDTrigger && !pdHas(a) && !a.PD.Escalated {
		switch {
		case p.pdMode(a.Severity) == model.PDModeOff:
			a.PD.State, a.PD.Error, a.PD.ErrorCode, a.PD.Retry = PDSkipped, "", "mode_off", ""
			return nil
		case p.standby(a):
			a.PD.State, a.PD.Error, a.PD.ErrorCode, a.PD.Retry = PDStandby, "", "", ""
			return nil
		}
	}
	t := now
	a.PD.AttemptAt = &t
	return &Command{Action: action, Alert: a.Clone()}
}

var (
	ErrNotActive    = errors.New("the alert is already resolved")
	ErrNotOpen      = errors.New("only an open alert can be acknowledged")
	ErrEmptyComment = errors.New("the comment is empty")
	ErrBadAction    = errors.New("unknown action")
)

// Act applies an action of a person: ack, resolve or comment.
func (e *Engine) Act(ctx context.Context, id, action, actor, text string) (Alert, error) {
	return e.ActIn(ctx, id, action, actor, text, nil)
}

// ActIn is Act for a person who sees only the alerts of some business services (see
// Alert.InScope): any other alert is ErrNotFound.
func (e *Engine) ActIn(ctx context.Context, id, action, actor, text string, scope []string) (Alert, error) {
	now := e.now()
	var out Alert
	var cmd *Command
	var followUp bool
	err := pgx.BeginFunc(ctx, e.db, func(tx pgx.Tx) error {
		followUp = false
		a, err := lockByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if a == nil || !a.InScope(scope) {
			return ErrNotFound
		}
		c := &change{a: a}
		switch action {
		case "ack":
			if a.Status != StatusOpen {
				return ErrNotOpen
			}
			followUp = e.acknowledge(c, now, actor)
			cmd = e.pdCmd(a, PDAcknowledge, now)
		case "resolve":
			if !Active(a.Status) {
				return ErrNotActive
			}
			followUp = e.resolve(c, now, "manual", actor)
			cmd = e.pdCmd(a, PDResolve, now)
		case "comment":
			text = strings.TrimSpace(text)
			if text == "" {
				return ErrEmptyComment
			}
			// The limit counts characters, not bytes, so a cut never splits one.
			text = textx.Runes(text, 4000)
			c.log(now, KindComment, "comment", map[string]string{"text": text}, actor)
			if pdHas(a) {
				cmd = &Command{Action: PDNote, Alert: a.Clone(), Text: text, Actor: actor}
			}
		default:
			return ErrBadAction
		}
		out = a.Clone()
		return c.save(ctx, tx)
	})
	if err != nil {
		return out, err
	}
	if cmd != nil {
		e.pd.Send(*cmd)
	}
	if followUp {
		e.notify.FollowUp(out)
	}
	return out, nil
}

// PDResult records the outcome of a delivery to PagerDuty: route is the name of the PagerDuty
// route used and routeID its ID.
func (e *Engine) PDResult(ctx context.Context, alertID string, action Action, route, routeID string, deliveryErr error) {
	now := e.now()
	var follow []Command
	err := pgx.BeginFunc(ctx, e.db, func(tx pgx.Tx) error {
		a, err := lockByID(ctx, tx, alertID)
		if err != nil || a == nil {
			return err
		}
		c := &change{a: a, dirty: true}
		if route != "" {
			a.PD.Route = route
		}
		delivered := a.PD.State == PDAccepted || a.PD.State == PDAcked
		switch {
		case errors.Is(deliveryErr, ErrPDOff):
			// Turned off after the command was made: not a failure, nothing on the timeline.
			a.PD.Error, a.PD.ErrorCode = "", ""
			if !delivered {
				a.PD.State, a.PD.Retry = PDOff, ""
			} else if action != PDTrigger {
				a.PD.Retry = string(action)
			}
		case errors.Is(deliveryErr, ErrPDSkipped):
			if action == PDTrigger && !delivered {
				code, min := DeliveryCode(deliveryErr)
				if code == "error" {
					code, min = "below_threshold", ""
				}
				a.PD.State = PDSkipped
				c.log(now, KindPagerDuty, "pd_skipped", map[string]string{"code": code, "min": min}, "")
			}
			a.PD.Retry, a.PD.Error, a.PD.ErrorCode = "", "", ""
		case deliveryErr != nil:
			code, detail := DeliveryCode(deliveryErr)
			if a.PD.Error != deliveryErr.Error() {
				c.log(now, KindPagerDuty, "pd_failed", map[string]string{"action": string(action), "code": code, "detail": detail}, "")
			}
			a.PD.Error, a.PD.ErrorCode, a.PD.Retry = deliveryErr.Error(), code, string(action)
			if action == PDTrigger && !delivered {
				a.PD.State = PDFailed
			}
		default:
			a.PD.Error, a.PD.ErrorCode, a.PD.Retry = "", "", ""
			switch action {
			case PDTrigger:
				if a.PD.State != PDAcked {
					a.PD.State = PDAccepted
				}
				if routeID != "" {
					a.PD.RouteID = routeID
				}
				switch a.Status {
				case StatusAcknowledged:
					if cmd := e.pdCmd(a, PDAcknowledge, now); cmd != nil {
						follow = append(follow, *cmd)
					}
				case StatusResolved:
					if cmd := e.pdCmd(a, PDResolve, now); cmd != nil {
						follow = append(follow, *cmd)
					}
				}
			case PDAcknowledge:
				a.PD.State = PDAcked
			}
			c.log(now, KindPagerDuty, "pd_accepted", map[string]string{"action": string(action), "key": a.PD.Key, "route": route}, "")
		}
		return c.save(ctx, tx)
	})
	if err != nil {
		slog.Error("pagerduty result not recorded", "alert", alertID, "err", err)
		return
	}
	for _, c := range follow {
		e.pd.Send(c)
	}
}

// PDUpdate is a change of a PagerDuty incident received by webhook.
type PDUpdate struct {
	DedupKey    string
	EventType   string
	Actor       string
	IncidentID  string
	IncidentURL string
	Detail      string
	// OccurredAt is when the change happened in PagerDuty; zero when unknown.
	OccurredAt time.Time
	// Queue and QueueName are the PagerDuty service the incident is in; empty when unknown.
	Queue, QueueName string
}

// PDKeys returns the dedup keys of the alerts bound to a PagerDuty incident or key.
func (e *Engine) PDKeys(ctx context.Context, incidentKey, incidentID string) ([]string, error) {
	rows, err := e.db.Query(ctx, "SELECT pd_key FROM alerts WHERE ($1 <> '' AND pd_key = $1) OR ($2 <> '' AND pd_incident = $2)", incidentKey, incidentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// PDActive lists active alerts PagerDuty has an incident for, oldest first.
func (e *Engine) PDActive(ctx context.Context, limit int) ([]Alert, error) {
	rows, err := e.db.Query(ctx, "SELECT doc FROM alerts WHERE "+sqlActive+" AND pd_state IN ($1, $2) ORDER BY seq LIMIT $3", PDAccepted, PDAcked, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// ActiveOnCIs lists the active alerts bound to configuration items, newest first, for the
// health of the items.
func (e *Engine) ActiveOnCIs(ctx context.Context, limit int) ([]Alert, error) {
	rows, err := e.db.Query(ctx, "SELECT doc FROM alerts WHERE "+sqlActive+" AND ci_id <> '' ORDER BY last_seen DESC LIMIT $1", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// PDInbound applies a status change made in PagerDuty: acknowledging or resolving the
// incident there acknowledges or resolves the alert here.
func (e *Engine) PDInbound(ctx context.Context, u PDUpdate) error {
	now := e.now()
	var followUp *Alert
	err := pgx.BeginFunc(ctx, e.db, func(tx pgx.Tx) error {
		followUp = nil
		a, err := scanAlert(tx.QueryRow(ctx, "SELECT doc FROM alerts WHERE pd_key = $1 ORDER BY last_seen DESC LIMIT 1 FOR UPDATE", u.DedupKey))
		if err != nil {
			return err
		}
		if a == nil {
			return ErrNotFound
		}
		c := &change{a: a, dirty: true}
		// A late webhook about the incident of an earlier opening must not acknowledge or resolve
		// the reopened alert: PagerDuty opened a new incident for it. Such an event is known by
		// its incident (one of the old ones) or by its time (before the alert opened again). An
		// incident ID that is merely new is taken: incidents merged in PagerDuty move the alert
		// to another incident.
		if (u.IncidentID != "" && slices.Contains(a.PD.OldIncidents, u.IncidentID)) ||
			(!u.OccurredAt.IsZero() && u.OccurredAt.Before(a.OpenedAt)) {
			c.log(now, KindPagerDuty, "pd_stale", map[string]string{"event": u.EventType, "incident": u.IncidentID}, u.Actor)
			return c.save(ctx, tx)
		}
		if u.IncidentID != "" {
			a.PD.IncidentID = u.IncidentID
		}
		if u.IncidentURL != "" {
			a.PD.IncidentURL = u.IncidentURL
		}
		if u.Queue != "" && u.Queue != a.PD.Queue {
			if a.PD.Queue != "" {
				c.log(now, KindPagerDuty, "pd_queue_moved", map[string]string{"from": cmp.Or(a.PD.QueueName, a.PD.Queue), "to": cmp.Or(u.QueueName, u.Queue)}, u.Actor)
			}
			a.PD.Queue, a.PD.QueueName = u.Queue, u.QueueName
		}
		args := map[string]string{}
		if u.Detail != "" {
			args["detail"] = u.Detail
		}
		add := func(code string) { c.log(now, KindPagerDuty, code, args, u.Actor) }
		switch u.EventType {
		case "":
			// Only the queue or the incident changed (read back).
		case "incident.triggered":
			if a.PD.State == PDPending || a.PD.State == PDFailed {
				a.PD.State = PDAccepted
			}
			add("pd_incident_triggered")
		case "incident.acknowledged":
			taken := false
			if a.Status == StatusOpen {
				taken = e.acknowledge(c, now, u.Actor)
			}
			a.PD.State = PDAcked
			add("pd_incident_acknowledged")
			if taken {
				cp := a.Clone()
				followUp = &cp
			}
		case "incident.resolved":
			if Active(a.Status) && e.resolve(c, now, "pagerduty", u.Actor) {
				cp := a.Clone()
				followUp = &cp
			}
		case "incident.unacknowledged", "incident.reopened":
			switch a.Status {
			case StatusAcknowledged:
				a.Status, a.AckedBy, a.AckedAt = StatusOpen, "", nil
			case StatusResolved:
				// The alert stays resolved here: the incident is resolved in PagerDuty again
				// (Tick retries the resolve), so it does not stay open there for ever.
				a.PD.Retry = string(PDResolve)
			}
			a.PD.State = PDAccepted
			add("pd_incident_reopened")
		case "incident.reassigned", "incident.escalated", "incident.annotated", "incident.priority_updated":
			add("pd_" + strings.TrimPrefix(u.EventType, "incident."))
		default:
			args["event"] = u.EventType
			add("pd_other")
		}
		return c.save(ctx, tx)
	})
	if err == nil && followUp != nil {
		e.notify.FollowUp(*followUp)
	}
	return err
}

// Tick runs once in a while: backup notification for alerts PagerDuty has not taken, the
// start and end of maintenance windows, retries of failed deliveries and cleanup.
func (e *Engine) Tick(ctx context.Context) error {
	now := e.now()
	rows, err := e.db.Query(ctx, "SELECT id FROM alerts WHERE attention ORDER BY seq LIMIT $1", tickBatch)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	w := e.world()
	p := e.policy()
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var out handover
		err := pgx.BeginFunc(ctx, e.db, func(tx pgx.Tx) error {
			out = handover{}
			a, err := lockByID(ctx, tx, id)
			if err != nil || a == nil {
				return err
			}
			c := &change{a: a}
			pdBefore := a.PD.State + "|" + a.PD.Retry
			if Active(a.Status) {
				m := w.maintenanceFor(a, now)
				switch {
				case m != nil && !a.Suppressed:
					suppress(c, m, now)
				case m == nil && a.Suppressed && !a.Excluded:
					unsuppress(c, now)
				}
				switch {
				case e.fallbackDue(c, p, now):
					out.note(true, false, a)
				case a.FallbackState == FallbackPending && a.Status != StatusOpen:
					// Acknowledged in between (an older version did not cancel it then).
					e.taken(c, now)
				case (a.FallbackState == FallbackPending || a.FallbackState == FallbackSending) && !a.Suppressed &&
					(a.FallbackTry == nil || now.Sub(*a.FallbackTry) >= p.fallbackRetry):
					// The pending state is saved with the hand-over, so a notification lost with
					// the process or dropped by a full queue goes out on a later tick.
					t := now
					a.FallbackTry = &t
					c.dirty = true
					out.note(true, false, a)
				}
				if a.PD.State == PDStandby && !p.pdOn {
					a.PD.State = PDOff
				}
				if reason := e.escalationDue(a, p, now); reason != "" {
					if cmd := e.escalate(c, reason, "", now); cmd != nil {
						out.cmds = append(out.cmds, *cmd)
					}
				}
			}
			if a.FollowUp != "" && (a.FollowUpTry == nil || now.Sub(*a.FollowUpTry) >= p.fallbackRetry) {
				t := now
				a.FollowUpTry = &t
				c.dirty = true
				out.note(false, true, a)
			}
			before := a.PD
			if cmd := e.retry(a, now); cmd != nil {
				c.dirty = true
				out.cmds = append(out.cmds, *cmd)
			}
			e.logHold(c, before, p, now)
			if a.PD.State+"|"+a.PD.Retry != pdBefore {
				c.dirty = true
			}
			if a.Status == StatusResolved && a.PD.Retry == "" {
				c.dirty = true // clears the attention flag
			}
			return c.save(ctx, tx)
		})
		if err != nil {
			slog.Error("alert tick", "alert", id, "err", err)
			continue
		}
		out.send(e)()
	}
	if err := e.expireTests(ctx, now, p.testLifetime); err != nil && ctx.Err() == nil {
		slog.Error("test alerts not resolved", "err", err)
	}
	if now.Sub(e.purgedAt) >= time.Hour {
		e.purgedAt = now
		if _, err := e.db.Exec(ctx, "DELETE FROM alerts WHERE status = '"+StatusResolved+"' AND resolved_at < $1", now.Add(-p.retention)); err != nil {
			slog.Error("old alerts not deleted", "err", err)
		}
	}
	return nil
}

// escalationDue tells why an alert in standby goes to PagerDuty now: nobody took it within
// the backup delay, backup notification reached nobody, or the settings no longer keep it in
// standby. Empty: it stays.
func (e *Engine) escalationDue(a *Alert, p policy, now time.Time) string {
	switch {
	case a.PD.State != PDStandby, a.Status != StatusOpen, a.Suppressed:
		return ""
	case !p.standby(a):
		return "settings"
	case a.FallbackState == FallbackSent && len(a.Notified) == 0:
		return "nobody_reached"
	case now.Sub(a.OpenedAt) >= p.pd.BackupAfter():
		return "not_taken"
	}
	return ""
}

// escalate sends an alert PagerDuty does not have yet there, whatever the mode says.
func (e *Engine) escalate(c *change, reason, actor string, now time.Time) *Command {
	a := c.a
	a.PD.Escalated = true
	a.PD.State, a.PD.Error, a.PD.ErrorCode, a.PD.Retry = PDPending, "", "", ""
	c.log(now, KindPagerDuty, "pd_handover", map[string]string{"reason": reason}, actor)
	return e.pdCmd(a, PDTrigger, now)
}

// ErrPDHas: PagerDuty already has the incident.
var ErrPDHas = errors.New("PagerDuty already has the incident")

// EscalatePD sends an active alert to PagerDuty now, whatever the mode of its severity says
// (a person asked for it, or an escalation step of incident response). scope as in ActIn.
func (e *Engine) EscalatePD(ctx context.Context, id, reason, actor string, scope []string) (Alert, error) {
	now := e.now()
	var out Alert
	var cmd *Command
	err := pgx.BeginFunc(ctx, e.db, func(tx pgx.Tx) error {
		cmd = nil
		a, err := lockByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if a == nil || !a.InScope(scope) {
			return ErrNotFound
		}
		switch {
		case !Active(a.Status):
			return ErrNotActive
		case pdHas(a) || (a.PD.Escalated && a.PD.State == PDPending):
			return ErrPDHas
		}
		c := &change{a: a}
		cmd = e.escalate(c, reason, actor, now)
		out = a.Clone()
		return c.save(ctx, tx)
	})
	if err == nil && cmd != nil {
		e.pd.Send(*cmd)
	}
	return out, err
}

// pdHas: PagerDuty took a trigger of the alert, so it has an incident to acknowledge or resolve.
func pdHas(a *Alert) bool { return a.PD.State == PDAccepted || a.PD.State == PDAcked }

func (e *Engine) retry(a *Alert, now time.Time) *Command {
	if a.PD.AttemptAt != nil && now.Sub(*a.PD.AttemptAt) < e.RetryEvery {
		return nil
	}
	delivered := pdHas(a)
	switch {
	case a.Status == StatusResolved:
		if !delivered || a.PD.Retry == "" {
			a.PD.Retry = ""
			return nil
		}
		return e.pdCmd(a, PDResolve, now)
	case !delivered:
		// In a window the trigger waits for its end (Tick turns skipped back to pending). An
		// alert that was not sent while PagerDuty was off is sent once it is turned on.
		if a.Suppressed || (a.PD.State != PDPending && a.PD.State != PDFailed && a.PD.State != PDOff) {
			return nil
		}
		return e.pdCmd(a, PDTrigger, now)
	case a.PD.Retry == string(PDAcknowledge) && a.Status == StatusAcknowledged:
		return e.pdCmd(a, PDAcknowledge, now)
	case a.PD.Retry == string(PDTrigger):
		return e.pdCmd(a, PDTrigger, now)
	}
	return nil
}

// Reroute routes the active alerts of the given configuration items again, after the
// catalog changed (an item moved to another service, a team got people).
func (e *Engine) Reroute(ctx context.Context) error {
	rows, err := e.db.Query(ctx, "SELECT id FROM alerts WHERE "+sqlActive+" AND ci_id <> ''")
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	w := e.world()
	now := e.now()
	for _, id := range ids {
		err := pgx.BeginFunc(ctx, e.db, func(tx pgx.Tx) error {
			a, err := lockByID(ctx, tx, id)
			if err != nil || a == nil || !Active(a.Status) {
				return err
			}
			ci, ok := w.cis[a.CIID]
			if !ok {
				return nil
			}
			r := w.route(&ci, now)
			if sameRoute(a.Route, r) {
				return nil
			}
			c := &change{a: a}
			e.reroute(c, w, &ci, now)
			return c.save(ctx, tx)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// mergeInto folds an alert into another active alert of the same item and signal: the sources
// move there and the alert is resolved. Two active alerts may not share a dedup key, so an
// alert that waited for its item (or moves to another item) joins the alert the item has.
func (e *Engine) mergeInto(ctx context.Context, tx pgx.Tx, c *change, other *Alert, ciName, actor string, now time.Time) (*Command, error) {
	a := c.a
	oc := &change{a: other}
	for k, src := range a.Sources {
		if _, ok := other.Sources[k]; !ok {
			other.Sources[k] = src
		}
	}
	other.Count += a.Count
	if a.FirstSeen.Before(other.FirstSeen) {
		other.FirstSeen = a.FirstSeen
	}
	if a.LastSeen.After(other.LastSeen) {
		other.LastSeen = a.LastSeen
	}
	other.Severity = firingSeverity(other, other.Severity)
	oc.log(now, KindRoute, "merged_from", map[string]string{"alert": a.ID, "ci": ciName}, actor)
	if err := oc.save(ctx, tx); err != nil {
		return nil, err
	}
	c.log(now, KindRoute, "merged_into", map[string]string{"alert": other.ID, "ci": ciName}, actor)
	e.resolve(c, now, "merged", actor)
	return e.pdCmd(a, PDResolve, now), nil
}

// BindUnknown binds the active alerts whose item was not in the catalog to the item their
// event names now resolve to (an item was created, or got the name as an alias), routes them
// and returns the IDs of the alerts bound. An alert whose item and signal already have another
// active alert is merged into it: its sources move there and it is resolved.
func (e *Engine) BindUnknown(ctx context.Context, actor string) ([]string, error) {
	w := e.world()
	now := e.now()
	bound := []string{}
	var cmds []Command
	err := pgx.BeginFunc(ctx, e.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(lockKey)); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, "SELECT id FROM alerts WHERE "+sqlActive+" AND ci_id = '' ORDER BY seq")
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, id := range ids {
			a, err := lockByID(ctx, tx, id)
			if err != nil {
				return err
			}
			if a == nil || !Active(a.Status) || a.CIID != "" {
				continue
			}
			name := cmp.Or(a.EventCI, a.CIName)
			ci, excluded := w.resolveCI(name, a.Labels)
			if ci == nil || excluded {
				continue
			}
			key := dedupKey(ci, name, a.Signal)
			other, err := activeByKey(ctx, tx, key)
			if err != nil {
				return err
			}
			c := &change{a: a}
			if other != nil {
				cmd, err := e.mergeInto(ctx, tx, c, other, ci.Name, actor, now)
				if err != nil {
					return err
				}
				if cmd != nil {
					cmds = append(cmds, *cmd)
				}
			} else {
				a.DedupKey = key
				e.bind(c, w, ci, now)
				if a.RelatedID == "" {
					if err := e.link(ctx, tx, c, now); err != nil {
						return err
					}
				}
			}
			if err := c.save(ctx, tx); err != nil {
				return err
			}
			bound = append(bound, a.ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, c := range cmds {
		e.pd.Send(c)
	}
	return bound, nil
}

func sameRef(a, b *Ref) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

func sameRoute(a, b Route) bool {
	if a.Via != b.Via || len(a.Services) != len(b.Services) || len(a.People) != len(b.People) || len(a.Owners) != len(b.Owners) {
		return false
	}
	if !sameRef(a.Team, b.Team) || !sameRef(a.Service, b.Service) {
		return false
	}
	if (a.Channel == nil) != (b.Channel == nil) || (a.Channel != nil && *a.Channel != *b.Channel) {
		return false
	}
	for i := range a.Services {
		if a.Services[i] != b.Services[i] {
			return false
		}
	}
	for i := range a.People {
		if a.People[i] != b.People[i] {
			return false
		}
	}
	for i := range a.Owners {
		if a.Owners[i] != b.Owners[i] {
			return false
		}
	}
	return true
}

// FallbackDue tells the notifier, right before it sends backup notification of an alert,
// whether it is still due: an alert acknowledged or resolved since it was queued is not sent
// (and its backup notification is cancelled), one in a maintenance window waits.
func (e *Engine) FallbackDue(ctx context.Context, id string) (bool, error) {
	now := e.now()
	due := false
	err := pgx.BeginFunc(ctx, e.db, func(tx pgx.Tx) error {
		due = false
		a, err := lockByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if a == nil {
			return ErrNotFound
		}
		if (a.FallbackState != FallbackPending && a.FallbackState != FallbackSending) || a.Suppressed {
			return nil
		}
		if a.FallbackState == FallbackSending && a.SendingAt != nil && now.Sub(*a.SendingAt) < SendLease {
			// Another notifier (this or another instance) is sending it right now.
			return nil
		}
		c := &change{a: a, dirty: true}
		if a.Status != StatusOpen {
			// A sending one left by a stopped process is cancelled the same way.
			a.FallbackState = FallbackPending
			e.taken(c, now)
			return c.save(ctx, tx)
		}
		t := now
		a.FallbackState, a.SendingAt = FallbackSending, &t
		due = true
		return c.save(ctx, tx)
	})
	return due, err
}

// FallbackDone records that backup notification of an alert was attempted and the addresses
// it reached; the notifier calls it once the outcome is on the timeline. When the alert was
// acknowledged or resolved while the messages were going out, those addresses get the
// follow-up.
func (e *Engine) FallbackDone(ctx context.Context, id string, sent []Notified) error {
	var followUp *Alert
	err := pgx.BeginFunc(ctx, e.db, func(tx pgx.Tx) error {
		followUp = nil
		a, err := lockByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if a == nil {
			return ErrNotFound
		}
		if a.FallbackState != FallbackPending && a.FallbackState != FallbackSending {
			return nil
		}
		a.FallbackState, a.Notified = FallbackSent, sent
		if a.Status != StatusOpen && len(sent) > 0 {
			a.FollowUp = a.Status
			t := e.now()
			a.FollowUpTry = &t
			cp := a.Clone()
			followUp = &cp
		}
		return save(ctx, tx, a, nil)
	})
	if err == nil && followUp != nil {
		e.notify.FollowUp(*followUp)
	}
	return err
}

// FollowUpDone records that the follow-up about event (acknowledged or resolved) was attempted.
// A follow-up about a later event that became due meanwhile stays pending.
func (e *Engine) FollowUpDone(ctx context.Context, id, event string) error {
	return pgx.BeginFunc(ctx, e.db, func(tx pgx.Tx) error {
		a, err := lockByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if a == nil {
			return ErrNotFound
		}
		if a.FollowUp != event {
			return nil
		}
		a.FollowUp, a.FollowUpTry = "", nil
		return save(ctx, tx, a, nil)
	})
}

// Note adds a line to the timeline of an alert, for what happened outside the engine (backup
// notification).
func (e *Engine) Note(ctx context.Context, id, kind, code string, args map[string]string) error {
	return note(ctx, e.db, id, []Entry{{At: e.now(), Kind: kind, Code: code, Args: args}})
}

func (e *Engine) List(ctx context.Context, f Filter) (Page, error) {
	p, err := list(ctx, e.db, f)
	p.Counts.PDEnabled = e.policy().pdOn
	return p, err
}

func (e *Engine) Get(ctx context.Context, id string) (Alert, []Entry, error) {
	return get(ctx, e.db, id)
}

// Run ticks every 15 seconds until ctx ends. When the catalog changed, the alerts that waited
// for an item are bound to it and the active alerts are routed again.
func (e *Engine) Run(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	routed := e.st.Version()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := e.Tick(ctx); err != nil && ctx.Err() == nil {
				slog.Error("alert engine tick failed", "err", err)
			}
			if v := e.st.Version(); v != routed {
				if err := e.Reresolve(ctx); err != nil && ctx.Err() == nil {
					slog.Error("alerts not resolved again", "err", err)
				}
				if err := e.Reroute(ctx); err != nil && ctx.Err() == nil {
					slog.Error("alerts not routed again", "err", err)
				} else {
					routed = v
				}
			}
		}
	}
}

// details takes the description and fields of a delivery. A resolution that carries none
// keeps what the firing delivery said.
func (s *Source) details(in Incoming) {
	if in.Description != "" || in.Status != SourceResolved {
		s.Description = in.Description
	}
	if len(in.Fields) > 0 || in.Status != SourceResolved {
		s.Fields = slices.Clone(in.Fields)
	}
}
