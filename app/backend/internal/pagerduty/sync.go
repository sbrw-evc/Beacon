package pagerduty

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

// Two-way synchronization through the REST API, beyond the Events API and the webhooks:
//   - incident states are read back on a schedule, so an acknowledgement or a resolution made
//     in PagerDuty reaches Umbrella even when a webhook was lost or PagerDuty cannot reach it;
//   - comments made in Umbrella become notes of the incident;
//   - the response priority (P1–P5) is set on the incident;
//   - who is on call is read, shown and given the notifications of Umbrella;
//   - the queues (services) are read and linked to the teams of Umbrella (queues.go).

var (
	ErrNoFrom       = errors.New("no PagerDuty user to write as (From e-mail) is set")
	ErrNoIncident   = errors.New("PagerDuty has no incident for the alert yet")
	ErrNoPriority   = errors.New("PagerDuty has no priority of that name (priorities may be off in the account)")
	ErrSyncDisabled = errors.New("the synchronization is turned off")
)

const (
	// syncBatch bounds the active alerts read back in one run; lookupBatch the incidents asked
	// for one by one (resolved ones no longer in the open list).
	syncBatch   = 500
	lookupBatch = 50
	// directoryEvery is how often the on-call people and the queues are read.
	directoryEvery = 5 * time.Minute
	// notePrefix marks the notes Umbrella writes, so their webhooks are not taken as news.
	notePrefix = "[Umbrella] "
)

// incident is what the read-back needs of a PagerDuty incident.
type incident struct {
	ID              string    `json:"id"`
	IncidentKey     string    `json:"incident_key"`
	Status          string    `json:"status"`
	HTMLURL         string    `json:"html_url"`
	LastStatusBy    *summary  `json:"last_status_change_by"`
	Priority        *summary  `json:"priority"`
	Service         summary   `json:"service"`
	Acknowledgments []ackInfo `json:"acknowledgements"`
}

type ackInfo struct {
	Acknowledger summary `json:"acknowledger"`
}

func (in incident) acker() string {
	if n := len(in.Acknowledgments); n > 0 {
		return in.Acknowledgments[n-1].Acknowledger.Summary
	}
	if in.LastStatusBy != nil {
		return in.LastStatusBy.Summary
	}
	return ""
}

// writer is the token and the From address of writes; an error when writing is not possible.
func (g *Gateway) writer(set model.PagerDuty) (string, error) {
	switch {
	case set.APITokenRef == "":
		return "", ErrNoAPIToken
	case strings.TrimSpace(set.Sync.FromEmail) == "":
		return "", ErrNoFrom
	}
	return strings.TrimSpace(set.Sync.FromEmail), nil
}

// incidentOf is the PagerDuty incident ID of an alert: the one known, or the one found by its
// dedup key.
func (g *Gateway) incidentOf(ctx context.Context, a alert.Alert) (string, error) {
	if a.PD.IncidentID != "" {
		return a.PD.IncidentID, nil
	}
	in, err := g.incidentByKey(ctx, a.PD.Key)
	if err != nil {
		return "", err
	}
	if in == nil {
		return "", ErrNoIncident
	}
	return in.ID, nil
}

func (g *Gateway) incidentByKey(ctx context.Context, key string) (*incident, error) {
	var resp struct {
		Incidents []incident `json:"incidents"`
	}
	q := url.Values{"incident_key": {key}, "date_range": {"all"}, "limit": {"1"}}
	if err := g.rest(ctx, "", http.MethodGet, "/incidents", q, nil, &resp); err != nil {
		return nil, err
	}
	if len(resp.Incidents) == 0 {
		return nil, nil
	}
	return &resp.Incidents[0], nil
}

// note writes a comment made in Umbrella as a note of the PagerDuty incident and records the
// outcome on the timeline. It is not a delivery: the state of the alert does not change.
func (g *Gateway) note(ctx context.Context, cmd alert.Command) {
	set, _ := g.settings()
	if !set.Enabled || !set.Sync.Notes {
		return
	}
	err := g.AddNote(ctx, set, cmd.Alert, cmd.Actor, cmd.Text)
	code, args := "pd_note", map[string]string{}
	if err != nil {
		code, args["error"] = "pd_note_failed", err.Error()
		slog.Warn("pagerduty note not added", "alert", cmd.Alert.ID, "err", err)
	}
	if g.results != nil {
		_ = g.results.Note(ctx, cmd.Alert.ID, alert.KindPagerDuty, code, args)
	}
}

// AddNote adds a note to the PagerDuty incident of an alert.
func (g *Gateway) AddNote(ctx context.Context, set model.PagerDuty, a alert.Alert, actor, text string) error {
	from, err := g.writer(set)
	if err != nil {
		return err
	}
	id, err := g.incidentOf(ctx, a)
	if err != nil {
		return err
	}
	content := notePrefix + strings.TrimSpace(actor+": "+text)
	body := map[string]any{"note": map[string]string{"content": truncate(content, 25000)}}
	return g.restAs(ctx, set, "", from, http.MethodPost, "/incidents/"+url.PathEscape(id)+"/notes", nil, body, nil)
}

// SetPriority sets a response priority on the PagerDuty incident of an alert: the PagerDuty
// priority named like it (P1–P5; a severity name is turned into its priority). It does nothing while the synchronization of
// priorities is off (ErrSyncDisabled).
func (g *Gateway) SetPriority(ctx context.Context, a alert.Alert, priority string) error {
	set, _ := g.settings()
	if !set.Enabled || !set.Sync.Priority {
		return ErrSyncDisabled
	}
	from, err := g.writer(set)
	if err != nil {
		return err
	}
	if p := model.SeverityPriority(priority); p != "" {
		priority = p
	}
	pid, err := g.priorityID(ctx, priority)
	if err != nil {
		return err
	}
	id, err := g.incidentOf(ctx, a)
	if err != nil {
		return err
	}
	body := map[string]any{"incident": map[string]any{"type": "incident_reference",
		"priority": map[string]string{"id": pid, "type": "priority_reference"}}}
	return g.restAs(ctx, set, "", from, http.MethodPut, "/incidents/"+url.PathEscape(id), nil, body, nil)
}

func (g *Gateway) priorityID(ctx context.Context, name string) (string, error) {
	key := strings.ToLower(name)
	g.mu.Lock()
	id, ok := g.priorities[key]
	g.mu.Unlock()
	if ok {
		return id, nil
	}
	var resp struct {
		Priorities []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"priorities"`
	}
	if err := g.rest(ctx, "", http.MethodGet, "/priorities", nil, nil, &resp); err != nil {
		return "", err
	}
	all := map[string]string{}
	for _, p := range resp.Priorities {
		all[strings.ToLower(p.Name)] = p.ID
	}
	g.mu.Lock()
	g.priorities = all
	g.mu.Unlock()
	if id := all[key]; id != "" {
		return id, nil
	}
	return "", ErrNoPriority
}

// SyncResult is what one read-back run did.
type SyncResult struct {
	Checked int `json:"checked"`
	Applied int `json:"applied"`
}

// Sync reads back the state of the PagerDuty incidents of active alerts and applies the
// changes the webhooks did not bring: acknowledged, resolved or unacknowledged in PagerDuty,
// and the incident itself (ID and address) when no webhook named it.
func (g *Gateway) Sync(ctx context.Context) (SyncResult, error) {
	g.syncMu.Lock()
	defer g.syncMu.Unlock()
	res, err := g.sync(ctx)
	now := time.Now().UTC()
	g.mu.Lock()
	g.stat.LastSyncAt, g.stat.SyncApplied = &now, res.Applied
	g.stat.LastSyncError = ""
	if err != nil {
		g.stat.LastSyncError = err.Error()
	}
	g.mu.Unlock()
	return res, err
}

func (g *Gateway) sync(ctx context.Context) (SyncResult, error) {
	var res SyncResult
	set, _ := g.settings()
	switch {
	case !set.Enabled:
		return res, ErrDisabled
	case set.APITokenRef == "":
		return res, ErrNoAPIToken
	case g.results == nil:
		return res, nil
	}
	alerts, err := g.results.PDActive(ctx, syncBatch)
	if err != nil || len(alerts) == 0 {
		return res, err
	}
	byKey, byID, err := g.openIncidents(ctx)
	if err != nil {
		return res, err
	}
	lookups := 0
	for _, a := range alerts {
		res.Checked++
		in := byKey[a.PD.Key]
		if in == nil && a.PD.IncidentID != "" {
			in = byID[a.PD.IncidentID]
		}
		if in == nil && a.PD.IncidentID != "" && lookups < lookupBatch {
			// Not open any more: resolved (or merged) in PagerDuty.
			lookups++
			var resp struct {
				Incident incident `json:"incident"`
			}
			if err := g.rest(ctx, "", http.MethodGet, "/incidents/"+url.PathEscape(a.PD.IncidentID), nil, nil, &resp); err == nil {
				in = &resp.Incident
			}
		}
		if in == nil {
			continue
		}
		u, ok := change(a, *in)
		if !ok {
			continue
		}
		if err := g.results.PDInbound(ctx, u); err == nil {
			res.Applied++
		} else if !errors.Is(err, alert.ErrNotFound) {
			return res, err
		}
	}
	return res, nil
}

// change is the update that brings an alert in line with its PagerDuty incident; false when
// they agree.
func change(a alert.Alert, in incident) (alert.PDUpdate, bool) {
	u := alert.PDUpdate{DedupKey: a.PD.Key, IncidentID: in.ID, IncidentURL: in.HTMLURL, Detail: "sync", Queue: in.Service.ID, QueueName: in.Service.Summary}
	switch {
	case in.Status == "resolved" && alert.Active(a.Status):
		u.EventType = "incident.resolved"
		if in.LastStatusBy != nil {
			u.Actor = in.LastStatusBy.Summary
		}
	case in.Status == "acknowledged" && a.Status == alert.StatusOpen:
		u.EventType, u.Actor = "incident.acknowledged", in.acker()
	case in.Status == "triggered" && a.Status == alert.StatusAcknowledged && a.PD.State == alert.PDAcked:
		u.EventType = "incident.unacknowledged"
	case a.PD.IncidentID == "" && in.ID != "":
		u.EventType = "incident.triggered"
	case in.Service.ID != "" && in.Service.ID != a.PD.Queue:
		// Moved to another queue (service) in PagerDuty: no change of state.
		u.EventType = ""
	default:
		return u, false
	}
	return u, true
}

// openIncidents are the triggered and acknowledged incidents of the account, by incident key
// and by ID.
func (g *Gateway) openIncidents(ctx context.Context) (map[string]*incident, map[string]*incident, error) {
	byKey, byID := map[string]*incident{}, map[string]*incident{}
	for page := 0; page < maxPages; page++ {
		q := url.Values{"statuses[]": {"triggered", "acknowledged"}, "date_range": {"all"},
			"limit": {strconv.Itoa(pageLimit)}, "offset": {strconv.Itoa(page * pageLimit)}}
		var resp struct {
			Incidents []incident `json:"incidents"`
			More      bool       `json:"more"`
		}
		if err := g.rest(ctx, "", http.MethodGet, "/incidents", q, nil, &resp); err != nil {
			return nil, nil, err
		}
		for i := range resp.Incidents {
			in := &resp.Incidents[i]
			byID[in.ID] = in
			if in.IncidentKey != "" {
				byKey[in.IncidentKey] = in
			}
		}
		if !resp.More {
			break
		}
	}
	return byKey, byID, nil
}

// OnCall is a person on call in PagerDuty for a route.
type OnCall struct {
	Name   string     `json:"name"`
	Email  string     `json:"email"`
	Level  int        `json:"level"`
	Policy string     `json:"policy"`
	Until  *time.Time `json:"until,omitempty"`
	// UserID is the Umbrella user with the same e-mail, if any.
	UserID string `json:"user_id,omitempty"`
}

// SetUsers lets the on-call people be matched with Umbrella users.
func (g *Gateway) SetUsers(f UserFinder) { g.users = f }

// RefreshOnCall reads who is on call for the PagerDuty services of the default integration
// and the routes (those picked from the list of services: a typed key names no service).
func (g *Gateway) RefreshOnCall(ctx context.Context) error {
	set, _ := g.settings()
	if !set.Enabled || set.APITokenRef == "" {
		g.mu.Lock()
		g.onCall = nil
		g.mu.Unlock()
		return nil
	}
	services := map[string]string{} // route ID → PagerDuty service ID
	if set.ServiceID != "" {
		services[DefaultRoute] = set.ServiceID
	}
	for _, r := range set.Routes {
		if r.PDServiceID != "" {
			services[r.ID] = r.PDServiceID
		}
	}
	policyOf := map[string]string{}
	for _, sid := range services {
		if _, ok := policyOf[sid]; ok {
			continue
		}
		var resp struct {
			Service struct {
				EscalationPolicy summary `json:"escalation_policy"`
			} `json:"service"`
		}
		if err := g.rest(ctx, "", http.MethodGet, "/services/"+url.PathEscape(sid), nil, nil, &resp); err != nil {
			return err
		}
		policyOf[sid] = resp.Service.EscalationPolicy.ID
	}
	q := url.Values{"include[]": {"users"}, "earliest": {"true"}, "limit": {"100"}}
	for _, p := range policyOf {
		if p != "" && !slices.Contains(q["escalation_policy_ids[]"], p) {
			q.Add("escalation_policy_ids[]", p)
		}
	}
	byPolicy := map[string][]OnCall{}
	if len(q["escalation_policy_ids[]"]) > 0 {
		var resp struct {
			OnCalls []struct {
				EscalationLevel  int        `json:"escalation_level"`
				EscalationPolicy summary    `json:"escalation_policy"`
				End              *time.Time `json:"end"`
				User             struct {
					Name    string `json:"name"`
					Summary string `json:"summary"`
					Email   string `json:"email"`
				} `json:"user"`
			} `json:"oncalls"`
		}
		if err := g.rest(ctx, "", http.MethodGet, "/oncalls", q, nil, &resp); err != nil {
			return err
		}
		for _, o := range resp.OnCalls {
			name := o.User.Name
			if name == "" {
				name = o.User.Summary
			}
			oc := OnCall{Name: name, Email: o.User.Email, Level: o.EscalationLevel, Policy: o.EscalationPolicy.Summary, Until: o.End}
			if g.users != nil && oc.Email != "" {
				if u := g.users(oc.Email); u != nil {
					oc.UserID = u.ID
				}
			}
			byPolicy[o.EscalationPolicy.ID] = append(byPolicy[o.EscalationPolicy.ID], oc)
		}
	}
	out := map[string][]OnCall{}
	for route, sid := range services {
		list := slices.Clone(byPolicy[policyOf[sid]])
		slices.SortStableFunc(list, func(a, b OnCall) int { return a.Level - b.Level })
		out[route] = list
	}
	now := time.Now().UTC()
	g.mu.Lock()
	g.onCall = out
	g.stat.OnCallAt = &now
	g.mu.Unlock()
	return nil
}

// OnCallByRoute is who is on call for each route, as last read.
func (g *Gateway) OnCallByRoute() map[string][]OnCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string][]OnCall, len(g.onCall))
	for k, v := range g.onCall {
		out[k] = slices.Clone(v)
	}
	return out
}

// OnCallPeople are the people on call at the first level for the PagerDuty route of an alert,
// as people the notifications of Umbrella reach (with the contacts of the matching Umbrella
// user). None while the synchronization of on-call people is off.
func (g *Gateway) OnCallPeople(a alert.Alert) []alert.Person {
	set, _ := g.settings()
	if !set.Enabled || !set.Sync.OnCall {
		return nil
	}
	_, route, _ := deliveryRoute(set, a)
	g.mu.Lock()
	list := slices.Clone(g.onCall[route])
	g.mu.Unlock()
	var out []alert.Person
	for _, o := range list {
		if o.Level != 1 {
			continue
		}
		p := alert.Person{Name: o.Name, Email: o.Email, Role: "on-call"}
		if g.users != nil && o.Email != "" {
			if u := g.users(o.Email); u != nil {
				p.UserID, p.Telegram = u.ID, u.Telegram
				if u.Email != "" {
					p.Email = u.Email
				}
			}
		}
		out = append(out, p)
	}
	return out
}

// RunSync reads incident states back, the on-call people and the queues on their schedules
// until ctx ends.
func (g *Gateway) RunSync(ctx context.Context) {
	var lastSync, lastDirectory time.Time
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		set, _ := g.settings()
		if !set.Enabled || set.APITokenRef == "" {
			continue
		}
		now := time.Now()
		if every := set.Sync.Interval(); every > 0 && now.Sub(lastSync) >= every {
			lastSync = now
			if _, err := g.Sync(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("pagerduty read-back failed", "err", err)
			}
		}
		if now.Sub(lastDirectory) >= directoryEvery {
			lastDirectory = now
			if err := g.RefreshOnCall(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("pagerduty on-call not read", "err", err)
			}
			if err := g.RefreshQueues(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("pagerduty queues not read", "err", err)
			}
		}
	}
}

// isOwnNote tells a note Umbrella wrote itself.
func isOwnNote(content string) bool { return strings.HasPrefix(content, notePrefix) }
