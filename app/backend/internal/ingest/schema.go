// Package ingest keeps the high-volume side of connectors in PostgreSQL: received requests
// (the queue and the raw archive in one table, partitioned by day), normalized events, failures
// and per-minute statistics.
package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const schema = `
CREATE SEQUENCE IF NOT EXISTS ingest_requests_id_seq;
CREATE TABLE IF NOT EXISTS ingest_requests (
	id           bigint NOT NULL DEFAULT nextval('ingest_requests_id_seq'),
	received_at  timestamptz NOT NULL DEFAULT now(),
	connector_id text NOT NULL,
	version      integer NOT NULL,
	status       text NOT NULL DEFAULT 'pending',
	attempts     integer NOT NULL DEFAULT 0,
	remote_ip    text NOT NULL DEFAULT '',
	method       text NOT NULL DEFAULT '',
	headers      jsonb NOT NULL DEFAULT '{}',
	query        jsonb NOT NULL DEFAULT '{}',
	body         bytea NOT NULL,
	processed_at timestamptz,
	events       integer NOT NULL DEFAULT 0,
	error        text NOT NULL DEFAULT '',
	PRIMARY KEY (id, received_at)
) PARTITION BY RANGE (received_at);
CREATE TABLE IF NOT EXISTS ingest_requests_default PARTITION OF ingest_requests DEFAULT;
CREATE INDEX IF NOT EXISTS ingest_requests_pending ON ingest_requests (received_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS ingest_requests_connector ON ingest_requests (connector_id, received_at DESC);

CREATE TABLE IF NOT EXISTS ingest_idempotency (
	connector_id text NOT NULL,
	key          text NOT NULL,
	created_at   timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (connector_id, key)
);

CREATE TABLE IF NOT EXISTS connector_events (
	id           bigserial PRIMARY KEY,
	connector_id text NOT NULL,
	version      integer NOT NULL,
	key          text NOT NULL,
	title        text NOT NULL,
	ci           text NOT NULL,
	signal       text NOT NULL,
	method       text NOT NULL,
	severity     text NOT NULL,
	status       text NOT NULL,
	external_id  text NOT NULL,
	value        text NOT NULL,
	labels       jsonb NOT NULL DEFAULT '{}',
	request_id   bigint NOT NULL,
	request_at   timestamptz NOT NULL,
	item         integer NOT NULL,
	first_seen   timestamptz NOT NULL DEFAULT now(),
	last_seen    timestamptz NOT NULL DEFAULT now(),
	seen         integer NOT NULL DEFAULT 1,
	UNIQUE (connector_id, key)
);
CREATE INDEX IF NOT EXISTS connector_events_recent ON connector_events (connector_id, last_seen DESC);
ALTER TABLE connector_events ADD COLUMN IF NOT EXISTS description text NOT NULL DEFAULT '';
ALTER TABLE connector_events ADD COLUMN IF NOT EXISTS fields jsonb NOT NULL DEFAULT '[]';

CREATE TABLE IF NOT EXISTS ingest_failures (
	id           bigserial PRIMARY KEY,
	connector_id text NOT NULL,
	version      integer NOT NULL,
	node_id      text NOT NULL,
	error        text NOT NULL,
	request_id   bigint NOT NULL,
	request_at   timestamptz NOT NULL,
	item         integer NOT NULL,
	data         jsonb,
	raw          text NOT NULL DEFAULT '',
	created_at   timestamptz NOT NULL DEFAULT now(),
	retries      integer NOT NULL DEFAULT 0,
	resolved_at  timestamptz
);
CREATE INDEX IF NOT EXISTS ingest_failures_open ON ingest_failures (connector_id, created_at DESC) WHERE resolved_at IS NULL;

CREATE TABLE IF NOT EXISTS connector_stats (
	connector_id text NOT NULL,
	bucket       timestamptz NOT NULL,
	received     integer NOT NULL DEFAULT 0,
	rejected     integer NOT NULL DEFAULT 0,
	filtered     integer NOT NULL DEFAULT 0,
	skipped      integer NOT NULL DEFAULT 0,
	failed       integer NOT NULL DEFAULT 0,
	events       integer NOT NULL DEFAULT 0,
	duplicates   integer NOT NULL DEFAULT 0,
	latency_ms   bigint NOT NULL DEFAULT 0,
	latency_max  integer NOT NULL DEFAULT 0,
	PRIMARY KEY (connector_id, bucket)
);
`

// lockKey serializes schema changes and partition upkeep between Umbrella instances.
const lockKey = 0x756d6272656c6c61

func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(lockKey)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, schema); err != nil {
			return fmt.Errorf("create ingest tables: %w", err)
		}
		return ensurePartitions(ctx, tx, time.Now().UTC(), 3)
	})
}

func partitionName(day time.Time) string {
	return "ingest_requests_" + day.Format("20060102")
}

func ensurePartitions(ctx context.Context, tx pgx.Tx, now time.Time, ahead int) error {
	day := now.Truncate(24 * time.Hour)
	for i := 0; i <= ahead; i++ {
		from := day.AddDate(0, 0, i)
		to := from.AddDate(0, 0, 1)
		sql := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s PARTITION OF ingest_requests FOR VALUES FROM ('%s') TO ('%s')",
			pgx.Identifier{partitionName(from)}.Sanitize(), from.Format(time.RFC3339), to.Format(time.RFC3339))
		if _, err := tx.Exec(ctx, sql); err != nil {
			// The default partition already holds rows of that day: they stay there.
			if strings.Contains(err.Error(), "would be violated by some row") {
				slog.Warn("ingest partition skipped: the default partition holds its rows", "day", from.Format(time.DateOnly))
				continue
			}
			return fmt.Errorf("create partition %s: %w", partitionName(from), err)
		}
	}
	return nil
}

type Retention struct {
	Requests time.Duration
	Failures time.Duration
	Stats    time.Duration
	Dedup    time.Duration
}

var DefaultRetention = Retention{Requests: 7 * 24 * time.Hour, Failures: 30 * 24 * time.Hour, Stats: 30 * 24 * time.Hour, Dedup: 24 * time.Hour}

// Maintain creates the coming partitions, drops expired ones and removes old rows. Only one
// instance does it at a time; the others skip.
func Maintain(ctx context.Context, pool *pgxpool.Pool, now time.Time, r Retention) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var got bool
		if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", int64(lockKey)).Scan(&got); err != nil || !got {
			return err
		}
		if err := ensurePartitions(ctx, tx, now, 3); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT c.relname FROM pg_inherits i
			JOIN pg_class c ON c.oid = i.inhrelid JOIN pg_class p ON p.oid = i.inhparent
			WHERE p.relname = 'ingest_requests' AND c.relname ~ '^ingest_requests_[0-9]{8}$'`)
		if err != nil {
			return err
		}
		names, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		cutoff := now.Add(-r.Requests).Truncate(24 * time.Hour)
		for _, n := range names {
			day, err := time.Parse("20060102", strings.TrimPrefix(n, "ingest_requests_"))
			if err != nil || !day.AddDate(0, 0, 1).Before(cutoff.Add(time.Nanosecond)) {
				continue
			}
			if _, err := tx.Exec(ctx, "DROP TABLE IF EXISTS "+pgx.Identifier{n}.Sanitize()); err != nil {
				return err
			}
			slog.Info("ingest partition dropped", "partition", n)
		}
		for _, q := range []struct {
			sql string
			age time.Duration
		}{
			{"DELETE FROM ingest_requests_default WHERE received_at < $1", r.Requests},
			{"DELETE FROM ingest_failures WHERE created_at < $1", r.Failures},
			{"DELETE FROM connector_stats WHERE bucket < $1", r.Stats},
			{"DELETE FROM ingest_idempotency WHERE created_at < $1", r.Dedup},
		} {
			if _, err := tx.Exec(ctx, q.sql, now.Add(-q.age)); err != nil {
				return err
			}
		}
		return nil
	})
}
