package app_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func (f connFixture) guide(c *client, page string) (app.Onboarding, map[string]app.OnboardingStep) {
	f.h.t.Helper()
	var out app.Onboarding
	f.expect(c, http.MethodGet, "/api/guides/"+page, nil, http.StatusOK, &out)
	steps := map[string]app.OnboardingStep{}
	for _, s := range out.Steps {
		steps[s.ID] = s
	}
	return out, steps
}

func TestPageGuidesFollowState(t *testing.T) {
	f := newConnFixture(t, false)
	now := time.Now()
	f.h.st.Write(func(d *store.Data) {
		d.EnsureSystemRoles(now)
		d.Roles["R-cis"] = &model.Role{ID: "R-cis", Name: "CIs", Permissions: []string{"cis:view", "cis:edit"}}
	})
	f.h.addLocal("cis", "Cis-pass-2026-x", "R-cis", now)
	cis := f.h.client()
	cis.login("cis", "Cis-pass-2026-x")

	// Every key page has a guide; a fresh installation has nothing done.
	for _, page := range []string{"monitoring", "connectors", "cis", "services", "cmdb", "teams", "users", "rules", "maintenance", "wallboards", "response", "settings.alerting", "netbox"} {
		got, _ := f.guide(f.admin, page)
		if got.Done || len(got.Steps) == 0 {
			t.Errorf("%s fresh = %+v", page, got)
		}
		for _, s := range got.Steps {
			if !s.CanFix || (s.Path == "" && s.Action == "" && s.ID == "") {
				t.Errorf("%s step = %+v", page, s)
			}
		}
	}
	// A page without a guide, and a page the user cannot see.
	if code := f.admin.call(http.MethodGet, "/api/guides/incidents", nil, nil); code != http.StatusNotFound {
		t.Errorf("incidents guide = %d", code)
	}
	if code := cis.call(http.MethodGet, "/api/guides/teams", nil, nil); code != http.StatusForbidden {
		t.Errorf("teams guide without teams:view = %d", code)
	}

	// Steps follow permissions: a CI editor can add items but not bind them to services.
	_, s := f.guide(cis, "cis")
	if !s["ci"].CanFix || s["ci"].Action != app.ActionCreate || s["service"].CanFix || s["service"].Path != "/services" {
		t.Errorf("cis for a CI editor = %+v", s)
	}

	// Steps follow the state: an item with an owner, then bound to a service.
	f.h.st.Write(func(d *store.Data) {
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "db-01", Status: model.CIStatusActive, Owners: []model.CIOwner{{UserID: "x"}}}
	})
	got, s := f.guide(f.admin, "cis")
	if !s["ci"].Done || !s["owners"].Done || s["service"].Done || got.Done {
		t.Fatalf("cis with an owned item = %+v", got)
	}
	f.h.st.Write(func(d *store.Data) {
		d.Services["S-1"] = &model.Service{ID: "S-1", Name: "Billing", Status: model.ServiceActive, CIIDs: []string{"CI-1"}}
	})
	// The NetBox step is optional: the guide is done without it.
	if got, s := f.guide(f.admin, "cis"); !got.Done || s["netbox"].Done || !s["netbox"].Optional {
		t.Fatalf("cis bound to a service = %+v", got)
	}

	// Maintenance: an item to cover is enough for the first step.
	if _, s := f.guide(f.admin, "maintenance"); !s["target"].Done || s["window"].Done {
		t.Errorf("maintenance = %+v", s)
	}

	// Response: dry run is a step, live is the last one.
	f.h.st.Write(func(d *store.Data) { d.Settings.Response.Mode = model.ModeDryRun })
	if _, s := f.guide(f.admin, "response"); !s["dry_run"].Done || s["live"].Done || s["integrations"].Action != app.ActionIntegration {
		t.Errorf("response in dry run = %+v", s)
	}
}
