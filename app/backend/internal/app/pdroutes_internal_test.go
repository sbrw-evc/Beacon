package app

import (
	"context"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// A PagerDuty route of a deleted team or service can never match again; it goes with them, so
// the PagerDuty settings stay savable.
func TestDeletedTeamAndServiceDropPDRoutes(t *testing.T) {
	st := store.New()
	st.Write(func(d *store.Data) {
		d.Teams["TEAM-1"] = &model.Team{ID: "TEAM-1", Name: "Ops"}
		d.Teams["TEAM-2"] = &model.Team{ID: "TEAM-2", Name: "DBA"}
		d.Services["SVC-1"] = &model.Service{ID: "SVC-1", Name: "Shop", Status: model.ServiceActive}
		d.Settings.Alerting.PagerDuty.Routes = []model.PDRoute{
			{ID: "PDR-1", Name: "ops", TeamID: "TEAM-1"},
			{ID: "PDR-2", Name: "shop", ServiceID: "SVC-1"},
			{ID: "PDR-3", Name: "dba", TeamID: "TEAM-2"},
		}
	})
	if err := NewTeamsService(st).Delete("admin", "TEAM-1"); err != nil {
		t.Fatal(err)
	}
	if err := NewServicesService(st, nil).Delete(context.Background(), "admin", "SVC-1"); err != nil {
		t.Fatal(err)
	}
	st.Read(func(d *store.Data) {
		r := d.Settings.Alerting.PagerDuty.Routes
		if len(r) != 1 || r[0].ID != "PDR-3" {
			t.Fatalf("routes = %+v", r)
		}
	})
}
