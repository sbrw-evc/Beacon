package app

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/monitoring"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/rules"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// The Machine tab opens on the metric the alert fired on:
//   - an alert of a RED or USE rule of Umbrella: the query of the rule on its source, with its
//     threshold;
//   - an alert of a Prometheus-compatible system: the expression of its alerting rule of the
//     same name (the signal), read from the rules API of the system;
//   - a Zabbix alert: the item whose key is the signal.
// The standard graph of the same kind (CPU for a CPU alert) comes next.

// ruleMetric is the RED or USE rule of Umbrella an incident came from.
type ruleMetric struct {
	rule model.Rule
	src  model.MetricSource
}

func ruleOf(d *store.Data, al alert.Alert) *ruleMetric {
	keys := make([]string, 0, len(al.Sources))
	for k := range al.Sources {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		id, ok := strings.CutPrefix(al.Sources[k].ConnectorID, rules.ConnectorPrefix)
		if !ok {
			continue
		}
		r := d.Rules[id]
		if r == nil {
			continue
		}
		src := d.MetricSource(r.SourceID)
		if src == nil {
			continue
		}
		cp := *r
		cp.State = nil
		return &ruleMetric{rule: cp, src: *src}
	}
	return nil
}

func (s *HostContextService) metricAuth(id string) (*monitoring.Auth, error) {
	if id == "" {
		return nil, nil
	}
	c, err := s.creds.Resolve(id)
	if err != nil {
		return nil, err
	}
	return &monitoring.Auth{Type: c.Type, Fields: c.Fields, Secrets: c.Secrets}, nil
}

// alertPanel reads the metric of the alert; nil when it is not known or has no data.
func (s *HostContextService) alertPanel(ctx context.Context, al alert.Alert, rule *ruleMetric, hosts []machineHost, names []string, from, to time.Time) *machinePanel {
	newPanel := func(title, source string) *machinePanel {
		return &machinePanel{ID: "alert", Title: title, Source: source, Alert: true, Series: []monitoring.Series{}, Errors: []sourceError{}}
	}
	if rule != nil {
		p := newPanel(firstSet(rule.rule.Name, rule.rule.Signal), rule.src.Name)
		p.Query = rule.rule.Query
		th := rule.rule.Threshold
		p.Op, p.Threshold = rule.rule.Op, &th
		auth, err := s.metricAuth(rule.src.CredentialID)
		if err == nil {
			p.Series, err = monitoring.HostSeries(ctx, rule.src, auth, rule.rule.Query, rule.rule.CILabel, "", names, from, to, 0)
		}
		if err != nil {
			p.Errors = append(p.Errors, sourceError{Source: rule.src.Name, Error: err.Error()})
		}
		return p
	}
	signal := strings.TrimSpace(al.Signal)
	if signal == "" {
		return nil
	}
	var failed *machinePanel
	tried := map[string]bool{}
	for _, h := range hosts {
		if tried[h.src.ID+"|"+h.Key] {
			continue
		}
		tried[h.src.ID+"|"+h.Key] = true
		auth, err := s.metricAuth(h.src.CredentialID)
		if err != nil {
			continue
		}
		switch h.Kind {
		case model.MonitoringPrometheus:
			src := model.MetricSource{ID: h.src.ID, Name: h.src.Name, URL: h.src.URL, SkipVerify: h.src.SkipVerify}
			query, err := rules.AlertRuleQuery(ctx, src, auth, signal)
			if err != nil || query == "" {
				continue
			}
			expr, op, th := monitoring.SplitThreshold(query)
			p := newPanel(signal, h.src.Name)
			p.Query, p.Op, p.Threshold = expr, op, th
			hostNames := append(slices.Clone(names), h.host.Endpoints...)
			p.Series, err = monitoring.HostSeries(ctx, src, auth, expr, h.src.HostLabel, "", hostNames, from, to, 0)
			if err != nil {
				p.Errors = append(p.Errors, sourceError{Source: h.src.Name, Error: err.Error()})
				failed = p
				continue
			}
			if len(p.Series) > 0 {
				return p
			}
		case model.MonitoringZabbix:
			if !zabbixKeyLike(signal) {
				continue
			}
			series, err := monitoring.ZabbixItem(ctx, h.src, auth, h.host, signal, from, to)
			if err != nil || len(series) == 0 {
				continue
			}
			p := newPanel(firstSet(series[0].Name, signal), h.src.Name)
			p.Query, p.Unit, p.Series = signal, series[0].Unit, series
			return p
		}
	}
	return failed
}

// zabbixKeyLike: an item key (system.cpu.util, vfs.fs.size[/,pused]) rather than a trigger name.
var zabbixKey = regexp.MustCompile(`^[A-Za-z0-9_.\-]+(\[.*\])?$`)

func zabbixKeyLike(v string) bool { return strings.Contains(v, ".") && zabbixKey.MatchString(v) }

// panelKinds tell the kind of a standard graph from the words of an alert.
var panelKinds = []struct {
	ids []string
	re  *regexp.Regexp
}{
	{[]string{"cpu"}, regexp.MustCompile(`(?i)cpu|processor|процессор|проц`)},
	{[]string{"memory"}, regexp.MustCompile(`(?i)mem|ram\b|swap|oom|памят`)},
	{[]string{"load"}, regexp.MustCompile(`(?i)load|нагрузк`)},
	{[]string{"disk"}, regexp.MustCompile(`(?i)disk|filesystem|vfs\.fs|space|inode|volume|storage|диск|место`)},
	{[]string{"net_in", "net_out"}, regexp.MustCompile(`(?i)net\.|network|interface|traffic|bandwidth|сеть|сетев|трафик`)},
}

// focusPanels are the graphs of the same kind as the alert: a graph whose Zabbix key is the
// signal, or a standard graph whose kind the signal or title names.
func focusPanels(panels []model.HostPanel, al alert.Alert) []string {
	var out []string
	signal := strings.TrimSpace(al.Signal)
	for _, p := range panels {
		if p.ZabbixKey != "" && strings.EqualFold(p.ZabbixKey, signal) {
			out = append(out, p.ID)
		}
	}
	if len(out) > 0 {
		return out
	}
	text := signal + " " + al.Title
	for _, k := range panelKinds {
		if k.re.MatchString(text) {
			for _, id := range k.ids {
				if slices.ContainsFunc(panels, func(p model.HostPanel) bool { return p.ID == id }) {
					out = append(out, id)
				}
			}
			break
		}
	}
	return out
}

// orderPanels puts the metric of the alert first, then the graphs of its kind, then the rest.
func orderPanels(panels []machinePanel, alertPanel *machinePanel, focus []string) []machinePanel {
	out := make([]machinePanel, 0, len(panels)+1)
	if alertPanel != nil {
		out = append(out, *alertPanel)
	}
	for _, id := range focus {
		if i := slices.IndexFunc(panels, func(p machinePanel) bool { return p.ID == id }); i >= 0 {
			p := panels[i]
			p.Focus = true
			out = append(out, p)
		}
	}
	for _, p := range panels {
		if !slices.Contains(focus, p.ID) {
			out = append(out, p)
		}
	}
	return out
}
