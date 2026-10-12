// Package rules evaluates RED and USE rules: PromQL queries against metric sources whose
// series fire events for configuration items into the alert engine.
package rules

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

var Ops = []string{">", ">=", "<", "<=", "==", "!="}

const (
	maxPreview = 200
	// ConnectorPrefix marks the alert sources of rules: rule:<id>.
	ConnectorPrefix = "rule:"
)

var (
	ErrNotFound       = errors.New("rule not found")
	ErrSourceNotFound = errors.New("metric source not found")
)

// Sink takes the events of rules (the alert engine).
type Sink interface {
	Ingest(ctx context.Context, events []alert.Incoming) error
}

// Credentials resolves a credential of the catalog.
type Credentials func(id string) (Auth, error)

// Querier runs a query; tests replace the Prometheus client.
type Querier func(ctx context.Context, src model.MetricSource, auth *Auth, promql string) ([]Sample, error)

type Engine struct {
	st    *store.Store
	creds Credentials
	query Querier
	now   func() time.Time

	mu       sync.Mutex
	sink     Sink
	lastEval map[string]time.Time
	// unsent are the resolved events of released rules that the sink did not take; Tick
	// sends them again (a released rule has no state left to recompute them from).
	unsent []unsent
}

type unsent struct {
	rule   string
	events []alert.Incoming
	since  time.Time
}

// maxUnsent is how long the events of a released rule are retried before they are dropped
// with an error in the log.
const maxUnsent = 24 * time.Hour

// transition is a change of a series made by an evaluation, kept to undo it when its event
// does not reach the alert engine.
type transition struct {
	key    string
	fired  time.Time         // the series started firing at this time
	series *model.RuleSeries // the series stopped firing and was removed (a copy)
}

func New(st *store.Store, creds Credentials) *Engine {
	return &Engine{st: st, creds: creds, query: Query, now: func() time.Time { return time.Now().UTC() }, lastEval: map[string]time.Time{}}
}

func (e *Engine) SetSink(s Sink) {
	e.mu.Lock()
	e.sink = s
	e.mu.Unlock()
}

func (e *Engine) SetQuerier(q Querier)          { e.query = q }
func (e *Engine) SetClock(now func() time.Time) { e.now = now }

// Normalize checks a rule and fills its defaults.
func Normalize(r *model.Rule) error {
	r.Name = strings.TrimSpace(r.Name)
	r.Query = strings.TrimSpace(r.Query)
	r.Signal = strings.ToLower(strings.TrimSpace(r.Signal))
	r.CILabel = strings.TrimSpace(r.CILabel)
	r.Title = strings.TrimSpace(r.Title)
	switch {
	case r.Name == "" || len(r.Name) > RuleLimits.MaxName:
		return errors.New("name")
	case !slices.Contains(model.RuleMethods, r.Method):
		return errors.New("method")
	case r.SourceID == "":
		return errors.New("source")
	case r.Query == "" || len(r.Query) > RuleLimits.MaxQuery:
		return errors.New("query")
	case !slices.Contains(Ops, r.Op):
		return errors.New("op")
	case math.IsNaN(r.Threshold) || math.IsInf(r.Threshold, 0):
		return errors.New("threshold")
	case alert.SeverityRank(r.Severity) == 0:
		return errors.New("severity")
	}
	if r.Signal == "" {
		r.Signal = r.Method + "." + strings.ToLower(strings.Join(strings.Fields(r.Name), "_"))
	}
	if r.For == "" {
		r.For = RuleDefaults.For
	}
	if d, err := time.ParseDuration(r.For); err != nil || d < 0 || d > MaxFor {
		return errors.New("for")
	}
	if r.Interval == "" {
		r.Interval = RuleDefaults.Interval
	}
	if d, err := time.ParseDuration(r.Interval); err != nil || d < MinInterval || d > MaxInterval {
		return errors.New("interval")
	}
	if r.CILabel == "" {
		r.CILabel = RuleDefaults.CILabel
	}
	if r.Title == "" {
		r.Title = RuleDefaults.title(r.Name)
	}
	return nil
}

func compare(v float64, op string, t float64) bool {
	switch op {
	case ">":
		return v > t
	case ">=":
		return v >= t
	case "<":
		return v < t
	case "<=":
		return v <= t
	case "==":
		return v == t
	case "!=":
		return v != t
	}
	return false
}

// SeriesKey identifies a series by its labels.
func SeriesKey(labels map[string]string) string {
	keys := slices.Sorted(maps.Keys(labels))
	var b strings.Builder
	for _, k := range keys {
		if k != "__name__" {
			b.WriteString(k + "=" + labels[k] + ",")
		}
	}
	return b.String()
}

// CIOf names the configuration item of a series: the rule's label, else the host of instance.
func CIOf(r model.Rule, labels map[string]string) string {
	if v := labels[r.CILabel]; v != "" {
		if r.CILabel == "instance" {
			if h, _, err := net.SplitHostPort(v); err == nil {
				return h
			}
		}
		return v
	}
	if inst := labels["instance"]; inst != "" {
		if h, _, err := net.SplitHostPort(inst); err == nil {
			return h
		}
		return inst
	}
	return ""
}

func FormatValue(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e12 {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func Title(r model.Rule, ci string, v float64, labels map[string]string) string {
	out := strings.NewReplacer("${ci}", ci, "${value}", FormatValue(v), "${threshold}", FormatValue(r.Threshold)).Replace(r.Title)
	for k, val := range labels {
		out = strings.ReplaceAll(out, "${labels."+k+"}", val)
	}
	return out
}

func (e *Engine) source(id string) (model.MetricSource, *Auth, error) {
	var src *model.MetricSource
	e.st.Read(func(d *store.Data) {
		src = d.MetricSource(id)
	})
	if src == nil {
		return model.MetricSource{}, nil, ErrSourceNotFound
	}
	if src.CredentialID == "" {
		return *src, nil, nil
	}
	a, err := e.creds(src.CredentialID)
	if err != nil {
		return *src, nil, fmt.Errorf("the credential of the source: %w", err)
	}
	return *src, &a, nil
}

// TestSource runs a query (up by default) against a source and returns the series count.
func (e *Engine) TestSource(ctx context.Context, src model.MetricSource, promql string) (int, error) {
	var auth *Auth
	if src.CredentialID != "" {
		a, err := e.creds(src.CredentialID)
		if err != nil {
			return 0, fmt.Errorf("the credential of the source: %w", err)
		}
		auth = &a
	}
	if promql == "" {
		promql = "up"
	}
	samples, err := e.query(ctx, src, auth, promql)
	return len(samples), err
}

type PreviewSeries struct {
	CI     string            `json:"ci"`
	Labels map[string]string `json:"labels"`
	Value  float64           `json:"value"`
	Match  bool              `json:"match"`
	Title  string            `json:"title"`
}

type Preview struct {
	Series  []PreviewSeries `json:"series"`
	Total   int             `json:"total"`
	Matched int             `json:"matched"`
	Error   string          `json:"error,omitempty"`
}

// Preview evaluates a rule once without firing anything.
func (e *Engine) Preview(ctx context.Context, r model.Rule) Preview {
	out := Preview{Series: []PreviewSeries{}}
	src, auth, err := e.source(r.SourceID)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	samples, err := e.query(ctx, src, auth, r.Query)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Total = len(samples)
	for _, s := range samples {
		ci := CIOf(r, s.Labels)
		ps := PreviewSeries{CI: ci, Labels: s.Labels, Value: s.Value, Match: compare(s.Value, r.Op, r.Threshold), Title: Title(r, ci, s.Value, s.Labels)}
		if ps.Match {
			out.Matched++
		}
		out.Series = append(out.Series, ps)
	}
	slices.SortStableFunc(out.Series, func(a, b PreviewSeries) int {
		switch {
		case a.Match == b.Match:
			return 0
		case a.Match:
			return -1
		}
		return 1
	})
	if len(out.Series) > maxPreview {
		out.Series = out.Series[:maxPreview]
	}
	return out
}

func (e *Engine) Run(ctx context.Context) {
	tk := time.NewTicker(5 * time.Second)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			e.Tick(ctx)
		}
	}
}

// Tick evaluates the rules that are due.
func (e *Engine) Tick(ctx context.Context) {
	e.resend(ctx)
	now := e.now()
	var due []string
	e.st.Read(func(d *store.Data) {
		e.mu.Lock()
		defer e.mu.Unlock()
		for id, r := range d.Rules {
			if !r.Enabled {
				continue
			}
			iv, err := time.ParseDuration(r.Interval)
			if err != nil || iv < MinInterval {
				iv = DefaultInterval
			}
			if last, ok := e.lastEval[id]; !ok || now.Sub(last) >= iv {
				due = append(due, id)
			}
		}
	})
	slices.Sort(due)
	for _, id := range due {
		if ctx.Err() != nil {
			return
		}
		if err := e.Evaluate(ctx, id); err != nil {
			slog.Debug("rule evaluation failed", "rule", id, "err", err)
		}
	}
}

// LastEval reports when a rule was last evaluated by this instance.
func (e *Engine) LastEval(id string) *time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	if t, ok := e.lastEval[id]; ok {
		return &t
	}
	return nil
}

// Evaluate runs a rule and fires and resolves its series. The store is written only when the
// state changed, so evaluations do not churn the catalog.
func (e *Engine) Evaluate(ctx context.Context, id string) error {
	var r model.Rule
	ok := false
	e.st.Read(func(d *store.Data) {
		if p := d.Rules[id]; p != nil {
			r, ok = *p, true
		}
	})
	if !ok {
		return ErrNotFound
	}
	e.mu.Lock()
	e.lastEval[id] = e.now()
	e.mu.Unlock()
	src, auth, err := e.source(r.SourceID)
	var samples []Sample
	if err == nil {
		qctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		samples, err = e.query(qctx, src, auth, r.Query)
		cancel()
	}
	now := e.now()
	var events []alert.Incoming
	var trans []transition
	e.st.Update(func(d *store.Data) bool {
		p := d.Rules[id]
		if p == nil {
			return false
		}
		if err != nil {
			if p.LastError == err.Error() {
				return false
			}
			p.LastError = err.Error()
			return true
		}
		changed := p.LastError != ""
		p.LastError = ""
		if p.State == nil {
			p.State = map[string]*model.RuleSeries{}
		}
		hold, _ := time.ParseDuration(p.For)
		seen := map[string]bool{}
		for _, s := range samples {
			key := SeriesKey(s.Labels)
			seen[key] = true
			st := p.State[key]
			if !compare(s.Value, p.Op, p.Threshold) {
				if st != nil {
					if st.Firing {
						events = append(events, incoming(*p, key, st, s.Value, "resolved"))
						cp := *st
						trans = append(trans, transition{key: key, series: &cp})
					}
					delete(p.State, key)
					changed = true
				}
				continue
			}
			if st == nil {
				st = &model.RuleSeries{CI: CIOf(*p, s.Labels), Labels: s.Labels, Since: now}
				p.State[key] = st
				changed = true
			}
			st.Value = s.Value
			if !st.Firing && now.Sub(st.Since) >= hold {
				st.Firing = true
				fired := now
				st.FiredAt = &fired
				events = append(events, incoming(*p, key, st, s.Value, "firing"))
				trans = append(trans, transition{key: key, fired: fired})
				changed = true
			}
		}
		for key, st := range p.State {
			if seen[key] {
				continue
			}
			if st.Firing {
				events = append(events, incoming(*p, key, st, st.Value, "resolved"))
				cp := *st
				trans = append(trans, transition{key: key, series: &cp})
			}
			delete(p.State, key)
			changed = true
		}
		if recount(p, len(samples)) {
			changed = true
		}
		return changed
	})
	if len(events) > 0 {
		if serr := e.emit(ctx, r.ID, events); serr != nil {
			e.undo(id, trans)
			if err == nil {
				err = serr
			}
		}
	}
	return err
}

// recount refreshes the series counters of a rule and reports whether they changed.
func recount(p *model.Rule, series int) bool {
	pending, firing := 0, 0
	for _, st := range p.State {
		if st.Firing {
			firing++
		} else {
			pending++
		}
	}
	changed := p.SeriesCount != series || p.Pending != pending || p.Firing != firing
	p.SeriesCount, p.Pending, p.Firing = series, pending, firing
	return changed
}

// undo takes back the transitions whose events the alert engine did not take (the database is
// down), so the state of a series changes only once its event is applied: a series that
// started firing is pending again and fires at the next evaluation, a series that stopped
// firing is firing again and is resolved at the next evaluation.
func (e *Engine) undo(id string, trans []transition) {
	e.st.Update(func(d *store.Data) bool {
		p := d.Rules[id]
		if p == nil {
			return false
		}
		if p.State == nil {
			p.State = map[string]*model.RuleSeries{}
		}
		for _, t := range trans {
			cur := p.State[t.key]
			switch {
			case t.series != nil:
				if cur == nil {
					p.State[t.key] = t.series
				}
			case cur != nil && cur.Firing && cur.FiredAt != nil && cur.FiredAt.Equal(t.fired):
				cur.Firing, cur.FiredAt = false, nil
			}
		}
		recount(p, p.SeriesCount)
		return true
	})
}

func (e *Engine) emit(ctx context.Context, id string, events []alert.Incoming) error {
	e.mu.Lock()
	sink := e.sink
	e.mu.Unlock()
	if sink == nil {
		return nil
	}
	err := sink.Ingest(ctx, events)
	if err != nil {
		slog.Error("rule events not applied, will retry", "rule", id, "err", err)
	}
	return err
}

// resend retries the events of released rules that the sink did not take.
func (e *Engine) resend(ctx context.Context) {
	e.mu.Lock()
	todo := e.unsent
	e.unsent = nil
	e.mu.Unlock()
	var left []unsent
	for _, u := range todo {
		if ctx.Err() != nil {
			left = append(left, u)
			continue
		}
		if e.emit(ctx, u.rule, u.events) == nil {
			continue
		}
		if e.now().Sub(u.since) > maxUnsent {
			slog.Error("rule events dropped after retrying", "rule", u.rule, "events", len(u.events), "since", u.since)
			continue
		}
		left = append(left, u)
	}
	e.mu.Lock()
	e.unsent = append(left, e.unsent...)
	e.mu.Unlock()
}

func incoming(r model.Rule, key string, st *model.RuleSeries, v float64, status string) alert.Incoming {
	labels := map[string]string{"rule": r.ID}
	for k, x := range st.Labels {
		if k != "__name__" && k != "ci" {
			labels[k] = x
		}
	}
	return alert.Incoming{
		ConnectorID: ConnectorPrefix + r.ID,
		Key:         key,
		Title:       Title(r, st.CI, v, st.Labels),
		CI:          st.CI,
		Signal:      r.Signal,
		Method:      r.Method,
		Severity:    r.Severity,
		Status:      status,
		Value:       FormatValue(v),
		Labels:      labels,
	}
}

// Release resolves what a rule fired, when it is turned off or deleted.
func (e *Engine) Release(ctx context.Context, r model.Rule) {
	var events []alert.Incoming
	for key, st := range r.State {
		if st.Firing {
			events = append(events, incoming(r, key, st, st.Value, "resolved"))
		}
	}
	e.mu.Lock()
	delete(e.lastEval, r.ID)
	e.mu.Unlock()
	if len(events) > 0 && e.emit(ctx, r.ID, events) != nil {
		e.mu.Lock()
		e.unsent = append(e.unsent, unsent{rule: r.ID, events: events, since: e.now()})
		e.mu.Unlock()
	}
}
