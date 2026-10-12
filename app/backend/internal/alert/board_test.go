package alert

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store/storetest"
)

type boardAlert struct {
	id, ci, team, sev, method, status string
	services                          []string
	opened                            time.Duration // before now
	resolved                          time.Duration // before now, for resolved alerts
	suppressed                        bool
}

func boardEngine(t *testing.T, now time.Time, alerts []boardAlert) *Engine {
	t.Helper()
	ctx := context.Background()
	cfg := storetest.TempDatabase(t)
	db, err := pgxpool.New(ctx, cfg.DSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	e := New(db, store.New())
	e.SetClock(func() time.Time { return now })
	err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		for _, b := range alerts {
			a := &Alert{ID: b.id, DedupKey: "k-" + b.id, Title: "t " + b.id, CIID: b.ci, CIName: "ci " + b.ci, Signal: "s", Method: b.method,
				Severity: b.sev, Status: b.status, Sources: map[string]*Source{}, Labels: map[string]string{}, Count: 1,
				FirstSeen: now.Add(-b.opened), OpenedAt: now.Add(-b.opened), LastSeen: now, Suppressed: b.suppressed, PD: PD{Key: "umb-" + b.id}}
			for _, s := range b.services {
				a.Route.Services = append(a.Route.Services, Ref{ID: s, Name: s})
			}
			if b.team != "" {
				a.Route.Team = &Ref{ID: b.team, Name: b.team}
			}
			if b.status == StatusResolved {
				r := now.Add(-b.resolved)
				a.ResolvedAt = &r
			}
			if err := save(ctx, tx, a, nil); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func boardIDs(t *testing.T, e *Engine, f BoardFilter) ([]string, BoardPage) {
	t.Helper()
	p, err := e.Board(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, a := range p.Alerts {
		ids = append(ids, a.ID)
	}
	return ids, p
}

func TestBoard(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	e := boardEngine(t, now, []boardAlert{
		{id: "INC-1", ci: "CI-1", team: "T-1", services: []string{"S-1"}, sev: "warning", method: "use", status: StatusOpen, opened: 10 * time.Minute},
		{id: "INC-2", ci: "CI-2", team: "T-2", services: []string{"S-2"}, sev: "critical", method: "red", status: StatusAcknowledged, opened: 20 * time.Minute},
		{id: "INC-3", ci: "CI-3", team: "T-3", services: []string{"S-3", "S-1"}, sev: "critical", method: "other", status: StatusOpen, opened: 5 * time.Minute},
		{id: "INC-4", ci: "CI-4", sev: "critical", method: "use", status: StatusOpen, opened: 30 * time.Minute},
		{id: "INC-5", ci: "CI-5", team: "T-2", sev: "info", method: "other", status: StatusOpen, opened: time.Minute, suppressed: true},
		{id: "INC-6", ci: "CI-1", team: "T-1", services: []string{"S-1"}, sev: "error", method: "red", status: StatusResolved, opened: time.Hour, resolved: 5 * time.Minute},
		{id: "INC-7", ci: "CI-1", team: "T-1", services: []string{"S-1"}, sev: "error", method: "red", status: StatusResolved, opened: 2 * time.Hour, resolved: 90 * time.Minute},
		{id: "INC-8", sev: "warning", method: "other", status: StatusOpen, opened: 3 * time.Minute},
	})
	cases := []struct {
		name string
		f    BoardFilter
		want []string
	}{
		{"open only by default, newest first", BoardFilter{}, []string{"INC-3", "INC-4", "INC-8", "INC-1"}},
		{"oldest first", BoardFilter{Oldest: true}, []string{"INC-4", "INC-3", "INC-1", "INC-8"}},
		{"acknowledged after open of the same severity", BoardFilter{ShowAcknowledged: true}, []string{"INC-3", "INC-4", "INC-2", "INC-8", "INC-1"}},
		{"suppressed", BoardFilter{ShowSuppressed: true}, []string{"INC-3", "INC-4", "INC-8", "INC-1", "INC-5"}},
		{"resolved within the window, last within the severity", BoardFilter{ResolvedMinutes: 60}, []string{"INC-3", "INC-4", "INC-6", "INC-8", "INC-1"}},
		{"severities", BoardFilter{Severities: []string{"warning", "info"}, ShowSuppressed: true}, []string{"INC-8", "INC-1", "INC-5"}},
		{"methods", BoardFilter{Methods: []string{"use", "red"}, ShowAcknowledged: true}, []string{"INC-4", "INC-2", "INC-1"}},
		{"ci scope", BoardFilter{CIIDs: []string{"CI-1"}, ResolvedMinutes: 120}, []string{"INC-6", "INC-7", "INC-1"}},
		{"service scope uses every service", BoardFilter{ServiceIDs: []string{"S-1"}}, []string{"INC-3", "INC-1"}},
		{"team scope", BoardFilter{TeamIDs: []string{"T-2"}, ShowAcknowledged: true, ShowSuppressed: true}, []string{"INC-2", "INC-5"}},
		{"union of scopes", BoardFilter{CIIDs: []string{"CI-4"}, ServiceIDs: []string{"S-2"}, TeamIDs: []string{"T-1"}, ShowAcknowledged: true},
			[]string{"INC-4", "INC-2", "INC-1"}},
	}
	for _, c := range cases {
		if got, _ := boardIDs(t, e, c.f); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}

	ids, p := boardIDs(t, e, BoardFilter{ShowAcknowledged: true, ShowSuppressed: true, ResolvedMinutes: 120, Limit: 3})
	if len(ids) != 3 || !p.More {
		t.Fatalf("limited page = %v more=%v", ids, p.More)
	}
	want := BoardCounts{Total: 8, SeverityCounts: model.SeverityCounts{Critical: 3, Error: 2, Warning: 2, Info: 1}, Open: 5, Acknowledged: 1, Resolved: 2}
	if p.Counts != want {
		t.Fatalf("counts cover every match: %+v, want %+v", p.Counts, want)
	}
	if _, p := boardIDs(t, e, BoardFilter{}); p.More || p.Counts.Total != 4 || p.Counts.Open != 4 || p.Counts.Critical != 2 {
		t.Fatalf("default counts = %+v", p.Counts)
	}
}

func TestBoardPriorityOrder(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	e := boardEngine(t, now, []boardAlert{
		{id: "INC-5", sev: "info", method: "other", status: StatusOpen, opened: time.Minute},
		{id: "INC-4", sev: "low", method: "other", status: StatusOpen, opened: time.Minute},
		{id: "INC-3", sev: "warning", method: "other", status: StatusOpen, opened: time.Minute},
		{id: "INC-2", sev: "error", method: "other", status: StatusOpen, opened: time.Minute},
		{id: "INC-1", sev: "critical", method: "other", status: StatusOpen, opened: time.Minute},
		{id: "INC-6", sev: "low", method: "other", status: StatusOpen, opened: 2 * time.Minute},
	})
	ids, p := boardIDs(t, e, BoardFilter{})
	if want := []string{"INC-1", "INC-2", "INC-3", "INC-4", "INC-6", "INC-5"}; !slices.Equal(ids, want) {
		t.Fatalf("P1..P5 order: got %v, want %v", ids, want)
	}
	if want := (model.SeverityCounts{Critical: 1, Error: 1, Warning: 1, Low: 2, Info: 1}); p.Counts.SeverityCounts != want {
		t.Fatalf("counts = %+v, want %+v", p.Counts.SeverityCounts, want)
	}
	if ids, _ := boardIDs(t, e, BoardFilter{Severities: []string{"low"}}); !slices.Equal(ids, []string{"INC-4", "INC-6"}) {
		t.Fatalf("low only: %v", ids)
	}
}
