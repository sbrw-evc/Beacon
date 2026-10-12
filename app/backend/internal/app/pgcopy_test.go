package app_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/config"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/ingest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets/secretstest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store/storetest"
)

func incoming(key, signal string) alert.Incoming {
	return alert.Incoming{ConnectorID: "CON-1", Key: key, Title: signal, CI: "host-1", Signal: signal, Method: "other", Severity: "critical", Status: "firing"}
}

func pool(t *testing.T, cfg store.PGConfig) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(context.Background(), cfg.DSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	if err := ingest.EnsureSchema(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err := alert.EnsureSchema(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

// A database switch carries the incidents, their timelines, the link key and the intake queue,
// and incident numbers continue where the source stopped.
func TestPostgresMigrationCopiesAlertsAndQueue(t *testing.T) {
	ctx := context.Background()
	src, dst, used := storetest.TempDatabase(t), storetest.TempDatabase(t), storetest.TempDatabase(t)
	_, vault := secretstest.New(t)

	backend, err := store.OpenPostgres(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	st := store.New()
	if _, err := st.Attach(ctx, backend); err != nil {
		t.Fatal(err)
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	sp := pool(t, src)
	engine := alert.New(sp, st)
	for _, ev := range []alert.Incoming{incoming("k1", "cpu"), incoming("k2", "disk"), incoming("k3", "mem")} {
		if err := engine.Ingest(ctx, []alert.Incoming{ev}); err != nil {
			t.Fatal(err)
		}
	}
	if err := engine.Note(ctx, "INC-2", "comment", "test.note", map[string]string{"text": "checked"}); err != nil {
		t.Fatal(err)
	}
	_, srcTimeline, err := engine.Get(ctx, "INC-2")
	if err != nil {
		t.Fatal(err)
	}
	key, err := alert.LinkKey(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	reqID, _, err := ingest.New(sp).Enqueue(ctx, ingest.Request{ConnectorID: "CON-1", Version: 1, Method: "POST", Body: []byte(`{"pending":true}`)}, "idem-1")
	if err != nil {
		t.Fatal(err)
	}

	// A database Umbrella ran on before, without a snapshot: its alerts are Umbrella data too.
	up := pool(t, used)
	if err := alert.New(up, store.New()).Ingest(ctx, []alert.Incoming{incoming("stale", "stale")}); err != nil {
		t.Fatal(err)
	}

	rt := &recordingRuntime{}
	svc := app.NewPostgresService(st, backend, vault, rt, config.File{Postgres: backend.Config()})
	if probe := svc.Probe(ctx, target(used)); !probe.OK || !probe.Probe.HasState {
		t.Fatalf("a database with alerts must ask for confirmation: %+v", probe)
	}
	if _, err := svc.Migrate(ctx, "admin", app.PostgresMigration{Target: target(used)}); inputCode(err) != "postgres_has_state" {
		t.Fatalf("target with alerts = %v", err)
	}

	for _, to := range []struct {
		cfg       store.PGConfig
		overwrite bool
	}{{dst, false}, {used, true}} {
		if _, err := svc.Migrate(ctx, "admin", app.PostgresMigration{Target: target(to.cfg), Overwrite: to.overwrite}); err != nil {
			t.Fatalf("%s: %v", to.cfg.Database, err)
		}
		tp := pool(t, to.cfg)
		moved := alert.New(tp, st)
		page, err := moved.List(ctx, alert.Filter{Status: "active"})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Alerts) != 3 {
			t.Fatalf("%s: active alerts = %+v", to.cfg.Database, page.Alerts)
		}
		for _, a := range page.Alerts {
			if a.Title == "stale" {
				t.Fatalf("%s: alerts of the replaced database must be gone", to.cfg.Database)
			}
		}
		_, timeline, err := moved.Get(ctx, "INC-2")
		if err != nil || len(timeline) != len(srcTimeline) || timeline[len(timeline)-1].Code != "test.note" {
			t.Fatalf("%s: timeline = %+v %v, want %+v", to.cfg.Database, timeline, err, srcTimeline)
		}
		if got, err := alert.LinkKey(ctx, tp); err != nil || !bytes.Equal(got, key) {
			t.Fatalf("%s: acknowledgement links must stay valid: %v", to.cfg.Database, err)
		}
		var status string
		var body []byte
		if err := tp.QueryRow(ctx, "SELECT status, body FROM ingest_requests WHERE id = $1", reqID).Scan(&status, &body); err != nil ||
			status != "pending" || string(body) != `{"pending":true}` {
			t.Fatalf("%s: pending request = %q %q %v", to.cfg.Database, status, body, err)
		}
		if _, dup, err := ingest.New(tp).Enqueue(ctx, ingest.Request{ConnectorID: "CON-1", Version: 1, Body: []byte("{}")}, "idem-1"); err != nil || !dup {
			t.Fatalf("%s: idempotency keys must move: dup=%v %v", to.cfg.Database, dup, err)
		}
		next, _, err := ingest.New(tp).Enqueue(ctx, ingest.Request{ConnectorID: "CON-1", Version: 1, Body: []byte("{}")}, "")
		if err != nil || next <= reqID {
			t.Fatalf("%s: request ids must continue: %d after %d (%v)", to.cfg.Database, next, reqID, err)
		}
		if err := moved.Ingest(ctx, []alert.Incoming{incoming("k4", "net")}); err != nil {
			t.Fatal(err)
		}
		page, _ = moved.List(ctx, alert.Filter{Status: "active", Query: "net"})
		if len(page.Alerts) != 1 || page.Alerts[0].ID != "INC-4" || page.Alerts[0].PD.Key != "umb-INC-4" {
			t.Fatalf("%s: incident numbers must continue: %+v", to.cfg.Database, page.Alerts)
		}
		if err := moved.Note(ctx, "INC-2", "comment", "test.after", nil); err != nil {
			t.Fatalf("%s: timeline ids must continue: %v", to.cfg.Database, err)
		}
	}
}
