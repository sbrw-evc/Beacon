package monitoring

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/rules"
)

// Series is one line of a graph: points are [unix milliseconds, value].
type Series struct {
	Name   string       `json:"name"`
	Unit   string       `json:"unit,omitempty"`
	Points [][2]float64 `json:"points"`
}

// MetricQuery is one graph of one host over a time range.
type MetricQuery struct {
	Panel model.HostPanel
	From  time.Time
	To    time.Time
	// MaxPoints bounds the points of a series; longer series are averaged in buckets.
	MaxPoints int
}

const (
	DefaultMaxPoints = 300
	maxSeries        = 12
	// zabbixHistoryLimit bounds the raw values read for one graph; trends replace the history for
	// long ranges.
	zabbixHistoryLimit = 50000
	zabbixTrendsAfter  = 48 * time.Hour
)

// ErrNoQuery: the panel has no query for this kind of system.
var ErrNoQuery = errors.New("the graph has no query for this monitoring system")

// Metrics reads the series of one graph of a host from its monitoring system.
func Metrics(ctx context.Context, src model.MonitoringSource, auth *Auth, host model.MonitoringHost, q MetricQuery) ([]Series, error) {
	if _, err := baseURL(src.URL); err != nil {
		return nil, err
	}
	if q.MaxPoints <= 0 {
		q.MaxPoints = DefaultMaxPoints
	}
	if !q.To.After(q.From) {
		return nil, errors.New("the time range is empty")
	}
	var (
		out []Series
		err error
	)
	switch src.Kind {
	case model.MonitoringPrometheus:
		out, err = promMetrics(ctx, src, auth, host, q)
	case model.MonitoringZabbix:
		out, err = zabbixMetrics(ctx, src, auth, host, q)
	default:
		return nil, fmt.Errorf("unknown monitoring system %q", src.Kind)
	}
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Points = Downsample(out[i].Points, q.MaxPoints)
	}
	slices.SortFunc(out, func(a, b Series) int { return strings.Compare(a.Name, b.Name) })
	if len(out) > maxSeries {
		out = out[:maxSeries]
	}
	return out, nil
}

// HostSelector is the PromQL label matcher of the series of a host: the host label matches any
// of its endpoints (instances), or the host name with any port.
func HostSelector(src model.MonitoringSource, host model.MonitoringHost) string {
	label := strings.TrimSpace(src.HostLabel)
	if label == "" {
		label = DefaultHostLabel
	}
	var alts []string
	for _, e := range host.Endpoints {
		if e = strings.TrimSpace(e); e != "" {
			alts = append(alts, regexp.QuoteMeta(e))
		}
	}
	if len(alts) == 0 {
		alts = []string{regexp.QuoteMeta(host.Host) + "(:[0-9]+)?"}
	}
	slices.Sort(alts)
	return label + "=~" + strconv.Quote(strings.Join(slices.Compact(alts), "|"))
}

// ExpandPromQL puts the selector and the name of a host in a panel query. The name goes in as
// it is, for a label value in quotes (nodename="$host"); quotes and backslashes are dropped.
func ExpandPromQL(query string, src model.MonitoringSource, host model.MonitoringHost) string {
	query = strings.ReplaceAll(query, model.HostSelector, HostSelector(src, host))
	name := strings.NewReplacer(`"`, "", `\`, "", "`", "", "'", "").Replace(host.Host)
	return strings.ReplaceAll(query, model.HostName, name)
}

func promMetrics(ctx context.Context, src model.MonitoringSource, auth *Auth, host model.MonitoringHost, q MetricQuery) ([]Series, error) {
	query := strings.TrimSpace(q.Panel.PromQL)
	if query == "" {
		return nil, ErrNoQuery
	}
	query = ExpandPromQL(query, src, host)
	step := q.To.Sub(q.From) / time.Duration(q.MaxPoints)
	step = max(step.Truncate(time.Second), 15*time.Second)
	raw, err := rules.QueryRange(ctx, model.MetricSource{URL: src.URL, SkipVerify: src.SkipVerify}, auth, query, q.From, q.To, step)
	if err != nil {
		return nil, err
	}
	out := make([]Series, 0, len(raw))
	for _, s := range raw {
		pts := make([][2]float64, 0, len(s.Points))
		for _, p := range s.Points {
			if math.IsNaN(p[1]) || math.IsInf(p[1], 0) {
				continue
			}
			pts = append(pts, [2]float64{p[0] * 1000, p[1]})
		}
		out = append(out, Series{Name: seriesName(s.Labels, src.HostLabel), Unit: q.Panel.Unit, Points: pts})
	}
	return out, nil
}

// seriesName names a series by the labels that tell it from the others of the host: everything
// but the metric name, job and host label.
func seriesName(labels map[string]string, hostLabel string) string {
	if hostLabel == "" {
		hostLabel = DefaultHostLabel
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		if k == "__name__" || k == "job" || k == hostLabel {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+labels[k])
	}
	if len(parts) == 0 {
		if v := labels[hostLabel]; v != "" {
			return v
		}
		return "value"
	}
	return strings.Join(parts, ", ")
}

type zabbixItem struct {
	ItemID    string  `json:"itemid"`
	Name      string  `json:"name"`
	Key       string  `json:"key_"`
	ValueType flexInt `json:"value_type"`
	Units     string  `json:"units"`
}

type zabbixValue struct {
	ItemID string `json:"itemid"`
	Clock  string `json:"clock"`
	Value  string `json:"value"`
	Avg    string `json:"value_avg"`
}

func zabbixMetrics(ctx context.Context, src model.MonitoringSource, auth *Auth, host model.MonitoringHost, q MetricQuery) ([]Series, error) {
	key := strings.TrimSpace(q.Panel.ZabbixKey)
	if key == "" {
		return nil, ErrNoQuery
	}
	z, _, closeFn, err := openZabbix(ctx, src, auth)
	if err != nil {
		return nil, err
	}
	defer closeFn()
	params := map[string]any{
		"output":  []string{"itemid", "name", "key_", "value_type", "units"},
		"hostids": []string{host.Key},
		// Numbers only: float (0) and unsigned (3).
		"filter":    map[string]any{"value_type": []int{0, 3}},
		"sortfield": "name",
		"limit":     maxSeries,
	}
	if strings.Contains(key, "*") {
		params["search"] = map[string]any{"key_": key}
		params["searchWildcardsEnabled"] = true
	} else {
		params["filter"] = map[string]any{"value_type": []int{0, 3}, "key_": key}
	}
	var items []zabbixItem
	if err := z.call(ctx, "item.get", params, &items); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return []Series{}, nil
	}
	byID := map[string]*Series{}
	ids := map[int][]string{}
	var all []string
	for _, it := range items {
		name := it.Name
		if name == "" {
			name = it.Key
		}
		unit := q.Panel.Unit
		if unit == "" {
			unit = it.Units
		}
		byID[it.ItemID] = &Series{Name: name, Unit: unit, Points: [][2]float64{}}
		ids[int(it.ValueType)] = append(ids[int(it.ValueType)], it.ItemID)
		all = append(all, it.ItemID)
	}
	from, till := q.From.Unix(), q.To.Unix()
	var values []zabbixValue
	if q.To.Sub(q.From) > zabbixTrendsAfter {
		if err := z.call(ctx, "trend.get", map[string]any{"output": []string{"itemid", "clock", "value_avg"}, "itemids": all,
			"time_from": from, "time_till": till, "limit": zabbixHistoryLimit}, &values); err != nil {
			return nil, err
		}
	} else {
		for _, vt := range []int{0, 3} {
			if len(ids[vt]) == 0 {
				continue
			}
			var part []zabbixValue
			if err := z.call(ctx, "history.get", map[string]any{"output": "extend", "history": vt, "itemids": ids[vt],
				"time_from": from, "time_till": till, "sortfield": "clock", "sortorder": "ASC", "limit": zabbixHistoryLimit}, &part); err != nil {
				return nil, err
			}
			values = append(values, part...)
		}
	}
	for _, v := range values {
		s := byID[v.ItemID]
		if s == nil {
			continue
		}
		clock, err := strconv.ParseInt(v.Clock, 10, 64)
		if err != nil {
			continue
		}
		raw := v.Value
		if v.Avg != "" {
			raw = v.Avg
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		s.Points = append(s.Points, [2]float64{float64(clock * 1000), f})
	}
	out := make([]Series, 0, len(byID))
	for _, s := range byID {
		slices.SortFunc(s.Points, func(a, b [2]float64) int { return cmp.Compare(a[0], b[0]) })
		out = append(out, *s)
	}
	return out, nil
}

// Downsample averages points in equal time buckets so that at most n remain.
func Downsample(pts [][2]float64, n int) [][2]float64 {
	if n <= 0 || len(pts) <= n {
		return pts
	}
	start, end := pts[0][0], pts[len(pts)-1][0]
	width := (end - start) / float64(n)
	if width <= 0 {
		return pts[:n]
	}
	out := make([][2]float64, 0, n)
	var sumT, sumV float64
	count, bucket := 0, 0
	flush := func() {
		if count > 0 {
			out = append(out, [2]float64{math.Round(sumT / float64(count)), sumV / float64(count)})
		}
		sumT, sumV, count = 0, 0, 0
	}
	for _, p := range pts {
		b := min(int((p[0]-start)/width), n-1)
		if b != bucket {
			flush()
			bucket = b
		}
		sumT += p[0]
		sumV += p[1]
		count++
	}
	flush()
	return out
}
