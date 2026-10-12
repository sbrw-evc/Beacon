package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
)

const requestColumns = `id, received_at, connector_id, version, status, attempts, remote_ip, method, headers, query,
	octet_length(body), processed_at, events, error`

func scanRequest(row pgx.Row, withBody bool) (Request, error) {
	var r Request
	dest := []any{&r.ID, &r.ReceivedAt, &r.ConnectorID, &r.Version, &r.Status, &r.Attempts, &r.RemoteIP, &r.Method,
		&r.Headers, &r.Query, &r.Size, &r.ProcessedAt, &r.Events, &r.Error}
	if withBody {
		dest = append(dest, &r.Body)
	}
	err := row.Scan(dest...)
	return r, err
}

func (q *Queue) Requests(ctx context.Context, connectorID, status string, limit int) ([]Request, error) {
	rows, err := q.pool.Query(ctx, `SELECT `+requestColumns+` FROM ingest_requests
		WHERE connector_id = $1 AND ($2 = '' OR status = $2) ORDER BY received_at DESC LIMIT $3`, connectorID, status, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Request, error) { return scanRequest(row, false) })
}

func (q *Queue) Request(ctx context.Context, connectorID string, id int64) (Request, error) {
	r, err := scanRequest(q.pool.QueryRow(ctx, `SELECT `+requestColumns+`, body FROM ingest_requests
		WHERE connector_id = $1 AND id = $2`, connectorID, id), true)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

type Failure struct {
	ID         int64          `json:"id"`
	Version    int            `json:"version"`
	Node       string         `json:"node"`
	Error      string         `json:"error"`
	RequestID  int64          `json:"request_id"`
	RequestAt  time.Time      `json:"request_at"`
	Item       int            `json:"item"`
	Data       map[string]any `json:"data,omitempty"`
	Raw        string         `json:"raw,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	Retries    int            `json:"retries"`
	ResolvedAt *time.Time     `json:"resolved_at,omitempty"`
}

func (q *Queue) Failures(ctx context.Context, connectorID string, resolved bool, limit int) ([]Failure, error) {
	rows, err := q.pool.Query(ctx, `SELECT id, version, node_id, error, request_id, request_at, item, data, raw, created_at, retries, resolved_at
		FROM ingest_failures WHERE connector_id = $1 AND (resolved_at IS NULL OR $2) ORDER BY created_at DESC, id DESC LIMIT $3`,
		connectorID, resolved, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Failure, error) {
		var f Failure
		var data []byte
		err := row.Scan(&f.ID, &f.Version, &f.Node, &f.Error, &f.RequestID, &f.RequestAt, &f.Item, &data, &f.Raw, &f.CreatedAt, &f.Retries, &f.ResolvedAt)
		if len(data) > 0 {
			_ = json.Unmarshal(data, &f.Data)
		}
		return f, err
	})
}

type Reprocessed struct {
	Requeued int `json:"requeued"`
	Missing  int `json:"missing"`
}

// Reprocess puts the requests behind the given failures (all open ones when ids is empty) back
// into the queue for the given version and closes the failures. Requests whose partition has
// expired cannot be processed again and are counted as missing.
//
// A requeued request is processed again in full, but only what is still news reaches the
// alerts (see handle): an event of a source that has sent something newer since is skipped,
// and an event the same request already folded is not folded again. What remains is the
// newest known state of its source and is applied as its current state.
func (q *Queue) Reprocess(ctx context.Context, connectorID string, ids []int64, version int) (Reprocessed, error) {
	var out Reprocessed
	err := pgx.BeginFunc(ctx, q.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE ingest_failures SET resolved_at = now(), retries = retries + 1
			WHERE connector_id = $1 AND resolved_at IS NULL AND (cardinality($2::bigint[]) = 0 OR id = ANY($2))
			RETURNING request_id, request_at`, connectorID, ids)
		if err != nil {
			return err
		}
		type key struct {
			id int64
			at time.Time
		}
		reqs := map[key]bool{}
		for rows.Next() {
			var k key
			if err := rows.Scan(&k.id, &k.at); err != nil {
				rows.Close()
				return err
			}
			reqs[k] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for k := range reqs {
			tag, err := tx.Exec(ctx, `UPDATE ingest_requests SET status = 'pending', version = $4, error = ''
				WHERE connector_id = $1 AND id = $2 AND received_at = $3`, connectorID, k.id, k.at, version)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				out.Missing++
			} else {
				out.Requeued++
			}
		}
		return nil
	})
	if err == nil && out.Requeued > 0 {
		q.Notify()
	}
	return out, err
}

type EventRow struct {
	ID          int64             `json:"id"`
	Version     int               `json:"version"`
	Key         string            `json:"key"`
	Title       string            `json:"title"`
	CI          string            `json:"ci"`
	Signal      string            `json:"signal"`
	Method      string            `json:"method"`
	Severity    string            `json:"severity"`
	Status      string            `json:"status"`
	ExternalID  string            `json:"external_id"`
	Value       string            `json:"value"`
	Description string            `json:"description,omitempty"`
	Fields      []flow.Field      `json:"fields,omitempty"`
	Labels      map[string]string `json:"labels"`
	RequestID   int64             `json:"request_id"`
	Item        int               `json:"item"`
	FirstSeen   time.Time         `json:"first_seen"`
	LastSeen    time.Time         `json:"last_seen"`
	Seen        int               `json:"seen"`
}

func (q *Queue) Events(ctx context.Context, connectorID string, limit int) ([]EventRow, error) {
	rows, err := q.pool.Query(ctx, `SELECT id, version, key, title, ci, signal, method, severity, status, external_id, value, labels,
			request_id, item, first_seen, last_seen, seen, description, fields
		FROM connector_events WHERE connector_id = $1 ORDER BY last_seen DESC, id DESC LIMIT $2`, connectorID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (EventRow, error) {
		var e EventRow
		err := row.Scan(&e.ID, &e.Version, &e.Key, &e.Title, &e.CI, &e.Signal, &e.Method, &e.Severity, &e.Status, &e.ExternalID,
			&e.Value, &e.Labels, &e.RequestID, &e.Item, &e.FirstSeen, &e.LastSeen, &e.Seen, &e.Description, &e.Fields)
		return e, err
	})
}

type Bucket struct {
	At         time.Time `json:"at"`
	Received   int       `json:"received"`
	Rejected   int       `json:"rejected"`
	Filtered   int       `json:"filtered"`
	Skipped    int       `json:"skipped"`
	Failed     int       `json:"failed"`
	Events     int       `json:"events"`
	Duplicates int       `json:"duplicates"`
	LatencyAvg int       `json:"latency_avg_ms"`
	LatencyMax int       `json:"latency_max_ms"`
}

// Stats returns per-bucket counters since the given time; step groups minutes into wider buckets.
func (q *Queue) Stats(ctx context.Context, connectorID string, since time.Time, step time.Duration) ([]Bucket, error) {
	rows, err := q.pool.Query(ctx, `SELECT to_timestamp(floor(extract(epoch FROM bucket) / $3) * $3) AS at,
			sum(received)::int, sum(rejected)::int, sum(filtered)::int, sum(skipped)::int, sum(failed)::int, sum(events)::int, sum(duplicates)::int,
			coalesce(sum(latency_ms) / nullif(sum(received), 0), 0)::int, max(latency_max)
		FROM connector_stats WHERE connector_id = $1 AND bucket >= $2 GROUP BY 1 ORDER BY 1`, connectorID, since, step.Seconds())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Bucket, error) {
		var b Bucket
		err := row.Scan(&b.At, &b.Received, &b.Rejected, &b.Filtered, &b.Skipped, &b.Failed, &b.Events, &b.Duplicates, &b.LatencyAvg, &b.LatencyMax)
		return b, err
	})
}

type Summary struct {
	Received     int        `json:"received"`
	Rejected     int        `json:"rejected"`
	Failed       int        `json:"failed"`
	Events       int        `json:"events"`
	OpenFailures int        `json:"open_failures"`
	LastReceived *time.Time `json:"last_received,omitempty"`
}

// Summaries returns the last 24 hours of every connector, for the connector list.
func (q *Queue) Summaries(ctx context.Context) (map[string]Summary, error) {
	out := map[string]Summary{}
	rows, err := q.pool.Query(ctx, `SELECT connector_id, sum(received)::int, sum(rejected)::int, sum(failed)::int, sum(events)::int,
			max(bucket) FILTER (WHERE received > 0)
		FROM connector_stats WHERE bucket >= now() - interval '24 hours' GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var s Summary
		if err := rows.Scan(&id, &s.Received, &s.Rejected, &s.Failed, &s.Events, &s.LastReceived); err != nil {
			rows.Close()
			return nil, err
		}
		out[id] = s
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = q.pool.Query(ctx, `SELECT connector_id, count(*)::int FROM ingest_failures WHERE resolved_at IS NULL GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		s := out[id]
		s.OpenFailures = n
		out[id] = s
	}
	return out, rows.Err()
}

// Forget removes what a deleted connector left behind. Its requests expire with their partitions.
func (q *Queue) Forget(ctx context.Context, connectorID string) error {
	return pgx.BeginFunc(ctx, q.pool, func(tx pgx.Tx) error {
		for _, sql := range []string{
			"DELETE FROM connector_events WHERE connector_id = $1",
			"DELETE FROM ingest_failures WHERE connector_id = $1",
			"DELETE FROM connector_stats WHERE connector_id = $1",
			"DELETE FROM ingest_idempotency WHERE connector_id = $1",
			"UPDATE ingest_requests SET status = 'failed', error = 'the connector was deleted' WHERE connector_id = $1 AND status = 'pending'",
		} {
			if _, err := tx.Exec(ctx, sql, connectorID); err != nil {
				return err
			}
		}
		return nil
	})
}

// Overview is the intake as a whole: the queue and the last 24 hours of every connector.
type Overview struct {
	Pending       int        `json:"pending"`
	OldestPending *time.Time `json:"oldest_pending,omitempty"`
	OpenFailures  int        `json:"open_failures"`
	Received      int        `json:"received_24h"`
	Rejected      int        `json:"rejected_24h"`
	Failed        int        `json:"failed_24h"`
	Events        int        `json:"events_24h"`
	Duplicates    int        `json:"duplicates_24h"`
	LatencyAvg    int        `json:"latency_avg_ms"`
	LatencyMax    int        `json:"latency_max_ms"`
	LastReceived  *time.Time `json:"last_received,omitempty"`
}

func (q *Queue) Overview(ctx context.Context) (Overview, error) {
	var o Overview
	err := q.pool.QueryRow(ctx, `SELECT
			(SELECT count(*)::int FROM ingest_requests WHERE status = 'pending'),
			(SELECT min(received_at) FROM ingest_requests WHERE status = 'pending'),
			(SELECT count(*)::int FROM ingest_failures WHERE resolved_at IS NULL),
			coalesce(sum(received), 0)::int, coalesce(sum(rejected), 0)::int, coalesce(sum(failed), 0)::int,
			coalesce(sum(events), 0)::int, coalesce(sum(duplicates), 0)::int,
			coalesce(sum(latency_ms) / nullif(sum(received), 0), 0)::int, coalesce(max(latency_max), 0),
			max(bucket) FILTER (WHERE received > 0)
		FROM connector_stats WHERE bucket >= now() - interval '24 hours'`).Scan(
		&o.Pending, &o.OldestPending, &o.OpenFailures, &o.Received, &o.Rejected, &o.Failed,
		&o.Events, &o.Duplicates, &o.LatencyAvg, &o.LatencyMax, &o.LastReceived)
	return o, err
}

// FiringEvent is an event that is still firing, for the health of configuration items.
type FiringEvent struct {
	ConnectorID string    `json:"connector_id"`
	CI          string    `json:"ci"`
	Title       string    `json:"title"`
	Severity    string    `json:"severity"`
	LastSeen    time.Time `json:"last_seen"`
}

const maxFiring = 5000

// Firing lists the newest events of every connector that fire and were seen since the given time.
func (q *Queue) Firing(ctx context.Context, since time.Time) ([]FiringEvent, error) {
	rows, err := q.pool.Query(ctx, `SELECT connector_id, ci, title, severity, last_seen FROM connector_events
		WHERE status = 'firing' AND ci <> '' AND last_seen >= $1 ORDER BY last_seen DESC LIMIT $2`, since, maxFiring)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (FiringEvent, error) {
		var e FiringEvent
		err := row.Scan(&e.ConnectorID, &e.CI, &e.Title, &e.Severity, &e.LastSeen)
		return e, err
	})
}
