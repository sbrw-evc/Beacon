package app_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func TestMaintenanceWindows(t *testing.T) {
	f := newConnFixture(t, true)
	f.h.st.Write(func(d *store.Data) {
		d.Teams["T-1"] = &model.Team{ID: "T-1", Name: "DBA"}
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "db-01", Status: model.CIStatusActive}
		d.ConfigItems["CI-2"] = &model.ConfigItem{ID: "CI-2", Name: "db-02", Status: model.CIStatusActive}
		d.Services["S-1"] = &model.Service{ID: "S-1", Name: "Billing", OwnerTeamID: "T-1", Status: model.ServiceActive, CIIDs: []string{"CI-1"}}
	})
	auth := f.webhookConnector()
	now := time.Now().UTC()
	body := map[string]any{"title": "Billing DB upgrade", "service_ids": []string{"S-1"}, "start": now.Add(-time.Minute), "end": now.Add(time.Hour)}

	f.expect(f.admin, http.MethodPost, "/api/maintenance", map[string]any{"title": "x", "start": now, "end": now.Add(time.Hour)}, http.StatusBadRequest, nil)
	f.expect(f.admin, http.MethodPost, "/api/maintenance", map[string]any{"title": "x", "ci_ids": []string{"CI-1"}, "start": now, "end": now.Add(-time.Hour)}, http.StatusBadRequest, nil)
	f.expect(f.admin, http.MethodPost, "/api/maintenance", map[string]any{"title": "x", "ci_ids": []string{"CI-9"}, "start": now, "end": now.Add(time.Hour)}, http.StatusBadRequest, nil)
	var mw app.MaintenanceView
	f.expect(f.admin, http.MethodPost, "/api/maintenance", body, http.StatusCreated, &mw)
	if mw.State != model.MaintenanceActive || len(mw.Services) != 1 || mw.Services[0].Name != "Billing" || mw.CreatedBy != "admin" {
		t.Fatalf("window = %+v", mw)
	}
	var planned app.MaintenanceView
	f.expect(f.admin, http.MethodPost, "/api/maintenance", map[string]any{"title": "Later", "ci_ids": []string{"CI-2"},
		"start": now.Add(24 * time.Hour), "end": now.Add(25 * time.Hour)}, http.StatusCreated, &planned)
	var list []app.MaintenanceView
	f.expect(f.admin, http.MethodGet, "/api/maintenance", nil, http.StatusOK, &list)
	if len(list) != 2 || list[0].ID != mw.ID || list[1].State != model.MaintenancePlanned {
		t.Fatalf("list = %+v", list)
	}
	var targets map[string][]app.TargetRef
	f.expect(f.admin, http.MethodGet, "/api/maintenance/targets?q=db-0", nil, http.StatusOK, &targets)
	if len(targets["cis"]) != 2 {
		t.Fatalf("targets = %+v", targets)
	}

	// An incident of an item of the service is kept but not sent anywhere.
	f.ingest("hook", `{"id":"a1","title":"Disk full","host":"db-01","signal":"disk","severity":"critical","status":"firing"}`, auth)
	var page struct {
		Alerts []alert.Alert `json:"alerts"`
	}
	eng := f.h.app.AlertEngine()
	waitFor(t, "the incident in maintenance", func() bool {
		_ = eng.Tick(t.Context())
		f.admin.call(http.MethodGet, "/api/incidents?suppressed=true", nil, &page)
		return len(page.Alerts) == 1 && page.Alerts[0].Suppressed && page.Alerts[0].MaintenanceID == mw.ID
	})

	f.expect(f.admin, http.MethodPost, "/api/maintenance/"+planned.ID+"/finish", nil, http.StatusBadRequest, nil)
	f.expect(f.admin, http.MethodPost, "/api/maintenance/"+mw.ID+"/finish", nil, http.StatusOK, &mw)
	if mw.State != model.MaintenanceFinished {
		t.Fatalf("finished = %+v", mw)
	}
	waitFor(t, "the end of maintenance", func() bool {
		_ = eng.Tick(t.Context())
		f.admin.call(http.MethodGet, "/api/incidents", nil, &page)
		return len(page.Alerts) == 1 && !page.Alerts[0].Suppressed
	})

	body["title"] = "Renamed"
	f.expect(f.admin, http.MethodPut, "/api/maintenance/"+planned.ID, body, http.StatusOK, &planned)
	if planned.Title != "Renamed" {
		t.Fatalf("updated = %+v", planned)
	}
	f.expect(f.admin, http.MethodDelete, "/api/maintenance/"+planned.ID, nil, http.StatusNoContent, nil)
	f.expect(f.admin, http.MethodDelete, "/api/maintenance/"+planned.ID, nil, http.StatusNotFound, nil)
}
