package rules

import (
	_ "embed"
	"encoding/json"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

// Defaults are what Normalize fills the empty fields of a rule with, and what the form of a new
// rule starts with.
type Defaults struct {
	CILabel  string `json:"ci_label"`
	For      string `json:"for"`
	Interval string `json:"interval"`
	// Title is the title template of a rule without one; ${name} is the name of the rule, the
	// other placeholders are those of the title (${ci}, ${value}, ${threshold}, ${labels.x}).
	Title string `json:"title"`
	// Form is the rule the form of a new rule starts with.
	Form FormDefaults `json:"form"`
}

// FormDefaults are the fields a new rule starts with in the interface.
type FormDefaults struct {
	Method    string  `json:"method"`
	CILabel   string  `json:"ci_label"`
	Op        string  `json:"op"`
	Threshold float64 `json:"threshold"`
	For       string  `json:"for"`
	Interval  string  `json:"interval"`
	Severity  string  `json:"severity"`
	Enabled   bool    `json:"enabled"`
}

// Limits bound the fields of a rule.
type Limits struct {
	MaxName            int `json:"max_name"`
	MaxQuery           int `json:"max_query"`
	MaxForSeconds      int `json:"max_for_seconds"`
	MinIntervalSeconds int `json:"min_interval_seconds"`
	MaxIntervalSeconds int `json:"max_interval_seconds"`
}

const (
	MinInterval     = 10 * time.Second
	MaxInterval     = time.Hour
	MaxFor          = 24 * time.Hour
	DefaultInterval = 30 * time.Second
)

// RuleDefaults are the defaults of every rule.
var RuleDefaults = Defaults{
	CILabel:  "instance",
	For:      "0s",
	Interval: DefaultInterval.String(),
	Title:    "${name}: ${ci} = ${value}",
	Form: FormDefaults{Method: model.MethodUSE, CILabel: "instance", Op: ">", For: "5m", Interval: DefaultInterval.String(),
		Severity: model.SeverityWarning, Enabled: true},
}

// RuleLimits are the limits Normalize checks.
var RuleLimits = Limits{MaxName: 200, MaxQuery: 8000, MaxForSeconds: int(MaxFor / time.Second),
	MinIntervalSeconds: int(MinInterval / time.Second), MaxIntervalSeconds: int(MaxInterval / time.Second)}

// title is the title template of a rule without one.
func (d Defaults) title(name string) string { return strings.ReplaceAll(d.Title, "${name}", name) }

//go:embed templates.json
var templatesJSON []byte

// Templates are ready rules for node_exporter, cAdvisor and HTTP services (templates.json).
func Templates() []model.Rule {
	var out []model.Rule
	if err := json.Unmarshal(templatesJSON, &out); err != nil {
		panic("rules: templates.json: " + err.Error())
	}
	return out
}
