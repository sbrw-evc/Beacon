package alert

import (
	"context"
	"testing"
	"time"
)

func TestCountsFollowListFilters(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	e := boardEngine(t, now, []boardAlert{
		{id: "A1", ci: "CI-1", team: "T-1", sev: "critical", method: "red", status: StatusOpen, services: []string{"S-1"}},
		{id: "A2", ci: "CI-1", team: "T-1", sev: "warning", method: "red", status: StatusAcknowledged, services: []string{"S-1"}, suppressed: true},
		{id: "A3", ci: "CI-2", team: "T-2", sev: "critical", method: "use", status: StatusOpen, services: []string{"S-2"}},
		{id: "A4", ci: "", team: "T-2", sev: "error", method: "other", status: StatusOpen},
		{id: "A5", ci: "CI-1", team: "T-1", sev: "critical", method: "red", status: StatusResolved, services: []string{"S-1"}, resolved: time.Minute},
	})
	counts := func(f Filter) Counts {
		t.Helper()
		p, err := e.List(context.Background(), f)
		if err != nil {
			t.Fatal(err)
		}
		return p.Counts
	}

	if c := counts(Filter{}); c.Active != 4 || c.Open != 3 || c.Acknowledged != 1 || c.BySeverity["critical"] != 2 || c.Unbound != 1 || c.Suppressed != 1 {
		t.Errorf("unfiltered = %+v", c)
	}
	if c := counts(Filter{TeamID: "T-1"}); c.Active != 2 || c.Open != 1 || c.Acknowledged != 1 || c.BySeverity["critical"] != 1 || c.Suppressed != 1 || c.Unbound != 0 {
		t.Errorf("by team = %+v", c)
	}
	if c := counts(Filter{ServiceID: "S-2"}); c.Active != 1 || c.BySeverity["critical"] != 1 {
		t.Errorf("by service = %+v", c)
	}
	if c := counts(Filter{CIID: "CI-1", Method: "red"}); c.Active != 2 {
		t.Errorf("by item and method = %+v", c)
	}
	if c := counts(Filter{Query: "ci ci-2"}); c.Active != 1 || c.Open != 1 {
		t.Errorf("by search = %+v", c)
	}
	if c := counts(Filter{Since: now.Add(time.Hour)}); c.Active != 0 {
		t.Errorf("since the future = %+v", c)
	}
	// The fields the tiles toggle do not narrow the tiles.
	if c := counts(Filter{TeamID: "T-1", Status: StatusOpen, Severities: []string{"critical"}, PD: "failed", Fallback: true, Suppressed: true}); c.Active != 2 || c.Acknowledged != 1 || c.BySeverity["warning"] != 1 {
		t.Errorf("tile toggles narrowed the counts: %+v", c)
	}
	// Resolved alerts never count, even when the list shows them.
	if c := counts(Filter{Status: StatusResolved, TeamID: "T-1"}); c.Active != 2 {
		t.Errorf("resolved list = %+v", c)
	}
}
