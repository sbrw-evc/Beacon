// Package pagerduty delivers alerts to PagerDuty with the Events API v2 and takes incident
// status changes back from Webhooks v3. It also uses the REST API to find services and their
// integration keys and to subscribe to webhooks.
package pagerduty

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sbrw-evc/beacon/app/backend/internal/alert"
	"github.com/sbrw-evc/beacon/app/backend/internal/model"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
	"github.com/sbrw-evc/beacon/app/backend/internal/textx"
)

// Delivery errors carry a code the interface translates (alert.DeliveryError). ErrDisabled is
// not a failure: the alert is then "off".
var (
	ErrDisabled   = alert.ErrPDOff
	ErrNoKey      = &alert.DeliveryError{Code: "no_key", Msg: "no Events API v2 integration key is set"}
	ErrNoAPIToken = errors.New("no PagerDuty REST API token is set")
	ErrQueueFull  = &alert.DeliveryError{Code: "queue_full", Msg: "the PagerDuty queue is full"}
	ErrBreaker    = &alert.DeliveryError{Code: "breaker", Msg: "the circuit breaker is open after a series of failures"}
)

const DefaultRoute = "default"

// Resolver reads a secret by its openbao:// reference.
type Resolver interface {
	Resolve(ref string) (string, error)
}

// Results is where the gateway reports deliveries and incident changes: the alert engine.
type Results interface {
	PDResult(ctx context.Context, alertID string, action alert.Action, route, routeID string, err error)
	PDInbound(ctx context.Context, u alert.PDUpdate) error
	PDKeys(ctx context.Context, incidentKey, incidentID string) ([]string, error)
	// PDActive lists active alerts PagerDuty has an incident for, for the read-back.
	PDActive(ctx context.Context, limit int) ([]alert.Alert, error)
	// Note records a line on the timeline of an alert.
	Note(ctx context.Context, id, kind, code string, args map[string]string) error
}

type Status struct {
	Enabled       bool       `json:"enabled"`
	Configured    bool       `json:"configured"`
	BreakerOpen   bool       `json:"breaker_open"`
	ConsecFails   int        `json:"consecutive_failures"`
	Sent          int        `json:"sent"`
	Failed        int        `json:"failed"`
	Queue         int        `json:"queue"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	LastErrorAt   *time.Time `json:"last_error_at,omitempty"`
	LastWebhookAt *time.Time `json:"last_webhook_at,omitempty"`
	// Read-back of incident states through the REST API.
	LastSyncAt    *time.Time `json:"last_sync_at,omitempty"`
	LastSyncError string     `json:"last_sync_error,omitempty"`
	SyncApplied   int        `json:"sync_applied"`
	// OnCallAt is when the on-call people were last read.
	OnCallAt *time.Time `json:"on_call_at,omitempty"`
	// QueuesAt is when the queues were last read; QueuesError why the last read failed.
	QueuesAt    *time.Time `json:"queues_at,omitempty"`
	QueuesError string     `json:"queues_error,omitempty"`
}

type Gateway struct {
	st      *store.Store
	sec     Resolver
	results Results
	client  *http.Client
	queue   chan alert.Command
	Retries int
	Backoff time.Duration

	mu          sync.Mutex
	stat        Status
	breakerTill time.Time
	// onCall is who is on call for each route (DefaultRoute for the default integration).
	onCall map[string][]OnCall
	// priorities are the IDs of the PagerDuty priorities by lower-case name.
	priorities map[string]string
	// syncMu serializes the read-back runs (the loop and «Synchronize now»).
	syncMu sync.Mutex
	// users finds Beacon users by e-mail, for the on-call people.
	users UserFinder
	// queues are the PagerDuty services as queues, as last read; queueHook runs after a read.
	queues    []Queue
	queueHook QueueHook
}

// UserFinder finds the Beacon user of a PagerDuty user by e-mail; nil without one.
type UserFinder func(email string) *model.User

func New(st *store.Store, sec Resolver) *Gateway {
	return &Gateway{st: st, sec: sec, client: &http.Client{Timeout: DefaultHTTPTimeout}, queue: make(chan alert.Command, DefaultQueueSize),
		Retries: DefaultRetries, Backoff: DefaultBackoff}
}

func (g *Gateway) SetResults(r Results) { g.results = r }

func (g *Gateway) settings() (model.PagerDuty, model.Alerting) {
	var s model.Alerting
	g.st.Read(func(d *store.Data) {
		s = d.Settings.Alerting
		s.PagerDuty.Routes = slices.Clone(d.Settings.Alerting.PagerDuty.Routes)
	})
	return s.PagerDuty, s
}

// Send queues a command; Run delivers it.
func (g *Gateway) Send(cmd alert.Command) {
	select {
	case g.queue <- cmd:
	default:
		g.report(cmd, "", "", ErrQueueFull)
	}
}

func (g *Gateway) Status() Status {
	set, _ := g.settings()
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.stat
	s.Enabled = set.Enabled
	s.Configured = set.RoutingKeyRef != "" || len(set.Routes) > 0
	s.Queue = len(g.queue)
	s.BreakerOpen = time.Now().Before(g.breakerTill)
	return s
}

func (g *Gateway) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd := <-g.queue:
			g.deliver(ctx, cmd)
		}
	}
}

type permanent struct{ error }

func (p permanent) Unwrap() error { return p.error }

func (g *Gateway) deliver(ctx context.Context, cmd alert.Command) {
	if cmd.Action == alert.PDNote {
		g.note(ctx, cmd)
		return
	}
	set, all := g.settings()
	if !set.Enabled {
		g.report(cmd, "", "", ErrDisabled)
		return
	}
	if min := set.MinSeverity; cmd.Action == alert.PDTrigger && min != "" && alert.SeverityRank(cmd.Alert.Severity) < alert.SeverityRank(min) {
		g.report(cmd, "", "", &alert.DeliveryError{Code: "below_threshold", Detail: min, Err: alert.ErrPDSkipped})
		return
	}
	routeName, routeID, ref := deliveryRoute(set, cmd.Alert)
	if ref == "" {
		g.report(cmd, routeName, routeID, ErrNoKey)
		return
	}
	key, err := g.sec.Resolve(ref)
	if err != nil {
		g.report(cmd, routeName, routeID, &alert.DeliveryError{Code: "key_unavailable", Msg: "integration key", Detail: err.Error(), Err: err})
		return
	}
	ev := Build(all.PublicURL, all.Grafana.DashboardURL != "", all.Flow.URL != "", key, cmd)
	backoff := g.Backoff
	for attempt := 0; attempt < g.Retries; attempt++ {
		if g.breakerOpen() {
			err = ErrBreaker
			break
		}
		err = g.post(ctx, set.Events(), ev)
		var perm permanent
		if err == nil || errors.As(err, &perm) {
			break
		}
		g.fail(err)
		if attempt == g.Retries-1 {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	g.mu.Lock()
	now := time.Now().UTC()
	if err == nil {
		g.stat.Sent++
		g.stat.ConsecFails = 0
		g.stat.LastSuccessAt = &now
	} else {
		g.stat.Failed++
		g.stat.LastError, g.stat.LastErrorAt = err.Error(), &now
	}
	g.mu.Unlock()
	g.report(cmd, routeName, routeID, err)
}

// Route picks the PagerDuty service of an alert: the first route from the top whose team and
// business service match the team and the primary service of the alert route (an empty field
// matches any), otherwise the default integration key.
func Route(set model.PagerDuty, a alert.Alert) (string, string) {
	name, _, ref := currentRoute(set, a)
	return name, ref
}

// RouteOf names the route a new trigger of the alert would take: its ID (DefaultRoute for the
// default integration) and name.
func RouteOf(set model.PagerDuty, a alert.Alert) (id, name string) {
	name, id, _ = currentRoute(set, a)
	return id, name
}

// deliveryRoute is the route of a command: the one the accepted trigger went by while it
// exists, because PagerDuty has the incident in that service and the alert may have been routed
// to another team since; otherwise the current route.
func deliveryRoute(set model.PagerDuty, a alert.Alert) (name, id, ref string) {
	switch id := a.PD.RouteID; {
	case id == "":
	case id == DefaultRoute:
		if set.RoutingKeyRef != "" {
			return DefaultRoute, DefaultRoute, set.RoutingKeyRef
		}
	default:
		for _, r := range set.Routes {
			if r.ID == id && r.RoutingKeyRef != "" {
				return r.Name, r.ID, r.RoutingKeyRef
			}
		}
	}
	return currentRoute(set, a)
}

func currentRoute(set model.PagerDuty, a alert.Alert) (name, id, ref string) {
	team := ""
	if a.Route.Team != nil {
		team = a.Route.Team.ID
	}
	// A route matches the primary service of the alert route, the one its team comes from, so
	// PagerDuty and backup notification agree on who owns the incident. Alerts routed before the
	// primary service was recorded match any of their services.
	services := a.Route.ServiceIDs()
	if a.Route.Service != nil {
		services = []string{a.Route.Service.ID}
	}
	for _, r := range set.Routes {
		if r.TeamID == "" && r.ServiceID == "" {
			continue
		}
		if (r.TeamID == "" || r.TeamID == team) && (r.ServiceID == "" || slices.Contains(services, r.ServiceID)) {
			return r.Name, r.ID, r.RoutingKeyRef
		}
	}
	return DefaultRoute, DefaultRoute, set.RoutingKeyRef
}

func (g *Gateway) report(cmd alert.Command, route, routeID string, err error) {
	if g.results != nil {
		g.results.PDResult(context.Background(), cmd.Alert.ID, cmd.Action, route, routeID, err)
	}
}

func (g *Gateway) breakerOpen() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return time.Now().Before(g.breakerTill)
}

func (g *Gateway) fail(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stat.ConsecFails++
	now := time.Now().UTC()
	g.stat.LastError, g.stat.LastErrorAt = err.Error(), &now
	if g.stat.ConsecFails >= DefaultBreakerThreshold {
		g.breakerTill = now.Add(DefaultBreakerPause)
		g.stat.ConsecFails = 0
		slog.Warn("pagerduty circuit breaker opened", "err", err)
	}
}

type Event struct {
	RoutingKey  string   `json:"routing_key"`
	EventAction string   `json:"event_action"`
	DedupKey    string   `json:"dedup_key"`
	Payload     *Payload `json:"payload,omitempty"`
	Links       []Link   `json:"links,omitempty"`
	Client      string   `json:"client,omitempty"`
	ClientURL   string   `json:"client_url,omitempty"`
}

type Payload struct {
	Summary       string         `json:"summary"`
	Source        string         `json:"source"`
	Severity      string         `json:"severity"`
	Timestamp     string         `json:"timestamp,omitempty"`
	Component     string         `json:"component,omitempty"`
	Group         string         `json:"group,omitempty"`
	Class         string         `json:"class,omitempty"`
	CustomDetails map[string]any `json:"custom_details,omitempty"`
}

type Link struct {
	Href string `json:"href"`
	Text string `json:"text"`
}

func people(list []alert.Person) []string {
	out := []string{}
	for _, p := range list {
		v := p.Name
		if p.Email != "" {
			v += " <" + p.Email + ">"
		}
		if p.Role != "" {
			v += " (" + p.Role + ")"
		}
		out = append(out, strings.TrimSpace(v))
	}
	return out
}

// Build makes the Events API v2 event of a command. Only trigger carries the payload.
func Build(publicURL string, grafana, flow bool, routingKey string, cmd alert.Command) Event {
	a := cmd.Alert
	ev := Event{RoutingKey: routingKey, EventAction: string(cmd.Action), DedupKey: a.PD.Key, Client: "Beacon"}
	base := strings.TrimRight(publicURL, "/")
	if base != "" {
		ev.ClientURL = model.IncidentURL(base, a.ID)
	}
	if cmd.Action != alert.PDTrigger {
		return ev
	}
	sources := make([]string, 0, len(a.Sources))
	for _, s := range a.Sources {
		if !slices.Contains(sources, s.ConnectorID) {
			sources = append(sources, s.ConnectorID)
		}
	}
	slices.Sort(sources)
	source := a.CIName
	if source == "" {
		source = "umbrella"
	}
	services := []string{}
	for _, s := range a.Route.Services {
		services = append(services, s.Name)
	}
	details := map[string]any{
		"umbrella_id": a.ID,
		"method":      a.Method,
		"ci_id":       a.CIID,
		"ci_kind":     a.CIKind,
		"services":    services,
		"count":       a.Count,
		"sources":     sources,
		"people":      people(a.Route.People),
		"owners":      people(a.Route.Owners),
	}
	if a.Route.Team != nil {
		details["team"] = a.Route.Team.Name
	}
	if a.RelatedID != "" {
		details["related"] = a.RelatedID
	}
	if len(a.Labels) > 0 {
		details["labels"] = a.Labels
	}
	// The description and fields of the newest firing source, the description cut short: the
	// incident in Beacon has the whole of it.
	var latest *alert.Source
	for _, s := range a.Sources {
		if s.Status == alert.SourceFiring && (latest == nil || s.LastSeen.After(latest.LastSeen)) {
			latest = s
		}
	}
	if latest != nil {
		if latest.Description != "" {
			details["description"] = truncate(latest.Description, 4000)
		}
		for _, f := range latest.Fields {
			if _, taken := details[f.Name]; !taken {
				details[f.Name] = f.Value
			}
		}
	}
	group := ""
	if len(services) > 0 {
		group = services[0]
	}
	ev.Payload = &Payload{
		Summary:       truncate(fmt.Sprintf("[%s] %s", strings.ToUpper(a.Severity), a.Title), 1024),
		Source:        truncate(source, 255),
		Severity:      eventSeverity(a.Severity),
		Timestamp:     a.OpenedAt.UTC().Format(time.RFC3339),
		Component:     a.CIName,
		Group:         group,
		Class:         a.Signal,
		CustomDetails: details,
	}
	if base != "" {
		ev.Links = []Link{{Href: model.IncidentURL(base, a.ID), Text: "Beacon incident"}}
		if grafana {
			ev.Links = append(ev.Links, Link{Href: model.GrafanaHopURL(base, a.ID), Text: "Incident context in Grafana"})
		}
		if flow {
			ev.Links = append(ev.Links, Link{Href: model.FlowHopURL(base, a.ID), Text: "Traffic in Current"})
		}
	}
	return ev
}

func (g *Gateway) post(ctx context.Context, url string, ev Event) error {
	body, _ := json.Marshal(ev)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return permanent{err}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		return &alert.DeliveryError{Code: "unreachable", Msg: "PagerDuty is not reachable", Detail: err.Error(), Err: err}
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	switch {
	case resp.StatusCode/100 == 2:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return &alert.DeliveryError{Code: "unavailable", Msg: "Events API answered",
			Detail: textx.Runes(strings.TrimSpace(fmt.Sprintf("%d %s", resp.StatusCode, msg)), 300)}
	default:
		return permanent{&alert.DeliveryError{Code: "rejected", Msg: "Events API rejected the event",
			Detail: textx.Runes(strings.TrimSpace(fmt.Sprintf("%d %s", resp.StatusCode, eventsError(msg))), 300)}}
	}
}

func eventsError(msg []byte) string {
	var e struct {
		Message string   `json:"message"`
		Errors  []string `json:"errors"`
	}
	if json.Unmarshal(msg, &e) == nil && (e.Message != "" || len(e.Errors) > 0) {
		return strings.TrimSpace(e.Message + " " + strings.Join(e.Errors, "; "))
	}
	return strings.TrimSpace(string(msg))
}

// SendTest triggers and resolves a test event with the default integration key, or with the
// given key before it is saved.
func (g *Gateway) SendTest(ctx context.Context, key string) error {
	set, _ := g.settings()
	if key == "" {
		if set.RoutingKeyRef == "" {
			return ErrNoKey
		}
		var err error
		if key, err = g.sec.Resolve(set.RoutingKeyRef); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	a := alert.Alert{ID: "TEST", PD: alert.PD{Key: fmt.Sprintf("umb-test-%d", now.Unix())}, Title: "Beacon → PagerDuty connection test",
		CIName: "umbrella", Severity: "info", Signal: "umbrella.test", OpenedAt: now, Sources: map[string]*alert.Source{}}
	if err := g.post(ctx, set.Events(), Build("", false, false, key, alert.Command{Action: alert.PDTrigger, Alert: a})); err != nil {
		return err
	}
	return g.post(ctx, set.Events(), Build("", false, false, key, alert.Command{Action: alert.PDResolve, Alert: a}))
}

// truncate cuts to n characters, as the Events API counts its limits.
// eventSeverity is the payload severity of Events v2, which knows critical, error, warning
// and info only: low (P4) goes as info, so that PagerDuty urgency rules made for warning are
// not triggered by a lower priority; anything unknown goes as info too.
func eventSeverity(s string) string {
	switch s {
	case model.SeverityCritical, model.SeverityError, model.SeverityWarning:
		return s
	}
	return model.SeverityInfo
}

func truncate(s string, n int) string { return textx.Runes(s, n) }

var _ alert.Sender = (*Gateway)(nil)
