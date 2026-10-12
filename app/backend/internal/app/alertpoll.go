package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/ingest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/monitoring"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// pollable: the kinds of systems whose alerts can be polled.
func pollable(kind string) bool {
	return kind == model.MonitoringGrafana || kind == model.MonitoringGraylog
}

// Polling the alerts of Grafana (Graylog: graylogpoll.go): besides its contact point (a webhook to the connector),
// Umbrella can read the firing alerts through the Alerting API. Each poll hands the alerts that
// began to fire and those that stopped to the connector of the system, in the body a Grafana
// webhook contact point sends, so one connector serves both ways and folds them into the same
// incidents. Only changes are sent: an alert that keeps firing is not delivered again.

const (
	// pollHeader marks a request made by the poll; it holds the ID of the monitoring system.
	pollHeader = "x-umbrella-poll"
	// pollBatch is the most alerts one request carries.
	pollBatch   = 200
	pollTimeout = 2 * time.Minute
)

var (
	ErrPollRunning = errors.New("the alerts of this system are already being read")
	ErrPollOff     = errors.New("the system is not Grafana or Graylog or has no connector for its alerts")
)

func init() {
	sensitiveHeaders[pollHeader] = true
	statuses = append(statuses,
		orgStatus{ErrPollRunning, http.StatusConflict, "poll_running"},
		orgStatus{ErrPollOff, http.StatusConflict, "poll_off"},
	)
}

type alertPoller struct {
	a       *App
	read    func(ctx context.Context, src model.MonitoringSource, auth *monitoring.Auth) (monitoring.GrafanaReading, error)
	graylog func(ctx context.Context, src model.MonitoringSource, auth *monitoring.Auth, span time.Duration) (monitoring.GraylogReading, error)
	now     func() time.Time
	mu      sync.Mutex
	state   map[string]model.MonitoringPoll
	running map[string]bool
}

func newAlertPoller(a *App) *alertPoller {
	return &alertPoller{a: a, read: monitoring.ReadGrafana, graylog: monitoring.ReadGraylog, now: func() time.Time { return time.Now().UTC() },
		state: map[string]model.MonitoringPoll{}, running: map[string]bool{}}
}

func (p *alertPoller) status(id string) *model.MonitoringPoll {
	p.mu.Lock()
	defer p.mu.Unlock()
	if st, ok := p.state[id]; ok {
		return &st
	}
	return nil
}

// Run polls the systems on their intervals until ctx ends.
func (p *alertPoller) Run(ctx context.Context) {
	tk := time.NewTicker(5 * time.Second)
	defer tk.Stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			for _, id := range p.due() {
				wg.Go(func() {
					if _, err := p.Poll(ctx, id); err != nil && !errors.Is(err, ErrPollRunning) {
						slog.Error("monitoring: polling alerts failed", "source", id, "err", err)
					}
				})
			}
		}
	}
}

func (p *alertPoller) due() []string {
	now := p.now()
	var out []string
	p.a.deps.Store.Read(func(d *store.Data) {
		for id, src := range d.MonitoringSources {
			if !src.Enabled || !src.PollAlerts || !pollable(src.Kind) || src.ConnectorID == "" {
				continue
			}
			last := p.status(id)
			if last == nil || !now.Before(last.At.Add(time.Duration(max(src.PollSeconds, minPollSeconds))*time.Second)) {
				out = append(out, id)
			}
		}
	})
	slices.Sort(out)
	return out
}

// Poll reads the alerts of one system and hands the changes to its connector. A failure to
// read or to hand over is the returned state; the error is for a poll that cannot start.
func (p *alertPoller) Poll(ctx context.Context, id string) (model.MonitoringPoll, error) {
	var (
		src model.MonitoringSource
		con model.Connector
		ok  bool
	)
	p.a.deps.Store.Read(func(d *store.Data) {
		s := d.MonitoringSources[id]
		if s == nil {
			return
		}
		src = *s
		src.Polled = maps.Clone(s.Polled)
		if c := d.Connectors[s.ConnectorID]; c != nil && pollable(s.Kind) {
			con, ok = *c, true
		}
	})
	if src.ID == "" {
		return model.MonitoringPoll{}, ErrNotFound
	}
	if !ok {
		return model.MonitoringPoll{}, ErrPollOff
	}
	p.mu.Lock()
	if p.running[id] {
		p.mu.Unlock()
		return model.MonitoringPoll{}, ErrPollRunning
	}
	p.running[id] = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.running, id)
		p.mu.Unlock()
	}()

	st := model.MonitoringPoll{At: p.now()}
	err := p.poll(ctx, src, con, &st)
	if err != nil {
		st.Error = err.Error()
	} else {
		st.OK = true
	}
	p.mu.Lock()
	p.state[id] = st
	p.mu.Unlock()
	return st, nil
}

func (p *alertPoller) poll(ctx context.Context, src model.MonitoringSource, con model.Connector, st *model.MonitoringPoll) error {
	if con.Published == 0 {
		return errors.New("the connector of the system is not published")
	}
	if !p.a.ingestReady() {
		return ErrIngestDown
	}
	auth, err := p.a.monitoring.auth(src.CredentialID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()
	if src.Kind == model.MonitoringGraylog {
		return p.pollGraylog(ctx, src, con, auth, st)
	}
	reading, err := p.read(ctx, src, auth)
	if err != nil {
		return err
	}
	now := p.now()
	firing := map[string]monitoring.GrafanaAlert{}
	for _, a := range reading.Alerts {
		if a.Firing() {
			firing[a.Fingerprint] = a
		}
	}
	st.Firing = len(firing)
	var send []grafanaWebhookAlert
	for _, fp := range slices.Sorted(maps.Keys(firing)) {
		if _, known := src.Polled[fp]; !known {
			send = append(send, webhookAlert(src.URL, firing[fp], now))
		}
	}
	for _, fp := range slices.Sorted(maps.Keys(src.Polled)) {
		if _, still := firing[fp]; !still {
			send = append(send, resolvedAlert(src.URL, fp, src.Polled[fp], now))
		}
	}
	for i := 0; i < len(send); i += pollBatch {
		batch := send[i:min(i+pollBatch, len(send))]
		if _, _, err := p.a.queue.Enqueue(ctx, ingest.Request{ConnectorID: con.ID, Version: con.Published, RemoteIP: "127.0.0.1",
			Method: http.MethodPost, Headers: map[string]string{"content-type": "application/json", pollHeader: src.ID},
			Body: webhookBody(src.URL, batch)}, ""); err != nil {
			// What was handed over stays handed over; the rest is sent by the next poll.
			p.remember(src.ID, firing, send[:i])
			return fmt.Errorf("%w: %v", ErrIngestDown, err)
		}
		st.Sent += len(batch)
	}
	p.remember(src.ID, firing, send)
	return nil
}

// remember keeps the alerts that were handed over as firing and forgets the resolved ones. It
// writes the store only when that changes something.
func (p *alertPoller) remember(id string, firing map[string]monitoring.GrafanaAlert, sent []grafanaWebhookAlert) {
	if len(sent) == 0 {
		return
	}
	p.a.deps.Store.Write(func(d *store.Data) {
		src := d.MonitoringSources[id]
		if src == nil {
			return
		}
		if src.Polled == nil {
			src.Polled = map[string]model.PolledAlert{}
		}
		for _, w := range sent {
			if w.Status == "resolved" {
				delete(src.Polled, w.Fingerprint)
				continue
			}
			a := firing[w.Fingerprint]
			src.Polled[w.Fingerprint] = model.PolledAlert{Labels: a.Labels, Annotations: a.Annotations, StartsAt: w.startsAt, RuleUID: a.RuleUID, Value: a.Value}
		}
	})
}

// grafanaWebhookAlert is an alert as a Grafana webhook contact point sends it.
type grafanaWebhookAlert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     string            `json:"startsAt"`
	EndsAt       string            `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
	SilenceURL   string            `json:"silenceURL"`
	DashboardURL string            `json:"dashboardURL"`
	PanelURL     string            `json:"panelURL"`
	ValueString  string            `json:"valueString"`

	startsAt time.Time
}

const zeroTime = "0001-01-01T00:00:00Z"

func webhookAlert(base string, a monitoring.GrafanaAlert, now time.Time) grafanaWebhookAlert {
	rule, dash, panel := a.Links(base)
	starts := a.ActiveAt
	if starts.IsZero() {
		starts = now
	}
	return grafanaWebhookAlert{Status: "firing", Labels: a.PublicLabels(), Annotations: a.PublicAnnotations(),
		StartsAt: starts.Format(time.RFC3339), EndsAt: zeroTime, GeneratorURL: rule, Fingerprint: a.Fingerprint,
		DashboardURL: dash, PanelURL: panel, ValueString: a.Value, startsAt: starts}
}

func resolvedAlert(base, fp string, prev model.PolledAlert, now time.Time) grafanaWebhookAlert {
	a := monitoring.GrafanaAlert{Fingerprint: fp, RuleUID: prev.RuleUID, Labels: prev.Labels, Annotations: prev.Annotations,
		ActiveAt: prev.StartsAt, Value: prev.Value}
	w := webhookAlert(base, a, now)
	w.Status, w.EndsAt = "resolved", now.Format(time.RFC3339)
	return w
}

func webhookBody(base string, alerts []grafanaWebhookAlert) []byte {
	status := "resolved"
	for _, a := range alerts {
		if a.Status == "firing" {
			status = "firing"
			break
		}
	}
	body := map[string]any{
		"receiver": "umbrella-poll", "status": status, "orgId": 1, "alerts": alerts,
		"groupLabels": map[string]string{}, "commonLabels": map[string]string{}, "commonAnnotations": map[string]string{},
		"externalURL": base, "version": "1", "groupKey": "umbrella-poll", "truncatedAlerts": 0,
		"title": "", "state": map[bool]string{true: "alerting", false: "ok"}[status == "firing"], "message": "",
	}
	b, _ := json.Marshal(body)
	return b
}

func (a *App) pollMonitoringSource(w http.ResponseWriter, r *http.Request) {
	out, err := a.alertPoll.Poll(context.WithoutCancel(r.Context()), r.PathValue("id"))
	reply(w, http.StatusOK, out, err)
}
