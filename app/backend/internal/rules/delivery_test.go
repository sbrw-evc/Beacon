package rules_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/rules"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// flaky is an alert engine whose database can be down.
type flaky struct {
	mu   sync.Mutex
	down bool
	got  []alert.Incoming
}

func (s *flaky) Ingest(_ context.Context, ev []alert.Incoming) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.down {
		return errors.New("connection refused")
	}
	s.got = append(s.got, ev...)
	return nil
}

func (s *flaky) set(down bool) { s.mu.Lock(); s.down = down; s.mu.Unlock() }

func (s *flaky) statuses() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, e := range s.got {
		out = append(out, e.Status)
	}
	return out
}

// The series state used to become firing (or be removed) before its event reached the alert
// engine: with the database down the event was lost and never sent again.
func TestRuleEventsSurviveSinkOutage(t *testing.T) {
	p := &prom{values: map[string]float64{"db-01:9100": 95}}
	srv := httptest.NewServer(http.HandlerFunc(p.handler))
	t.Cleanup(srv.Close)
	st := store.New()
	r := model.Rule{Name: "CPU", Method: "use", SourceID: "MS-1", Query: "cpu", Op: ">", Threshold: 90, For: "0s", Severity: "warning", Enabled: true}
	if err := rules.Normalize(&r); err != nil {
		t.Fatal(err)
	}
	r.ID = "R-1"
	st.Write(func(d *store.Data) {
		d.MetricSources["MS-1"] = &model.MetricSource{ID: "MS-1", Name: "Prometheus", URL: srv.URL}
		d.Rules["R-1"] = &r
	})
	e := rules.New(st, func(string) (rules.Auth, error) { return rules.Auth{}, nil })
	out := &flaky{down: true}
	e.SetSink(out)
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	e.SetClock(func() time.Time { return now })
	ctx := context.Background()
	rule := func() model.Rule {
		var cur model.Rule
		st.Read(func(d *store.Data) { cur = *d.Rules["R-1"] })
		return cur
	}

	if err := e.Evaluate(ctx, "R-1"); err == nil {
		t.Error("a failed delivery is reported")
	}
	if cur := rule(); cur.Firing != 0 || cur.Pending != 1 {
		t.Fatalf("the series is not firing while its event is not applied: %+v", cur)
	}
	out.set(false)
	if err := e.Evaluate(ctx, "R-1"); err != nil {
		t.Fatal(err)
	}
	if got := out.statuses(); len(got) != 1 || got[0] != "firing" || rule().Firing != 1 {
		t.Fatalf("the firing is sent again: %v %+v", got, rule())
	}

	p.set(map[string]float64{})
	out.set(true)
	_ = e.Evaluate(ctx, "R-1")
	if cur := rule(); cur.Firing != 1 {
		t.Fatalf("the series keeps firing while its resolved is not applied: %+v", cur)
	}
	out.set(false)
	if err := e.Evaluate(ctx, "R-1"); err != nil {
		t.Fatal(err)
	}
	if got := out.statuses(); len(got) != 2 || got[1] != "resolved" || rule().Firing != 0 || len(rule().State) != 0 {
		t.Fatalf("the resolved is sent again: %v %+v", got, rule())
	}

	// A released rule has no state to recompute from: its events are kept and retried.
	p.set(map[string]float64{"db-01:9100": 95})
	if err := e.Evaluate(ctx, "R-1"); err != nil {
		t.Fatal(err)
	}
	out.set(true)
	e.Release(ctx, rule())
	st.Write(func(d *store.Data) { delete(d.Rules, "R-1") })
	e.Tick(ctx)
	if got := out.statuses(); len(got) != 3 {
		t.Fatalf("nothing is delivered while the engine is down: %v", got)
	}
	out.set(false)
	e.Tick(ctx)
	if got := out.statuses(); len(got) != 4 || got[3] != "resolved" {
		t.Fatalf("the release is delivered once the engine is back: %v", got)
	}
	e.Tick(ctx)
	if got := out.statuses(); len(got) != 4 {
		t.Errorf("the release is delivered once: %v", got)
	}
}
