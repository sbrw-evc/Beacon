package model

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
)

func TestSeverities(t *testing.T) {
	if got := SeverityNames(); !slices.Equal(got, []string{"critical", "error", "warning", "low", "info"}) {
		t.Fatalf("names = %v", got)
	}
	for i, s := range Severities {
		if want := len(Severities) - i; s.Rank != want {
			t.Errorf("%s rank = %d, want %d", s.Name, s.Rank, want)
		}
		if want := fmt.Sprintf("P%d", i+1); s.Priority != want {
			t.Errorf("%s priority = %s, want %s", s.Name, s.Priority, want)
		}
		if s.Tone == "" || s.Title.En == "" || s.Title.Ru == "" {
			t.Errorf("%s has no tone or title: %+v", s.Name, s)
		}
		var c SeverityCounts
		c.Add(s.Name, 2)
		if c.Get(s.Name) != 2 {
			t.Errorf("SeverityCounts has no slot for %s", s.Name)
		}
	}
	if SeverityRank("low") <= SeverityRank("info") || SeverityRank("low") >= SeverityRank("warning") {
		t.Fatal("low is not between warning and info")
	}
	if SeverityPriority("critical") != "P1" || SeverityPriority("info") != "P5" || SeverityPriority("bogus") != "" {
		t.Fatal("SeverityPriority")
	}
	for i := 1; i < len(Severities); i++ {
		if Severities[i].Rank >= Severities[i-1].Rank {
			t.Fatalf("severities are not ordered most severe first: %v", Severities)
		}
	}
	if ValidSeverity("bogus") || !ValidSeverity("warning") {
		t.Fatal("ValidSeverity")
	}
}

func TestSeverityCountsJSON(t *testing.T) {
	type counts struct {
		Total int `json:"total"`
		SeverityCounts
		Open int `json:"open"`
	}
	c := counts{Total: 4}
	for _, s := range []string{"critical", "critical", "low", "info", "bogus"} {
		c.Add(s, 1)
	}
	b, _ := json.Marshal(c)
	if want := `{"total":4,"critical":2,"error":0,"warning":0,"low":1,"info":1,"open":0}`; string(b) != want {
		t.Fatalf("json = %s, want %s", b, want)
	}
	if c.Get("critical") != 2 || c.Get("bogus") != 0 {
		t.Fatal("Get")
	}
}

func TestCriticalityRank(t *testing.T) {
	for v, want := range map[string]int{CriticalityCritical: 4, CriticalityHigh: 3, CriticalityMedium: 2, CriticalityLow: 1, "": 0, "bogus": 0} {
		if got := CriticalityRank(v); got != want {
			t.Errorf("CriticalityRank(%q) = %d, want %d", v, got, want)
		}
	}
}

func TestMethods(t *testing.T) {
	if MethodCounterpart(MethodRED) != MethodUSE || MethodCounterpart(MethodUSE) != MethodRED || MethodCounterpart(MethodOther) != "" {
		t.Fatal("counterparts")
	}
	if !ValidMethod("other") || ValidMethod("") || ValidMethod("RED") {
		t.Fatal("ValidMethod")
	}
}
