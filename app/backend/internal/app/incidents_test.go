package app_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func TestIncidentsFromWebhook(t *testing.T) {
	f := newConnFixture(t, true)
	f.h.st.Write(func(d *store.Data) {
		d.Teams["T-1"] = &model.Team{ID: "T-1", Name: "DBA"}
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "db-01", Status: model.CIStatusActive}
		d.Services["S-1"] = &model.Service{ID: "S-1", Name: "Billing", OwnerTeamID: "T-1", Status: model.ServiceActive, CIIDs: []string{"CI-1"}}
	})
	userID := f.h.addLocal("dba", "Dba-pass-2026-x", model.RoleUser, time.Now())
	f.h.st.Write(func(d *store.Data) { d.Users[userID].TeamIDs = []string{"T-1"} })
	cred := f.credential(flow.CredBearer, nil, map[string]string{"token": "tkn-1"})
	var c app.ConnectorView
	f.expect(f.admin, http.MethodPost, "/api/connectors", map[string]any{"name": "Webhook", "slug": "hook", "preset": "webhook",
		"credentials": map[string]string{"webhook.credential": cred.ID}}, http.StatusCreated, &c)
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/publish", map[string]any{}, http.StatusOK, nil)
	auth := map[string]string{"Authorization": "Bearer tkn-1", "Content-Type": "application/json"}

	f.ingest("hook", `{"id":"a1","title":"Disk full","host":"db-01","signal":"disk","severity":"critical","status":"firing"}`, auth)
	var page alert.Page
	waitFor(t, "an incident", func() bool {
		f.admin.call(http.MethodGet, "/api/incidents?status=active", nil, &page)
		return len(page.Alerts) == 1
	})
	inc := page.Alerts[0]
	if inc.CIID != "CI-1" || inc.Route.Team == nil || inc.Route.Team.ID != "T-1" || len(inc.Route.People) != 1 || inc.Severity != "critical" {
		t.Fatalf("incident = %+v", inc)
	}

	// A user without the incidents permission sees nothing.
	dba := f.h.client()
	dba.login("dba", "Dba-pass-2026-x")
	if code := dba.call(http.MethodGet, "/api/incidents", nil, nil); code != http.StatusForbidden {
		t.Errorf("without permission = %d", code)
	}

	var view struct {
		Alert    alert.Alert   `json:"alert"`
		Timeline []alert.Entry `json:"timeline"`
	}
	f.expect(f.admin, http.MethodPost, "/api/incidents/"+inc.ID+"/ack", nil, http.StatusOK, nil)
	f.expect(f.admin, http.MethodPost, "/api/incidents/"+inc.ID+"/ack", nil, http.StatusConflict, nil)
	f.expect(f.admin, http.MethodPost, "/api/incidents/"+inc.ID+"/comment", map[string]string{"text": "looking"}, http.StatusOK, nil)
	f.expect(f.admin, http.MethodGet, "/api/incidents/"+inc.ID, nil, http.StatusOK, &view)
	if view.Alert.Status != alert.StatusAcknowledged || view.Alert.AckedBy != "admin" || view.Timeline[len(view.Timeline)-1].Args["text"] != "looking" {
		t.Errorf("view = %+v", view)
	}

	// Recovery from the source resolves the incident.
	f.ingest("hook", `{"id":"a1","title":"Disk full","host":"db-01","signal":"disk","severity":"critical","status":"resolved"}`, auth)
	waitFor(t, "the incident resolved", func() bool {
		f.admin.call(http.MethodGet, "/api/incidents?status=active", nil, &page)
		return len(page.Alerts) == 0
	})
	f.expect(f.admin, http.MethodGet, "/api/incidents?status=resolved&q=billing", nil, http.StatusOK, &page)
	if len(page.Alerts) != 1 {
		t.Errorf("resolved and found by service name: %+v", page.Alerts)
	}
	var bulk map[string]any
	f.expect(f.admin, http.MethodPost, "/api/incidents/bulk", map[string]any{"ids": []string{inc.ID, "INC-999"}, "action": "resolve"}, http.StatusOK, &bulk)
	if failed := bulk["failed"].(map[string]any); failed[inc.ID] != "not_active" || failed["INC-999"] != "not_found" {
		t.Errorf("bulk = %v", bulk)
	}
}

func TestIncidentsWithoutPostgres(t *testing.T) {
	f := newConnFixture(t, false)
	if code := f.admin.call(http.MethodGet, "/api/incidents", nil, nil); code != http.StatusServiceUnavailable {
		t.Errorf("without PostgreSQL = %d", code)
	}
}
