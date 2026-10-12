package ingest_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/ingest"
)

func statusPipeline(t *testing.T) *flow.Pipeline {
	t.Helper()
	g := flow.Graph{
		Nodes: []flow.Node{
			{ID: "in", Type: "trigger.webhook", TypeVersion: 1, Params: map[string]any{"anonymous": true}},
			{ID: "parse", Type: "parse.json", TypeVersion: 1, Params: map[string]any{"items": "alerts"}},
			{ID: "map", Type: "map.event", TypeVersion: 1, Params: map[string]any{"title": "${name}", "ci": "${host}",
				"severity": "${sev}", "status": "${status}", "external_id": "${id}"}},
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

type recorder struct {
	mu  sync.Mutex
	got []string
}

func (r *recorder) sink(ctx context.Context, tx pgx.Tx, connectorID string, events []flow.Event) (func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range events {
		r.got = append(r.got, e.ExternalID+":"+e.Status)
	}
	return nil, nil
}

func (r *recorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.got, ",")
}

const (
	firing   = `{"alerts":[{"id":"x1","name":"CPU","host":"db-01","sev":"error","status":"firing"}]}`
	resolved = `{"alerts":[{"id":"x1","name":"CPU","host":"db-01","sev":"error","status":"resolved"}]}`
)

// Two workers used to claim different batches (SKIP LOCKED) and fold them in whatever order
// they reached the alert engine: a resolved received later could be folded before its firing,
// which then opened an alert that nothing closes.
func TestWorkersFoldInArrivalOrder(t *testing.T) {
	ctx := context.Background()
	db := pool(t)
	q := ingest.New(db)
	rec := &recorder{}
	q.SetSink(rec.sink)
	run := runner(statusPipeline(t))

	first := enqueue(t, q, "CON-1", firing)
	started, release := make(chan struct{}), make(chan struct{})
	slow := func(ctx context.Context, r ingest.Request) (ingest.Outcome, error) {
		if r.ID == first {
			close(started)
			<-release // worker A is still running the pipeline of the firing
		}
		return run(ctx, r)
	}
	errs := make(chan error, 2)
	go func() { _, err := q.Drain(ctx, slow); errs <- err }()
	<-started

	enqueue(t, q, "CON-1", resolved)
	bDone := make(chan struct{})
	go func() { _, err := q.Drain(ctx, run); errs <- err; close(bDone) }()
	select {
	case <-bDone:
		t.Errorf("worker B finished while the batch before it was open; folded: %s", rec)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got := rec.String(); got != "x1:firing,x1:resolved" {
		t.Errorf("folded out of arrival order: %s", got)
	}
	ev, _ := q.Events(ctx, "CON-1", 10)
	if len(ev) != 1 || ev[0].Status != flow.StatusResolved {
		t.Errorf("events = %+v", ev)
	}
}

// An event older than the state already stored for its source is never applied, whichever
// way it comes late (a request committed late, a reprocessed request).
func TestStaleEventIsNotApplied(t *testing.T) {
	ctx := context.Background()
	db := pool(t)
	q := ingest.New(db)
	rec := &recorder{}
	q.SetSink(rec.sink)
	run := runner(statusPipeline(t))

	old := enqueue(t, q, "CON-1", firing)
	enqueue(t, q, "CON-1", resolved)
	if _, err := q.Drain(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "UPDATE ingest_requests SET status = 'pending' WHERE id = $1", old); err != nil {
		t.Fatal(err)
	}
	if n, err := q.Drain(ctx, run); err != nil || n != 1 {
		t.Fatalf("drain: %d %v", n, err)
	}
	if got := rec.String(); got != "x1:firing,x1:resolved" {
		t.Errorf("the old firing is folded again: %s", got)
	}
	ev, _ := q.Events(ctx, "CON-1", 10)
	if len(ev) != 1 || ev[0].Status != flow.StatusResolved {
		t.Errorf("the stored state went back: %+v", ev)
	}
	if r := request(t, q, "CON-1", old); r.Status != ingest.StatusDone {
		t.Errorf("the stale request is done: %+v", r)
	}
}

// Reprocessing a request used to fold all of its events again with the current time: a firing
// the source had since resolved, or one folded the first time, opened or reopened an alert.
func TestReprocessOnlyAppliesNews(t *testing.T) {
	ctx := context.Background()
	db := pool(t)
	q := ingest.New(db)
	rec := &recorder{}
	q.SetSink(rec.sink)
	run := runner(statusPipeline(t))

	// x1 fired and was resolved later; y1 still fires. Both requests have a broken record.
	enqueue(t, q, "CON-1", `{"alerts":[{"id":"x1","name":"CPU","host":"db-01","sev":"error","status":"firing"},
		{"id":"x2","name":"Disk","host":"db-01","sev":"bogus","status":"firing"}]}`)
	enqueue(t, q, "CON-1", `{"alerts":[{"id":"y1","name":"Memory","host":"db-02","sev":"warning","status":"firing"},
		{"id":"y2","name":"Swap","host":"db-02","sev":"bogus","status":"firing"}]}`)
	enqueue(t, q, "CON-1", resolved)
	if _, err := q.Drain(ctx, run); err != nil {
		t.Fatal(err)
	}
	if got := rec.String(); got != "x1:firing,y1:firing,x1:resolved" {
		t.Fatalf("folded: %s", got)
	}
	out, err := q.Reprocess(ctx, "CON-1", []int64{}, 1)
	if err != nil || out.Requeued != 2 {
		t.Fatalf("reprocess: %+v %v", out, err)
	}
	if n, err := q.Drain(ctx, run); err != nil || n != 2 {
		t.Fatalf("drain: %d %v", n, err)
	}
	if got := rec.String(); got != "x1:firing,y1:firing,x1:resolved" {
		t.Errorf("reprocessing folded old events again: %s", got)
	}
	ev, _ := q.Events(ctx, "CON-1", 10)
	state := map[string]string{}
	for _, e := range ev {
		state[e.ExternalID] = e.Status
	}
	if state["x1"] != flow.StatusResolved || state["y1"] != flow.StatusFiring {
		t.Errorf("events = %v", state)
	}
}
