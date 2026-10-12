package app

import (
	"context"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/ingest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store/storetest"
)

// Every table the alert and ingest schemas create must move with a database switch.
func TestUmbrellaTablesCoverSchema(t *testing.T) {
	ctx := context.Background()
	cfg := storetest.TempDatabase(t)
	p, err := pgxpool.New(ctx, cfg.DSN())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := ingest.EnsureSchema(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := alert.EnsureSchema(ctx, p); err != nil {
		t.Fatal(err)
	}
	rows, err := p.Query(ctx, `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p') AND NOT c.relispartition ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Sorted(slices.Values(umbrellaTables))
	if !slices.Equal(tables, want) {
		t.Fatalf("tables in the schema %v, copied on a switch %v", tables, want)
	}
}
