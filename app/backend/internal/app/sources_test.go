package app_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func activeIncidents(f connFixture) []alert.Alert {
	var page alert.Page
	f.admin.call(http.MethodGet, "/api/incidents?status=active", nil, &page)
	return page.Alerts
}

// Quick connect makes a token, a connector from the template and publishes it: the returned
// token and address take deliveries right away.
func TestQuickConnect(t *testing.T) {
	f := newConnFixture(t, true)
	var out app.QuickConnectResult
	f.expect(f.admin, http.MethodPost, "/api/connectors/quick", map[string]string{"preset": "zabbix"}, http.StatusCreated, &out)
	if out.Token == "" || out.Connector.Status != app.StatusPublished || out.Connector.Published != 1 || out.Connector.Slug != "zabbix" {
		t.Fatalf("quick connect = %+v", out)
	}
	if !strings.HasSuffix(out.IngestURL, "/api/ingest/zabbix") || !strings.Contains(out.MediaTypeYAML, out.Token) ||
		!strings.Contains(out.MediaTypeYAML, out.IngestURL) || out.Instructions.AuthHeader != "Authorization: Bearer "+out.Token {
		t.Errorf("instructions = %+v\n%s", out.Instructions, out.MediaTypeYAML)
	}
	var cred app.CredentialView
	f.expect(f.admin, http.MethodGet, "/api/credentials/"+out.CredentialID, nil, http.StatusOK, &cred)
	if cred.Type != flow.CredBearer || len(cred.UsedBy) != 1 || cred.UsedBy[0].ID != out.Connector.ID {
		t.Errorf("credential = %+v", cred)
	}

	p := f.ingest("zabbix", `{"event_id":"1","host":"db-01","trigger":"High CPU","item_key":"cpu","severity":"4","status":"1"}`,
		map[string]string{"Authorization": "Bearer " + out.Token, "Content-Type": "application/json"})
	if p.status != http.StatusAccepted {
		t.Fatalf("ingest with the returned token = %d %v", p.status, p.body)
	}
	waitFor(t, "an incident", func() bool { return len(activeIncidents(f)) == 1 })
	if p := f.ingest("zabbix", `{}`, map[string]string{"Authorization": "Bearer wrong"}); p.status != http.StatusUnauthorized {
		t.Errorf("a wrong token = %d", p.status)
	}

	// A second Zabbix gets its own address; an unknown template is refused.
	var second app.QuickConnectResult
	f.expect(f.admin, http.MethodPost, "/api/connectors/quick", map[string]string{"preset": "zabbix"}, http.StatusCreated, &second)
	if second.Connector.Slug != "zabbix-2" || second.Token == out.Token {
		t.Errorf("second = %s", second.Connector.Slug)
	}
	f.expect(f.admin, http.MethodPost, "/api/connectors/quick", map[string]string{"preset": "nope"}, http.StatusBadRequest, nil)

	// From a monitoring system: the connector becomes its alert intake.
	var sys app.MonitoringSourceView
	f.expect(f.admin, http.MethodPost, "/api/monitoring/sources", map[string]any{"name": "Prom Prod", "kind": "prometheus",
		"url": "http://prom.example:9090", "enabled": true}, http.StatusCreated, &sys)
	var am app.QuickConnectResult
	f.expect(f.admin, http.MethodPost, "/api/connectors/quick", map[string]string{"preset": "alertmanager", "monitoring_id": sys.ID}, http.StatusCreated, &am)
	if am.Connector.Name != "Prom Prod" || am.Connector.Slug != "prom-prod" || !strings.Contains(am.Instructions.Snippet, am.Token) {
		t.Errorf("alertmanager = %+v", am)
	}
	var mv app.MonitoringView
	f.expect(f.admin, http.MethodGet, "/api/monitoring", nil, http.StatusOK, &mv)
	if len(mv.Sources) != 1 || mv.Sources[0].Connector == nil || mv.Sources[0].Connector.ID != am.Connector.ID {
		t.Errorf("system intake = %+v", mv.Sources)
	}
}

// The test event goes through the published connector into an incident on the chosen item with
// its route; it never goes to PagerDuty and resolves itself after five minutes.
func TestTestEvent(t *testing.T) {
	f := newConnFixture(t, true)
	f.h.st.Write(func(d *store.Data) {
		d.Teams["T-1"] = &model.Team{ID: "T-1", Name: "DBA"}
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "db-01", Status: model.CIStatusActive}
		d.Services["S-1"] = &model.Service{ID: "S-1", Name: "Billing", OwnerTeamID: "T-1", Status: model.ServiceActive, CIIDs: []string{"CI-1"}}
	})
	var q app.QuickConnectResult
	f.expect(f.admin, http.MethodPost, "/api/connectors/quick", map[string]string{"preset": "alertmanager"}, http.StatusCreated, &q)

	var res app.TestEventResult
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+q.Connector.ID+"/test-event", map[string]string{"ci": "db-01"}, http.StatusOK, &res)
	if res.IncidentID == "" {
		t.Fatalf("test event = %+v", res)
	}
	var view struct {
		Alert alert.Alert `json:"alert"`
	}
	f.expect(f.admin, http.MethodGet, "/api/incidents/"+res.IncidentID, nil, http.StatusOK, &view)
	a := view.Alert
	if a.Title != app.TestEventTitle || a.CIID != "CI-1" || a.Route.Team == nil || a.Route.Team.ID != "T-1" || !a.IsTest() ||
		a.PD.State != alert.PDSkipped {
		t.Fatalf("test incident = %+v", a)
	}
	// Without a name the event goes to umbrella-test, which is not in the catalog.
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+q.Connector.ID+"/test-event", nil, http.StatusOK, &res)
	view.Alert = alert.Alert{}
	f.expect(f.admin, http.MethodGet, "/api/incidents/"+res.IncidentID, nil, http.StatusOK, &view)
	if view.Alert.CIName != "umbrella-test" || view.Alert.CIID != "" {
		t.Errorf("default test incident = %+v", view.Alert)
	}
	if n := len(activeIncidents(f)); n != 2 {
		t.Fatalf("two test incidents, got %d", n)
	}

	// An outside request cannot pass itself off as a test.
	p := f.ingest(q.Connector.Slug, `{"alerts":[{"status":"firing","labels":{"alertname":"X","instance":"db-01","severity":"critical"}}]}`,
		map[string]string{"Authorization": "Bearer " + q.Token, "X-Umbrella-Test": "fake", "Content-Type": "application/json"})
	if p.status != http.StatusAccepted {
		t.Fatalf("ingest = %d", p.status)
	}
	waitFor(t, "the real incident", func() bool { return len(activeIncidents(f)) == 3 })
	for _, a := range activeIncidents(f) {
		if a.Signal == "X" && a.IsTest() {
			t.Errorf("a real alert marked as a test: %+v", a)
		}
	}

	e := f.h.app.AlertEngine()
	e.SetClock(func() time.Time { return time.Now().UTC().Add(alert.TestLifetime + time.Minute) })
	if err := e.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	list := activeIncidents(f)
	if len(list) != 1 || list[0].IsTest() {
		t.Errorf("the tests resolved themselves, the real one stays: %+v", list)
	}

	// An unpublished connector cannot send a test.
	var c app.ConnectorView
	f.expect(f.admin, http.MethodPost, "/api/connectors", map[string]any{"name": "Draft", "slug": "draft", "preset": "webhook"}, http.StatusCreated, &c)
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/test-event", nil, http.StatusConflict, nil)
}

// A Prometheus monitoring system is a metric source for rules without a separate metric source,
// and a metric source can be merged into the system.
func TestPrometheusSystemAsMetricSource(t *testing.T) {
	f := newConnFixture(t, false)
	var sys app.MonitoringSourceView
	f.expect(f.admin, http.MethodPost, "/api/monitoring/sources", map[string]any{"name": "Prom", "kind": "prometheus",
		"url": "http://prom.example:9090", "enabled": true}, http.StatusCreated, &sys)
	rule := map[string]any{"name": "CPU", "method": "use", "signal": "cpu", "source_id": sys.ID, "query": "node_cpu", "ci_label": "instance",
		"op": ">", "threshold": 90, "for": "5m", "interval": "1m", "severity": "error", "title": "CPU", "enabled": true}
	var r model.Rule
	f.expect(f.admin, http.MethodPost, "/api/rules", rule, http.StatusCreated, &r)
	var rv app.RulesView
	f.expect(f.admin, http.MethodGet, "/api/rules", nil, http.StatusOK, &rv)
	if len(rv.Sources) != 1 || !rv.Sources[0].System || rv.Sources[0].ID != sys.ID || rv.Sources[0].Rules != 1 || rv.Rules[0].SourceName != "Prom" {
		t.Fatalf("sources = %+v rules = %+v", rv.Sources, rv.Rules)
	}
	// The system cannot be deleted or turned into Zabbix while rules use it.
	f.expect(f.admin, http.MethodDelete, "/api/monitoring/sources/"+sys.ID, nil, http.StatusConflict, nil)

	// An old metric source at the same address merges into the system with its rules.
	var ms model.MetricSource
	f.expect(f.admin, http.MethodPost, "/api/metric-sources", map[string]any{"name": "Old", "url": "http://prom.example:9090/"}, http.StatusCreated, &ms)
	rule["source_id"], rule["name"] = ms.ID, "Disk"
	f.expect(f.admin, http.MethodPost, "/api/rules", rule, http.StatusCreated, nil)
	f.expect(f.admin, http.MethodGet, "/api/rules", nil, http.StatusOK, &rv)
	for _, s := range rv.Sources {
		if s.ID == ms.ID && s.SystemID != sys.ID {
			t.Errorf("the metric source points at its system: %+v", s)
		}
	}
	var mr app.MergeResult
	f.expect(f.admin, http.MethodPost, "/api/metric-sources/"+ms.ID+"/merge", nil, http.StatusOK, &mr)
	if mr.SystemID != sys.ID || mr.Created || mr.Rules != 1 {
		t.Errorf("merge = %+v", mr)
	}
	f.expect(f.admin, http.MethodGet, "/api/rules", nil, http.StatusOK, &rv)
	if len(rv.Sources) != 1 || rv.Sources[0].Rules != 2 {
		t.Errorf("after merge = %+v", rv.Sources)
	}
	// A metric source at another address becomes a new system.
	f.expect(f.admin, http.MethodPost, "/api/metric-sources", map[string]any{"name": "VM", "url": "http://vm.example:8428"}, http.StatusCreated, &ms)
	f.expect(f.admin, http.MethodPost, "/api/metric-sources/"+ms.ID+"/merge", nil, http.StatusOK, &mr)
	var mv app.MonitoringView
	f.expect(f.admin, http.MethodGet, "/api/monitoring", nil, http.StatusOK, &mv)
	if !mr.Created || len(mv.Sources) != 2 {
		t.Errorf("new system = %+v %d", mr, len(mv.Sources))
	}
}

// «Используется» lists every consumer of a credential, and deleting it names them.
func TestCredentialUsage(t *testing.T) {
	f := newConnFixture(t, false)
	cred := f.credential(flow.CredBearer, nil, map[string]string{"token": "api"})
	var c app.ConnectorView
	f.expect(f.admin, http.MethodPost, "/api/connectors", map[string]any{"name": "Hook", "slug": "hook", "preset": "webhook",
		"credentials": map[string]string{"webhook.credential": cred.ID}}, http.StatusCreated, &c)
	f.expect(f.admin, http.MethodPost, "/api/monitoring/sources", map[string]any{"name": "Zabbix", "kind": "zabbix",
		"url": "http://zbx.example", "credential_id": cred.ID, "enabled": true}, http.StatusCreated, nil)
	f.expect(f.admin, http.MethodPost, "/api/metric-sources", map[string]any{"name": "VM", "url": "http://vm.example", "credential_id": cred.ID}, http.StatusCreated, nil)

	var got app.CredentialView
	f.expect(f.admin, http.MethodGet, "/api/credentials/"+cred.ID, nil, http.StatusOK, &got)
	kinds := []string{}
	for _, u := range got.UsedBy {
		kinds = append(kinds, u.Kind+":"+u.Name)
	}
	if strings.Join(kinds, ",") != "connector:Hook,monitoring:Zabbix,metric_source:VM" {
		t.Errorf("used by = %v", kinds)
	}
	var problem struct {
		Error  string              `json:"error"`
		UsedBy []app.CredentialUse `json:"used_by"`
	}
	if code := f.admin.call(http.MethodDelete, "/api/credentials/"+cred.ID, nil, &problem); code != http.StatusConflict ||
		problem.Error != "credential_in_use" || len(problem.UsedBy) != 3 {
		t.Errorf("delete = %d %+v", code, problem)
	}
}

// Linking a host on the monitoring page moves its open incident to the item at once.
func TestHostLinkReresolvesIncident(t *testing.T) {
	f := newConnFixture(t, true)
	f.h.st.Write(func(d *store.Data) {
		d.Teams["T-1"] = &model.Team{ID: "T-1", Name: "DBA"}
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "db-01-prod", Status: model.CIStatusActive}
		d.Services["S-1"] = &model.Service{ID: "S-1", Name: "Billing", OwnerTeamID: "T-1", Status: model.ServiceActive, CIIDs: []string{"CI-1"}}
		d.MonitoringSources["MON-1"] = &model.MonitoringSource{ID: "MON-1", Name: "Zabbix", Kind: model.MonitoringZabbix, Enabled: true,
			Hosts: []model.MonitoringHost{{Key: "10101", Host: "zbx-db-01.corp", Name: "DB"}}, Links: map[string]string{}}
	})
	auth := f.webhookConnector()
	f.ingest("hook", `{"id":"a1","title":"Disk full","host":"zbx-db-01.corp","signal":"disk","severity":"critical","status":"firing"}`, auth)
	waitFor(t, "an incident", func() bool { return len(activeIncidents(f)) == 1 })
	if a := activeIncidents(f)[0]; a.CIID != "" {
		t.Fatalf("not in the catalog at first: %+v", a)
	}
	f.expect(f.admin, http.MethodPost, "/api/monitoring/link", map[string]string{"source_id": "MON-1", "key": "10101", "mode": "ci", "ci_id": "CI-1"}, http.StatusOK, nil)
	a := activeIncidents(f)[0]
	if a.CIID != "CI-1" || a.Route.Team == nil || a.Route.Team.ID != "T-1" {
		t.Fatalf("after linking = %+v", a)
	}
	f.expect(f.admin, http.MethodPost, "/api/monitoring/link", map[string]string{"source_id": "MON-1", "key": "10101", "mode": "none"}, http.StatusOK, nil)
	if a := activeIncidents(f)[0]; !a.Excluded || !a.Suppressed || a.CIID != "" {
		t.Fatalf("after «Не является КЕ» = %+v", a)
	}
}
