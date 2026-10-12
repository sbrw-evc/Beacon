package model

const (
	SeverityCritical = "critical"
	SeverityError    = "error"
	SeverityWarning  = "warning"
	SeverityLow      = "low"
	SeverityInfo     = "info"
)

// Severity is a level of the alert scale: an incident priority from P1 (critical) to P5
// (informational). Name is what incidents, rules and settings store; Priority is how the
// interface and the messages show it next to Title. Rank orders the levels (higher is more
// severe, unknown ones rank 0); Tone is the colour the interface shows the level in.
type Severity struct {
	Name     string        `json:"name"`
	Priority string        `json:"priority"`
	Rank     int           `json:"rank"`
	Tone     string        `json:"tone"`
	Title    SeverityTitle `json:"title"`
}

// SeverityTitle is the word for a level in the languages of the interface.
type SeverityTitle struct {
	En string `json:"en"`
	Ru string `json:"ru"`
}

// Severities is the alert scale, most severe first: the engine, the wallboards, the catalog
// map and the interface all derive their order, counters and colours from it. The stored
// names of the first four levels predate priorities and stay as they were; low is P4.
var Severities = []Severity{
	{Name: SeverityCritical, Priority: "P1", Rank: 5, Tone: "critical", Title: SeverityTitle{"Critical", "Критический"}},
	{Name: SeverityError, Priority: "P2", Rank: 4, Tone: "error", Title: SeverityTitle{"High", "Высокий"}},
	{Name: SeverityWarning, Priority: "P3", Rank: 3, Tone: "warn", Title: SeverityTitle{"Medium", "Средний"}},
	{Name: SeverityLow, Priority: "P4", Rank: 2, Tone: "low", Title: SeverityTitle{"Low", "Низкий"}},
	{Name: SeverityInfo, Priority: "P5", Rank: 1, Tone: "info", Title: SeverityTitle{"Informational", "Информационный"}},
}

// SeverityOf is the level of a name; ok is false for an unknown one.
func SeverityOf(name string) (Severity, bool) {
	for _, s := range Severities {
		if s.Name == name {
			return s, true
		}
	}
	return Severity{}, false
}

// SeverityPriority is the priority (P1..P5) of a severity, or "" for an unknown one.
func SeverityPriority(name string) string {
	s, _ := SeverityOf(name)
	return s.Priority
}

// SeverityNames lists the names of Severities, most severe first.
func SeverityNames() []string {
	out := make([]string, 0, len(Severities))
	for _, s := range Severities {
		out = append(out, s.Name)
	}
	return out
}

// SeverityRank orders severities; unknown ones rank 0.
func SeverityRank(name string) int {
	s, _ := SeverityOf(name)
	return s.Rank
}

// ValidSeverity reports whether the name is a level of the scale.
func ValidSeverity(name string) bool { return SeverityRank(name) > 0 }

// SeverityCounts counts alerts or events per severity; it is embedded in the counters of the
// API, whose fields are the names of the levels.
type SeverityCounts struct {
	Critical int `json:"critical"`
	Error    int `json:"error"`
	Warning  int `json:"warning"`
	Low      int `json:"low"`
	Info     int `json:"info"`
}

// Add counts n of a severity; an unknown severity is not counted.
func (c *SeverityCounts) Add(name string, n int) {
	if p := c.slot(name); p != nil {
		*p += n
	}
}

// Get is the count of a severity.
func (c SeverityCounts) Get(name string) int {
	if p := c.slot(name); p != nil {
		return *p
	}
	return 0
}

func (c *SeverityCounts) slot(name string) *int {
	switch name {
	case SeverityCritical:
		return &c.Critical
	case SeverityError:
		return &c.Error
	case SeverityWarning:
		return &c.Warning
	case SeverityLow:
		return &c.Low
	case SeverityInfo:
		return &c.Info
	}
	return nil
}
