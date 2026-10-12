package alert

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const schema = `
CREATE SEQUENCE IF NOT EXISTS alerts_seq;
CREATE TABLE IF NOT EXISTS alerts (
	id          text PRIMARY KEY,
	seq         bigint NOT NULL,
	dedup_key   text NOT NULL,
	status      text NOT NULL,
	severity    text NOT NULL,
	method      text NOT NULL,
	ci_id       text NOT NULL DEFAULT '',
	team_id     text NOT NULL DEFAULT '',
	service_ids text[] NOT NULL DEFAULT '{}',
	pd_key      text NOT NULL,
	pd_incident text NOT NULL DEFAULT '',
	pd_state    text NOT NULL DEFAULT '',
	search      text NOT NULL DEFAULT '',
	first_seen  timestamptz NOT NULL,
	last_seen   timestamptz NOT NULL,
	resolved_at timestamptz,
	attention   boolean NOT NULL DEFAULT false,
	doc         jsonb NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS alerts_active_key ON alerts (dedup_key) WHERE status <> 'resolved';
CREATE INDEX IF NOT EXISTS alerts_key ON alerts (dedup_key, last_seen DESC);
CREATE INDEX IF NOT EXISTS alerts_recent ON alerts (last_seen DESC);
CREATE INDEX IF NOT EXISTS alerts_pd_key ON alerts (pd_key);
CREATE INDEX IF NOT EXISTS alerts_attention ON alerts (seq) WHERE attention;

CREATE TABLE IF NOT EXISTS alert_timeline (
	id       bigserial PRIMARY KEY,
	alert_id text NOT NULL REFERENCES alerts (id) ON DELETE CASCADE,
	at       timestamptz NOT NULL,
	kind     text NOT NULL,
	code     text NOT NULL,
	args     jsonb NOT NULL DEFAULT '{}',
	author   text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS alert_timeline_alert ON alert_timeline (alert_id, id);

CREATE TABLE IF NOT EXISTS alert_keys (
	name  text PRIMARY KEY,
	value bytea NOT NULL
);
`

// pdOffMigration: earlier versions marked every alert "failed" while PagerDuty was turned off
// and put the English error on the timeline. Such alerts were never meant for PagerDuty: they
// are "off", and the false failures are taken off the timeline. It runs at every start and
// finds nothing once done.
const pdOffMigration = `
UPDATE alerts SET pd_state = 'off', attention = (status <> 'resolved'),
	doc = jsonb_set(doc, '{pd}', ((doc->'pd') - 'error' - 'retry') || '{"state": "off"}'::jsonb)
WHERE pd_state = 'failed' AND doc->'pd'->>'error' = 'PagerDuty is not enabled';
DELETE FROM alert_timeline WHERE kind = 'pagerduty' AND code = 'pd_failed'
	AND 'PagerDuty is not enabled' IN (args->>'error', args->>'detail');
`

// lockKey serializes folding events into alerts between workers and Umbrella instances, so
// two events of the same item and signal never open two alerts.
const lockKey = 0x756d622d616c7274

func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(lockKey)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, schema); err != nil {
			return fmt.Errorf("create alert tables: %w", err)
		}
		if _, err := tx.Exec(ctx, feedSchema); err != nil {
			return fmt.Errorf("create the incident change feed: %w", err)
		}
		if _, err := tx.Exec(ctx, pdOffMigration); err != nil {
			return fmt.Errorf("mark alerts not sent to a disabled PagerDuty: %w", err)
		}
		return nil
	})
}

var ErrNotFound = errors.New("alert not found")

// LinkKey is the key acknowledgement links in notifications are signed with. It is made once
// and shared by every Umbrella instance through the database.
func LinkKey(ctx context.Context, pool *pgxpool.Pool) ([]byte, error) {
	fresh := make([]byte, 32)
	if _, err := rand.Read(fresh); err != nil {
		return nil, err
	}
	var key []byte
	err := pool.QueryRow(ctx, `WITH ins AS (INSERT INTO alert_keys (name, value) VALUES ('links', $1) ON CONFLICT (name) DO NOTHING RETURNING value)
		SELECT value FROM ins UNION ALL SELECT value FROM alert_keys WHERE name = 'links' LIMIT 1`, fresh).Scan(&key)
	return key, err
}

// note adds lines to the timeline of an alert without changing the alert.
func note(ctx context.Context, db *pgxpool.Pool, id string, entries []Entry) error {
	for _, e := range entries {
		args := e.Args
		if args == nil {
			args = map[string]string{}
		}
		tag, err := db.Exec(ctx, `INSERT INTO alert_timeline (alert_id, at, kind, code, args, author)
			SELECT id, $2, $3, $4, $5, $6 FROM alerts WHERE id = $1`, id, e.At, e.Kind, e.Code, args, e.Author)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
	}
	return nil
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func scanAlert(row pgx.Row) (*Alert, error) {
	var doc []byte
	if err := row.Scan(&doc); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	a := &Alert{}
	if err := json.Unmarshal(doc, a); err != nil {
		return nil, err
	}
	if a.Sources == nil {
		a.Sources = map[string]*Source{}
	}
	if a.Labels == nil {
		a.Labels = map[string]string{}
	}
	return a, nil
}

// sqlActive selects the alerts that are not resolved (open or acknowledged). The schema keeps
// the literal in its partial index.
const sqlActive = "status <> '" + StatusResolved + "'"

func lockByID(ctx context.Context, tx pgx.Tx, id string) (*Alert, error) {
	return scanAlert(tx.QueryRow(ctx, "SELECT doc FROM alerts WHERE id = $1 FOR UPDATE", id))
}

// current returns the alert of a key that is active or was resolved within the window.
func current(ctx context.Context, tx pgx.Tx, key string, since time.Time) (*Alert, error) {
	return scanAlert(tx.QueryRow(ctx, `SELECT doc FROM alerts WHERE dedup_key = $1 AND (`+sqlActive+` OR resolved_at >= $2)
		ORDER BY (`+sqlActive+`) DESC, last_seen DESC LIMIT 1 FOR UPDATE`, key, since))
}

func activeByKey(ctx context.Context, tx pgx.Tx, key string) (*Alert, error) {
	return scanAlert(tx.QueryRow(ctx, "SELECT doc FROM alerts WHERE dedup_key = $1 AND "+sqlActive+" FOR UPDATE", key))
}

func nextID(ctx context.Context, tx pgx.Tx) (string, int64, error) {
	var n int64
	err := tx.QueryRow(ctx, "SELECT nextval('alerts_seq')").Scan(&n)
	return fmt.Sprintf("INC-%d", n), n, err
}

func searchText(a *Alert) string {
	parts := []string{a.ID, a.Title, a.CIName, a.Signal}
	for _, s := range a.Route.Services {
		parts = append(parts, s.Name)
	}
	if a.Route.Team != nil {
		parts = append(parts, a.Route.Team.Name)
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func attention(a *Alert) bool { return Active(a.Status) || a.PD.Retry != "" || a.FollowUp != "" }

// save writes the alert and appends the timeline entries.
func save(ctx context.Context, tx pgx.Tx, a *Alert, entries []Entry) error {
	doc, err := json.Marshal(a)
	if err != nil {
		return err
	}
	var seq int64
	fmt.Sscanf(a.ID, "INC-%d", &seq)
	team := ""
	if a.Route.Team != nil {
		team = a.Route.Team.ID
	}
	_, err = tx.Exec(ctx, `INSERT INTO alerts (id, seq, dedup_key, status, severity, method, ci_id, team_id, service_ids, pd_key, pd_incident,
			pd_state, search, first_seen, last_seen, resolved_at, attention, doc)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		ON CONFLICT (id) DO UPDATE SET dedup_key = EXCLUDED.dedup_key, status = EXCLUDED.status, severity = EXCLUDED.severity,
			method = EXCLUDED.method, ci_id = EXCLUDED.ci_id, team_id = EXCLUDED.team_id, service_ids = EXCLUDED.service_ids,
			pd_incident = EXCLUDED.pd_incident, pd_state = EXCLUDED.pd_state, search = EXCLUDED.search, last_seen = EXCLUDED.last_seen,
			resolved_at = EXCLUDED.resolved_at, attention = EXCLUDED.attention, doc = EXCLUDED.doc`,
		a.ID, seq, a.DedupKey, a.Status, a.Severity, a.Method, a.CIID, team, a.Route.ServiceIDs(), a.PD.Key, a.PD.IncidentID,
		a.PD.State, searchText(a), a.FirstSeen, a.LastSeen, a.ResolvedAt, attention(a), doc)
	if err != nil {
		return err
	}
	for _, e := range entries {
		args := e.Args
		if args == nil {
			args = map[string]string{}
		}
		if _, err := tx.Exec(ctx, "INSERT INTO alert_timeline (alert_id, at, kind, code, args, author) VALUES ($1, $2, $3, $4, $5, $6)",
			a.ID, e.At, e.Kind, e.Code, args, e.Author); err != nil {
			return err
		}
	}
	return nil
}

// Filter selects alerts for the incident list.
type Filter struct {
	// Status: active (open and acknowledged), open, acknowledged, resolved or empty for all.
	Status     string
	Severities []string
	Method     string
	TeamID     string
	ServiceID  string
	CIID       string
	Query      string
	// PD: failed lists alerts PagerDuty has not taken (pending or failed).
	PD         string
	Fallback   bool
	Suppressed bool
	Since      time.Time
	Limit      int
	// ScopeServiceIDs limits everything, counts included, to alerts of these business services;
	// empty means no limit.
	ScopeServiceIDs []string
}

type Counts struct {
	Active       int            `json:"active"`
	Open         int            `json:"open"`
	Acknowledged int            `json:"acknowledged"`
	BySeverity   map[string]int `json:"by_severity"`
	PDNotTaken   int            `json:"pd_not_taken"`
	Fallback     int            `json:"fallback"`
	Suppressed   int            `json:"suppressed"`
	Unbound      int            `json:"unbound"`
	// PDEnabled: PagerDuty is turned on; while it is off the interface hides what is about it.
	PDEnabled bool `json:"pd_enabled"`
}

type Page struct {
	Alerts []Alert `json:"alerts"`
	Counts Counts  `json:"counts"`
	More   bool    `json:"more"`
}

const MaxLimit = 1000

func (f Filter) where() (string, []any) {
	var conds []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	switch f.Status {
	case "active":
		conds = append(conds, sqlActive)
	case StatusOpen, StatusAcknowledged, StatusResolved:
		conds = append(conds, "status = "+arg(f.Status))
	}
	if len(f.Severities) > 0 {
		conds = append(conds, "severity = ANY("+arg(f.Severities)+")")
	}
	if f.Method != "" {
		conds = append(conds, "method = "+arg(f.Method))
	}
	if f.TeamID != "" {
		conds = append(conds, "team_id = "+arg(f.TeamID))
	}
	if f.ServiceID != "" {
		conds = append(conds, arg(f.ServiceID)+" = ANY(service_ids)")
	}
	if len(f.ScopeServiceIDs) > 0 {
		if f.ServiceID != "" && !slices.Contains(f.ScopeServiceIDs, f.ServiceID) {
			conds = append(conds, "FALSE")
		}
		conds = append(conds, "service_ids && "+arg(f.ScopeServiceIDs)+"::text[]")
	}
	if f.CIID != "" {
		conds = append(conds, "ci_id = "+arg(f.CIID))
	}
	if q := strings.ToLower(strings.TrimSpace(f.Query)); q != "" {
		q = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q)
		conds = append(conds, "search LIKE "+arg("%"+q+"%"))
	}
	if f.PD == "failed" {
		conds = append(conds, "pd_state IN ('"+PDPending+"', '"+PDFailed+"') AND "+sqlActive)
	}
	if f.Fallback {
		conds = append(conds, "(doc->>'fallback')::boolean")
	}
	if f.Suppressed {
		conds = append(conds, "(doc->>'suppressed')::boolean")
	}
	if !f.Since.IsZero() {
		conds = append(conds, "last_seen >= "+arg(f.Since))
	}
	if len(conds) == 0 {
		return "TRUE", args
	}
	return strings.Join(conds, " AND "), args
}

// forCounts is the filter behind the incident tiles: active alerts narrowed by
// everything except the fields the tiles themselves toggle (status,
// severities, PagerDuty, fallback, suppressed), so each tile shows how many
// alerts it would list together with the other filters.
func (f Filter) forCounts() Filter {
	f.Status, f.Severities, f.PD, f.Fallback, f.Suppressed, f.Limit = "active", nil, "", false, false, 0
	return f
}

func list(ctx context.Context, q querier, f Filter) (Page, error) {
	out := Page{Alerts: []Alert{}, Counts: Counts{BySeverity: map[string]int{}}}
	limit := f.Limit
	if limit <= 0 || limit > MaxLimit {
		limit = 200
	}
	where, args := f.where()
	rows, err := q.Query(ctx, "SELECT doc FROM alerts WHERE "+where+fmt.Sprintf(" ORDER BY ("+sqlActive+") DESC, last_seen DESC LIMIT %d", limit+1), args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		out.Alerts = append(out.Alerts, *a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(out.Alerts) > limit {
		out.Alerts, out.More = out.Alerts[:limit], true
	}
	c := &out.Counts
	cwhere, cargs := f.forCounts().where()
	rows, err = q.Query(ctx, `SELECT status, severity, pd_state, (doc->>'fallback')::boolean, (doc->>'suppressed')::boolean, ci_id = '', count(*)::int
		FROM alerts WHERE `+cwhere+` GROUP BY 1, 2, 3, 4, 5, 6`, cargs...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var status, sev, pd string
		var fallback, suppressed, unbound bool
		var n int
		if err := rows.Scan(&status, &sev, &pd, &fallback, &suppressed, &unbound, &n); err != nil {
			return out, err
		}
		c.Active += n
		if status == StatusOpen {
			c.Open += n
		} else {
			c.Acknowledged += n
		}
		c.BySeverity[sev] += n
		if pd == PDPending || pd == PDFailed {
			c.PDNotTaken += n
		}
		if fallback {
			c.Fallback += n
		}
		if suppressed {
			c.Suppressed += n
		}
		if unbound {
			c.Unbound += n
		}
	}
	return out, rows.Err()
}

func get(ctx context.Context, q querier, id string) (Alert, []Entry, error) {
	a, err := scanAlert(q.QueryRow(ctx, "SELECT doc FROM alerts WHERE id = $1", id))
	if err != nil {
		return Alert{}, nil, err
	}
	if a == nil {
		return Alert{}, nil, ErrNotFound
	}
	rows, err := q.Query(ctx, "SELECT id, at, kind, code, args, author FROM alert_timeline WHERE alert_id = $1 ORDER BY id", id)
	if err != nil {
		return Alert{}, nil, err
	}
	entries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Entry, error) {
		var e Entry
		err := row.Scan(&e.ID, &e.At, &e.Kind, &e.Code, &e.Args, &e.Author)
		return e, err
	})
	return *a, entries, err
}
