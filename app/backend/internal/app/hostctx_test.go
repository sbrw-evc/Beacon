package app_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/monitoring/monitoringtest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type machineView struct {
	From          time.Time       `json:"from"`
	To            time.Time       `json:"to"`
	WindowMinutes int             `json:"window_minutes"`
	DefaultWindow int             `json:"default_window"`
	Names         []string        `json:"names"`
	Hosts         []app.CIMonitor `json:"hosts"`
	LogSources    int             `json:"log_sources"`
	EventsError   string          `json:"events_error"`
	SourceEvents  []struct {
		Title    string `json:"title"`
		Severity string `json:"severity"`
		Status   string `json:"status"`
		Source   string `json:"source"`
	} `json:"source_events"`
	SourceEventsErrors []struct {
		Source string `json:"source"`
		Error  string `json:"error"`
	} `json:"source_events_errors"`
	SourceEventHosts int               `json:"source_event_hosts"`
	Events           []json.RawMessage `json:"events"`
	Incidents        []struct {
		ID string `json:"id"`
	} `json:"incidents"`
	Panels []struct {
		ID        string   `json:"id"`
		Alert     bool     `json:"alert"`
		Query     string   `json:"query"`
		Op        string   `json:"op"`
		Threshold *float64 `json:"threshold"`
		Series    []struct {
			Name   string       `json:"name"`
			Points [][2]float64 `json:"points"`
		} `json:"series"`
		Errors []struct {
			Source string `json:"source"`
			Error  string `json:"error"`
		} `json:"errors"`
	} `json:"panels"`
}

type machineLogs struct {
	Sources int `json:"sources"`
	Lines   []struct {
		Source string `json:"source"`
		Text   string `json:"text"`
	} `json:"lines"`
	Errors []struct {
		Source string `json:"source"`
	} `json:"errors"`
}

func TestIncidentMachine(t *testing.T) {
	f := newConnFixture(t, true)
	prom := monitoringtest.StartPrometheus(t)
	now := time.Now().UTC()
	prom.AlertRule("cpu", `cpu_busy{job="node"} > 90`)
	prom.Range(map[string]string{"instance": "db-09:9100", "job": "node"}, [2]float64{float64(now.Unix()), 1})
	prom.Range(map[string]string{"instance": "web-01:9100", "job": "node"}, [2]float64{float64(now.Add(-5 * time.Minute).Unix()), 12}, [2]float64{float64(now.Unix()), 90})
	var lokiQuery string
	loki := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lokiQuery = r.URL.Query().Get("query")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "streams", "result": []any{
			map[string]any{"stream": map[string]string{"host": "web-01"}, "values": [][2]string{{"1700000060000000000", "nginx: worker exited"}}},
		}}})
	}))
	t.Cleanup(loki.Close)
	// Zabbix watches the machine too and keeps its own problems and log lines.
	zbx := monitoringtest.StartZabbix(t, "8.0.0")
	zbx.AddEvent(monitoringtest.Event{HostID: "10101", ID: "77", TriggerID: "500", Name: "High CPU utilization", Clock: now.Add(-2 * time.Minute).Unix(), Severity: 4})
	zbx.AddEvent(monitoringtest.Event{HostID: "10101", ID: "70", TriggerID: "501", Name: "Long ago", Clock: now.Add(-48 * time.Hour).Unix(), Severity: 2,
		Recovery: "71", RecoveryClock: now.Add(-47 * time.Hour).Unix()})
	zbx.AddLogItem(10101, 9, "nginx error log", "log[/var/log/nginx/error.log]", 2,
		monitoringtest.LogValue{Clock: now.Add(-time.Minute).Unix(), Value: "ERROR nginx: upstream timed out"},
		monitoringtest.LogValue{Clock: now.Add(-time.Minute).Unix(), Value: "INFO php-fpm: pool www started"})
	zcred := f.credential(flow.CredBearer, nil, map[string]string{"token": "zbx-token"})
	f.h.st.Write(func(d *store.Data) {
		d.MonitoringSources["MON-2"] = &model.MonitoringSource{ID: "MON-2", Name: "Zabbix", Kind: model.MonitoringZabbix, URL: zbx.URL, Enabled: true, CredentialID: zcred.ID,
			Hosts: []model.MonitoringHost{{Key: "10101", Host: "web-01", Name: "web-01", IPs: []string{"10.0.0.7"}, State: model.HostUp}}}
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "web-01", Kind: model.CIKindVM, Status: model.CIStatusActive, IPs: []string{"10.0.0.7"}}
		d.MonitoringSources["MON-1"] = &model.MonitoringSource{ID: "MON-1", Name: "Metrics", Kind: model.MonitoringPrometheus, URL: prom.URL, Enabled: true,
			Hosts: []model.MonitoringHost{{Key: "web-01", Host: "web-01", Name: "web-01", Endpoints: []string{"web-01:9100"}, State: model.HostUp}}}
	})

	// Settings and log sources.
	var view app.HostContextView
	f.expect(f.admin, http.MethodGet, "/api/host-context", nil, http.StatusOK, &view)
	if view.Settings.WindowMinutes != 60 || len(view.Settings.Panels) != len(model.DefaultHostPanels()) || len(view.LogSources) != 0 {
		t.Fatalf("defaults %+v", view)
	}
	f.expect(f.admin, http.MethodPut, "/api/host-context", map[string]any{"window_minutes": 0, "log_limit": 100}, http.StatusBadRequest, nil)
	f.expect(f.admin, http.MethodPut, "/api/host-context", map[string]any{"window_minutes": 30, "log_limit": 100,
		"panels": []map[string]string{{"title": "CPU", "unit": "%", "promql": "cpu{$selector}"}, {"title": "Empty"}}}, http.StatusBadRequest, nil)
	f.expect(f.admin, http.MethodPut, "/api/host-context", map[string]any{"window_minutes": 30, "log_limit": 100,
		"panels": []map[string]string{{"title": "CPU", "unit": "%", "promql": "cpu{$selector}"}, {"title": "Disk", "zabbix_key": "vfs.fs.size[/,pused]"}}}, http.StatusOK, nil)
	f.expect(f.admin, http.MethodPost, "/api/host-context/logs", map[string]any{"name": "Logs", "kind": "elk", "url": loki.URL}, http.StatusBadRequest, nil)
	var ls app.LogSourceView
	f.expect(f.admin, http.MethodPost, "/api/host-context/logs", map[string]any{"name": "Logs", "kind": model.LogLoki, "url": loki.URL, "enabled": true}, http.StatusCreated, &ls)
	var test app.LogTestReport
	f.expect(f.admin, http.MethodPost, "/api/host-context/logs/test", map[string]any{"name": "Logs", "kind": model.LogLoki, "url": loki.URL, "host": "web-01"}, http.StatusOK, &test)
	if !test.OK || len(test.Lines) != 1 {
		t.Fatalf("test %+v", test)
	}

	// An incident on the machine.
	cred := f.credential(flow.CredBearer, nil, map[string]string{"token": "tkn-1"})
	var c app.ConnectorView
	f.expect(f.admin, http.MethodPost, "/api/connectors", map[string]any{"name": "Webhook", "slug": "hook", "preset": "webhook",
		"credentials": map[string]string{"webhook.credential": cred.ID}}, http.StatusCreated, &c)
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/publish", map[string]any{}, http.StatusOK, nil)
	f.ingest("hook", `[{"id":"a1","title":"High CPU","host":"web-01","signal":"cpu","severity":"critical","status":"firing"}]`,
		map[string]string{"Authorization": "Bearer tkn-1", "Content-Type": "application/json"})
	var page alert.Page
	waitFor(t, "an incident", func() bool {
		f.admin.call(http.MethodGet, "/api/incidents?status=active", nil, &page)
		return len(page.Alerts) == 1
	})
	id := page.Alerts[0].ID
	if page.Alerts[0].CIID != "CI-1" {
		t.Fatalf("incident %+v", page.Alerts[0])
	}

	var m machineView
	f.expect(f.admin, http.MethodGet, "/api/incidents/"+id+"/machine", nil, http.StatusOK, &m)
	if m.WindowMinutes != 30 || m.DefaultWindow != 30 || len(m.Hosts) != 2 || m.LogSources != 2 || m.EventsError != "" {
		t.Fatalf("machine %+v", m)
	}
	// The problems Zabbix raised on the machine in the window, with the host named.
	if m.SourceEventHosts != 1 || len(m.SourceEventsErrors) != 0 || len(m.SourceEvents) != 1 || m.SourceEvents[0].Title != "High CPU utilization" ||
		m.SourceEvents[0].Severity != model.SeverityError || m.SourceEvents[0].Status != "firing" || m.SourceEvents[0].Source != "Zabbix" {
		t.Fatalf("zabbix events %+v %+v", m.SourceEvents, m.SourceEventsErrors)
	}
	// The metric of the alert (the alerting rule named as the signal) comes first, with its
	// threshold, and only the series of the machine.
	if len(m.Panels) != 3 || !m.Panels[0].Alert || m.Panels[0].Query != `cpu_busy{job="node"}` || m.Panels[0].Op != ">" ||
		m.Panels[0].Threshold == nil || *m.Panels[0].Threshold != 90 || len(m.Panels[0].Series) != 1 || m.Panels[0].Series[0].Name != "web-01:9100" {
		t.Fatalf("alert panel %+v", m.Panels)
	}
	// The fake answers every query with all its series; the graphs keep what the selector chose.
	if len(m.Panels[1].Series) != 2 || m.Panels[1].Alert || len(m.Panels[2].Series) != 0 || len(m.Panels[2].Errors) != 0 {
		t.Fatalf("panels %+v", m.Panels)
	}
	if !slices.Contains(prom.Queries, `cpu{instance=~"web-01:9100"}`) || !slices.Contains(prom.Queries, `cpu_busy{job="node"}`) {
		t.Fatalf("queries %q", prom.Queries)
	}
	if len(m.Events) != 1 || len(m.Incidents) != 1 || m.Incidents[0].ID != id {
		t.Fatalf("history %+v %+v", m.Events, m.Incidents)
	}
	f.expect(f.admin, http.MethodGet, "/api/incidents/"+id+"/machine?minutes=360&span=true", nil, http.StatusOK, &m)
	if m.WindowMinutes != 360 || !m.To.After(m.From.Add(6*time.Hour)) {
		t.Fatalf("window %+v", m)
	}
	f.expect(f.admin, http.MethodGet, "/api/incidents/"+id+"/machine?minutes=99999", nil, http.StatusBadRequest, nil)

	var lg machineLogs
	f.expect(f.admin, http.MethodGet, "/api/incidents/"+id+"/machine/logs?q=nginx", nil, http.StatusOK, &lg)
	if lg.Sources != 2 || len(lg.Lines) != 2 || len(lg.Errors) != 0 {
		t.Fatalf("logs %+v", lg)
	}
	sources := []string{lg.Lines[0].Source, lg.Lines[1].Source}
	slices.Sort(sources)
	if !slices.Equal(sources, []string{"Logs", "Zabbix"}) || !slices.ContainsFunc(lg.Lines, func(l struct {
		Source string `json:"source"`
		Text   string `json:"text"`
	}) bool {
		return l.Text == "ERROR nginx: upstream timed out"
	}) {
		t.Fatalf("logs %+v", lg.Lines)
	}
	if !strings.Contains(lokiQuery, `10\\.0\\.0\\.7`) || !strings.Contains(lokiQuery, `|~ "(?i)nginx"`) {
		t.Fatalf("loki query %s", lokiQuery)
	}

	// Somebody who sees incidents but not monitoring reads the machine but not the settings.
	f.h.addRole("duty", "incidents:view")
	f.h.addLocal("duty", "Duty-pass-2026-x", "duty", time.Now())
	duty := f.h.client()
	duty.login("duty", "Duty-pass-2026-x")
	f.expect(duty, http.MethodGet, "/api/incidents/"+id+"/machine", nil, http.StatusOK, nil)
	f.expect(duty, http.MethodGet, "/api/host-context", nil, http.StatusForbidden, nil)

	// A credential used by a log source is not deleted.
	f.expect(f.admin, http.MethodPut, "/api/host-context/logs/"+ls.ID, map[string]any{"name": "Logs", "kind": model.LogLoki, "url": loki.URL,
		"enabled": true, "credential_id": cred.ID}, http.StatusOK, nil)
	f.expect(f.admin, http.MethodDelete, "/api/credentials/"+cred.ID, nil, http.StatusConflict, nil)
	f.expect(f.admin, http.MethodDelete, "/api/host-context/logs/"+ls.ID, nil, http.StatusNoContent, nil)
}
