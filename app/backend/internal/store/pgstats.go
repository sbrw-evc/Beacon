package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	pgTopTables     = 50
	pgTopOperations = 10
	pgTopStatements = 10
	pgQueryPreview  = 1000
)

type PGDatabaseStats struct {
	Name        string     `json:"name"`
	SizeBytes   int64      `json:"size_bytes"`
	Backends    int64      `json:"backends"`
	Commits     int64      `json:"xact_commit"`
	Rollbacks   int64      `json:"xact_rollback"`
	BlocksRead  int64      `json:"blks_read"`
	BlocksHit   int64      `json:"blks_hit"`
	TupReturned int64      `json:"tup_returned"`
	TupFetched  int64      `json:"tup_fetched"`
	TupInserted int64      `json:"tup_inserted"`
	TupUpdated  int64      `json:"tup_updated"`
	TupDeleted  int64      `json:"tup_deleted"`
	Conflicts   int64      `json:"conflicts"`
	Deadlocks   int64      `json:"deadlocks"`
	TempFiles   int64      `json:"temp_files"`
	TempBytes   int64      `json:"temp_bytes"`
	StatsReset  *time.Time `json:"stats_reset,omitempty"`
}

type PGTable struct {
	Schema     string `json:"schema"`
	Name       string `json:"name"`
	TotalBytes int64  `json:"total_bytes"`
	TableBytes int64  `json:"table_bytes"`
	IndexBytes int64  `json:"index_bytes"`
	Rows       int64  `json:"rows"`
	DeadRows   int64  `json:"dead_rows"`
}

type PGOperation struct {
	PID           int64      `json:"pid"`
	Database      string     `json:"database"`
	User          string     `json:"user"`
	Application   string     `json:"application"`
	Client        string     `json:"client"`
	BackendType   string     `json:"backend_type"`
	State         string     `json:"state"`
	WaitEventType string     `json:"wait_event_type"`
	WaitEvent     string     `json:"wait_event"`
	Started       *time.Time `json:"started,omitempty"`
	DurationMS    int64      `json:"duration_ms"`
	Query         string     `json:"query"`
	Truncated     bool       `json:"truncated"`
}

type PGStatement struct {
	QueryID string  `json:"query_id"`
	Query   string  `json:"query"`
	Calls   int64   `json:"calls"`
	TotalMS float64 `json:"total_ms"`
	MeanMS  float64 `json:"mean_ms"`
	Rows    int64   `json:"rows"`
}

type PGStatements struct {
	Installed bool          `json:"installed"`
	Error     string        `json:"error,omitempty"`
	ByTotal   []PGStatement `json:"by_total"`
	ByMean    []PGStatement `json:"by_mean"`
}

type PGStats struct {
	CollectedAt    time.Time       `json:"collected_at"`
	FullVisibility bool            `json:"full_visibility"`
	Database       PGDatabaseStats `json:"database"`
	TableCount     int             `json:"table_count"`
	Tables         []PGTable       `json:"tables"`
	Operations     []PGOperation   `json:"operations"`
	Statements     PGStatements    `json:"statements"`
}

func (p *PGBackend) Config() PGConfig {
	c := p.cfg
	c.Password = ""
	return c
}

func (p *PGBackend) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	err := p.pool.Ping(ctx)
	return time.Since(start), err
}

func (p *PGBackend) Stats(ctx context.Context) (PGStats, error) {
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return PGStats{}, err
	}
	defer conn.Release()
	c := pgStatsReader{conn: conn.Conn()}
	out := PGStats{CollectedAt: time.Now().UTC()}
	steps := []struct {
		name string
		run  func(context.Context, *PGStats) error
	}{
		{"visibility", c.visibility},
		{"pg_stat_database", c.database},
		{"table sizes", c.tables},
		{"pg_stat_activity", c.operations},
	}
	for _, s := range steps {
		if err := s.run(ctx, &out); err != nil {
			return out, fmt.Errorf("%s: %w", s.name, err)
		}
	}
	out.Statements = c.statements(ctx)
	return out, nil
}

type pgStatsReader struct {
	conn *pgx.Conn
}

func (r pgStatsReader) visibility(ctx context.Context, out *PGStats) error {
	return r.conn.QueryRow(ctx, `SELECT rolsuper OR pg_has_role(current_user, 'pg_read_all_stats', 'USAGE')
		FROM pg_roles WHERE rolname = current_user`).Scan(&out.FullVisibility)
}

func (r pgStatsReader) database(ctx context.Context, out *PGStats) error {
	d := &out.Database
	return r.conn.QueryRow(ctx, `SELECT datname, pg_database_size(datid), numbackends, xact_commit, xact_rollback,
		blks_read, blks_hit, tup_returned, tup_fetched, tup_inserted, tup_updated, tup_deleted,
		conflicts, deadlocks, temp_files, temp_bytes, stats_reset
		FROM pg_stat_database WHERE datname = current_database()`).
		Scan(&d.Name, &d.SizeBytes, &d.Backends, &d.Commits, &d.Rollbacks, &d.BlocksRead, &d.BlocksHit,
			&d.TupReturned, &d.TupFetched, &d.TupInserted, &d.TupUpdated, &d.TupDeleted,
			&d.Conflicts, &d.Deadlocks, &d.TempFiles, &d.TempBytes, &d.StatsReset)
}

func (r pgStatsReader) tables(ctx context.Context, out *PGStats) error {
	rows, err := r.conn.Query(ctx, `SELECT n.nspname, c.relname, pg_total_relation_size(c.oid), pg_table_size(c.oid),
		pg_indexes_size(c.oid), COALESCE(s.n_live_tup, GREATEST(c.reltuples, 0)::bigint), COALESCE(s.n_dead_tup, 0),
		count(*) OVER ()
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_stat_user_tables s ON s.relid = c.oid
		WHERE c.relkind IN ('r', 'm') AND n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg_toast%'
		ORDER BY pg_total_relation_size(c.oid) DESC, n.nspname, c.relname
		LIMIT $1`, pgTopTables)
	if err != nil {
		return err
	}
	out.Tables, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (PGTable, error) {
		var t PGTable
		err := row.Scan(&t.Schema, &t.Name, &t.TotalBytes, &t.TableBytes, &t.IndexBytes, &t.Rows, &t.DeadRows, &out.TableCount)
		return t, err
	})
	return err
}

func (r pgStatsReader) operations(ctx context.Context, out *PGStats) error {
	rows, err := r.conn.Query(ctx, `WITH a AS (
			SELECT *, CASE WHEN state = 'active' THEN query_start ELSE COALESCE(xact_start, query_start) END AS op_start
			FROM pg_stat_activity
			WHERE pid <> pg_backend_pid() AND state IS NOT NULL AND state <> 'idle'
		)
		SELECT pid, COALESCE(datname, ''), COALESCE(usename, ''), COALESCE(application_name, ''), COALESCE(client_addr::text, ''),
			COALESCE(backend_type, ''), state, COALESCE(wait_event_type, ''), COALESCE(wait_event, ''), op_start,
			COALESCE((EXTRACT(EPOCH FROM clock_timestamp() - op_start) * 1000)::bigint, 0),
			left(query, $1), length(query) > $1
		FROM a
		ORDER BY op_start ASC NULLS LAST
		LIMIT $2`, pgQueryPreview, pgTopOperations)
	if err != nil {
		return err
	}
	out.Operations, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (PGOperation, error) {
		var o PGOperation
		err := row.Scan(&o.PID, &o.Database, &o.User, &o.Application, &o.Client, &o.BackendType, &o.State,
			&o.WaitEventType, &o.WaitEvent, &o.Started, &o.DurationMS, &o.Query, &o.Truncated)
		return o, err
	})
	return err
}

func (r pgStatsReader) statements(ctx context.Context) PGStatements {
	var out PGStatements
	var schema string
	var versionNum int
	err := r.conn.QueryRow(ctx, `SELECT n.nspname, current_setting('server_version_num')::int
		FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace WHERE e.extname = 'pg_stat_statements'`).Scan(&schema, &versionNum)
	if errors.Is(err, pgx.ErrNoRows) {
		return out
	}
	out.Installed = true
	if err != nil {
		out.Error = err.Error()
		return out
	}
	total, mean := "total_exec_time", "mean_exec_time"
	if versionNum < 130000 {
		total, mean = "total_time", "mean_time"
	}
	view := pgx.Identifier{schema, "pg_stat_statements"}.Sanitize()
	top := func(order string) ([]PGStatement, error) {
		rows, err := r.conn.Query(ctx, fmt.Sprintf(`SELECT COALESCE(queryid::text, ''), left(query, $1), calls, %[1]s, %[2]s, rows
			FROM %[3]s WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
			ORDER BY %[4]s DESC LIMIT $2`, total, mean, view, order), pgQueryPreview, pgTopStatements)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, func(row pgx.CollectableRow) (PGStatement, error) {
			var s PGStatement
			err := row.Scan(&s.QueryID, &s.Query, &s.Calls, &s.TotalMS, &s.MeanMS, &s.Rows)
			return s, err
		})
	}
	if out.ByTotal, err = top(total); err == nil {
		out.ByMean, err = top(mean)
	}
	if err != nil {
		out.Error = err.Error()
	}
	return out
}
