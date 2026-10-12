package model

import (
	"slices"
	"time"
)

// MetricSource is a Prometheus-compatible HTTP API (Prometheus, VictoriaMetrics, Thanos,
// Mimir) RED and USE rules query. The credential, when set, is one of the credential catalog.
type MetricSource struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	URL          string    `json:"url"`
	CredentialID string    `json:"credential_id,omitempty"`
	SkipVerify   bool      `json:"skip_verify"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedBy    string    `json:"updated_by"`
	UpdatedAt    time.Time `json:"updated_at"`
}

const (
	MethodRED   = "red"
	MethodUSE   = "use"
	MethodOther = "other"
)

// Methods are the methods of an event and an alert: RED (requests, errors, duration of a
// service), USE (utilization, saturation, errors of a resource) or other. Rules are RED or USE.
var Methods = []string{MethodRED, MethodUSE, MethodOther}

// RuleMethods are the methods a rule may have.
var RuleMethods = []string{MethodRED, MethodUSE}

// methodCounterpart pairs RED and USE: an alert of one is linked to an alert of the other on
// the same service (the USE one is the probable cause of the RED one).
var methodCounterpart = map[string]string{MethodRED: MethodUSE, MethodUSE: MethodRED}

func ValidMethod(v string) bool { return slices.Contains(Methods, v) }

// MethodCounterpart is the method linked alerts have: use for red, red for use, "" for others.
func MethodCounterpart(method string) string { return methodCounterpart[method] }

// Rule is a RED or USE rule: a PromQL query evaluated on a schedule; every series whose value
// meets the condition for the set time fires an event for the configuration item named by a
// label of the series.
type Rule struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Method    string  `json:"method"`
	Signal    string  `json:"signal"`
	SourceID  string  `json:"source_id"`
	Query     string  `json:"query"`
	CILabel   string  `json:"ci_label"`
	Op        string  `json:"op"`
	Threshold float64 `json:"threshold"`
	For       string  `json:"for"`
	Interval  string  `json:"interval"`
	Severity  string  `json:"severity"`
	Title     string  `json:"title"`
	Enabled   bool    `json:"enabled"`

	// Evaluation state, kept so a restart still resolves what fired.
	State       map[string]*RuleSeries `json:"-"`
	LastError   string                 `json:"last_error,omitempty"`
	SeriesCount int                    `json:"series"`
	Pending     int                    `json:"pending"`
	Firing      int                    `json:"firing"`

	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RuleSeries is a series that meets the condition of a rule: pending until it has lasted the
// set time, then firing.
type RuleSeries struct {
	CI      string            `json:"ci"`
	Labels  map[string]string `json:"labels"`
	Value   float64           `json:"value"`
	Since   time.Time         `json:"since"`
	Firing  bool              `json:"firing"`
	FiredAt *time.Time        `json:"fired_at,omitempty"`
}
