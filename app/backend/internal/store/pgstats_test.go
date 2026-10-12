package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store/storetest"
)

func TestPostgresStats(t *testing.T) {
	cfg := storetest.TempDatabase(t)
	ctx := context.Background()
	backend, err := store.OpenPostgres(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	if err := backend.Save(ctx, []byte(strings.Repeat("x", 64<<10))); err != nil {
		t.Fatal(err)
	}
	if got := backend.Config(); got.Password != "" || got.Database != cfg.Database {
		t.Fatalf("config = %+v", got)
	}

	sleeper, err := pgx.Connect(ctx, cfg.DSN())
	if err != nil {
		t.Fatal(err)
	}
	defer sleeper.Close(context.Background())
	sleepCtx, stopSleep := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		_, _ = sleeper.Exec(sleepCtx, "SELECT pg_sleep(30) /* umbrella-stats-probe */")
		close(done)
	}()
	defer func() {
		stopSleep()
		<-done
	}()

	var stats store.PGStats
	deadline := time.Now().Add(10 * time.Second)
	for {
		stats, err = backend.Stats(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(stats.Operations) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if stats.Database.Name != cfg.Database || stats.Database.SizeBytes <= 0 || stats.Database.Commits <= 0 {
		t.Fatalf("database stats = %+v", stats.Database)
	}
	if stats.TableCount < 1 || len(stats.Tables) == 0 || stats.Tables[0].Name != "umbrella_state" || stats.Tables[0].TotalBytes <= 0 {
		t.Fatalf("tables = %d %+v", stats.TableCount, stats.Tables)
	}
	found := false
	for _, op := range stats.Operations {
		if strings.Contains(op.Query, "umbrella-stats-probe") {
			found = op.State == "active" && op.DurationMS >= 0 && op.Database == cfg.Database
		}
	}
	if !found {
		t.Fatalf("the running pg_sleep is not among the operations: %+v", stats.Operations)
	}
	if stats.Statements.Installed {
		t.Fatalf("a fresh database has no pg_stat_statements: %+v", stats.Statements)
	}
	ext, err := pgx.Connect(ctx, cfg.DSN())
	if err != nil {
		t.Fatal(err)
	}
	defer ext.Close(context.Background())
	if _, err := ext.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS pg_stat_statements"); err != nil {
		t.Skip("pg_stat_statements is not available: ", err)
	}
	if stats, err = backend.Stats(ctx); err != nil {
		t.Fatal(err)
	}
	if st := stats.Statements; !st.Installed || (st.Error == "" && st.ByTotal == nil) {
		t.Fatalf("statements = %+v", st)
	}
	t.Logf("pg_stat_statements: error %q, %d by total", stats.Statements.Error, len(stats.Statements.ByTotal))
	if d, err := backend.Ping(ctx); err != nil || d <= 0 {
		t.Fatalf("ping = %v %v", d, err)
	}
}
