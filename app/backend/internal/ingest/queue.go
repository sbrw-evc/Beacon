package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/textx"
)

const (
	StatusPending = "pending"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

var ErrNotFound = errors.New("not found")

type Request struct {
	ID          int64             `json:"id"`
	ReceivedAt  time.Time         `json:"received_at"`
	ConnectorID string            `json:"connector_id"`
	Version     int               `json:"version"`
	Status      string            `json:"status"`
	Attempts    int               `json:"attempts"`
	RemoteIP    string            `json:"remote_ip"`
	Method      string            `json:"method"`
	Headers     map[string]string `json:"headers"`
	Query       map[string]string `json:"query"`
	Body        []byte            `json:"-"`
	Size        int               `json:"size"`
	ProcessedAt *time.Time        `json:"processed_at,omitempty"`
	Events      int               `json:"events"`
	Error       string            `json:"error,omitempty"`
}

// Outcome is what processing one request produced.
type Outcome struct {
	Version int
	Result  *flow.Result
}

// Processor runs a request through the connector version it was received for.
type Processor func(ctx context.Context, r Request) (Outcome, error)

// Sink receives the events of a processed request inside the transaction that stores them
// (the alert engine). after runs once the transaction is committed.
type Sink func(ctx context.Context, tx pgx.Tx, connectorID string, events []flow.Event) (after func(), err error)

type Queue struct {
	pool *pgxpool.Pool
	cfg  Config
	wake chan struct{}
	sink Sink

	mu       sync.Mutex
	rejected map[string]int
}

// New is a queue with the default configuration.
func New(pool *pgxpool.Pool) *Queue { return NewWithConfig(pool, Config{}) }

// NewWithConfig is a queue with the batch size, attempts and retention of cfg (zero fields
// are the defaults).
func NewWithConfig(pool *pgxpool.Pool, cfg Config) *Queue {
	return &Queue{pool: pool, cfg: cfg.WithDefaults(), wake: make(chan struct{}, 1), rejected: map[string]int{}}
}

// Config is the queue's configuration with the defaults filled in.
func (q *Queue) Config() Config { return q.cfg }

// Enqueue stores a request durably. With an idempotency key, a request seen before is not
// stored again and dup is true.
func (q *Queue) Enqueue(ctx context.Context, r Request, idemKey string) (id int64, dup bool, err error) {
	h, _ := json.Marshal(cleanMap(nonNil(r.Headers)))
	qs, _ := json.Marshal(cleanMap(nonNil(r.Query)))
	err = pgx.BeginFunc(ctx, q.pool, func(tx pgx.Tx) error {
		if idemKey != "" {
			tag, err := tx.Exec(ctx, "INSERT INTO ingest_idempotency (connector_id, key) VALUES ($1, $2) ON CONFLICT DO NOTHING", r.ConnectorID, idemKey)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				dup = true
				return nil
			}
		}
		return tx.QueryRow(ctx, `INSERT INTO ingest_requests (connector_id, version, remote_ip, method, headers, query, body)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			r.ConnectorID, r.Version, r.RemoteIP, r.Method, h, qs, r.Body).Scan(&id)
	})
	if err == nil && !dup {
		q.Notify()
	}
	return id, dup, err
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// SetSink sets the receiver of processed events. Call it before Run.
func (q *Queue) SetSink(s Sink) { q.sink = s }

// Notify wakes a worker right away instead of at its next poll.
func (q *Queue) Notify() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Reject counts a request turned away by the intake (authentication, network, size, rate).
func (q *Queue) Reject(connectorID string) {
	q.mu.Lock()
	q.rejected[connectorID]++
	q.mu.Unlock()
}

func (q *Queue) flushRejected(ctx context.Context) {
	q.mu.Lock()
	counts := q.rejected
	q.rejected = map[string]int{}
	q.mu.Unlock()
	for id, n := range counts {
		if _, err := q.pool.Exec(ctx, `INSERT INTO connector_stats (connector_id, bucket, rejected) VALUES ($1, date_trunc('minute', now()), $2)
			ON CONFLICT (connector_id, bucket) DO UPDATE SET rejected = connector_stats.rejected + EXCLUDED.rejected`, id, n); err != nil {
			slog.Warn("connector stats not written", "err", err)
		}
	}
}

// Run processes pending requests until ctx ends. Each batch is one transaction: the events,
// failures, statistics and the request status are written together, so a crash leaves the
// requests pending and they are processed again (at least once; event keys absorb repeats).
func (q *Queue) Run(ctx context.Context, workers int, process Processor) {
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				n, err := q.Drain(ctx, process)
				if err != nil && ctx.Err() == nil {
					slog.Error("ingest batch failed", "err", err)
				}
				if n == q.cfg.BatchSize {
					continue
				}
				select {
				case <-ctx.Done():
					return
				case <-q.wake:
				case <-tick.C:
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(10 * time.Second)
		defer tick.Stop()
		upkeep := time.NewTicker(time.Hour)
		defer upkeep.Stop()
		for {
			select {
			case <-ctx.Done():
				q.flushRejected(context.Background())
				return
			case <-tick.C:
				q.flushRejected(ctx)
			case <-upkeep.C:
				if err := Maintain(ctx, q.pool, time.Now().UTC(), q.cfg.Retention); err != nil && ctx.Err() == nil {
					slog.Error("ingest maintenance failed", "err", err)
				}
			}
		}
	}()
	wg.Wait()
}

type statDelta struct {
	received, filtered, skipped, failed, events, duplicates int
	latency                                                 int64
	latencyMax                                              int
}

func (s *statDelta) add(o statDelta) {
	s.received += o.received
	s.filtered += o.filtered
	s.skipped += o.skipped
	s.failed += o.failed
	s.events += o.events
	s.duplicates += o.duplicates
	s.latency += o.latency
	s.latencyMax = max(s.latencyMax, o.latencyMax)
}

const (
	// drainLockKey makes batches run one at a time across workers and Umbrella instances, so
	// events are folded into alerts in the order the requests were received.
	drainLockKey = 0x756d622d696e6773
)

// Drain claims one batch of pending requests and processes it. It returns how many it took.
//
// Ordering: a resolved applied before its firing leaves an alert burning for good, so the
// requests are folded strictly in the order they were received. Each batch takes a
// transaction-level advisory lock before it claims anything: a second worker (or another
// instance) waits until the batch before it is committed and then claims the requests that
// follow it. Folding was already serialized by the alert engine's global lock, held to the end
// of a batch, so little parallelism is lost (only pipelines no longer overlap), and batches can
// no longer deadlock on that lock and the rows of connector_events. Behind that, an event
// older than the one already stored for its connector and key is never applied (see handle),
// which covers requests reprocessed or committed late.
//
// Isolation: every request runs in a savepoint of its own. A request that cannot be stored or
// folded is rolled back alone and retried up to Config.MaxAttempts times, or at once marked failed
// with a failure record when the error is about its data; the rest of the batch goes on.
func (q *Queue) Drain(ctx context.Context, process Processor) (int, error) {
	n := 0
	var after []func()
	err := pgx.BeginFunc(ctx, q.pool, func(tx pgx.Tx) error {
		after = after[:0]
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(drainLockKey)); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id, received_at, connector_id, version, attempts, remote_ip, method, headers, query, body
			FROM ingest_requests WHERE status = 'pending' ORDER BY received_at, id LIMIT $1 FOR UPDATE SKIP LOCKED`, q.cfg.BatchSize)
		if err != nil {
			return err
		}
		reqs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Request, error) {
			var r Request
			err := row.Scan(&r.ID, &r.ReceivedAt, &r.ConnectorID, &r.Version, &r.Attempts, &r.RemoteIP, &r.Method, &r.Headers, &r.Query, &r.Body)
			return r, err
		})
		if err != nil {
			return err
		}
		n = len(reqs)
		stats := map[string]*statDelta{}
		for _, r := range reqs {
			st := stats[r.ConnectorID]
			if st == nil {
				st = &statDelta{}
				stats[r.ConnectorID] = st
			}
			f, err := q.handleIsolated(ctx, tx, r, process, st)
			if err != nil {
				return err
			}
			if f != nil {
				after = append(after, f)
			}
		}
		for id, st := range stats {
			if _, err := tx.Exec(ctx, `INSERT INTO connector_stats AS s (connector_id, bucket, received, filtered, skipped, failed, events, duplicates, latency_ms, latency_max)
				VALUES ($1, date_trunc('minute', now()), $2, $3, $4, $5, $6, $7, $8, $9)
				ON CONFLICT (connector_id, bucket) DO UPDATE SET
					received = s.received + EXCLUDED.received, filtered = s.filtered + EXCLUDED.filtered,
					skipped = s.skipped + EXCLUDED.skipped, failed = s.failed + EXCLUDED.failed,
					events = s.events + EXCLUDED.events, duplicates = s.duplicates + EXCLUDED.duplicates,
					latency_ms = s.latency_ms + EXCLUDED.latency_ms, latency_max = greatest(s.latency_max, EXCLUDED.latency_max)`,
				id, st.received, st.filtered, st.skipped, st.failed, st.events, st.duplicates, st.latency, st.latencyMax); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		// Nothing was taken: the batch is rolled back and Run must not spin on it.
		return 0, err
	}
	for _, f := range after {
		f()
	}
	return n, nil
}

// sinkError is a failure to fold the events of a request into alerts.
type sinkError struct{ err error }

func (e *sinkError) Error() string { return "events not folded into alerts: " + e.err.Error() }
func (e *sinkError) Unwrap() error { return e.err }

// permanent reports an error that a retry cannot fix: the data of the request is rejected.
func permanent(err error) bool {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return false
	}
	switch pe.Code[:2] {
	case "22", "23", "42", "54": // data exception, integrity, syntax or access, program limit
		return true
	}
	return false
}

// handleIsolated processes one request in a savepoint. When it fails, only its own writes are
// undone and the request is left for a retry or marked failed; the batch goes on.
func (q *Queue) handleIsolated(ctx context.Context, tx pgx.Tx, r Request, process Processor, st *statDelta) (func(), error) {
	var after func()
	var own statDelta
	err := pgx.BeginFunc(ctx, tx, func(sp pgx.Tx) error {
		own = statDelta{}
		var err error
		after, err = q.handle(ctx, sp, r, process, &own)
		return err
	})
	if err == nil {
		st.add(own)
		return after, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	st.received++
	st.failed++
	return nil, q.fail(ctx, tx, r, err)
}

// fail records why a request could not be stored or folded. A request is never left done
// with its events or alerts missing: it stays pending for another attempt or becomes failed
// with a failure record, which Reprocess can put back into the queue.
func (q *Queue) fail(ctx context.Context, tx pgx.Tx, r Request, cause error) error {
	msg := clip(cleanText(cause.Error()), 2000)
	if r.Attempts+1 < q.cfg.MaxAttempts && !permanent(cause) {
		slog.Warn("ingest request will be retried", "connector", r.ConnectorID, "request", r.ID, "attempt", r.Attempts+1, "err", cause)
		_, err := tx.Exec(ctx, "UPDATE ingest_requests SET attempts = attempts + 1, error = $3 WHERE id = $1 AND received_at = $2",
			r.ID, r.ReceivedAt, msg)
		return err
	}
	slog.Error("ingest request failed", "connector", r.ConnectorID, "request", r.ID, "attempts", r.Attempts+1, "err", cause)
	node := ""
	var se *sinkError
	if errors.As(cause, &se) {
		node = "alerts"
	}
	raw, note := rawBody(r.Body)
	if _, err := tx.Exec(ctx, `INSERT INTO ingest_failures (connector_id, version, node_id, error, request_id, request_at, item, raw)
		VALUES ($1, $2, $3, $4, $5, $6, 0, $7)`, r.ConnectorID, r.Version, node, msg+note, r.ID, r.ReceivedAt, clip(raw, 4000)); err != nil {
		return err
	}
	return finish(ctx, tx, r, StatusFailed, 0, clip(msg+note, 2000))
}

// handle stores what a request produced and folds its events into alerts.
//
// Every event is checked against the one stored for its connector and key (connector_events
// keeps the receive time and id of the request that set it):
//   - an event from an older request than the stored one is stale and is not applied, so a
//     late or reprocessed request never takes a source back to an earlier state;
//   - an event its own request already stored with the same status and severity was folded
//     then and is not folded again: a reprocessed request does not re-fire (or reopen) what
//     it fired before;
//   - anything else is the newest known state of the source and is folded now. The alert
//     engine stamps it with the time of folding, so reprocessing applies the last word of a
//     source as its current state - never a state the source has since left.
func (q *Queue) handle(ctx context.Context, tx pgx.Tx, r Request, process Processor, st *statDelta) (func(), error) {
	st.received++
	out, perr := process(ctx, r)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	version := r.Version
	if out.Version != 0 {
		version = out.Version
	}
	if perr != nil || out.Result == nil {
		msg := "the connector produced no result"
		if perr != nil {
			msg = perr.Error()
		}
		raw, note := rawBody(r.Body)
		msg = clip(cleanText(msg), 2000) + note
		st.failed++
		if _, err := tx.Exec(ctx, `INSERT INTO ingest_failures (connector_id, version, node_id, error, request_id, request_at, item, raw)
			VALUES ($1, $2, '', $3, $4, $5, 0, $6)`, r.ConnectorID, version, msg, r.ID, r.ReceivedAt, clip(raw, 4000)); err != nil {
			return nil, err
		}
		return nil, finish(ctx, tx, r, StatusFailed, 0, msg)
	}
	res := out.Result
	st.filtered += res.Filtered
	st.skipped += res.Skipped
	inserted := 0
	var apply []flow.Event
	for i := range res.Events {
		e := &res.Events[i]
		cleanEvent(&e.Event)
		labels, _ := json.Marshal(e.Event.Labels)
		fields, _ := json.Marshal(e.Event.Fields)
		if e.Event.Fields == nil {
			fields = []byte("[]")
		}
		// changed: the first delivery of the alert or a change of its status or severity. The
		// rest are duplicates (prev is read before the upsert, in the same snapshot). No row
		// comes back when a newer request already set this key.
		var changed, again bool
		err := tx.QueryRow(ctx, `WITH prev AS (SELECT status, severity, request_id, request_at FROM connector_events WHERE connector_id = $1 AND key = $3)
			INSERT INTO connector_events AS ce (connector_id, version, key, title, ci, signal, method, severity, status,
				external_id, value, labels, request_id, request_at, item, description, fields)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
			ON CONFLICT (connector_id, key) DO UPDATE SET version = EXCLUDED.version, title = EXCLUDED.title, ci = EXCLUDED.ci,
				signal = EXCLUDED.signal, method = EXCLUDED.method, severity = EXCLUDED.severity, status = EXCLUDED.status,
				external_id = EXCLUDED.external_id, value = EXCLUDED.value, labels = EXCLUDED.labels, request_id = EXCLUDED.request_id,
				request_at = EXCLUDED.request_at, item = EXCLUDED.item, description = EXCLUDED.description, fields = EXCLUDED.fields,
				last_seen = now(), seen = ce.seen + 1
			WHERE (ce.request_at, ce.request_id) <= (EXCLUDED.request_at, EXCLUDED.request_id)
			RETURNING coalesce((SELECT status <> $9 OR severity <> $8 FROM prev), true),
				coalesce((SELECT request_id = $13 AND request_at = $14 FROM prev), false)`,
			r.ConnectorID, version, e.Event.Key, e.Event.Title, e.Event.CI, e.Event.Signal, e.Event.Method, e.Event.Severity, e.Event.Status,
			e.Event.ExternalID, e.Event.Value, labels, r.ID, r.ReceivedAt, e.Lineage.Item, e.Event.Description, fields).Scan(&changed, &again)
		if errors.Is(err, pgx.ErrNoRows) {
			st.duplicates++ // stale: a newer request has spoken for this source
			continue
		}
		if err != nil {
			return nil, err
		}
		if changed {
			inserted++
		} else {
			st.duplicates++
		}
		if changed || !again {
			apply = append(apply, e.Event)
		}
	}
	st.events += inserted
	note := bodyNote(r.Body)
	for i := range res.Failures {
		f := &res.Failures[i]
		cleanFailure(f)
		data, _ := json.Marshal(f.Data)
		if _, err := tx.Exec(ctx, `INSERT INTO ingest_failures (connector_id, version, node_id, error, request_id, request_at, item, data, raw)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, r.ConnectorID, version, f.Node, clip(f.Error, 2000)+note, r.ID, r.ReceivedAt, f.Lineage.Item, data, f.Raw); err != nil {
			return nil, err
		}
	}
	st.failed += len(res.Failures)
	lat := time.Since(r.ReceivedAt).Milliseconds()
	st.latency += lat
	if int(lat) > st.latencyMax {
		st.latencyMax = int(lat)
	}
	status, msg := StatusDone, ""
	if len(res.Failures) > 0 {
		status = StatusFailed
		msg = fmt.Sprintf("%d of the records failed; first: %s", len(res.Failures), clip(res.Failures[0].Error, 500)) + note
	}
	if _, err := tx.Exec(ctx, "UPDATE ingest_requests SET version = $3 WHERE id = $1 AND received_at = $2", r.ID, r.ReceivedAt, version); err != nil {
		return nil, err
	}
	after, err := q.deliver(ctx, tx, r, apply)
	if err != nil {
		return nil, err
	}
	return after, finish(ctx, tx, r, status, len(res.Events), msg)
}

// deliver hands the events to the sink in the request's own savepoint. When folding fails the
// request's events are rolled back with it, so the request is retried or marked failed as a
// whole (see fail) and an alert is never lost behind a request marked done.
func (q *Queue) deliver(ctx context.Context, tx pgx.Tx, r Request, events []flow.Event) (func(), error) {
	if q.sink == nil || len(events) == 0 {
		return nil, nil
	}
	after, err := q.sink(ctx, tx, r.ConnectorID, events)
	if err != nil {
		return nil, &sinkError{err}
	}
	return after, nil
}

func finish(ctx context.Context, tx pgx.Tx, r Request, status string, events int, msg string) error {
	_, err := tx.Exec(ctx, `UPDATE ingest_requests SET status = $3, attempts = attempts + 1, processed_at = now(), events = $4, error = $5
		WHERE id = $1 AND received_at = $2`, r.ID, r.ReceivedAt, status, events, msg)
	return err
}

func clip(s string, n int) string {
	if c := textx.Runes(s, n); c != s {
		return c + "…"
	}
	return s
}
