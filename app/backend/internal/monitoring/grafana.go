package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

// Grafana is read through its HTTP API: /api/health for the version and the Prometheus-style
// rules API of Grafana-managed alerting (/api/prometheus/grafana/api/v1/rules), which lists every
// alert rule with all its instances and their states. The instances give the host list (one host
// per value of the host label, healthy while all its instances are Normal) and, when polling is
// on, the alerts themselves.

// GrafanaHostLabels are the labels that name the host of an alert instance when the source sets none.
var GrafanaHostLabels = []string{"instance", "host", "hostname", "node", "server"}

// GrafanaAlert is one alert instance of a Grafana alert rule.
type GrafanaAlert struct {
	Fingerprint string            `json:"fingerprint"`
	RuleUID     string            `json:"rule_uid,omitempty"`
	Rule        string            `json:"rule"`
	Folder      string            `json:"folder,omitempty"`
	Group       string            `json:"group,omitempty"`
	State       string            `json:"state"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	ActiveAt    time.Time         `json:"active_at"`
	Value       string            `json:"value,omitempty"`
}

// Firing: the instance alerts (Alerting, also with NoData or Error) or reports a failed query.
func (a GrafanaAlert) Firing() bool {
	s := strings.ToLower(a.State)
	return strings.HasPrefix(s, "alerting") || strings.HasPrefix(s, "firing") || s == "nodata" || s == "error"
}

// GrafanaReading is what one call of the rules API returned.
type GrafanaReading struct {
	Version string
	Alerts  []GrafanaAlert
}

// MaxGrafanaAlerts bounds the alert instances read from one Grafana.
const MaxGrafanaAlerts = 100000

func grafanaGet(ctx context.Context, src model.MonitoringSource, auth *Auth, path string, out any) (int, error) {
	base, err := baseURL(src.URL)
	if err != nil {
		return 0, err
	}
	u := strings.TrimRight(base.String(), "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	if auth != nil {
		switch auth.Type {
		case "bearer":
			req.Header.Set("Authorization", "Bearer "+auth.Secrets["token"])
		case "basic":
			req.SetBasicAuth(auth.Fields["username"], auth.Secrets["password"])
		case "header":
			if h := auth.Fields["header"]; h != "" {
				req.Header.Set(h, auth.Secrets["value"])
			}
		default:
			return 0, fmt.Errorf("a %s credential cannot be used with Grafana", auth.Type)
		}
	}
	resp, err := clients[src.SkipVerify].Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return resp.StatusCode, fmt.Errorf("Grafana answered %d: check the service account token and its role (Viewer is enough)", resp.StatusCode)
	case resp.StatusCode/100 != 2:
		return resp.StatusCode, fmt.Errorf("Grafana answered %d on %s", resp.StatusCode, path)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, errors.New("the address does not answer with the Grafana API; give the address of the Grafana web interface")
	}
	return resp.StatusCode, nil
}

// ReadGrafana reads the version and every alert instance of the Grafana-managed rules.
func ReadGrafana(ctx context.Context, src model.MonitoringSource, auth *Auth) (GrafanaReading, error) {
	var out GrafanaReading
	var health struct {
		Version string `json:"version"`
	}
	if _, err := grafanaGet(ctx, src, auth, "/api/health", &health); err != nil {
		return out, err
	}
	out.Version = health.Version
	var rules struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			Groups []struct {
				Name  string `json:"name"`
				File  string `json:"file"`
				Rules []struct {
					UID         string            `json:"uid"`
					Name        string            `json:"name"`
					Type        string            `json:"type"`
					Labels      map[string]string `json:"labels"`
					Annotations map[string]string `json:"annotations"`
					Alerts      []struct {
						Labels      map[string]string `json:"labels"`
						Annotations map[string]string `json:"annotations"`
						State       string            `json:"state"`
						ActiveAt    *time.Time        `json:"activeAt"`
						Value       string            `json:"value"`
					} `json:"alerts"`
				} `json:"rules"`
			} `json:"groups"`
		} `json:"data"`
	}
	if _, err := grafanaGet(ctx, src, auth, "/api/prometheus/grafana/api/v1/rules", &rules); err != nil {
		return out, err
	}
	if rules.Status != "" && rules.Status != "success" {
		return out, fmt.Errorf("Grafana: %s", firstNonEmpty(rules.Error, rules.Status))
	}
	out.Alerts = []GrafanaAlert{}
	for _, g := range rules.Data.Groups {
		for _, r := range g.Rules {
			if r.Type != "" && r.Type != "alerting" {
				continue
			}
			for _, in := range r.Alerts {
				a := GrafanaAlert{RuleUID: r.UID, Rule: r.Name, Folder: g.File, Group: g.Name, State: in.State, Value: in.Value,
					Labels: map[string]string{}, Annotations: map[string]string{}}
				for k, v := range r.Labels {
					a.Labels[k] = v
				}
				for k, v := range in.Labels {
					a.Labels[k] = v
				}
				for k, v := range r.Annotations {
					a.Annotations[k] = v
				}
				for k, v := range in.Annotations {
					a.Annotations[k] = v
				}
				if a.Labels["alertname"] == "" {
					a.Labels["alertname"] = r.Name
				}
				if a.Labels["grafana_folder"] == "" && g.File != "" {
					a.Labels["grafana_folder"] = g.File
				}
				if in.ActiveAt != nil && in.ActiveAt.Year() > 1 {
					a.ActiveAt = in.ActiveAt.UTC()
				}
				a.Fingerprint = fingerprint(a.Labels)
				out.Alerts = append(out.Alerts, a)
				if len(out.Alerts) > MaxGrafanaAlerts {
					return out, fmt.Errorf("Grafana has more than %d alert instances; at most %d are read", MaxGrafanaAlerts, MaxGrafanaAlerts)
				}
			}
		}
	}
	slices.SortFunc(out.Alerts, func(a, b GrafanaAlert) int { return strings.Compare(a.Fingerprint, b.Fingerprint) })
	return out, nil
}

// fingerprint identifies an alert instance by its labels the way Alertmanager does (FNV-1a over
// the sorted names and values), without the private labels (__name__), which Grafana does not send.
func fingerprint(labels map[string]string) string {
	names := make([]string, 0, len(labels))
	for k := range labels {
		if !strings.HasPrefix(k, "__") {
			names = append(names, k)
		}
	}
	slices.Sort(names)
	h := fnv.New64a()
	for _, k := range names {
		h.Write([]byte(k))
		h.Write([]byte{0xff})
		h.Write([]byte(labels[k]))
		h.Write([]byte{0xff})
	}
	return fmt.Sprintf("%016x", h.Sum64())
}

// PublicLabels are the labels of an instance without the private ones (__…__), as Grafana
// sends them to a contact point.
func (a GrafanaAlert) PublicLabels() map[string]string {
	out := make(map[string]string, len(a.Labels))
	for k, v := range a.Labels {
		if !strings.HasPrefix(k, "__") {
			out[k] = v
		}
	}
	return out
}

// PublicAnnotations are the annotations of an instance without the private ones (__dashboardUid__…).
func (a GrafanaAlert) PublicAnnotations() map[string]string {
	out := make(map[string]string, len(a.Annotations))
	for k, v := range a.Annotations {
		if !strings.HasPrefix(k, "__") {
			out[k] = v
		}
	}
	return out
}

// Links are the addresses Grafana puts into a contact point delivery: the rule, the dashboard
// and the panel of the instance.
func (a GrafanaAlert) Links(base string) (rule, dashboard, panel string) {
	base = strings.TrimRight(base, "/")
	if a.RuleUID != "" {
		rule = base + "/alerting/grafana/" + url.PathEscape(a.RuleUID) + "/view"
	}
	if uid := a.Annotations["__dashboardUid__"]; uid != "" {
		dashboard = base + "/d/" + url.PathEscape(uid)
		if id := a.Annotations["__panelId__"]; id != "" {
			panel = dashboard + "?viewPanel=" + url.QueryEscape(id)
		}
	}
	return rule, dashboard, panel
}

// fetchGrafana makes the host list of the alert instances: a host per value of the host label
// (instance, host, hostname… unless the source names one), up while all its instances are
// Normal or Pending, down while all alert, partial in between.
func fetchGrafana(ctx context.Context, src model.MonitoringSource, auth *Auth) (Result, error) {
	reading, err := ReadGrafana(ctx, src, auth)
	if err != nil {
		return Result{}, err
	}
	labels := GrafanaHostLabels
	if l := strings.TrimSpace(src.HostLabel); l != "" {
		labels = []string{l}
	}
	type acc struct {
		host          model.MonitoringHost
		firing, quiet int
		folders, raw  []string
	}
	byKey := map[string]*acc{}
	for _, a := range reading.Alerts {
		raw := ""
		for _, l := range labels {
			if raw = a.Labels[l]; raw != "" {
				break
			}
		}
		key := hostOf(raw)
		if key == "" {
			continue
		}
		h := byKey[key]
		if h == nil {
			h = &acc{host: model.MonitoringHost{Key: key, Host: key, Name: key}}
			byKey[key] = h
		}
		if a.Firing() {
			h.firing++
		} else {
			h.quiet++
		}
		h.folders = append(h.folders, a.Folder)
		h.raw = append(h.raw, raw)
	}
	base := strings.TrimRight(src.URL, "/")
	out := Result{Version: reading.Version, Hosts: make([]model.MonitoringHost, 0, len(byKey))}
	for key, a := range byKey {
		h := a.host
		h.IPs, h.DNS = addresses(key)
		h.IPs, h.DNS = orEmpty(h.IPs), orEmpty(h.DNS)
		h.Groups, h.Endpoints = sortedSet(a.folders), sortedSet(a.raw)
		switch {
		case a.firing == 0:
			h.State = model.HostUp
		case a.quiet == 0:
			h.State = model.HostDown
		default:
			h.State = model.HostPartial
		}
		h.URL = base + "/alerting/list?search=" + url.QueryEscape(key)
		out.Hosts = append(out.Hosts, h)
	}
	return out, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
