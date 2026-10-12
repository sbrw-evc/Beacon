package ingest_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/ingest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store/storetest"
)

func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg := storetest.TempDatabase(t)
	p, err := pgxpool.New(context.Background(), cfg.DSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	ctx := context.Background()
	if err := ingest.EnsureSchema(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := ingest.EnsureSchema(ctx, p); err != nil {
		t.Fatalf("the schema is created idempotently: %v", err)
	}
	return p
}

func pipeline(t *testing.T) *flow.Pipeline {
	t.Helper()
	g := flow.Graph{
		Nodes: []flow.Node{
			{ID: "in", Type: "trigger.webhook", TypeVersion: 1, Params: map[string]any{"anonymous": true}},
			{ID: "parse", Type: "parse.json", TypeVersion: 1, Params: map[string]any{"items": "alerts"}},
			{ID: "map", Type: "map.event", TypeVersion: 1, Params: map[string]any{"title": "${name}", "ci": "${host}", "severity": "${sev}", "external_id": "${id}"}},
			{ID: "out", Type: "out.event", TypeVersion: 1},
		},
		Edges: []flow.Edge{{ID: "1", Source: "in", Target: "parse"}, {ID: "2", Source: "parse", Target: "map"}, {ID: "3", Source: "map", Target: "out"}},
	}
	p, issues := flow.Compile(g, flow.CompileOptions{})
	if p == nil {
		t.Fatal(issues)
	}
	return p
}

func TestQueue(t *testing.T) {
	ctx := context.Background()
	db := pool(t)
	q := ingest.New(db)
	p := pipeline(t)
	process := func(ctx context.Context, r ingest.Request) (ingest.Outcome, error) {
		if r.ConnectorID == "gone" {
			return ingest.Outcome{}, fmt.Errorf("the connector does not exist")
		}
		res, err := p.Run(ctx, flow.Input{RequestID: fmt.Sprint(r.ID), Body: r.Body}, flow.RunOptions{})
		return ingest.Outcome{Version: r.Version, Result: res}, err
	}
	body := `{"alerts":[{"id":"a1","name":"CPU","host":"db-01","sev":"critical"},{"id":"a2","name":"Disk","host":"db-02","sev":"bogus"}]}`
	id1, dup, err := q.Enqueue(ctx, ingest.Request{ConnectorID: "CON-1", Version: 1, RemoteIP: "10.0.0.1", Method: "POST",
		Headers: map[string]string{"content-type": "application/json"}, Body: []byte(body)}, "k1")
	if err != nil || dup {
		t.Fatal(err, dup)
	}
	if _, dup, _ := q.Enqueue(ctx, ingest.Request{ConnectorID: "CON-1", Version: 1, Body: []byte(body)}, "k1"); !dup {
		t.Error("the idempotency key suppresses a repeated request")
	}
	if _, _, err := q.Enqueue(ctx, ingest.Request{ConnectorID: "CON-1", Version: 1, Body: []byte(body)}, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := q.Enqueue(ctx, ingest.Request{ConnectorID: "gone", Version: 1, Body: []byte("{}")}, ""); err != nil {
		t.Fatal(err)
	}
	n, err := q.Drain(ctx, process)
	if err != nil || n != 3 {
		t.Fatalf("drain = %d, %v", n, err)
	}
	if n, _ := q.Drain(ctx, process); n != 0 {
		t.Errorf("processed requests are not claimed again, got %d", n)
	}

	events, err := q.Events(ctx, "CON-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Title != "CPU" || events[0].Seen != 2 || events[0].Severity != "critical" {
		t.Errorf("events = %+v", events)
	}
	reqs, err := q.Requests(ctx, "CON-1", "", 10)
	if err != nil || len(reqs) != 2 {
		t.Fatalf("requests = %+v, %v", reqs, err)
	}
	if reqs[0].Status != ingest.StatusFailed || reqs[0].Events != 1 || reqs[0].Attempts != 1 {
		t.Errorf("request = %+v", reqs[0])
	}
	full, err := q.Request(ctx, "CON-1", id1)
	if err != nil || string(full.Body) != body || full.Headers["content-type"] != "application/json" || full.RemoteIP != "10.0.0.1" {
		t.Errorf("request %d = %+v, %v", id1, full, err)
	}
	if _, err := q.Request(ctx, "CON-2", id1); err != ingest.ErrNotFound {
		t.Error("requests are scoped to their connector")
	}

	fails, err := q.Failures(ctx, "CON-1", false, 10)
	if err != nil || len(fails) != 2 || fails[0].Node != "map" || fails[0].Item != 1 || fails[0].Data["sev"] != "bogus" {
		t.Fatalf("failures = %+v, %v", fails, err)
	}
	gone, _ := q.Failures(ctx, "gone", false, 10)
	if len(gone) != 1 || gone[0].Node != "" {
		t.Errorf("a processing error is a failure too: %+v", gone)
	}

	stats, err := q.Stats(ctx, "CON-1", time.Now().Add(-time.Hour), time.Minute)
	if err != nil || len(stats) != 1 {
		t.Fatalf("stats = %+v, %v", stats, err)
	}
	if s := stats[0]; s.Received != 2 || s.Events != 1 || s.Duplicates != 1 || s.Failed != 2 {
		t.Errorf("bucket = %+v", s)
	}
	q.Reject("CON-1")
	sum, err := q.Summaries(ctx)
	if err != nil || sum["CON-1"].Received != 2 || sum["CON-1"].OpenFailures != 2 || sum["CON-1"].LastReceived == nil {
		t.Errorf("summary = %+v, %v", sum, err)
	}
	if o, err := q.Overview(ctx); err != nil || o.Pending != 0 || o.Received != 3 || o.OpenFailures != 3 || o.Events != 1 || o.LastReceived == nil {
		t.Errorf("overview = %+v, %v", o, err)
	}

	out, err := q.Reprocess(ctx, "CON-1", []int64{fails[0].ID}, 2)
	if err != nil || out.Requeued != 1 {
		t.Fatalf("reprocess = %+v, %v", out, err)
	}
	if left, _ := q.Failures(ctx, "CON-1", false, 10); len(left) != 1 {
		t.Errorf("the reprocessed failure is closed, open = %d", len(left))
	}
	if n, _ := q.Drain(ctx, process); n != 1 {
		t.Errorf("the request is processed again, got %d", n)
	}
	if left, _ := q.Failures(ctx, "CON-1", false, 10); len(left) != 2 {
		t.Errorf("the same record fails again with the same pipeline, open = %d", len(left))
	}
	reqs, _ = q.Requests(ctx, "CON-1", "", 10)
	for _, r := range reqs {
		if r.ID == fails[0].RequestID && (r.Version != 2 || r.Attempts != 2) {
			t.Errorf("the request ran on version 2: %+v", r)
		}
	}

	if err := q.Forget(ctx, "CON-1"); err != nil {
		t.Fatal(err)
	}
	if ev, _ := q.Events(ctx, "CON-1", 10); len(ev) != 0 {
		t.Error("forget removes the events of a deleted connector")
	}
}

func TestMaintain(t *testing.T) {
	ctx := context.Background()
	db := pool(t)
	old := time.Now().UTC().AddDate(0, 0, -10).Truncate(24 * time.Hour)
	name := "ingest_requests_" + old.Format("20060102")
	if _, err := db.Exec(ctx, fmt.Sprintf("CREATE TABLE %s PARTITION OF ingest_requests FOR VALUES FROM ('%s') TO ('%s')",
		name, old.Format(time.RFC3339), old.AddDate(0, 0, 1).Format(time.RFC3339))); err != nil {
		t.Fatal(err)
	}
	if err := ingest.Maintain(ctx, db, time.Now().UTC(), ingest.DefaultRetention); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := db.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", name).Scan(&exists); err != nil || exists {
		t.Errorf("the expired partition is dropped: %v %v", exists, err)
	}
	ahead := "ingest_requests_" + time.Now().UTC().AddDate(0, 0, 3).Format("20060102")
	if err := db.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", ahead).Scan(&exists); err != nil || !exists {
		t.Errorf("partitions are created ahead: %v %v", exists, err)
	}
}

func TestResolvedClosesFiring(t *testing.T) {
	ctx := context.Background()
	db := pool(t)
	q := ingest.New(db)
	g := flow.Graph{
		Nodes: []flow.Node{
			{ID: "in", Type: "trigger.webhook", TypeVersion: 1, Params: map[string]any{"anonymous": true}},
			{ID: "parse", Type: "parse.json", TypeVersion: 1, Params: map[string]any{}},
			{ID: "map", Type: "map.event", TypeVersion: 1, Params: map[string]any{"title": "${name}", "ci": "${host}", "signal": "${signal}",
				"severity": "${sev}", "status": "${status}", "external_id": "${id}"}},
			{ID: "out", Type: "out.event", TypeVersion: 1},
		},
		Edges: []flow.Edge{{ID: "1", Source: "in", Target: "parse"}, {ID: "2", Source: "parse", Target: "map"}, {ID: "3", Source: "map", Target: "out"}},
	}
	p, issues := flow.Compile(g, flow.CompileOptions{})
	if p == nil {
		t.Fatal(issues)
	}
	process := func(ctx context.Context, r ingest.Request) (ingest.Outcome, error) {
		res, err := p.Run(ctx, flow.Input{RequestID: fmt.Sprint(r.ID), Body: r.Body}, flow.RunOptions{})
		return ingest.Outcome{Version: r.Version, Result: res}, err
	}
	for _, body := range []string{
		`{"id":"z1","name":"Problem: CPU","host":"app-01","sev":"error","status":"firing"}`,
		`{"id":"z1","name":"Problem: CPU","host":"app-01","sev":"error","status":"firing"}`,
		`{"id":"z1","name":"Resolved: CPU","host":"app-01","sev":"error","status":"resolved"}`,
		`{"name":"Disk","host":"db-01","signal":"disk","sev":"warning","status":"firing"}`,
		`{"name":"Disk is fine again","host":"db-01","signal":"disk","sev":"warning","status":"ok"}`,
	} {
		if _, _, err := q.Enqueue(ctx, ingest.Request{ConnectorID: "CON-1", Version: 1, Body: []byte(body)}, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := q.Drain(ctx, process); err != nil {
			t.Fatal(err)
		}
	}
	events, err := q.Events(ctx, "CON-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("firing and resolved of one alert are one event, got %+v", events)
	}
	for _, e := range events {
		if e.Status != flow.StatusResolved {
			t.Errorf("resolved replaced firing: %+v", e)
		}
	}
	if firing, err := q.Firing(ctx, time.Now().Add(-time.Hour)); err != nil || len(firing) != 0 {
		t.Errorf("nothing fires after recovery: %+v, %v", firing, err)
	}
	stats, _ := q.Stats(ctx, "CON-1", time.Now().Add(-time.Hour), time.Hour)
	if len(stats) != 1 || stats[0].Events != 4 || stats[0].Duplicates != 1 {
		t.Errorf("status changes are events, only the repeated firing is a duplicate: %+v", stats)
	}
}
