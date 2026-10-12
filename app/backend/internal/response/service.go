package response

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/notify"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	// KindResponse is the timeline kind of what incident response did.
	KindResponse = "response"
	// maxAttempts: a failed action is tried this many times, retryEvery apart (growing).
	maxAttempts = 5
	retryEvery  = 2 * time.Minute
	tickEvery   = 10 * time.Second
	tickBatch   = 500
	lockKey     = 0x756d622d72657370
	secretPath  = "response"
)

// Actions people can ask for by hand on an incident.
const (
	ActionRoom       = "room"
	ActionBridge     = "bridge"
	ActionTask       = "task"
	ActionPostmortem = "postmortem"
)

// defaultPostmortemDays is the due date of a postmortem when the policy names none.
const defaultPostmortemDays = 10

var (
	ErrOff       = errors.New("incident response is turned off")
	ErrBadAction = errors.New("unknown action")
	// errIntegrationOff: the integration an action needs is turned off.
	errIntegrationOff = errors.New("the integration is turned off")
)

// Incidents is what response needs of the alert engine.
type Incidents interface {
	Note(ctx context.Context, id, kind, code string, args map[string]string) error
	Get(ctx context.Context, id string) (alert.Alert, []alert.Entry, error)
}

// Messenger sends messages through the backup notification channels.
type Messenger interface {
	Ready(kind string) bool
	SendDirect(ctx context.Context, m notify.Direct, to []notify.Target) []notify.Outcome
}

// PagerDuty is what response needs of PagerDuty: sending an incident there at an escalation
// step and keeping the priority of its PagerDuty incident in step with the response priority.
type PagerDuty interface {
	// Escalate sends an active incident to PagerDuty; ErrPDHas when it is already there.
	Escalate(ctx context.Context, id string) error
	// SetPriority sets the priority of the PagerDuty incident; ErrPDSyncOff when that is off.
	SetPriority(ctx context.Context, a alert.Alert, priority string) error
}

// ErrPDSyncOff: the priority is not synchronized with PagerDuty.
var ErrPDSyncOff = errors.New("the PagerDuty priority is not synchronized")

// Secrets resolves and stores secrets (OpenBao).
type Secrets interface {
	Resolve(ref string) (string, error)
	PutRef(ctx context.Context, path, key, value string) (string, error)
}

type Service struct {
	db     *pgxpool.Pool
	st     *store.Store
	inc    Incidents
	msg    Messenger
	pd     PagerDuty
	sec    Secrets
	client *http.Client
	now    func() time.Time

	mu       sync.Mutex
	graph    *Graph
	graphKey string
	zoom     *Zoom
	zoomKey  string

	// run serializes ticks and manual actions inside this instance; the advisory lock does it
	// between instances.
	run sync.Mutex
	// ticks counts the ticks of this instance (old voice audio is dropped now and then).
	ticks int
}

func New(db *pgxpool.Pool, st *store.Store, inc Incidents, msg Messenger, sec Secrets) *Service {
	return &Service{db: db, st: st, inc: inc, msg: msg, sec: sec, client: &http.Client{Timeout: 20 * time.Second}, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) SetClock(now func() time.Time) { s.now = now }

// SetPagerDuty connects response to PagerDuty.
func (s *Service) SetPagerDuty(pd PagerDuty)    { s.pd = pd }
func (s *Service) SetHTTPClient(c *http.Client) { s.client = c }

// snap is the settings and the catalog one pass works with.
type snap struct {
	set     model.Response
	cat     Catalog
	public  string
	grafana bool
	locale  string
	tz      *time.Location
}

func (s *Service) snapshot() snap {
	var sn snap
	tz := ""
	s.st.Read(func(d *store.Data) {
		sn.set = d.Settings.Response.Effective()
		sn.public = strings.TrimRight(d.Settings.Alerting.PublicURL, "/")
		sn.grafana = d.Settings.Alerting.Grafana.DashboardURL != ""
		sn.locale = d.Settings.DefaultLocale
		tz = d.Settings.DefaultTZ
		sn.cat = Catalog{Services: map[string]model.Service{}, Teams: map[string]model.Team{}, Users: map[string]model.User{}}
		for id, v := range d.Services {
			sn.cat.Services[id] = *v
		}
		for id, v := range d.Teams {
			sn.cat.Teams[id] = *v
		}
		for id, v := range d.Users {
			u := *v
			u.Avatar = nil
			sn.cat.Users[id] = u
		}
	})
	sn.tz = time.UTC
	if l, err := time.LoadLocation(tz); err == nil && tz != "" {
		sn.tz = l
	}
	return sn
}

// Run handles incidents every 10 seconds until ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
				slog.Error("incident response tick failed", "err", err)
			}
		}
	}
}

type row struct {
	a  alert.Alert
	st *State
}

// Tick handles the active incidents and the resolved ones whose response is not finished.
func (s *Service) Tick(ctx context.Context) error {
	sn := s.snapshot()
	if sn.set.Mode == model.ModeOff {
		return nil
	}
	s.run.Lock()
	defer s.run.Unlock()
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var got bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", int64(lockKey)).Scan(&got); err != nil || !got {
		return err
	}
	defer conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", int64(lockKey))

	if s.ticks++; s.ticks%360 == 1 {
		s.sweepVoice(ctx)
	}
	counts, err := s.counts(ctx)
	if err != nil {
		return err
	}
	rows, err := s.db.Query(ctx, `SELECT a.doc, r.doc FROM alerts a LEFT JOIN incident_response r ON r.alert_id = a.id
		WHERE a.status <> 'resolved' OR NOT COALESCE(r.finished, true) ORDER BY a.seq LIMIT $1`, tickBatch)
	if err != nil {
		return err
	}
	var list []row
	for rows.Next() {
		var adoc, sdoc []byte
		if err := rows.Scan(&adoc, &sdoc); err != nil {
			rows.Close()
			return err
		}
		var r row
		if err := json.Unmarshal(adoc, &r.a); err != nil {
			continue
		}
		if sdoc != nil {
			r.st = &State{}
			if json.Unmarshal(sdoc, r.st) != nil {
				r.st = nil
			}
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range list {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if r.st == nil && sn.set.ActiveSince != nil && r.a.OpenedAt.Before(*sn.set.ActiveSince) {
			continue
		}
		st, changed := s.handle(ctx, sn, r.a, r.st, counts, nil)
		if changed {
			st.Updated = s.now()
			if err := saveState(ctx, s.db, st); err != nil {
				slog.Error("incident response state not saved", "alert", r.a.ID, "err", err)
			}
		}
	}
	return nil
}

// counts are the active incidents per business service.
func (s *Service) counts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.Query(ctx, `SELECT sid, count(*) FROM alerts, unnest(service_ids) AS sid WHERE status <> 'resolved' GROUP BY sid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// Get is the response state of an incident; nil when response never handled it.
func (s *Service) Get(ctx context.Context, id string) (*State, error) {
	return loadState(ctx, s.db, id)
}

// Force carries out an action on an incident now, as asked by a person: the war room, the
// bridge call, the Jira task or the postmortem; an empty action assesses the incident again.
func (s *Service) Force(ctx context.Context, id, action, actor string) (*State, error) {
	switch action {
	case "", ActionRoom, ActionBridge, ActionTask, ActionPostmortem:
	default:
		return nil, ErrBadAction
	}
	sn := s.snapshot()
	if sn.set.Mode == model.ModeOff {
		return nil, ErrOff
	}
	s.run.Lock()
	defer s.run.Unlock()
	a, _, err := s.inc.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	st, err := loadState(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	counts, err := s.counts(ctx)
	if err != nil {
		return nil, err
	}
	var force []string
	if action != "" {
		force = []string{action}
		if st != nil {
			delete(st.Failures, action)
		}
		s.note(ctx, id, "response_forced", map[string]string{"action": action, "by": actor})
	}
	st, _ = s.handle(ctx, sn, a, st, counts, force)
	st.Updated = s.now()
	return st, saveState(ctx, s.db, st)
}

func (s *Service) note(ctx context.Context, id, code string, args map[string]string) {
	if err := s.inc.Note(ctx, id, KindResponse, code, args); err != nil && !errors.Is(err, alert.ErrNotFound) {
		slog.Warn("incident response not recorded", "alert", id, "code", code, "err", err)
	}
}

// pass is one handling of one incident.
type pass struct {
	s     *Service
	ctx   context.Context
	sn    snap
	a     alert.Alert
	st    *State
	now   time.Time
	dry   bool
	force []string
	v     view
}

func (p *pass) note(code string, args map[string]string) {
	if p.dry {
		if args == nil {
			args = map[string]string{}
		}
		args["dry"] = "1"
	}
	p.s.note(p.ctx, p.a.ID, code, args)
}

func (p *pass) forced(action string) bool { return slices.Contains(p.force, action) }

// attempt runs an action unless it failed recently or too often; a failure is recorded and
// retried later.
func (p *pass) attempt(key string, fn func() error) bool {
	st := p.st
	if f := st.Failures[key]; f != nil && (f.Attempts >= maxAttempts || p.now.Before(f.Next)) {
		return false
	}
	err := fn()
	if errors.Is(err, errIntegrationOff) {
		if !st.Skipped[key] {
			if st.Skipped == nil {
				st.Skipped = map[string]bool{}
			}
			st.Skipped[key] = true
			p.note("response_skipped", map[string]string{"action": key})
		}
		return false
	}
	if err != nil {
		if st.Failures == nil {
			st.Failures = map[string]*Failure{}
		}
		f := st.Failures[key]
		if f == nil {
			f = &Failure{}
			st.Failures[key] = f
		}
		f.Attempts++
		f.Next = p.now.Add(time.Duration(f.Attempts) * retryEvery)
		f.Error = err.Error()
		code := "response_failed"
		if f.Attempts >= maxAttempts {
			code = "response_gave_up"
		}
		p.note(code, map[string]string{"action": key, "error": f.Error, "attempt": fmt.Sprint(f.Attempts)})
		slog.Warn("incident response action failed", "alert", p.a.ID, "action", key, "attempt", f.Attempts, "err", err)
		return false
	}
	delete(st.Failures, key)
	return true
}

// handle runs the policy for an incident once; it reports whether the state changed.
func (s *Service) handle(ctx context.Context, sn snap, a alert.Alert, st *State, counts map[string]int, force []string) (*State, bool) {
	now := s.now()
	fresh := st == nil
	if fresh {
		st = &State{AlertID: a.ID, OpenedAt: a.OpenedAt, Steps: []StepRun{}, Status: alert.StatusOpen, Created: now}
	}
	before, _ := json.Marshal(st)
	p := &pass{s: s, ctx: ctx, sn: sn, a: a, st: st, now: now, dry: sn.set.Mode == model.ModeDryRun, force: force}
	p.v = view{locale: sn.locale, tz: sn.tz, a: a, st: st}
	if sn.public != "" {
		p.v.open = model.IncidentURL(sn.public, a.ID)
		if sn.grafana {
			p.v.gf = model.GrafanaHopURL(sn.public, a.ID)
		}
	}
	active := alert.Active(a.Status)
	if a.IsTest() || a.Excluded {
		// Nothing is done for them; the state is written once, not on every pass.
		was := st.Finished
		st.Finished = true
		return st, fresh || !was
	}
	if active && !fresh && !st.OpenedAt.Equal(a.OpenedAt) {
		st.OpenedAt, st.Steps, st.Finished, st.Transitioned, st.Failures = a.OpenedAt, []StepRun{}, false, false, nil
		p.note("response_reopened", nil)
		p.roomPost(p.v.t("upd.reopened"))
		p.comment("comment.reopened")
		st.Status = a.Status
	}
	if active && a.Suppressed && len(force) == 0 {
		return st, fresh
	}
	as := Assess(sn.set.Impact, sn.cat, a, counts, now)
	if active || st.Priority == "" {
		st.Assessment = as
	}
	switch {
	case st.Priority == "":
		st.Priority = as.Priority
		p.note("response_assessed", map[string]string{"impact": as.Impact, "urgency": as.Urgency, "priority": as.Priority, "services": as.ServiceNames()})
	case active && model.SeverityRank(as.Priority) > model.SeverityRank(st.Priority):
		from := st.Priority
		st.Priority = as.Priority
		p.note("response_priority_raised", map[string]string{"from": from, "to": as.Priority, "impact": as.Impact})
		p.roomPost(p.v.t("upd.raised", "from", p.v.t(from), "to", p.v.t(as.Priority)))
		p.comment("comment.raised", "from", p.v.t(from), "to", p.v.t(as.Priority))
	}
	if active && s.pd != nil && st.PDPriority != st.Priority && !p.dry && (a.PD.State == alert.PDAccepted || a.PD.State == alert.PDAcked) {
		p.attempt("pd_priority", p.syncPDPriority)
	}
	pol, ok := sn.set.PolicyFor(st.Priority)
	if !ok && len(force) == 0 {
		if st.Skipped == nil || !st.Skipped["policy"] {
			if st.Skipped == nil {
				st.Skipped = map[string]bool{}
			}
			st.Skipped["policy"] = true
			p.note("response_no_policy", map[string]string{"priority": st.Priority})
		}
		if !active {
			st.Finished = true
		}
		return st, changed(before, st)
	}

	if active {
		// Due steps decide whether a call is needed now.
		var due []int
		if !(pol.StopOnAck && a.Status == alert.StatusAcknowledged) {
			for i, step := range pol.Steps {
				if !st.stepDone(i) && now.Sub(a.OpenedAt) >= time.Duration(step.AfterMinutes)*time.Minute {
					due = append(due, i)
				}
			}
		}
		bridge := pol.Bridge
		for _, i := range due {
			for _, m := range pol.Steps[i].Methods {
				if m == model.CommCallTeams && bridge == "" {
					bridge = "teams"
				} else if m == model.CommCallZoom && bridge == "" {
					bridge = "zoom"
				}
			}
		}
		if p.forced(ActionBridge) && bridge == "" {
			bridge = "teams"
			if sn.set.Graph.Mode == model.ModeOff && sn.set.ZoomAPI.Mode != model.ModeOff {
				bridge = "zoom"
			}
		}
		if st.Bridge == nil && bridge != "" && (len(due) > 0 || pol.Bridge != "" || p.forced(ActionBridge)) {
			p.attempt(ActionBridge, func() error { return p.openBridge(bridge) })
		}
		if st.Task == nil && (pol.Jira.Task || p.forced(ActionTask)) {
			p.attempt(ActionTask, p.createTask)
		}
		if st.Room == nil && (pol.WarRoom.Enabled || p.forced(ActionRoom)) {
			if p.attempt(ActionRoom, func() error { return p.openRoom(pol) }) && st.Task != nil && st.Room.URL != "" {
				p.commentText(p.v.t("room") + ": " + st.Room.URL)
			}
		}
		for _, i := range due {
			p.runStep(pol, i)
		}
	} else if p.forced(ActionTask) && st.Task == nil {
		p.attempt(ActionTask, p.createTask)
	}

	if st.Status != a.Status {
		switch a.Status {
		case alert.StatusAcknowledged:
			p.roomPost(p.v.t("upd.ack", "who", cmp.Or(a.AckedBy, "PagerDuty")))
			if pol.Jira.Comment {
				p.comment("comment.ack", "who", cmp.Or(a.AckedBy, "PagerDuty"))
			}
		case alert.StatusResolved:
			who := ""
			if a.ResolvedBy != "" {
				who = p.v.t("by", "who", a.ResolvedBy)
			}
			p.roomPost(p.v.t("upd.resolved", "who", who))
			if pol.Jira.Comment {
				p.comment("comment.resolved", "who", who)
			}
		}
		st.Status = a.Status
	}

	if !active {
		if st.Postmortem == nil && (pol.Jira.Postmortem || p.forced(ActionPostmortem)) {
			if p.attempt(ActionPostmortem, func() error { return p.createPostmortem(pol) }) {
				p.roomPost(p.v.t("upd.postmortem", "key", st.Postmortem.Key, "url", cmp.Or(st.Postmortem.URL, st.Postmortem.Key)))
			}
		}
		if t := sn.set.Jira.DoneTransition; t != "" && st.Task != nil && !st.Transitioned && !st.Task.DryRun {
			if p.attempt("transition", func() error { return p.transition(t) }) {
				st.Transitioned = true
			}
		}
		st.Finished = true
		for _, f := range st.Failures {
			if f.Attempts < maxAttempts {
				st.Finished = false
			}
		}
	}
	return st, changed(before, st)
}

func changed(before []byte, st *State) bool {
	after, _ := json.Marshal(st)
	return string(after) != string(before)
}

// person is someone a step or the war room reaches.
type person struct {
	id, name, email, telegram string
}

// audience is who a set of targets names: people and team channels.
type audience struct {
	people   []person
	channels []alert.Channel
}

func (au *audience) addPerson(p person) {
	for _, q := range au.people {
		if (p.id != "" && q.id == p.id) || (p.id == "" && p.email != "" && strings.EqualFold(q.email, p.email)) {
			return
		}
	}
	au.people = append(au.people, p)
}

func (au *audience) addChannel(c alert.Channel) {
	if c.Empty() || slices.Contains(au.channels, c) {
		return
	}
	au.channels = append(au.channels, c)
}

func userPerson(u model.User) person {
	name := u.Name
	if name == "" {
		name = u.Username
	}
	return person{id: u.ID, name: name, email: u.Email, telegram: u.Telegram}
}

func (sn snap) lead(teamID string) (person, bool) {
	t, ok := sn.cat.Teams[teamID]
	if !ok {
		return person{}, false
	}
	u, ok := sn.cat.Users[t.LeadID]
	if !ok || u.Disabled {
		return person{}, false
	}
	return userPerson(u), true
}

// audienceOf resolves targets, users and teams to people and channels.
func (sn snap) audienceOf(a alert.Alert, as Assessment, targets, userIDs, teamIDs []string) audience {
	var au audience
	for _, t := range targets {
		switch t {
		case model.TargetRoute:
			for _, p := range a.Route.Recipients() {
				au.addPerson(person{id: p.UserID, name: p.Name, email: p.Email, telegram: p.Telegram})
			}
			if a.Route.Channel != nil {
				au.addChannel(*a.Route.Channel)
			}
		case model.TargetLead:
			if a.Route.Team != nil {
				if p, ok := sn.lead(a.Route.Team.ID); ok {
					au.addPerson(p)
				}
			}
		case model.TargetParentLead:
			if a.Route.Team != nil {
				if t, ok := sn.cat.Teams[a.Route.Team.ID]; ok && t.ParentID != "" {
					if p, ok := sn.lead(t.ParentID); ok {
						au.addPerson(p)
					}
				}
			}
		case model.TargetOwners:
			for _, p := range a.Route.Owners {
				au.addPerson(person{id: p.UserID, name: p.Name, email: p.Email, telegram: p.Telegram})
			}
		case model.TargetServiceOwners:
			for _, s := range as.Services {
				if p, ok := sn.lead(s.OwnerTeamID); ok {
					au.addPerson(p)
				}
				if t, ok := sn.cat.Teams[s.OwnerTeamID]; ok {
					au.addChannel(alert.TeamChannel(t))
				}
			}
		}
	}
	for _, id := range userIDs {
		if u, ok := sn.cat.Users[id]; ok && !u.Disabled {
			au.addPerson(userPerson(u))
		}
	}
	for _, id := range teamIDs {
		t, ok := sn.cat.Teams[id]
		if !ok {
			continue
		}
		if ch := alert.TeamChannel(t); !ch.Empty() {
			au.addChannel(ch)
			if p, ok := sn.lead(id); ok {
				au.addPerson(p)
			}
			continue
		}
		for _, u := range sn.cat.Users {
			if !u.Disabled && slices.Contains(u.TeamIDs, id) {
				au.addPerson(userPerson(u))
			}
		}
	}
	slices.SortStableFunc(au.people, func(x, y person) int { return strings.Compare(x.name, y.name) })
	return au
}

func (au audience) names() []string {
	out := make([]string, 0, len(au.people))
	for _, p := range au.people {
		out = append(out, p.name)
	}
	return out
}

// addresses of the audience in the message channels of methods.
func (au audience) addresses(methods []string) []notify.Target {
	var out []notify.Target
	add := func(ch, addr string) {
		if addr != "" {
			out = append(out, notify.Target{Channel: ch, Address: addr})
		}
	}
	for _, m := range methods {
		for _, p := range au.people {
			switch m {
			case model.CommEmail:
				add(notify.ChannelEmail, p.email)
			case model.CommTelegram:
				add(notify.ChannelTelegram, p.telegram)
			}
		}
		for _, c := range au.channels {
			switch m {
			case model.CommEmail:
				add(notify.ChannelEmail, c.Email)
			case model.CommTelegram:
				add(notify.ChannelTelegram, c.Telegram)
			case model.CommTeams:
				add(notify.ChannelTeams, c.Teams)
			case model.CommZoom:
				add(notify.ChannelZoom, c.Zoom)
			}
		}
	}
	return out
}

// runStep notifies the targets of a step and records it.
func (p *pass) runStep(pol model.ResponsePolicy, i int) {
	step := pol.Steps[i]
	level := i + 1
	au := p.sn.audienceOf(p.a, p.st.Assessment, step.Targets, step.UserIDs, step.TeamIDs)
	run := StepRun{Index: i, At: p.now, Reached: []string{}, People: au.names(), DryRun: p.dry}
	var targets, off []notify.Target
	for _, t := range au.addresses(step.Methods) {
		if p.s.msg.Ready(t.Channel) {
			targets = append(targets, t)
		} else {
			off = append(off, t)
		}
	}
	if p.dry {
		for _, t := range targets {
			run.Reached = append(run.Reached, t.Channel+": "+notify.ShowAddress(t.Channel, t.Address))
		}
	} else if len(targets) > 0 {
		for _, o := range p.s.msg.SendDirect(p.ctx, p.v.stepMessage(level), targets) {
			if o.Err != nil {
				run.Failed = append(run.Failed, o.Channel+": "+o.Shown+" ("+o.Err.Error()+")")
			} else {
				run.Reached = append(run.Reached, o.Channel+": "+o.Shown)
			}
		}
	}
	if slices.Contains(step.Methods, model.CommPagerDuty) {
		switch err := p.escalatePD(); {
		case err != nil:
			run.Failed = append(run.Failed, "pagerduty ("+err.Error()+")")
		default:
			run.Reached = append(run.Reached, "pagerduty")
		}
	}
	if slices.Contains(step.Methods, model.CommWarRoom) && p.st.Room != nil {
		if pol.WarRoom.AddEscalated && level > 1 {
			p.addMembers(au.people)
		}
		p.roomPost(p.v.t("upd.step", "level", fmt.Sprint(level), "people", cmp.Or(strings.Join(au.names(), ", "), p.v.t("none"))))
		run.Reached = append(run.Reached, "war_room")
	}
	if b := p.st.Bridge; b != nil && (slices.Contains(step.Methods, model.CommCallTeams) || slices.Contains(step.Methods, model.CommCallZoom)) {
		run.Reached = append(run.Reached, "call_"+b.Provider)
	}
	p.voiceStep(step.Methods, au, level, &run)
	p.st.Steps = append(p.st.Steps, run)
	args := map[string]string{"level": fmt.Sprint(level), "after": fmt.Sprint(step.AfterMinutes), "people": strings.Join(au.names(), ", "),
		"methods": strings.Join(step.Methods, ","), "reached": fmt.Sprint(len(run.Reached)), "failed": fmt.Sprint(len(run.Failed))}
	if len(off) > 0 {
		var chs []string
		for _, t := range off {
			if !slices.Contains(chs, t.Channel) {
				chs = append(chs, t.Channel)
			}
		}
		args["off"] = strings.Join(chs, ",")
	}
	p.note("response_step", args)
	for _, f := range run.Failed {
		p.note("response_step_failed", map[string]string{"level": fmt.Sprint(level), "to": f})
	}
}

// escalatePD sends the incident to PagerDuty at an escalation step; one PagerDuty already has
// counts as reached.
func (p *pass) escalatePD() error {
	switch {
	case p.s.pd == nil:
		return errIntegrationOff
	case p.dry:
		return nil
	}
	if err := p.s.pd.Escalate(p.ctx, p.a.ID); err != nil && !errors.Is(err, alert.ErrPDHas) {
		return err
	}
	return nil
}

// syncPDPriority sets the response priority on the PagerDuty incident.
func (p *pass) syncPDPriority() error {
	err := p.s.pd.SetPriority(p.ctx, p.a, p.st.Priority)
	switch {
	case errors.Is(err, ErrPDSyncOff):
		return errIntegrationOff
	case err != nil:
		return err
	}
	p.st.PDPriority = p.st.Priority
	p.note("response_pd_priority", map[string]string{"priority": p.st.Priority})
	return nil
}

// graphClient is the Microsoft Graph client of the settings; dry when the integration or the
// whole response is in dry run.
func (s *Service) graphClient(set model.Response) (*Graph, bool, error) {
	g := set.Graph
	switch {
	case g.Mode == model.ModeOff || g.Mode == "":
		return nil, false, errIntegrationOff
	case g.Mode == model.ModeDryRun || set.Mode == model.ModeDryRun:
		return nil, true, nil
	}
	key := g.TenantID + "|" + g.ClientID + "|" + g.ClientSecretRef + "|" + g.RefreshTokenRef + "|" + g.LoginURL + "|" + g.GraphURL
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.graph != nil && s.graphKey == key {
		return s.graph, false, nil
	}
	if s.sec == nil {
		return nil, false, errors.New("secrets are not available")
	}
	secret, err := s.sec.Resolve(g.ClientSecretRef)
	if err != nil {
		return nil, false, fmt.Errorf("the Microsoft Graph client secret is not available: %v", err)
	}
	refresh, err := s.sec.Resolve(g.RefreshTokenRef)
	if err != nil {
		return nil, false, fmt.Errorf("the Microsoft Graph refresh token is not available: %v", err)
	}
	gc := NewGraph(g, secret, refresh, s.client)
	gc.Rotated = func(tok string) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ref, err := s.sec.PutRef(ctx, secretPath, "graph_refresh_token", tok)
		if err != nil {
			slog.Error("the new Microsoft Graph refresh token is not stored", "err", err)
			return
		}
		s.st.Write(func(d *store.Data) { d.Settings.Response.Graph.RefreshTokenRef = ref })
		s.mu.Lock()
		s.graphKey = g.TenantID + "|" + g.ClientID + "|" + g.ClientSecretRef + "|" + ref + "|" + g.LoginURL + "|" + g.GraphURL
		s.mu.Unlock()
	}
	s.graph, s.graphKey = gc, key
	return gc, false, nil
}

// TestGraph checks the Microsoft Graph connection and returns the service account.
func (s *Service) TestGraph(ctx context.Context) (string, error) {
	set := s.snapshot().set
	set.Mode = model.ModeLive
	set.Graph.Mode = model.ModeLive
	g, _, err := s.graphClient(set)
	if err != nil {
		return "", err
	}
	me, err := g.Me(ctx)
	if err != nil {
		return "", err
	}
	return cmp.Or(me.UPN, me.Name), nil
}

func (s *Service) zoomClient(set model.Response) (*Zoom, bool, error) {
	z := set.ZoomAPI
	switch {
	case z.Mode == model.ModeOff || z.Mode == "":
		return nil, false, errIntegrationOff
	case z.Mode == model.ModeDryRun || set.Mode == model.ModeDryRun:
		return nil, true, nil
	}
	key := z.AccountID + "|" + z.ClientID + "|" + z.ClientSecretRef + "|" + z.User + "|" + z.OAuthURL + "|" + z.APIURL
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.zoom != nil && s.zoomKey == key {
		return s.zoom, false, nil
	}
	if s.sec == nil {
		return nil, false, errors.New("secrets are not available")
	}
	secret, err := s.sec.Resolve(z.ClientSecretRef)
	if err != nil {
		return nil, false, fmt.Errorf("the Zoom client secret is not available: %v", err)
	}
	s.zoom, s.zoomKey = NewZoom(z, secret, s.client), key
	return s.zoom, false, nil
}

// TestZoom checks the Zoom connection and returns the e-mail of the meeting host.
func (s *Service) TestZoom(ctx context.Context) (string, error) {
	set := s.snapshot().set
	set.Mode = model.ModeLive
	set.ZoomAPI.Mode = model.ModeLive
	z, _, err := s.zoomClient(set)
	if err != nil {
		return "", err
	}
	return z.Check(ctx)
}

func (s *Service) jiraClient(set model.Response) (*Jira, bool, error) {
	j := set.Jira
	switch {
	case j.Mode == model.ModeOff || j.Mode == "":
		return nil, false, errIntegrationOff
	case j.Mode == model.ModeDryRun || set.Mode == model.ModeDryRun:
		return nil, true, nil
	}
	if s.sec == nil {
		return nil, false, errors.New("secrets are not available")
	}
	tok, err := s.sec.Resolve(j.TokenRef)
	if err != nil {
		return nil, false, fmt.Errorf("the Jira API token is not available: %v", err)
	}
	return NewJira(j, tok, s.client), false, nil
}

// TestJira checks the Jira connection and the project and returns the account and the project.
func (s *Service) TestJira(ctx context.Context) (string, error) {
	set := s.snapshot().set
	set.Mode = model.ModeLive
	set.Jira.Mode = model.ModeLive
	j, _, err := s.jiraClient(set)
	if err != nil {
		return "", err
	}
	return j.Check(ctx)
}

func (p *pass) openBridge(provider string) error {
	topic := p.v.t("room.topic", "priority", model.SeverityPriority(p.st.Priority), "id", p.a.ID, "title", p.a.Title)
	var m Meeting
	dry := false
	switch provider {
	case "teams":
		g, d, err := p.s.graphClient(p.sn.set)
		if err != nil {
			return err
		}
		if dry = d; !dry {
			if m, err = g.CreateMeeting(p.ctx, topic, p.now); err != nil {
				return err
			}
		}
	case "zoom":
		z, d, err := p.s.zoomClient(p.sn.set)
		if err != nil {
			return err
		}
		if dry = d; !dry {
			if m, err = z.CreateMeeting(p.ctx, topic); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unknown bridge %q", provider)
	}
	p.st.Bridge = &Bridge{Provider: provider, ID: m.ID, URL: m.URL, At: p.now, DryRun: dry}
	args := map[string]string{"provider": provider}
	if m.URL != "" {
		args["url"] = m.URL
	}
	if dry {
		args["dry"] = "1"
	}
	p.s.note(p.ctx, p.a.ID, "response_bridge", args)
	return nil
}

// roomMembers are the people the war room starts with.
func (p *pass) roomMembers(pol model.ResponsePolicy) audience {
	return p.sn.audienceOf(p.a, p.st.Assessment, pol.WarRoom.Members, pol.WarRoom.UserIDs, nil)
}

func (p *pass) openRoom(pol model.ResponsePolicy) error {
	g, dry, err := p.s.graphClient(p.sn.set)
	if err != nil {
		return err
	}
	au := p.roomMembers(pol)
	topic := p.v.t("room.topic", "priority", model.SeverityPriority(p.st.Priority), "id", p.a.ID, "title", p.a.Title)
	room := &Room{Members: []string{}, At: p.now, DryRun: dry}
	var missing []string
	if dry {
		room.ID = "dry-run"
		for _, x := range au.people {
			if x.email != "" {
				room.Members = append(room.Members, x.email)
			}
		}
	} else {
		var ids []string
		for _, x := range au.people {
			if x.email == "" {
				missing = append(missing, x.name)
				continue
			}
			u, err := g.User(p.ctx, x.email)
			if err != nil {
				missing = append(missing, x.name)
				continue
			}
			ids = append(ids, u.ID)
			room.Members = append(room.Members, x.email)
		}
		c, err := g.CreateChat(p.ctx, topic, ids)
		if err != nil {
			return err
		}
		room.ID, room.URL = c.ID, c.URL
		p.st.Room = room
		if err := g.Post(p.ctx, c.ID, p.v.roomDoc(au.names()).HTML()); err != nil {
			slog.Warn("war room summary not posted", "alert", p.a.ID, "err", err)
		}
	}
	p.st.Room = room
	args := map[string]string{"members": fmt.Sprint(len(room.Members)), "people": strings.Join(au.names(), ", ")}
	if room.URL != "" {
		args["url"] = room.URL
	}
	if len(missing) > 0 {
		args["missing"] = strings.Join(missing, ", ")
	}
	if dry {
		args["dry"] = "1"
	}
	p.s.note(p.ctx, p.a.ID, "response_room", args)
	return nil
}

// addMembers adds people to the war room; those already in or without an account are skipped.
func (p *pass) addMembers(people []person) {
	r := p.st.Room
	if r == nil {
		return
	}
	g, dry, err := p.s.graphClient(p.sn.set)
	if err != nil {
		return
	}
	var added []string
	for _, x := range people {
		if x.email == "" || slices.ContainsFunc(r.Members, func(m string) bool { return strings.EqualFold(m, x.email) }) {
			continue
		}
		if !dry && !r.DryRun {
			u, err := g.User(p.ctx, x.email)
			if err != nil {
				continue
			}
			if err := g.AddMember(p.ctx, r.ID, u.ID); err != nil {
				slog.Warn("war room member not added", "alert", p.a.ID, "err", err)
				continue
			}
		}
		r.Members = append(r.Members, x.email)
		added = append(added, x.name)
	}
	if len(added) > 0 {
		p.note("response_room_member", map[string]string{"people": strings.Join(added, ", ")})
	}
}

// roomPost posts an update in the war room; nothing without one.
func (p *pass) roomPost(text string) {
	r := p.st.Room
	if r == nil || r.DryRun || p.dry {
		return
	}
	g, dry, err := p.s.graphClient(p.sn.set)
	if err != nil || dry {
		return
	}
	if err := g.Post(p.ctx, r.ID, "<p>"+escape(text)+"</p>"); err != nil {
		slog.Warn("war room update not posted", "alert", p.a.ID, "err", err)
	}
}

func (p *pass) createTask() error {
	j, dry, err := p.s.jiraClient(p.sn.set)
	if err != nil {
		return err
	}
	set := p.sn.set.Jira
	issue := Issue{Key: "DRY-" + p.a.ID}
	if !dry {
		issue, err = j.Create(p.ctx, NewIssue{Type: set.TaskType, Summary: p.v.t("task.summary", "priority", model.SeverityPriority(p.st.Priority), "id", p.a.ID, "title", p.a.Title),
			Priority: set.Priorities[p.st.Priority], Labels: set.Labels, Body: p.v.taskDoc()})
		if err != nil {
			return err
		}
	}
	p.st.Task = &JiraIssue{Key: issue.Key, URL: issue.URL, At: p.now, DryRun: dry}
	args := map[string]string{"key": issue.Key}
	if issue.URL != "" {
		args["url"] = issue.URL
	}
	if dry {
		args["dry"] = "1"
	}
	p.s.note(p.ctx, p.a.ID, "response_jira_task", args)
	if p.st.Room != nil && issue.URL != "" {
		p.roomPost(p.v.t("upd.task", "key", issue.Key, "url", issue.URL))
	}
	return nil
}

func (p *pass) createPostmortem(pol model.ResponsePolicy) error {
	j, dry, err := p.s.jiraClient(p.sn.set)
	if err != nil {
		return err
	}
	set := p.sn.set.Jira
	issue := Issue{Key: "DRY-PM-" + p.a.ID}
	days := pol.Jira.PostmortemDays
	if days <= 0 {
		days = defaultPostmortemDays
	}
	if !dry {
		_, entries, err := p.s.inc.Get(p.ctx, p.a.ID)
		if err != nil {
			return err
		}
		issue, err = j.Create(p.ctx, NewIssue{Type: set.PostmortemType, Summary: p.v.t("pm.summary", "id", p.a.ID, "title", p.a.Title),
			Priority: set.Priorities[p.st.Priority], Labels: append(slices.Clone(set.Labels), "postmortem"),
			Due: p.now.In(p.sn.tz).AddDate(0, 0, days).Format("2006-01-02"), Body: p.v.postmortemDoc(entries)})
		if err != nil {
			return err
		}
		if p.st.Task != nil && !p.st.Task.DryRun && set.LinkType != "" {
			if err := j.Link(p.ctx, set.LinkType, issue.Key, p.st.Task.Key); err != nil {
				slog.Warn("postmortem not linked to the task", "alert", p.a.ID, "err", err)
			}
		}
	}
	p.st.Postmortem = &JiraIssue{Key: issue.Key, URL: issue.URL, At: p.now, DryRun: dry}
	args := map[string]string{"key": issue.Key, "days": fmt.Sprint(days)}
	if issue.URL != "" {
		args["url"] = issue.URL
	}
	if dry {
		args["dry"] = "1"
	}
	p.s.note(p.ctx, p.a.ID, "response_jira_postmortem", args)
	return nil
}

// comment adds a comment of words to the Jira task; failures are logged, not retried.
func (p *pass) comment(key string, kv ...string) {
	p.commentText(p.v.t(key, append([]string{"id", p.a.ID}, kv...)...))
}

func (p *pass) commentText(text string) {
	if p.st.Task == nil || p.st.Task.DryRun || p.dry {
		return
	}
	j, dry, err := p.s.jiraClient(p.sn.set)
	if err != nil || dry {
		return
	}
	if err := j.Comment(p.ctx, p.st.Task.Key, Doc{P(text)}); err != nil {
		slog.Warn("Jira comment not added", "alert", p.a.ID, "err", err)
	}
}

func (p *pass) transition(name string) error {
	j, dry, err := p.s.jiraClient(p.sn.set)
	if err != nil || dry {
		return err
	}
	if err := j.Transition(p.ctx, p.st.Task.Key, name); err != nil {
		return err
	}
	p.note("response_jira_transition", map[string]string{"key": p.st.Task.Key, "to": name})
	return nil
}

// People names who targets, users and teams would reach for an incident (the simulation of the
// settings page).
func People(c Catalog, a alert.Alert, as Assessment, targets, userIDs, teamIDs []string) []string {
	sn := snap{cat: c}
	names := sn.audienceOf(a, as, targets, userIDs, teamIDs).names()
	if names == nil {
		names = []string{}
	}
	return names
}
