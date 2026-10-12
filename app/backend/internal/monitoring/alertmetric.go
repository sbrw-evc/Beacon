package monitoring

import (
	"context"
	"math"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/rules"
)

// comparison is a PromQL expression that ends in a comparison with a number: what an alerting
// rule usually is (cpu > 90).
var comparison = regexp.MustCompile(`(?s)^(.*\S)\s*(>=|<=|==|!=|>|<)\s*(?:bool\s+)?(-?[0-9]*\.?[0-9]+(?:[eE][-+]?[0-9]+)?)\s*$`)

// SplitThreshold takes the comparison off the end of an alerting expression: the expression
// alone is drawn (the series below the threshold too) and the threshold as a line. An
// expression with brackets left open at the cut is not split.
func SplitThreshold(query string) (expr, op string, threshold *float64) {
	query = strings.TrimSpace(query)
	m := comparison.FindStringSubmatch(query)
	if m == nil || !balanced(m[1]) {
		return query, "", nil
	}
	v, err := strconv.ParseFloat(m[3], 64)
	if err != nil {
		return query, "", nil
	}
	return strings.TrimSpace(m[1]), m[2], &v
}

func balanced(s string) bool {
	depth, quote := 0, rune(0)
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'' || r == '`':
			quote = r
		case r == '(' || r == '{' || r == '[':
			depth++
		case r == ')' || r == '}' || r == ']':
			depth--
		}
		if depth < 0 {
			return false
		}
	}
	return depth == 0 && quote == 0
}

// hostLabels are the labels that usually name the machine of a series.
var hostLabels = []string{"instance", "host", "hostname", "nodename", "node", "server", "machine"}

// HostSeries runs a query over a range and keeps the series of the machine: those with a label
// that names it. When none does, only the series without a machine label (aggregates) are kept.
func HostSeries(ctx context.Context, src model.MetricSource, auth *Auth, query, hostLabel, unit string, names []string, from, to time.Time, maxPoints int) ([]Series, error) {
	if maxPoints <= 0 {
		maxPoints = DefaultMaxPoints
	}
	step := max((to.Sub(from) / time.Duration(maxPoints)).Truncate(time.Second), 15*time.Second)
	raw, err := rules.QueryRange(ctx, src, auth, query, from, to, step)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, n := range names {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
			known[n] = true
			if short, _, ok := strings.Cut(n, "."); ok && !isIP(n) {
				known[short] = true
			}
		}
	}
	labels := hostLabels
	if hostLabel != "" {
		labels = append([]string{hostLabel}, hostLabels...)
	}
	var mine, aggregate []rules.Series
	for _, s := range raw {
		named, match := false, false
		for _, l := range labels {
			v := s.Labels[l]
			if v == "" {
				continue
			}
			named = true
			h := hostOf(v)
			short, _, _ := strings.Cut(h, ".")
			if known[h] || (!isIP(h) && known[short]) {
				match = true
			}
		}
		switch {
		case match:
			mine = append(mine, s)
		case !named:
			aggregate = append(aggregate, s)
		}
	}
	if len(mine) == 0 {
		mine = aggregate
	}
	out := make([]Series, 0, len(mine))
	for _, s := range mine {
		pts := make([][2]float64, 0, len(s.Points))
		for _, p := range s.Points {
			if !math.IsNaN(p[1]) && !math.IsInf(p[1], 0) {
				pts = append(pts, [2]float64{p[0] * 1000, p[1]})
			}
		}
		out = append(out, Series{Name: seriesName(s.Labels, hostLabel), Unit: unit, Points: Downsample(pts, maxPoints)})
	}
	if len(out) > maxSeries {
		out = out[:maxSeries]
	}
	return out, nil
}

func isIP(v string) bool { return net.ParseIP(v) != nil }

// ZabbixItem reads the series of one item key of a host: the item an alert fired on.
func ZabbixItem(ctx context.Context, src model.MonitoringSource, auth *Auth, host model.MonitoringHost, key string, from, to time.Time) ([]Series, error) {
	return Metrics(ctx, src, auth, host, MetricQuery{Panel: model.HostPanel{ZabbixKey: key}, From: from, To: to})
}
