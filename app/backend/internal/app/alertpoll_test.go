package app_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/monitoring/monitoringtest"
)

// Polling Grafana hands a firing alert to the connector of the system once, as a contact point
// would: it opens an incident with a one-line title and the stack trace as its description,
// and the alert is resolved when Grafana stops reporting it.
func TestGrafanaPoll(t *testing.T) {
	f := newConnFixture(t, true)
	g := monitoringtest.StartGrafana(t)
	trace := "java.lang.IllegalStateException: pool exhausted\n\tat com.example.Pay.run(Pay.java:7)\n\tat com.example.Main.main(Main.java:3)"
	firing := monitoringtest.GrafanaInstance{Labels: map[string]string{"instance": "pay-01", "severity": "critical"},
		Annotations: map[string]string{"description": trace}, State: "Alerting", Value: "42"}
	g.SetRule("errs", "Payments errors", map[string]string{"__dashboardUid__": "pay", "__panelId__": "4"}, firing)

	var cred app.CredentialView
	f.expect(f.admin, http.MethodPost, "/api/credentials", app.CredentialInput{Name: "Grafana", Type: "bearer", Secrets: map[string]string{"token": g.Token}},
		http.StatusCreated, &cred)
	in := map[string]any{"name": "Grafana", "kind": "grafana", "url": g.URL, "credential_id": cred.ID, "enabled": true, "poll_seconds": 5}
	f.expect(f.admin, http.MethodPost, "/api/monitoring/sources", in, http.StatusBadRequest, nil)
	in["poll_seconds"] = 0
	var sys app.MonitoringSourceView
	f.expect(f.admin, http.MethodPost, "/api/monitoring/sources", in, http.StatusCreated, &sys)
	if sys.PollSeconds != 60 || sys.PollAlerts {
		t.Fatalf("system = %+v", sys)
	}
	// No connector yet: nothing to poll into.
	f.expect(f.admin, http.MethodPost, "/api/monitoring/sources/"+sys.ID+"/poll", nil, http.StatusConflict, nil)

	var q app.QuickConnectResult
	f.expect(f.admin, http.MethodPost, "/api/connectors/quick", map[string]string{"preset": "grafana", "monitoring_id": sys.ID}, http.StatusCreated, &q)

	var st model.MonitoringPoll
	f.expect(f.admin, http.MethodPost, "/api/monitoring/sources/"+sys.ID+"/poll", nil, http.StatusOK, &st)
	if !st.OK || st.Firing != 1 || st.Sent != 1 {
		t.Fatalf("poll = %+v", st)
	}
	waitFor(t, "the incident", func() bool { return len(activeIncidents(f)) == 1 })
	a := activeIncidents(f)[0]
	if a.Title != "java.lang.IllegalStateException: pool exhausted" || a.CIName != "pay-01" || a.Severity != "critical" {
		t.Errorf("incident = %+v", a)
	}
	var src *alert.Source
	for _, s := range a.Sources {
		src = s
	}
	if src == nil || !strings.Contains(src.Description, "Main.java:3") {
		t.Fatalf("source = %+v", src)
	}
	fields := map[string]string{}
	for _, fl := range src.Fields {
		fields[fl.Name] = fl.Value
	}
	if fields["Правило в Grafana"] != g.URL+"/alerting/grafana/errs/view" || fields["Дашборд"] != g.URL+"/d/pay?viewPanel=4" {
		t.Errorf("fields = %+v", src.Fields)
	}

	// Still firing: nothing new is sent.
	f.expect(f.admin, http.MethodPost, "/api/monitoring/sources/"+sys.ID+"/poll", nil, http.StatusOK, &st)
	if !st.OK || st.Sent != 0 {
		t.Errorf("second poll = %+v", st)
	}
	// Normal again: the incident resolves.
	firing.State = "Normal"
	g.SetRule("errs", "Payments errors", nil, firing)
	f.expect(f.admin, http.MethodPost, "/api/monitoring/sources/"+sys.ID+"/poll", nil, http.StatusOK, &st)
	if !st.OK || st.Firing != 0 || st.Sent != 1 {
		t.Errorf("third poll = %+v", st)
	}
	waitFor(t, "the resolution", func() bool { return len(activeIncidents(f)) == 0 })

	var mv app.MonitoringView
	f.expect(f.admin, http.MethodGet, "/api/monitoring", nil, http.StatusOK, &mv)
	if p := mv.Sources[0].Poll; p == nil || !p.OK || mv.Defaults.PollSeconds != 60 {
		t.Errorf("view = %+v", mv.Sources[0])
	}

	// A wrong token is the state of the poll, not a failure of the call.
	g.SetToken("other")
	f.expect(f.admin, http.MethodPost, "/api/monitoring/sources/"+sys.ID+"/poll", nil, http.StatusOK, &st)
	if st.OK || !strings.Contains(st.Error, "401") {
		t.Errorf("poll with a wrong token = %+v", st)
	}
}
