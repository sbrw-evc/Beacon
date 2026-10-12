package alert

import (
	"strings"
	"testing"
	"time"
)

func TestSeverityRankSQL(t *testing.T) {
	want := "CASE severity WHEN 'critical' THEN 5 WHEN 'error' THEN 4 WHEN 'warning' THEN 3 WHEN 'low' THEN 2 WHEN 'info' THEN 1 ELSE 0 END"
	if got := severityRankSQL(); got != want {
		t.Fatalf("severityRankSQL() = %q, want %q", got, want)
	}
	if !strings.Contains(boardOrder, "CASE status WHEN 'open' THEN 0 WHEN 'acknowledged' THEN 1 ELSE 2 END") {
		t.Fatalf("board order = %q", boardOrder)
	}
	where, _ := BoardFilter{ShowAcknowledged: true, ResolvedMinutes: 5}.where(time.Now())
	if !strings.Contains(where, "(status = 'open' OR status = 'acknowledged' OR (status = 'resolved' AND resolved_at >= $1))") {
		t.Fatalf("where = %q", where)
	}
	for s, rank := range map[string]int{"critical": 5, "error": 4, "warning": 3, "low": 2, "info": 1, "bogus": 0, "": 0} {
		if got := SeverityRank(s); got != rank {
			t.Errorf("SeverityRank(%q) = %d, want %d", s, got, rank)
		}
	}
}
