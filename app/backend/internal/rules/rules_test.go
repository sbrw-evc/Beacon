package rules_test

import (
	"context"
	"encoding/json"
	"fmt"
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

type prom struct {
	mu     sync.Mutex
	values map[string]float64 // instance -> value
	auth   string
	query  string
}

func (p *prom) handler(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.URL.Path != "/api/v1/query" {
		http.NotFound(w, r)
		return
	}
	p.auth = r.Header.Get("Authorization")
	_ = r.ParseForm()
	p.query = r.Form.Get("query")
	res := []map[string]any{}
	for inst, v := range p.values {
		res = append(res, map[string]any{"metric": map[string]string{"__name__": "x", "instance": inst, "job": "node"},
			"value": []any{1700000000, fmt.Sprint(v)}})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": res}})
}

func (p *prom) set(v map[string]float64) {
	p.mu.Lock()
	p.values = v
	p.mu.Unlock()
}

type sink struct {
	mu  sync.Mutex
	got []alert.Incoming
}

func (s *sink) Ingest(_ context.Context, ev []alert.Incoming) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, ev...)
	return nil
}

func TestRuleFiresAndResolves(t *testing.T) {
	p := &prom{values: map[string]float64{"db-01:9100": 95, "db-02:9100": 40}}
	srv := httptest.NewServer(http.HandlerFunc(p.handler))
	t.Cleanup(srv.Close)
	st := store.New()
	r := model.Rule{Name: "CPU", Method: "use", SourceID: "MS-1", Query: "cpu", Op: ">", Threshold: 90, For: "1m", Severity: "warning", Enabled: true}
	if err := rules.Normalize(&r); err != nil {
		t.Fatal(err)
	}
	r.ID = "R-1"
	if r.Signal != "use.cpu" || r.CILabel != "instance" || r.Interval != "30s" {
		t.Fatalf("defaults: %+v", r)
	}
	st.Write(func(d *store.Data) {
		d.MetricSources["MS-1"] = &model.MetricSource{ID: "MS-1", Name: "Prometheus", URL: srv.URL, CredentialID: "C-1"}
		d.Rules["R-1"] = &r
	})
	e := rules.New(st, func(id string) (rules.Auth, error) {
		return rules.Auth{Type: "bearer", Secrets: map[string]string{"token": "prom-token"}}, nil
	})
	out := &sink{}
	e.SetSink(out)
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	e.SetClock(func() time.Time { return now })

	pv := e.Preview(context.Background(), r)
	if pv.Error != "" || pv.Total != 2 || pv.Matched != 1 || pv.Series[0].CI != "db-01" || pv.Series[0].Title != "CPU: db-01 = 95" {
		t.Fatalf("preview: %+v", pv)
	}
	if p.auth != "Bearer prom-token" || p.query != "cpu" {
		t.Fatalf("request: %q %q", p.auth, p.query)
	}

	// Pending until the condition has lasted a minute.
	v := st.Version()
	e.Tick(context.Background())
	if len(out.got) != 0 {
		t.Fatalf("fired too early: %+v", out.got)
	}
	if st.Version() == v {
		t.Fatal("the pending series was not recorded")
	}
	now = now.Add(30 * time.Second)
	v = st.Version()
	e.Tick(context.Background())
	if st.Version() != v {
		t.Error("an evaluation that changed nothing wrote the store")
	}
	now = now.Add(31 * time.Second)
	e.Tick(context.Background())
	if len(out.got) != 1 || out.got[0].Status != "firing" || out.got[0].CI != "db-01" || out.got[0].ConnectorID != "rule:R-1" ||
		out.got[0].Method != "use" || out.got[0].Signal != "use.cpu" || out.got[0].Value != "95" {
		t.Fatalf("fired: %+v", out.got)
	}
	var cur model.Rule
	st.Read(func(d *store.Data) { cur = *d.Rules["R-1"] })
	if cur.Firing != 1 || cur.SeriesCount != 2 {
		t.Fatalf("counts: %+v", cur)
	}

	// The series goes away: resolved with the same key.
	p.set(map[string]float64{"db-02:9100": 40})
	now = now.Add(30 * time.Second)
	e.Tick(context.Background())
	if len(out.got) != 2 || out.got[1].Status != "resolved" || out.got[1].Key != out.got[0].Key {
		t.Fatalf("resolved: %+v", out.got)
	}

	// Errors are recorded once.
	srv.Close()
	now = now.Add(30 * time.Second)
	e.Tick(context.Background())
	st.Read(func(d *store.Data) { cur = *d.Rules["R-1"] })
	if cur.LastError == "" {
		t.Fatal("no error recorded")
	}
}

func TestNormalize(t *testing.T) {
	bad := []model.Rule{
		{Method: "use", SourceID: "s", Query: "q", Op: ">", Severity: "info"},
		{Name: "x", Method: "nope", SourceID: "s", Query: "q", Op: ">", Severity: "info"},
		{Name: "x", Method: "red", SourceID: "s", Query: "q", Op: "=~", Severity: "info"},
		{Name: "x", Method: "red", SourceID: "s", Query: "q", Op: ">", Severity: "info", Interval: "1s"},
		{Name: "x", Method: "red", SourceID: "s", Query: "q", Op: ">", Severity: "loud"},
	}
	for i, r := range bad {
		if err := rules.Normalize(&r); err == nil {
			t.Errorf("rule %d passed", i)
		}
	}
	for _, r := range rules.Templates() {
		r.SourceID = "s"
		if err := rules.Normalize(&r); err != nil {
			t.Errorf("template %s: %v", r.Name, err)
		}
	}
}
