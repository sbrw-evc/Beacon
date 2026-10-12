package pagerduty_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/pagerduty"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/pagerduty/pdtest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type secrets map[string]string

func (s secrets) Resolve(ref string) (string, error) {
	if v, ok := s[ref]; ok {
		return v, nil
	}
	return "", errors.New("no secret")
}

type result struct {
	action  alert.Action
	route   string
	routeID string
	err     error
}

type results struct {
	mu      sync.Mutex
	got     []result
	inbound []alert.PDUpdate
	active  []alert.Alert
	notes   []string
}

func (r *results) PDResult(_ context.Context, _ string, action alert.Action, route, routeID string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, result{action, route, routeID, err})
}
func (r *results) PDInbound(_ context.Context, u alert.PDUpdate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inbound = append(r.inbound, u)
	return nil
}
func (r *results) PDKeys(context.Context, string, string) ([]string, error) {
	return nil, nil
}
func (r *results) PDActive(context.Context, int) ([]alert.Alert, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]alert.Alert(nil), r.active...), nil
}
func (r *results) Note(_ context.Context, _, _, code string, args map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notes = append(r.notes, code+" "+args["error"])
	return nil
}

func (r *results) wait(t *testing.T, n int) []result {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.mu.Lock()
		got := append([]result(nil), r.got...)
		r.mu.Unlock()
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("results = %+v, want %d", got, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDelivery(t *testing.T) {
	fake := pdtest.New(t)
	st := store.New()
	st.Write(func(d *store.Data) {
		d.Settings.Alerting.PagerDuty = model.PagerDuty{Enabled: true, EventsURL: fake.EventsURL(), RoutingKeyRef: "ref", MinSeverity: "error"}
	})
	g := pagerduty.New(st, secrets{"ref": "key-1"})
	g.Backoff = time.Millisecond
	res := &results{}
	g.SetResults(res)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	a := alert.Alert{ID: "INC-1", PD: alert.PD{Key: "umb-INC-1"}, Title: "Down", Severity: "critical", Sources: map[string]*alert.Source{}}
	g.Send(alert.Command{Action: alert.PDTrigger, Alert: a})
	if got := res.wait(t, 1); got[0].err != nil || got[0].route != pagerduty.DefaultRoute || len(fake.Events()) != 1 || fake.Events()[0].RoutingKey != "key-1" {
		t.Fatalf("delivered: %+v %+v", got, fake.Events())
	}
	warn := a
	warn.Severity = "warning"
	g.Send(alert.Command{Action: alert.PDTrigger, Alert: warn})
	if got := res.wait(t, 2); !errors.Is(got[1].err, alert.ErrPDSkipped) {
		t.Errorf("below the threshold: %+v", got[1])
	}
	fake.SetEventsStatus(http.StatusServiceUnavailable)
	g.Send(alert.Command{Action: alert.PDResolve, Alert: a})
	if got := res.wait(t, 3); got[2].err == nil {
		t.Errorf("5xx is an error after retries: %+v", got[2])
	}
	if s := g.Status(); s.Sent != 1 || s.Failed != 1 || s.LastError == "" {
		t.Errorf("status = %+v", s)
	}
	// Two more failing deliveries of 3 attempts open the breaker.
	g.Send(alert.Command{Action: alert.PDResolve, Alert: a})
	g.Send(alert.Command{Action: alert.PDResolve, Alert: a})
	res.wait(t, 5)
	if !g.Status().BreakerOpen {
		t.Error("the breaker opens after 5 failures in a row")
	}
	st.Write(func(d *store.Data) { d.Settings.Alerting.PagerDuty.Enabled = false })
	g.Send(alert.Command{Action: alert.PDTrigger, Alert: a})
	if got := res.wait(t, 6); !errors.Is(got[5].err, pagerduty.ErrDisabled) {
		t.Errorf("disabled: %+v", got[5])
	}
}

func TestSignature(t *testing.T) {
	body := []byte(`{"event":{}}`)
	sig := pdtest.Sign("s", body)
	if !pagerduty.VerifySignature("s", body, "v1=00,"+sig) {
		t.Error("one of several signatures matches")
	}
	if pagerduty.VerifySignature("", body, sig) || pagerduty.VerifySignature("other", body, sig) {
		t.Error("no secret or a wrong one fails")
	}
}

// The webhook hands the time of the change to the engine, which ignores changes older than the
// latest opening of the alert.
func TestWebhookOccurredAt(t *testing.T) {
	st := store.New()
	st.Write(func(d *store.Data) {
		d.Settings.Alerting.PagerDuty = model.PagerDuty{Enabled: true, WebhookSecretRef: "wh"}
	})
	g := pagerduty.New(st, secrets{"wh": "s"})
	res := &results{}
	g.SetResults(res)
	body := []byte(`{"event":{"id":"E1","event_type":"incident.resolved","occurred_at":"2026-10-05T12:00:01.250Z",
		"data":{"id":"Q1","type":"incident","html_url":"https://pd/incidents/Q1","incident_key":"umb-INC-1"}}}`)
	n, err := g.HandleWebhook(context.Background(), body, pdtest.Sign("s", body))
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	u := res.inbound[0]
	if u.DedupKey != "umb-INC-1" || u.IncidentID != "Q1" || !u.OccurredAt.Equal(time.Date(2026, 10, 5, 12, 0, 1, 250e6, time.UTC)) {
		t.Errorf("update = %+v", u)
	}
}

func TestBuildSeverity(t *testing.T) {
	for sev, want := range map[string]string{"critical": "critical", "error": "error", "warning": "warning", "low": "info", "info": "info"} {
		a := alert.Alert{ID: "INC-1", PD: alert.PD{Key: "umb-INC-1"}, Title: "Down", Severity: sev, Sources: map[string]*alert.Source{}}
		ev := pagerduty.Build("", false, false, "key", alert.Command{Action: alert.PDTrigger, Alert: a})
		if ev.Payload == nil || ev.Payload.Severity != want {
			t.Errorf("%s: payload severity = %+v, want %s", sev, ev.Payload, want)
		}
	}
}

// With a public address the event links to the incident, and to Grafana and Umbrella Flow
// when they are set up, all through Umbrella.
func TestBuildLinks(t *testing.T) {
	a := alert.Alert{ID: "INC-1", PD: alert.PD{Key: "umb-INC-1"}, Title: "Down", Severity: "critical", Sources: map[string]*alert.Source{}}
	links := func(grafana, flow bool) []string {
		var out []string
		for _, l := range pagerduty.Build("https://umb.example/", grafana, flow, "key", alert.Command{Action: alert.PDTrigger, Alert: a}).Links {
			out = append(out, l.Href)
		}
		return out
	}
	if got := links(false, false); len(got) != 1 || got[0] != "https://umb.example/incidents?id=INC-1" {
		t.Errorf("links = %v", got)
	}
	if got := links(false, true); len(got) != 2 || got[1] != "https://umb.example/go/incidents/INC-1/flow" {
		t.Errorf("links = %v", got)
	}
	if got := links(true, true); len(got) != 3 || got[1] != "https://umb.example/go/incidents/INC-1/grafana" {
		t.Errorf("links = %v", got)
	}
}
