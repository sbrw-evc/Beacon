package app_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type serviceFixture struct {
	h     *harness
	admin *client
}

func newServiceFixture(t *testing.T) serviceFixture {
	h := newHarness(t)
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	now := time.Now().UTC()
	h.st.Write(func(d *store.Data) {
		for _, team := range []model.Team{
			{ID: "TEAM-1", Name: "Platform"},
			{ID: "TEAM-2", Name: "SRE", ParentID: "TEAM-1"},
			{ID: "TEAM-3", Name: "Payments"},
			{ID: "TEAM-4", Name: "Oncall", ParentID: "TEAM-2"},
		} {
			team.CreatedAt, team.UpdatedAt = now, now
			d.Teams[team.ID] = &team
		}
	})
	admin := h.client()
	admin.login("admin", "Admin-pass-2026")
	return serviceFixture{h: h, admin: admin}
}

func (f serviceFixture) create(in app.ServiceInput) app.ServiceView {
	f.h.t.Helper()
	var out app.ServiceView
	if code := f.admin.call(http.MethodPost, "/api/services", in, &out); code != http.StatusCreated {
		f.h.t.Fatalf("create %s = %d", in.Name, code)
	}
	return out
}

func (f serviceFixture) expect(method, path string, body any, status int, code string) {
	f.h.t.Helper()
	var problem map[string]any
	if got := f.admin.call(method, path, body, &problem); got != status || (code != "" && problem["error"] != code) {
		f.h.t.Fatalf("%s %s = %d %v, want %d %s", method, path, got, problem, status, code)
	}
}

func (f serviceFixture) cleanup() {
	f.h.t.Helper()
	var list app.ServiceList
	f.admin.call(http.MethodGet, "/api/services", nil, &list)
	for _, s := range list.Services {
		f.expect(http.MethodDelete, "/api/services/"+s.ID, nil, http.StatusNoContent, "")
	}
	f.h.st.Write(func(d *store.Data) {
		for id := range d.Teams {
			delete(d.Teams, id)
		}
	})
	f.h.st.Read(func(d *store.Data) {
		if len(d.Services) != 0 {
			f.h.t.Fatalf("services left: %d", len(d.Services))
		}
	})
}

func TestServiceValidation(t *testing.T) {
	f := newServiceFixture(t)
	defer f.cleanup()
	base := app.ServiceInput{Name: "Billing", OwnerTeamID: "TEAM-3"}
	long := make([]byte, 121)
	for i := range long {
		long[i] = 'a'
	}
	cases := []struct {
		name string
		edit func(*app.ServiceInput)
		code string
	}{
		{"empty name", func(in *app.ServiceInput) { in.Name = "  " }, "invalid_service_name"},
		{"long name", func(in *app.ServiceInput) { in.Name = string(long) }, "invalid_service_name"},
		{"no owner", func(in *app.ServiceInput) { in.OwnerTeamID = "" }, "owner_team_required"},
		{"missing owner", func(in *app.ServiceInput) { in.OwnerTeamID = "TEAM-404" }, "unknown_team"},
		{"missing supporting", func(in *app.ServiceInput) { in.TeamIDs = []string{"TEAM-404"} }, "unknown_team"},
		{"owner also supporting", func(in *app.ServiceInput) { in.TeamIDs = []string{"TEAM-3"} }, "duplicate_team"},
		{"criticality", func(in *app.ServiceInput) { in.Criticality = "urgent" }, "invalid_criticality"},
		{"status", func(in *app.ServiceInput) { in.Status = "gone" }, "invalid_service_status"},
		{"tag", func(in *app.ServiceInput) { in.Tags = []string{"two words"} }, "invalid_tag"},
		{"link scheme", func(in *app.ServiceInput) { in.Links = []model.Link{{Title: "x", URL: "javascript:alert(1)"}} }, "invalid_link"},
		{"link host", func(in *app.ServiceInput) { in.Links = []model.Link{{URL: "https://"}} }, "invalid_link"},
		{"unknown dependency", func(in *app.ServiceInput) { in.DependsOn = []string{"SVC-404"} }, "unknown_service"},
	}
	for _, c := range cases {
		in := base
		c.edit(&in)
		f.expect(http.MethodPost, "/api/services", in, http.StatusBadRequest, c.code)
	}

	in := base
	in.TeamIDs = []string{"TEAM-2", "TEAM-2", ""}
	in.Tags = []string{" Payments ", "payments", "PCI"}
	in.Links = []model.Link{{Title: "Runbook", URL: "https://wiki.example.com/billing"}, {}}
	got := f.create(in)
	if got.Criticality != model.CriticalityMedium || got.Status != model.ServiceActive || len(got.TeamIDs) != 1 || len(got.Tags) != 2 || got.Tags[0] != "payments" || len(got.Links) != 1 {
		t.Fatalf("normalized service = %+v", got)
	}
	if got.Owner.Name != "Payments" || len(got.Teams) != 1 || len(got.Teams[0].Path) != 2 || got.Teams[0].Path[0] != "Platform" || got.Teams[0].Path[1] != "SRE" {
		t.Fatalf("team labels = %+v %+v", got.Owner, got.Teams)
	}
	f.h.st.Read(func(d *store.Data) {
		if last := d.Audit[len(d.Audit)-1]; last.Action != "service.create" || last.Object != got.ID {
			t.Fatalf("create not audited: %+v", last)
		}
	})
}

func TestServiceUniqueName(t *testing.T) {
	f := newServiceFixture(t)
	defer f.cleanup()
	first := f.create(app.ServiceInput{Name: "Checkout", OwnerTeamID: "TEAM-3"})
	second := f.create(app.ServiceInput{Name: "Search", OwnerTeamID: "TEAM-1"})
	f.expect(http.MethodPost, "/api/services", app.ServiceInput{Name: "checkout ", OwnerTeamID: "TEAM-1"}, http.StatusConflict, "service_name_taken")
	f.expect(http.MethodPut, "/api/services/"+second.ID, app.ServiceInput{Name: "CHECKOUT", OwnerTeamID: "TEAM-1"}, http.StatusConflict, "service_name_taken")
	var renamed app.ServiceView
	if code := f.admin.call(http.MethodPut, "/api/services/"+first.ID, app.ServiceInput{Name: "CheckOut", OwnerTeamID: "TEAM-3", Criticality: "critical"}, &renamed); code != 200 || renamed.Name != "CheckOut" || renamed.Criticality != "critical" {
		t.Fatalf("rename self = %d %+v", code, renamed)
	}
	f.expect(http.MethodPut, "/api/services/SVC-404", app.ServiceInput{Name: "Nope", OwnerTeamID: "TEAM-1"}, http.StatusNotFound, "not_found")
	f.expect(http.MethodGet, "/api/services/SVC-404", nil, http.StatusNotFound, "not_found")
}

func TestServiceDependencies(t *testing.T) {
	f := newServiceFixture(t)
	defer f.cleanup()
	a := f.create(app.ServiceInput{Name: "A", OwnerTeamID: "TEAM-1"})
	b := f.create(app.ServiceInput{Name: "B", OwnerTeamID: "TEAM-1", DependsOn: []string{a.ID}})
	c := f.create(app.ServiceInput{Name: "C", OwnerTeamID: "TEAM-1", DependsOn: []string{b.ID, a.ID}})

	f.expect(http.MethodPut, "/api/services/"+a.ID, app.ServiceInput{Name: "A", OwnerTeamID: "TEAM-1", DependsOn: []string{a.ID}}, http.StatusBadRequest, "dependency_self")
	f.expect(http.MethodPut, "/api/services/"+a.ID, app.ServiceInput{Name: "A", OwnerTeamID: "TEAM-1", DependsOn: []string{c.ID}}, http.StatusBadRequest, "dependency_cycle")
	f.expect(http.MethodPut, "/api/services/"+a.ID, app.ServiceInput{Name: "A", OwnerTeamID: "TEAM-1", DependsOn: []string{b.ID}}, http.StatusBadRequest, "dependency_cycle")

	var got app.ServiceView
	f.admin.call(http.MethodGet, "/api/services/"+a.ID, nil, &got)
	if len(got.Dependents) != 2 || got.Dependents[0].Name != "B" || got.Dependents[1].Name != "C" {
		t.Fatalf("dependents of A = %+v", got.Dependents)
	}

	f.expect(http.MethodDelete, "/api/services/"+a.ID, nil, http.StatusNoContent, "")
	f.expect(http.MethodDelete, "/api/services/"+a.ID, nil, http.StatusNotFound, "not_found")
	f.h.st.Read(func(d *store.Data) {
		if len(d.Services[b.ID].DependsOn) != 0 || len(d.Services[c.ID].DependsOn) != 1 || d.Services[c.ID].DependsOn[0] != b.ID {
			t.Fatalf("dependencies after delete: B %v C %v", d.Services[b.ID].DependsOn, d.Services[c.ID].DependsOn)
		}
		if last := d.Audit[len(d.Audit)-1]; last.Action != "service.delete" || last.Object != a.ID {
			t.Fatalf("delete not audited: %+v", last)
		}
	})
}

func TestTeamServicesAndFilters(t *testing.T) {
	f := newServiceFixture(t)
	defer f.cleanup()
	owned := f.create(app.ServiceInput{Name: "Gateway", OwnerTeamID: "TEAM-1", Criticality: "critical", Tags: []string{"edge"}})
	deep := f.create(app.ServiceInput{Name: "Pager", OwnerTeamID: "TEAM-3", TeamIDs: []string{"TEAM-4"}, Status: "planned"})
	f.create(app.ServiceInput{Name: "Accounts", OwnerTeamID: "TEAM-3", Criticality: "low"})

	var direct app.ServicesOfTeam
	if code := f.admin.call(http.MethodGet, "/api/teams/TEAM-1/services", nil, &direct); code != 200 || len(direct.Services) != 1 || direct.Services[0].ID != owned.ID || direct.Team.Name != "Platform" {
		t.Fatalf("direct team services = %d %+v", code, direct)
	}
	var nested app.ServicesOfTeam
	if code := f.admin.call(http.MethodGet, "/api/teams/TEAM-1/services?descendants=true", nil, &nested); code != 200 || len(nested.Services) != 2 || nested.Services[0].ID != owned.ID || nested.Services[1].ID != deep.ID {
		t.Fatalf("team services with descendants = %d %+v", code, nested)
	}
	f.expect(http.MethodGet, "/api/teams/TEAM-404/services", nil, http.StatusNotFound, "not_found")

	for _, c := range []struct {
		query string
		want  int
	}{
		{"", 3},
		{"?q=gate", 1},
		{"?owner=TEAM-1", 1},
		{"?owner=TEAM-3", 2},
		{"?team=TEAM-2", 1},
		{"?team=TEAM-2&descendants=false", 0},
		{"?criticality=critical", 1},
		{"?status=planned", 1},
		{"?tag=edge", 1},
		{"?q=edge&criticality=low", 0},
	} {
		var list app.ServiceList
		if code := f.admin.call(http.MethodGet, "/api/services"+c.query, nil, &list); code != 200 || len(list.Services) != c.want || list.Total != 3 {
			t.Fatalf("list %q = %d %d services", c.query, code, len(list.Services))
		}
	}

	f.h.st.Write(func(d *store.Data) { delete(d.Teams, "TEAM-4") })
	var got app.ServiceView
	f.admin.call(http.MethodGet, "/api/services/"+deep.ID, nil, &got)
	if len(got.Teams) != 1 || !got.Teams[0].Deleted || got.Teams[0].ID != "TEAM-4" {
		t.Fatalf("dangling team = %+v", got.Teams)
	}
}

func TestServicePermissions(t *testing.T) {
	f := newServiceFixture(t)
	defer f.cleanup()
	svc := f.create(app.ServiceInput{Name: "Mail", OwnerTeamID: "TEAM-1"})
	f.h.st.Write(func(d *store.Data) {
		d.Roles["ROLE-T5-VIEW"] = &model.Role{ID: "ROLE-T5-VIEW", Name: "Viewers", Permissions: []string{"services:view"}}
	})
	defer f.h.st.Write(func(d *store.Data) { delete(d.Roles, "ROLE-T5-VIEW") })
	f.h.addLocal("viewer", "Viewer-pass-2026", "ROLE-T5-VIEW", time.Now())
	f.h.addLocal("nobody", "Nobody-pass-2026", model.RoleUser, time.Now())

	viewer := f.h.client()
	viewer.login("viewer", "Viewer-pass-2026")
	if code := viewer.call(http.MethodGet, "/api/services", nil, nil); code != http.StatusOK {
		t.Fatalf("viewer list = %d", code)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/api/services"},
		{http.MethodPut, "/api/services/" + svc.ID},
		{http.MethodDelete, "/api/services/" + svc.ID},
	} {
		if code := viewer.call(c.method, c.path, app.ServiceInput{Name: "X", OwnerTeamID: "TEAM-1"}, nil); code != http.StatusForbidden {
			t.Fatalf("viewer %s %s = %d", c.method, c.path, code)
		}
	}
	nobody := f.h.client()
	nobody.login("nobody", "Nobody-pass-2026")
	for _, path := range []string{"/api/services", "/api/services/" + svc.ID, "/api/teams/TEAM-1/services"} {
		if code := nobody.call(http.MethodGet, path, nil, nil); code != http.StatusForbidden {
			t.Fatalf("user without permission GET %s = %d", path, code)
		}
	}
}
