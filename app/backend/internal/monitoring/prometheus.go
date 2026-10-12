package monitoring

import (
	"context"
	"net/url"
	"slices"
	"strings"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/rules"
)

// fetchPrometheus runs the query (up by default) and makes a host of every value of the host
// label (instance by default) without its port: node_exporter and blackbox on one server are
// one host. The host is up when all its series are 1, down when all are 0.
func fetchPrometheus(ctx context.Context, src model.MonitoringSource, auth *Auth) (Result, error) {
	query := strings.TrimSpace(src.Query)
	if query == "" {
		query = DefaultQuery
	}
	label := strings.TrimSpace(src.HostLabel)
	if label == "" {
		label = DefaultHostLabel
	}
	samples, err := rules.Query(ctx, model.MetricSource{URL: src.URL, SkipVerify: src.SkipVerify}, auth, query)
	if err != nil {
		return Result{}, err
	}
	type acc struct {
		host       model.MonitoringHost
		up, down   int
		jobs, ends []string
	}
	byKey := map[string]*acc{}
	var order []string
	for _, s := range samples {
		raw := s.Labels[label]
		key := hostOf(raw)
		if key == "" {
			continue
		}
		a := byKey[key]
		if a == nil {
			a = &acc{host: model.MonitoringHost{Key: key, Host: key, Name: key}}
			byKey[key] = a
			order = append(order, key)
		}
		if s.Value >= 1 {
			a.up++
		} else {
			a.down++
		}
		a.jobs = append(a.jobs, s.Labels["job"])
		a.ends = append(a.ends, raw)
	}
	base := strings.TrimRight(src.URL, "/")
	out := Result{Hosts: make([]model.MonitoringHost, 0, len(order))}
	for _, key := range order {
		a := byKey[key]
		h := a.host
		h.IPs, h.DNS = addresses(key)
		h.IPs, h.DNS = orEmpty(h.IPs), orEmpty(h.DNS)
		h.Groups, h.Endpoints = sortedSet(a.jobs), sortedSet(a.ends)
		switch {
		case a.down == 0:
			h.State = model.HostUp
		case a.up == 0:
			h.State = model.HostDown
		default:
			h.State = model.HostPartial
		}
		h.URL = base + "/targets?search=" + url.QueryEscape(key)
		out.Hosts = append(out.Hosts, h)
	}
	slices.SortFunc(out.Hosts, func(a, b model.MonitoringHost) int { return strings.Compare(a.Key, b.Key) })
	return out, nil
}
