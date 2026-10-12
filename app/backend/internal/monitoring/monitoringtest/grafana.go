package monitoringtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Grafana answers /api/health and the rules API of Grafana-managed alerting. Requests need the
// bearer Token.
type Grafana struct {
	URL   string
	Token string

	mu    sync.Mutex
	rules []map[string]any
	Calls int
}

func StartGrafana(t *testing.T) *Grafana {
	g := &Grafana{Token: "glsa_test"}
	srv := httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(srv.Close)
	g.URL = srv.URL
	return g
}

// GrafanaInstance is an alert instance of a rule: state Normal, Pending, Alerting, NoData or Error.
type GrafanaInstance struct {
	Labels      map[string]string
	Annotations map[string]string
	State       string
	Value       string
}

// SetRule adds or replaces a rule with its instances; every rule is in folder Infra.
func (g *Grafana) SetRule(uid, name string, annotations map[string]string, instances ...GrafanaInstance) {
	g.mu.Lock()
	defer g.mu.Unlock()
	alerts := []map[string]any{}
	for _, in := range instances {
		alerts = append(alerts, map[string]any{"labels": in.Labels, "annotations": in.Annotations, "state": in.State,
			"activeAt": "2026-10-07T10:00:00Z", "value": in.Value})
	}
	rule := map[string]any{"uid": uid, "name": name, "type": "alerting", "labels": map[string]string{}, "annotations": annotations, "alerts": alerts}
	for i, r := range g.rules {
		if r["uid"] == uid {
			g.rules[i] = rule
			return
		}
	}
	g.rules = append(g.rules, rule)
}

func (g *Grafana) DeleteRule(uid string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, r := range g.rules {
		if r["uid"] == uid {
			g.rules = append(g.rules[:i], g.rules[i+1:]...)
			return
		}
	}
}

func (g *Grafana) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Calls++
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/health":
		_ = json.NewEncoder(w).Encode(map[string]string{"database": "ok", "version": "11.3.0"})
	case "/api/prometheus/grafana/api/v1/rules":
		if r.Header.Get("Authorization") != "Bearer "+g.Token {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Unauthorized"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{
			"groups": []any{map[string]any{"name": "infra", "file": "Infra", "rules": g.rules}}}})
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not found"}`))
	}
}

// SetToken changes the token Grafana accepts.
func (g *Grafana) SetToken(token string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Token = token
}
