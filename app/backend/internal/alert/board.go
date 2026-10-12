package alert

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

// BoardLimit is the most incidents a wallboard shows.
const BoardLimit = 200

// BoardFilter selects the incidents of a TV wallboard.
type BoardFilter struct {
	// Scope: an alert matches when its item is in CIIDs, or one of its services is in
	// ServiceIDs, or its team is in TeamIDs. All empty: every alert.
	CIIDs      []string
	ServiceIDs []string
	TeamIDs    []string
	// Severities and Methods: empty means all.
	Severities       []string
	Methods          []string
	ShowAcknowledged bool
	ShowSuppressed   bool
	// ResolvedMinutes also selects alerts resolved within the last N minutes.
	ResolvedMinutes int
	// Oldest orders alerts of one severity and status by the time they opened, oldest first.
	Oldest bool
	Limit  int
}

// BoardCounts count every matching alert, not only the ones returned.
type BoardCounts struct {
	Total int `json:"total"`
	model.SeverityCounts
	Acknowledged int `json:"acknowledged"`
	Open         int `json:"open"`
	Resolved     int `json:"resolved"`
}

type BoardPage struct {
	Alerts []Alert
	Counts BoardCounts
	More   bool
}

func (f BoardFilter) where(now time.Time) (string, []any) {
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	var conds []string
	var scope []string
	if len(f.CIIDs) > 0 {
		scope = append(scope, "ci_id = ANY("+arg(f.CIIDs)+")")
	}
	if len(f.ServiceIDs) > 0 {
		scope = append(scope, "service_ids && "+arg(f.ServiceIDs)+"::text[]")
	}
	if len(f.TeamIDs) > 0 {
		scope = append(scope, "team_id = ANY("+arg(f.TeamIDs)+")")
	}
	if len(scope) > 0 {
		conds = append(conds, "("+strings.Join(scope, " OR ")+")")
	}
	statuses := []string{"status = '" + StatusOpen + "'"}
	if f.ShowAcknowledged {
		statuses = append(statuses, "status = '"+StatusAcknowledged+"'")
	}
	if f.ResolvedMinutes > 0 {
		statuses = append(statuses, "(status = '"+StatusResolved+"' AND resolved_at >= "+arg(now.Add(-time.Duration(f.ResolvedMinutes)*time.Minute))+")")
	}
	conds = append(conds, "("+strings.Join(statuses, " OR ")+")")
	if len(f.Severities) > 0 {
		conds = append(conds, "severity = ANY("+arg(f.Severities)+")")
	}
	if len(f.Methods) > 0 {
		conds = append(conds, "method = ANY("+arg(f.Methods)+")")
	}
	if !f.ShowSuppressed {
		conds = append(conds, "NOT COALESCE((doc->>'suppressed')::boolean, false)")
	}
	return strings.Join(conds, " AND "), args
}

// severityRankSQL is the rank of the severity column by model.Severities; unknown ones rank 0.
func severityRankSQL() string {
	var b strings.Builder
	b.WriteString("CASE severity")
	for _, s := range model.Severities {
		fmt.Fprintf(&b, " WHEN '%s' THEN %d", s.Name, s.Rank)
	}
	b.WriteString(" ELSE 0 END")
	return b.String()
}

var boardOrder = " ORDER BY " + severityRankSQL() + " DESC,\n\tCASE status WHEN '" + StatusOpen + "' THEN 0 WHEN '" + StatusAcknowledged +
	"' THEN 1 ELSE 2 END, (doc->>'opened_at')::timestamptz %s, seq %s"

// Board returns the alerts of a wallboard: most severe first, open before acknowledged and
// resolved ones last, then by the time they opened.
func (e *Engine) Board(ctx context.Context, f BoardFilter) (BoardPage, error) {
	out := BoardPage{Alerts: []Alert{}}
	limit := f.Limit
	if limit <= 0 || limit > BoardLimit {
		limit = BoardLimit
	}
	where, args := f.where(e.now())
	dir := "DESC"
	if f.Oldest {
		dir = "ASC"
	}
	rows, err := e.db.Query(ctx, "SELECT doc FROM alerts WHERE "+where+fmt.Sprintf(boardOrder, dir, dir)+fmt.Sprintf(" LIMIT %d", limit+1), args...)
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
	rows, err = e.db.Query(ctx, "SELECT status, severity, count(*)::int FROM alerts WHERE "+where+" GROUP BY 1, 2", args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	c := &out.Counts
	for rows.Next() {
		var status, sev string
		var n int
		if err := rows.Scan(&status, &sev, &n); err != nil {
			return out, err
		}
		c.Total += n
		switch status {
		case StatusOpen:
			c.Open += n
		case StatusAcknowledged:
			c.Acknowledged += n
		case StatusResolved:
			c.Resolved += n
		}
		c.Add(sev, n)
	}
	return out, rows.Err()
}
