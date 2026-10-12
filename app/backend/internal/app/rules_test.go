package app_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/rules"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func TestRulesRoundTrip(t *testing.T) {
	f := newConnFixture(t, true)
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": []any{
			map[string]any{"metric": map[string]string{"instance": "db-01:9100"}, "value": []any{1, "97"}},
		}}})
	}))
	t.Cleanup(prom.Close)
	f.h.st.Write(func(d *store.Data) {
		d.Teams["T-1"] = &model.Team{ID: "T-1", Name: "DBA"}
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "db-01.corp.local", Status: model.CIStatusActive}
		d.Services["S-1"] = &model.Service{ID: "S-1", Name: "Billing", OwnerTeamID: "T-1", Status: model.ServiceActive, CIIDs: []string{"CI-1"}}
	})

	f.expect(f.admin, http.MethodPost, "/api/metric-sources", map[string]any{"name": "Prom", "url": "ftp://x"}, http.StatusBadRequest, nil)
	var test map[string]int
	f.expect(f.admin, http.MethodPost, "/api/metric-sources/test", map[string]any{"name": "Prom", "url": prom.URL}, http.StatusOK, &test)
	if test["series"] != 1 {
		t.Fatalf("test = %v", test)
	}
	var src model.MetricSource
	f.expect(f.admin, http.MethodPost, "/api/metric-sources", map[string]any{"name": "Prom", "url": prom.URL + "/"}, http.StatusCreated, &src)

	rule := map[string]any{"name": "CPU", "method": "use", "source_id": src.ID, "query": "cpu", "op": ">", "threshold": 90, "for": "0s", "severity": "error", "enabled": true}
	var pv rules.Preview
	f.expect(f.admin, http.MethodPost, "/api/rules/preview", rule, http.StatusOK, &pv)
	if pv.Matched != 1 || pv.Series[0].CI != "db-01" {
		t.Fatalf("preview = %+v", pv)
	}
	f.expect(f.admin, http.MethodPost, "/api/rules", map[string]any{"name": "x", "method": "use", "source_id": src.ID, "query": "q", "op": "~", "severity": "error"}, http.StatusBadRequest, nil)
	var r model.Rule
	f.expect(f.admin, http.MethodPost, "/api/rules", rule, http.StatusCreated, &r)
	f.expect(f.admin, http.MethodPost, "/api/rules/"+r.ID+"/evaluate", nil, http.StatusOK, nil)

	var page struct {
		Alerts []alert.Alert `json:"alerts"`
	}
	waitFor(t, "the rule incident", func() bool {
		f.admin.call(http.MethodGet, "/api/incidents", nil, &page)
		return len(page.Alerts) == 1
	})
	a := page.Alerts[0]
	if a.CIID != "CI-1" || a.Method != "use" || a.Signal != "use.cpu" || a.Route.Team == nil || a.Route.Team.Name != "DBA" {
		t.Fatalf("incident = %+v", a)
	}
	var detail struct {
		Connectors map[string]string `json:"connectors"`
	}
	f.admin.call(http.MethodGet, "/api/incidents/"+a.ID, nil, &detail)
	if detail.Connectors["rule:"+r.ID] != "CPU" {
		t.Errorf("source name = %v", detail.Connectors)
	}

	var view app.RulesView
	f.expect(f.admin, http.MethodGet, "/api/rules", nil, http.StatusOK, &view)
	if len(view.Rules) != 1 || view.Rules[0].Firing != 1 || view.Rules[0].SourceName != "Prom" || len(view.Templates) == 0 || view.Sources[0].Rules != 1 {
		t.Fatalf("view = %+v", view)
	}
	f.expect(f.admin, http.MethodDelete, "/api/metric-sources/"+src.ID, nil, http.StatusConflict, nil)

	// Deleting the rule resolves what it fired.
	f.expect(f.admin, http.MethodDelete, "/api/rules/"+r.ID, nil, http.StatusNoContent, nil)
	waitFor(t, "the resolution", func() bool {
		var d struct {
			Alert alert.Alert `json:"alert"`
		}
		f.admin.call(http.MethodGet, "/api/incidents/"+a.ID, nil, &d)
		return d.Alert.Status == alert.StatusResolved
	})
	f.expect(f.admin, http.MethodDelete, "/api/metric-sources/"+src.ID, nil, http.StatusNoContent, nil)
}
