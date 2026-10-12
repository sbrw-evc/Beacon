package app_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/monitoring/monitoringtest"
)

// Polling Graylog makes an alert of the events of one definition and key: the first event
// opens an incident, later ones of the same alert send nothing, and the incident resolves once
// the events stop coming for the quiet time.
func TestGraylogPoll(t *testing.T) {
	f := newConnFixture(t, true)
	g := monitoringtest.StartGraylog(t)
	ev := monitoringtest.GraylogEvent{ID: "e1", DefinitionID: "6720a1b2c3d4e5f601234567", Title: "Too many 5xx", Key: "web-01",
		Message: "Too many 5xx: count()=73.0", Priority: 3, At: time.Now().Add(-time.Minute), GroupBy: map[string]string{"source": "web-01"}}
	g.AddEvent(ev)

	var cred app.CredentialView
	f.expect(f.admin, http.MethodPost, "/api/credentials", app.CredentialInput{Name: "Graylog", Type: "bearer", Secrets: map[string]string{"token": g.Token}},
		http.StatusCreated, &cred)
	in := map[string]any{"name": "Graylog", "kind": "graylog", "url": g.URL, "credential_id": cred.ID, "enabled": true, "quiet_minutes": 5000}
	f.expect(f.admin, http.MethodPost, "/api/monitoring/sources", in, http.StatusBadRequest, nil)
	in["quiet_minutes"] = 0
	var sys app.MonitoringSourceView
	f.expect(f.admin, http.MethodPost, "/api/monitoring/sources", in, http.StatusCreated, &sys)
	if sys.PollSeconds != 60 || sys.QuietMinutes != 15 {
		t.Fatalf("system = %+v", sys)
	}
	var q app.QuickConnectResult
	f.expect(f.admin, http.MethodPost, "/api/connectors/quick", map[string]string{"preset": "graylog", "monitoring_id": sys.ID}, http.StatusCreated, &q)

	var st model.MonitoringPoll
	f.expect(f.admin, http.MethodPost, "/api/monitoring/sources/"+sys.ID+"/poll", nil, http.StatusOK, &st)
	if !st.OK || st.Firing != 1 || st.Sent != 1 {
		t.Fatalf("poll = %+v", st)
	}
	waitFor(t, "the incident", func() bool { return len(activeIncidents(f)) == 1 })
	a := activeIncidents(f)[0]
	if a.Title != "Too many 5xx" || a.CIName != "web-01" || a.Severity != "error" {
		t.Errorf("incident = %+v", a)
	}

	// The definition keeps firing for the same key: nothing new is sent.
	ev.ID, ev.At = "e2", time.Now()
	g.AddEvent(ev)
	f.expect(f.admin, http.MethodPost, "/api/monitoring/sources/"+sys.ID+"/poll", nil, http.StatusOK, &st)
	if !st.OK || st.Firing != 1 || st.Sent != 0 {
		t.Errorf("second poll = %+v", st)
	}
	// No more events: the alert resolves.
	g.ClearEvents()
	f.expect(f.admin, http.MethodPost, "/api/monitoring/sources/"+sys.ID+"/poll", nil, http.StatusOK, &st)
	if !st.OK || st.Firing != 0 || st.Sent != 1 {
		t.Errorf("third poll = %+v", st)
	}
	waitFor(t, "the resolution", func() bool { return len(activeIncidents(f)) == 0 })
}
