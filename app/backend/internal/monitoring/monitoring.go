// Package monitoring reads the host lists of monitoring systems: Zabbix through its JSON-RPC
// API, Prometheus-compatible servers through an instant query over their targets, Grafana
// through the instances of its alert rules and Graylog through the sources of its messages.
package monitoring

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/rules"
)

const (
	DefaultQuery     = "up"
	DefaultHostLabel = "instance"
	// MaxHosts bounds what one source may return.
	MaxHosts = 50000
)

// Auth is the credential a source is read with; rules.Auth for Prometheus, bearer (an API
// token) or basic (user name and password) for Zabbix.
type Auth = rules.Auth

// Result is what one reading returned.
type Result struct {
	Version string
	Hosts   []model.MonitoringHost
}

var clients = map[bool]*http.Client{
	false: {Timeout: 60 * time.Second},
	true:  {Timeout: 60 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}, //nolint:gosec // the administrator's choice
}

// Fetch reads the hosts of a source.
func Fetch(ctx context.Context, src model.MonitoringSource, auth *Auth) (Result, error) {
	if _, err := baseURL(src.URL); err != nil {
		return Result{}, err
	}
	var (
		out Result
		err error
	)
	switch src.Kind {
	case model.MonitoringZabbix:
		out, err = fetchZabbix(ctx, src, auth)
	case model.MonitoringPrometheus:
		out, err = fetchPrometheus(ctx, src, auth)
	case model.MonitoringGrafana:
		out, err = fetchGrafana(ctx, src, auth)
	case model.MonitoringGraylog:
		out, err = fetchGraylog(ctx, src, auth)
	default:
		return Result{}, fmt.Errorf("unknown monitoring system %q", src.Kind)
	}
	if err != nil {
		return Result{}, err
	}
	if len(out.Hosts) > MaxHosts {
		return Result{}, fmt.Errorf("the source returned %d hosts; at most %d are read", len(out.Hosts), MaxHosts)
	}
	slices.SortFunc(out.Hosts, func(a, b model.MonitoringHost) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out, nil
}

func baseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("the source address is not an http or https URL")
	}
	return u, nil
}

// hostOf takes the host out of an address the way targets are written: host, host:port or
// a URL (blackbox probes).
func hostOf(v string) string {
	v = strings.TrimSpace(v)
	if strings.Contains(v, "://") {
		if u, err := url.Parse(v); err == nil && u.Hostname() != "" {
			return strings.ToLower(u.Hostname())
		}
	}
	if h, _, err := net.SplitHostPort(v); err == nil {
		v = h
	}
	return strings.ToLower(strings.Trim(v, "[]"))
}

// addresses splits names into IP addresses and DNS names, without repeats.
func addresses(values ...string) (ips, dns []string) {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if ip := net.ParseIP(v); ip != nil {
			if s := ip.String(); !slices.Contains(ips, s) {
				ips = append(ips, s)
			}
			continue
		}
		if v = strings.ToLower(v); !slices.Contains(dns, v) {
			dns = append(dns, v)
		}
	}
	return ips, dns
}

func sortedSet(v []string) []string {
	out := []string{}
	for _, x := range v {
		if x != "" && !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	slices.Sort(out)
	return out
}

func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
