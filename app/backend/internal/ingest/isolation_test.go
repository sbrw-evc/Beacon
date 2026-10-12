package ingest_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/ingest"
)

func runner(p *flow.Pipeline) ingest.Processor {
	return func(ctx context.Context, r ingest.Request) (ingest.Outcome, error) {
		res, err := p.Run(ctx, flow.Input{RequestID: fmt.Sprint(r.ID), Body: r.Body}, flow.RunOptions{})
		return ingest.Outcome{Version: r.Version, Result: res}, err
	}
}

func enqueue(t *testing.T, q *ingest.Queue, connector, body string) int64 {
	t.Helper()
	id, _, err := q.Enqueue(context.Background(), ingest.Request{ConnectorID: connector, Version: 1, Body: []byte(body)}, "")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func request(t *testing.T, q *ingest.Queue, connector string, id int64) ingest.Request {
	t.Helper()
	r, err := q.Request(context.Background(), connector, id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// A body that is not UTF-8 and an event with \u0000 used to fail the INSERTs of the whole batch
// (SQLSTATE 22021) on every drain, stopping ingest for every connector.
func TestPoisonBatch(t *testing.T) {
	ctx := context.Background()
	db := pool(t)
	q := ingest.New(db)
	var mu sync.Mutex
	var folded []string
	q.SetSink(func(ctx context.Context, tx pgx.Tx, connectorID string, events []flow.Event) (func(), error) {
		mu.Lock()
		defer mu.Unlock()
		for _, e := range events {
			folded = append(folded, connectorID+":"+e.Title)
		}
		return nil, nil
	})
	process := runner(pipeline(t))

	good1 := enqueue(t, q, "CON-1", `{"alerts":[{"id":"a1","name":"CPU","host":"db-01","sev":"critical"}]}`)
	cp1251 := enqueue(t, q, "CON-1", "\xff\xfe \xcf\xf0\xee\xe1\xeb\xe5\xec\xe0 not json")
	nul := enqueue(t, q, "CON-2", `{"alerts":[{"id":"n\u00001","name":"Disk\u0000","host":"db\u0000-02","sev":"error","extra":"x\u0000"}]}`)
	good2 := enqueue(t, q, "CON-2", `{"alerts":[{"id":"a2","name":"Memory","host":"db-03","sev":"warning"}]}`)

	n, err := q.Drain(ctx, process)
	if err != nil || n != 4 {
		t.Fatalf("the batch with poison requests is processed: n=%d err=%v", n, err)
	}
	if n, err := q.Drain(ctx, process); err != nil || n != 0 {
		t.Fatalf("nothing is left pending: n=%d err=%v", n, err)
	}
	for _, c := range []struct {
		connector string
		id        int64
	}{{"CON-1", good1}, {"CON-2", nul}, {"CON-2", good2}} {
		if r := request(t, q, c.connector, c.id); r.Status != ingest.StatusDone || r.Events != 1 {
			t.Errorf("request %d: %+v", c.id, r)
		}
	}
	bad := request(t, q, "CON-1", cp1251)
	if bad.Status != ingest.StatusFailed || !strings.Contains(bad.Error, "not valid UTF-8") {
		t.Errorf("the cp1251 request fails with a reason: %+v", bad)
	}
	fails, err := q.Failures(ctx, "CON-1", false, 10)
	if err != nil || len(fails) != 1 || fails[0].RequestID != cp1251 || !strings.Contains(fails[0].Raw, "�") ||
		!strings.Contains(fails[0].Error, "not valid UTF-8") {
		t.Errorf("the failure keeps a readable body: %+v, %v", fails, err)
	}
	ev, _ := q.Events(ctx, "CON-2", 10)
	titles := map[string]string{}
	for _, e := range ev {
		titles[e.Title] = e.CI
	}
	if titles["Disk"] != "db-02" || titles["Memory"] != "db-03" {
		t.Errorf("NUL characters are dropped from the event: %+v", ev)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(folded, ",") != "CON-1:CPU,CON-2:Disk,CON-2:Memory" {
		t.Errorf("folded = %v", folded)
	}
}

// An error folding the events of a request used to be logged while the request was marked done:
// the alert was lost and nothing could be reprocessed.
func TestFoldingErrorIsNotLost(t *testing.T) {
	ctx := context.Background()
	db := pool(t)
	q := ingest.New(db)
	var mu sync.Mutex
	folded := map[string]int{}
	ready := false
	q.SetSink(func(ctx context.Context, tx pgx.Tx, connectorID string, events []flow.Event) (func(), error) {
		// The sink writes in the request's transaction before it fails, like the alert engine.
		if _, err := tx.Exec(ctx, "SELECT 1"); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case connectorID == "BROKEN":
			_, err := tx.Exec(ctx, "SELECT 1/0") // aborts the transaction, not only the request
			return nil, err
		case connectorID == "LATE" && !ready, connectorID == "DOWN":
			return nil, errors.New("the alert tables are not ready yet")
		}
		return func() { mu.Lock(); folded[connectorID] += len(events); mu.Unlock() }, nil
	})
	process := runner(pipeline(t))
	body := `{"alerts":[{"id":"a1","name":"CPU","host":"db-01","sev":"critical"}]}`
	late := enqueue(t, q, "LATE", body)
	broken := enqueue(t, q, "BROKEN", body)
	down := enqueue(t, q, "DOWN", body)
	ok := enqueue(t, q, "OK", body)

	if n, err := q.Drain(ctx, process); err != nil || n != 4 {
		t.Fatalf("drain: n=%d err=%v", n, err)
	}
	if r := request(t, q, "OK", ok); r.Status != ingest.StatusDone {
		t.Errorf("the healthy request is done: %+v", r)
	}
	if r := request(t, q, "LATE", late); r.Status != ingest.StatusPending || r.Attempts != 1 || !strings.Contains(r.Error, "not ready") {
		t.Errorf("a request that could not be folded waits for a retry: %+v", r)
	}
	if ev, _ := q.Events(ctx, "LATE", 10); len(ev) != 0 {
		t.Errorf("its events are rolled back with it: %+v", ev)
	}
	if r := request(t, q, "BROKEN", broken); r.Status != ingest.StatusFailed || !strings.Contains(r.Error, "division by zero") {
		t.Errorf("a request rejected by the database fails at once: %+v", r)
	}
	if f, _ := q.Failures(ctx, "BROKEN", false, 10); len(f) != 1 || f[0].Node != "alerts" {
		t.Errorf("the failure names the alerts step: %+v", f)
	}

	mu.Lock()
	ready = true
	mu.Unlock()
	if n, err := q.Drain(ctx, process); err != nil || n != 2 {
		t.Fatalf("the retries are claimed again: n=%d err=%v", n, err)
	}
	if r := request(t, q, "LATE", late); r.Status != ingest.StatusDone || r.Attempts != 2 {
		t.Errorf("the retry folds the request: %+v", r)
	}
	if _, err := q.Drain(ctx, process); err != nil {
		t.Fatal(err)
	}
	if r := request(t, q, "DOWN", down); r.Status != ingest.StatusFailed || r.Attempts != 3 {
		t.Errorf("retries are bounded: %+v", r)
	}
	if f, _ := q.Failures(ctx, "DOWN", false, 10); len(f) != 1 || f[0].Node != "alerts" || !strings.Contains(f[0].Error, "not ready") {
		t.Errorf("the request can be reprocessed: %+v", f)
	}
	if n, _ := q.Drain(ctx, process); n != 0 {
		t.Errorf("nothing is left pending, got %d", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if folded["OK"] != 1 || folded["LATE"] != 1 || folded["BROKEN"] != 0 || folded["DOWN"] != 0 {
		t.Errorf("folded = %v", folded)
	}
}
