package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/ingest"
)

// umbrellaTables are the tables Umbrella keeps next to its snapshot (alert/db.go,
// ingest/schema.go), parents before the tables referencing them. A database switch copies them
// all; TestUmbrellaTablesCoverSchema fails when a new table is not listed here.
var umbrellaTables = []string{
	"alert_keys",
	"alerts",
	"alert_timeline",
	"ingest_requests",
	"ingest_idempotency",
	"connector_events",
	"ingest_failures",
	"connector_stats",
}

type pgQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// umbrellaData reports whether any of the Umbrella tables exists and holds rows.
func umbrellaData(ctx context.Context, q pgQuerier) (bool, error) {
	for _, t := range umbrellaTables {
		var exists bool
		if err := q.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", t).Scan(&exists); err != nil {
			return false, err
		}
		if !exists {
			continue
		}
		var rows bool
		if err := q.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM "+pgx.Identifier{t}.Sanitize()+")").Scan(&rows); err != nil {
			return false, err
		}
		if rows {
			return true, nil
		}
	}
	return false, nil
}

var errTargetHasData = errors.New("the target database already holds Umbrella data")

// copyUmbrella copies the alert and ingest tables from src to dst and writes the snapshot with
// save, all in one transaction of dst. The source is read from one snapshot, after the writers
// still holding its tables have finished. With overwrite the Umbrella tables of dst are emptied
// first; without it dst must hold none of their rows. Sequences of dst end at least where those of
// src are, so incident numbers (and PagerDuty dedup keys) continue instead of starting over.
func copyUmbrella(ctx context.Context, src, dst *pgxpool.Pool, overwrite bool, save func(pgx.Tx) error) error {
	if err := ingest.EnsureSchema(ctx, dst); err != nil {
		return err
	}
	if err := alert.EnsureSchema(ctx, dst); err != nil {
		return err
	}
	sconn, err := src.Acquire(ctx)
	if err != nil {
		return err
	}
	defer sconn.Release()
	stx, err := sconn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return err
	}
	defer stx.Rollback(context.Background())
	present := map[string]bool{}
	for _, t := range umbrellaTables {
		var ok bool
		if err := stx.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", t).Scan(&ok); err != nil {
			return err
		}
		present[t] = ok
	}
	// LOCK comes before any query that reads table data, so the snapshot sees every write that
	// was still running and no later one can slip in.
	var locked []string
	for _, t := range umbrellaTables {
		if present[t] {
			locked = append(locked, pgx.Identifier{t}.Sanitize())
		}
	}
	if len(locked) > 0 {
		if _, err := stx.Exec(ctx, "LOCK TABLE "+strings.Join(locked, ", ")+" IN SHARE MODE"); err != nil {
			return err
		}
	}

	return pgx.BeginFunc(ctx, dst, func(dtx pgx.Tx) error {
		all := make([]string, len(umbrellaTables))
		for i, t := range umbrellaTables {
			all[i] = pgx.Identifier{t}.Sanitize()
		}
		if _, err := dtx.Exec(ctx, "LOCK TABLE "+strings.Join(all, ", ")+" IN ACCESS EXCLUSIVE MODE"); err != nil {
			return err
		}
		if overwrite {
			if _, err := dtx.Exec(ctx, "TRUNCATE "+strings.Join(all, ", ")); err != nil {
				return err
			}
		} else if has, err := umbrellaData(ctx, dtx); err != nil {
			return err
		} else if has {
			return errTargetHasData
		}
		for _, t := range umbrellaTables {
			if !present[t] {
				continue
			}
			if err := copyTable(ctx, stx, dtx, t); err != nil {
				return fmt.Errorf("copy %s: %w", t, err)
			}
		}
		if err := copySequences(ctx, stx, dtx); err != nil {
			return err
		}
		return save(dtx)
	})
}

func copyTable(ctx context.Context, stx, dtx pgx.Tx, table string) error {
	rows, err := dtx.Query(ctx, `SELECT column_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = $1 ORDER BY ordinal_position`, table)
	if err != nil {
		return err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	cols := make([]string, len(names))
	for i, n := range names {
		cols[i] = pgx.Identifier{n}.Sanitize()
	}
	list, name := strings.Join(cols, ", "), pgx.Identifier{table}.Sanitize()
	pr, pw := io.Pipe()
	read := make(chan error, 1)
	go func() {
		_, err := stx.Conn().PgConn().CopyTo(ctx, pw, "COPY (SELECT "+list+" FROM "+name+") TO STDOUT")
		pw.CloseWithError(err)
		read <- err
	}()
	_, err = dtx.Conn().PgConn().CopyFrom(ctx, pr, "COPY "+name+" ("+list+") FROM STDIN")
	pr.CloseWithError(errors.New("copy aborted"))
	if rerr := <-read; rerr != nil && err == nil {
		err = rerr
	}
	return err
}

// copySequences moves every sequence of dst that also exists in src to the larger of the two
// positions.
func copySequences(ctx context.Context, stx, dtx pgx.Tx) error {
	const list = `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'S' AND n.nspname = current_schema() ORDER BY 1`
	rows, err := stx.Query(ctx, list)
	if err != nil {
		return err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, n := range names {
		var exists bool
		if err := dtx.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", n).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			continue
		}
		from, err := sequenceUsed(ctx, stx, n)
		if err != nil {
			return err
		}
		to, err := sequenceUsed(ctx, dtx, n)
		if err != nil {
			return err
		}
		if v := max(from, to); v > 0 {
			if _, err := dtx.Exec(ctx, "SELECT setval($1::regclass, $2, true)", n, v); err != nil {
				return fmt.Errorf("sequence %s: %w", n, err)
			}
		}
	}
	return nil
}

// sequenceUsed is the last value handed out by a sequence, 0 when none was.
func sequenceUsed(ctx context.Context, tx pgx.Tx, seq string) (int64, error) {
	var last int64
	var called bool
	if err := tx.QueryRow(ctx, "SELECT last_value, is_called FROM "+pgx.Identifier{seq}.Sanitize()).Scan(&last, &called); err != nil {
		return 0, err
	}
	if !called {
		last--
	}
	return last, nil
}
