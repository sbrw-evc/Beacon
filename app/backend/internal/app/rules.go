package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/rules"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const maxRules = 1000

var ErrSourceInUse = errors.New("the metric source is used by rules")

// RulesService keeps RED and USE rules and the metric sources they query.
type RulesService struct {
	st     *store.Store
	engine *rules.Engine
	creds  *CredentialsService
}

func NewRulesService(st *store.Store, engine *rules.Engine, creds *CredentialsService) *RulesService {
	return &RulesService{st: st, engine: engine, creds: creds}
}

type RuleView struct {
	model.Rule
	SourceName string     `json:"source_name"`
	LastEvalAt *time.Time `json:"last_eval_at,omitempty"`
}

type SourceView struct {
	model.MetricSource
	CredentialName string `json:"credential_name,omitempty"`
	Rules          int    `json:"rules"`
	// System: a Prometheus monitoring system, edited on the monitoring systems page.
	System bool `json:"system"`
	// SystemID: a metric source with the address of a Prometheus monitoring system; it can be
	// merged into that system.
	SystemID string `json:"system_id,omitempty"`
}

type RulesView struct {
	Rules     []RuleView   `json:"rules"`
	Sources   []SourceView `json:"sources"`
	Templates []model.Rule `json:"templates"`
	Ops       []string     `json:"ops"`
	// Defaults, Limits, Severities and Methods describe the form of a rule.
	Defaults   rules.Defaults   `json:"defaults"`
	Limits     rules.Limits     `json:"limits"`
	Severities []model.Severity `json:"severities"`
	Methods    []string         `json:"methods"`
}

func (s *RulesService) View() RulesView {
	out := RulesView{Rules: []RuleView{}, Sources: []SourceView{}, Templates: rules.Templates(), Ops: rules.Ops,
		Defaults: rules.RuleDefaults, Limits: rules.RuleLimits, Severities: model.Severities, Methods: model.RuleMethods}
	s.st.Read(func(d *store.Data) {
		count := map[string]int{}
		for _, r := range d.Rules {
			v := RuleView{Rule: *r, LastEvalAt: s.engine.LastEval(r.ID)}
			v.State = nil
			if src := d.MetricSource(r.SourceID); src != nil {
				v.SourceName = src.Name
			}
			count[r.SourceID]++
			out.Rules = append(out.Rules, v)
		}
		for _, src := range d.MetricSources {
			v := SourceView{MetricSource: *src, Rules: count[src.ID]}
			if c := d.Credentials[src.CredentialID]; c != nil {
				v.CredentialName = c.Name
			}
			if sys := prometheusSystemAt(d, src.URL); sys != nil {
				v.SystemID = sys.ID
			}
			out.Sources = append(out.Sources, v)
		}
		for _, m := range d.MonitoringSources {
			if m.Kind != model.MonitoringPrometheus {
				continue
			}
			v := SourceView{MetricSource: *d.MetricSource(m.ID), Rules: count[m.ID], System: true}
			if c := d.Credentials[m.CredentialID]; c != nil {
				v.CredentialName = c.Name
			}
			out.Sources = append(out.Sources, v)
		}
	})
	slices.SortFunc(out.Rules, func(a, b RuleView) int {
		if a.Method != b.Method {
			return strings.Compare(a.Method, b.Method)
		}
		return byName(a.Name, b.Name)
	})
	slices.SortFunc(out.Sources, func(a, b SourceView) int { return byName(a.Name, b.Name) })
	return out
}

func ruleInput(in model.Rule) (model.Rule, error) {
	r := model.Rule{Name: in.Name, Method: in.Method, Signal: in.Signal, SourceID: in.SourceID, Query: in.Query, CILabel: in.CILabel, Op: in.Op,
		Threshold: in.Threshold, For: in.For, Interval: in.Interval, Severity: in.Severity, Title: in.Title, Enabled: in.Enabled}
	if err := rules.Normalize(&r); err != nil {
		return r, invalid("rule_"+err.Error(), nil)
	}
	return r, nil
}

func (s *RulesService) Create(actor string, in model.Rule) (model.Rule, error) {
	r, err := ruleInput(in)
	if err != nil {
		return r, err
	}
	now := time.Now().UTC()
	s.st.Write(func(d *store.Data) {
		switch {
		case d.MetricSource(r.SourceID) == nil:
			err = invalid("source_not_found", nil)
		case len(d.Rules) >= maxRules:
			err = invalid("too_many_rules", nil)
		default:
			r.ID = d.NextID("RULE")
			r.CreatedBy, r.CreatedAt, r.UpdatedBy, r.UpdatedAt = actor, now, actor, now
			cp := r
			d.Rules[r.ID] = &cp
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "rule.create", Object: r.ID, Detail: r.Name})
		}
	})
	return r, err
}

// Update changes a rule. A change of what it watches (query, condition, label, signal or
// source) or turning it off resolves what it fired, and it starts over.
func (s *RulesService) Update(ctx context.Context, actor, id string, in model.Rule) (model.Rule, error) {
	r, err := ruleInput(in)
	if err != nil {
		return r, err
	}
	var released *model.Rule
	err = ErrNotFound
	s.st.Write(func(d *store.Data) {
		p := d.Rules[id]
		if p == nil {
			return
		}
		if d.MetricSource(r.SourceID) == nil {
			err = invalid("source_not_found", nil)
			return
		}
		err = nil
		reset := !r.Enabled || p.Query != r.Query || p.Op != r.Op || p.Threshold != r.Threshold || p.CILabel != r.CILabel ||
			p.Signal != r.Signal || p.SourceID != r.SourceID || p.Severity != r.Severity || p.Method != r.Method
		r.ID, r.CreatedBy, r.CreatedAt = p.ID, p.CreatedBy, p.CreatedAt
		r.UpdatedBy, r.UpdatedAt = actor, time.Now().UTC()
		if reset {
			old := *p
			released = &old
		} else {
			r.State, r.SeriesCount, r.Pending, r.Firing, r.LastError = p.State, p.SeriesCount, p.Pending, p.Firing, p.LastError
		}
		cp := r
		d.Rules[id] = &cp
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "rule.update", Object: id, Detail: r.Name})
	})
	if released != nil {
		s.engine.Release(ctx, *released)
	}
	return r, err
}

func (s *RulesService) Delete(ctx context.Context, actor, id string) error {
	var old *model.Rule
	s.st.Write(func(d *store.Data) {
		if p := d.Rules[id]; p != nil {
			cp := *p
			old = &cp
			delete(d.Rules, id)
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "rule.delete", Object: id, Detail: p.Name})
		}
	})
	if old == nil {
		return ErrNotFound
	}
	s.engine.Release(ctx, *old)
	return nil
}

type SourceInput struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	CredentialID string `json:"credential_id"`
	SkipVerify   bool   `json:"skip_verify"`
}

func (s *RulesService) checkSource(d *store.Data, in *SourceInput) error {
	in.Name = strings.TrimSpace(in.Name)
	u, err := optionalURL(in.URL)
	if err != nil || u == "" {
		return invalid("url_invalid", err)
	}
	in.URL = strings.TrimRight(u, "/")
	if in.Name == "" || len(in.Name) > 200 {
		return invalid("name_invalid", nil)
	}
	if in.CredentialID != "" {
		c := d.Credentials[in.CredentialID]
		if c == nil {
			return invalid("credential_not_found", nil)
		}
		if c.Type != "bearer" && c.Type != "basic" && c.Type != "header" {
			return invalid("credential_type", nil)
		}
	}
	return nil
}

func (s *RulesService) CreateSource(actor string, in SourceInput) (model.MetricSource, error) {
	var out model.MetricSource
	var err error
	now := time.Now().UTC()
	s.st.Write(func(d *store.Data) {
		if err = s.checkSource(d, &in); err != nil {
			return
		}
		out = model.MetricSource{ID: d.NextID("MS"), Name: in.Name, URL: in.URL, CredentialID: in.CredentialID, SkipVerify: in.SkipVerify,
			CreatedBy: actor, CreatedAt: now, UpdatedBy: actor, UpdatedAt: now}
		cp := out
		d.MetricSources[out.ID] = &cp
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "metric_source.create", Object: out.ID, Detail: out.Name})
	})
	return out, err
}

func (s *RulesService) UpdateSource(actor, id string, in SourceInput) (model.MetricSource, error) {
	var out model.MetricSource
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		p := d.MetricSources[id]
		if p == nil {
			return
		}
		if err = s.checkSource(d, &in); err != nil {
			return
		}
		p.Name, p.URL, p.CredentialID, p.SkipVerify = in.Name, in.URL, in.CredentialID, in.SkipVerify
		p.UpdatedBy, p.UpdatedAt = actor, time.Now().UTC()
		out = *p
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "metric_source.update", Object: id, Detail: p.Name})
	})
	return out, err
}

func (s *RulesService) DeleteSource(actor, id string) error {
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		p := d.MetricSources[id]
		if p == nil {
			return
		}
		for _, r := range d.Rules {
			if r.SourceID == id {
				err = ErrSourceInUse
				return
			}
		}
		delete(d.MetricSources, id)
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "metric_source.delete", Object: id, Detail: p.Name})
		err = nil
	})
	return err
}

// prometheusSystemAt is the Prometheus monitoring system at the address, if any.
func prometheusSystemAt(d *store.Data, url string) *model.MonitoringSource {
	url = strings.TrimRight(strings.ToLower(strings.TrimSpace(url)), "/")
	var out *model.MonitoringSource
	for _, m := range sortedSources(d) {
		if m.Kind == model.MonitoringPrometheus && strings.TrimRight(strings.ToLower(m.URL), "/") == url {
			if out == nil {
				out = m
			}
		}
	}
	return out
}

// MergeResult is what merging a metric source into a monitoring system did.
type MergeResult struct {
	SystemID string `json:"system_id"`
	Created  bool   `json:"created"`
	Rules    int    `json:"rules"`
}

// MergeSource makes a metric source part of a Prometheus monitoring system: the system at the
// same address, or a new one made of the source (address, credential, certificate check). Its
// rules move to the system with their state, and the metric source is deleted. The rules keep
// querying the same server.
func (s *RulesService) MergeSource(actor, id string) (MergeResult, error) {
	var out MergeResult
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		p := d.MetricSources[id]
		if p == nil {
			return
		}
		err = nil
		sys := prometheusSystemAt(d, p.URL)
		now := time.Now().UTC()
		if sys == nil {
			if len(d.MonitoringSources) >= maxMonitoringSources {
				err = invalid("too_many_sources", nil)
				return
			}
			sys = &model.MonitoringSource{ID: d.NextID("MON"), Name: p.Name, Kind: model.MonitoringPrometheus, URL: p.URL,
				CredentialID: p.CredentialID, SkipVerify: p.SkipVerify, Enabled: true, Links: map[string]string{},
				CreatedBy: actor, CreatedAt: now, UpdatedBy: actor, UpdatedAt: now}
			d.MonitoringSources[sys.ID] = sys
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "monitoring.create", Object: sys.ID, Detail: sys.Name + " (prometheus) " + sys.URL + ", from metric source " + p.ID})
			out.Created = true
		}
		out.SystemID = sys.ID
		for _, r := range d.Rules {
			if r.SourceID == id {
				r.SourceID = sys.ID
				out.Rules++
			}
		}
		delete(d.MetricSources, id)
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "metric_source.merge", Object: id,
			Detail: fmt.Sprintf("%s merged into %s %s; %d rules moved", p.Name, sys.ID, sys.Name, out.Rules)})
	})
	return out, err
}

func (s *RulesService) TestSource(ctx context.Context, in SourceInput) (int, error) {
	var err error
	s.st.Read(func(d *store.Data) { err = s.checkSource(d, &in) })
	if err != nil {
		return 0, err
	}
	return s.engine.TestSource(ctx, model.MetricSource{URL: in.URL, CredentialID: in.CredentialID, SkipVerify: in.SkipVerify}, "")
}

// ruleCredentials resolves a credential of the catalog for the rule engine.
func ruleCredentials(creds *CredentialsService) rules.Credentials {
	return func(id string) (rules.Auth, error) {
		c, err := creds.Resolve(id)
		if err != nil {
			return rules.Auth{}, err
		}
		return rules.Auth{Type: c.Type, Fields: c.Fields, Secrets: c.Secrets}, nil
	}
}

func (a *App) registerRules(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/rules", a.authed(a.can("rules:view", a.listRules)))
	mux.HandleFunc("POST /api/rules", a.authed(a.can("rules:edit", a.createRule)))
	mux.HandleFunc("POST /api/rules/preview", a.authed(a.can("rules:edit", a.previewRule)))
	mux.HandleFunc("PUT /api/rules/{id}", a.authed(a.can("rules:edit", a.updateRule)))
	mux.HandleFunc("DELETE /api/rules/{id}", a.authed(a.can("rules:edit", a.deleteRule)))
	mux.HandleFunc("POST /api/rules/{id}/evaluate", a.authed(a.can("rules:edit", a.evaluateRule)))
	mux.HandleFunc("POST /api/metric-sources", a.authed(a.can("rules:edit", a.createSource)))
	mux.HandleFunc("POST /api/metric-sources/test", a.authed(a.can("rules:edit", a.testSource)))
	mux.HandleFunc("PUT /api/metric-sources/{id}", a.authed(a.can("rules:edit", a.updateSource)))
	mux.HandleFunc("DELETE /api/metric-sources/{id}", a.authed(a.can("rules:edit", a.deleteSource)))
	mux.HandleFunc("POST /api/metric-sources/{id}/merge", a.authed(a.can("rules:edit", a.can("monitoring:edit", a.mergeSource))))
}

func (a *App) listRules(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, a.rules.View())
}

func (a *App) createRule(w http.ResponseWriter, r *http.Request) {
	var in model.Rule
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.rules.Create(current(r).user.Username, in)
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (a *App) updateRule(w http.ResponseWriter, r *http.Request) {
	var in model.Rule
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.rules.Update(r.Context(), current(r).user.Username, r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (a *App) deleteRule(w http.ResponseWriter, r *http.Request) {
	if err := a.rules.Delete(r.Context(), current(r).user.Username, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) previewRule(w http.ResponseWriter, r *http.Request) {
	var in model.Rule
	if !httpx.Decode(w, r, &in) {
		return
	}
	rule, err := ruleInput(in)
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, a.ruleEngine.Preview(r.Context(), rule))
}

func (a *App) evaluateRule(w http.ResponseWriter, r *http.Request) {
	err := a.ruleEngine.Evaluate(r.Context(), r.PathValue("id"))
	if errors.Is(err, rules.ErrNotFound) {
		writeError(w, ErrNotFound)
		return
	}
	out := map[string]any{"ok": err == nil}
	if err != nil {
		out["error"] = err.Error()
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (a *App) createSource(w http.ResponseWriter, r *http.Request) {
	var in SourceInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.rules.CreateSource(current(r).user.Username, in)
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (a *App) updateSource(w http.ResponseWriter, r *http.Request) {
	var in SourceInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.rules.UpdateSource(current(r).user.Username, r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (a *App) deleteSource(w http.ResponseWriter, r *http.Request) {
	if err := a.rules.DeleteSource(current(r).user.Username, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) mergeSource(w http.ResponseWriter, r *http.Request) {
	out, err := a.rules.MergeSource(current(r).user.Username, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (a *App) testSource(w http.ResponseWriter, r *http.Request) {
	var in SourceInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	n, err := a.rules.TestSource(r.Context(), in)
	var ie *InputError
	switch {
	case errors.As(err, &ie):
		writeError(w, err)
	case err != nil:
		httpx.Error(w, http.StatusBadGateway, "source_failed", err)
	default:
		httpx.JSON(w, http.StatusOK, map[string]int{"series": n})
	}
}
