package app_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory/directorytest"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// A wallboard for a supporting team shows the incidents of the services the team supports.
func TestWallboardTeamFilterIncludesSupportingTeams(t *testing.T) {
	f := newConnFixture(t, true)
	seedCatalog(f.h.st)
	f.h.st.Write(func(d *store.Data) {
		d.Teams["T-2"] = &model.Team{ID: "T-2", Name: "Support"}
		d.Teams["T-3"] = &model.Team{ID: "T-3", Name: "Unrelated"}
		d.Services["S-1"].TeamIDs = []string{"T-2"}
	})
	hook := f.webhookConnector()
	f.ingest("hook", `{"id":"a1","title":"Disk full","host":"db-01","signal":"disk","severity":"critical","status":"firing"}`, hook)
	f.expect(f.admin, http.MethodPost, "/api/wallboards", wallboardBody(map[string]any{"slug": "support", "team_ids": []string{"T-2"}}), http.StatusCreated, nil)
	f.expect(f.admin, http.MethodPost, "/api/wallboards", wallboardBody(map[string]any{"slug": "other", "team_ids": []string{"T-3"}}), http.StatusCreated, nil)
	var payload struct {
		Ready     bool `json:"ready"`
		Incidents []map[string]any
	}
	waitFor(t, "the supporting team's board", func() bool {
		r := fetch(t, f.h, "/api/public/tv/support", nil)
		_ = json.Unmarshal([]byte(r.body), &payload)
		return payload.Ready && len(payload.Incidents) == 1
	})
	if payload.Incidents[0]["team"] != "DBA" {
		t.Fatalf("routing stays with the owner team: %v", payload.Incidents[0])
	}
	r := fetch(t, f.h, "/api/public/tv/other", nil)
	_ = json.Unmarshal([]byte(r.body), &payload)
	if !payload.Ready || len(payload.Incidents) != 0 {
		t.Fatalf("an unrelated team sees nothing: %s", r.body)
	}
}

func TestTeamChannelSettings(t *testing.T) {
	f := newConnFixture(t, false)
	seedCatalog(f.h.st)
	var team map[string]any
	f.expect(f.admin, http.MethodPut, "/api/teams/T-1", map[string]any{"email": "not-mail"}, http.StatusBadRequest, nil)
	f.expect(f.admin, http.MethodPut, "/api/teams/T-1", map[string]any{"telegram": "chat?"}, http.StatusBadRequest, nil)
	f.expect(f.admin, http.MethodPut, "/api/teams/T-1", map[string]any{"email": " dba@example.com ", "telegram": "-100123"}, http.StatusOK, &team)
	if team["email"] != "dba@example.com" || team["telegram"] != "-100123" {
		t.Fatalf("team = %v", team)
	}
}

// scopeCatalog: Billing (S-1, owner DBA T-1, supported by Support T-2) has db-01; Shop (S-2,
// owner Web T-3) has web-01; shared-01 is in both.
func scopeCatalog(st *store.Store) {
	st.Write(func(d *store.Data) {
		d.EnsureSystemRoles(time.Now())
		d.Teams["T-1"] = &model.Team{ID: "T-1", Name: "DBA"}
		d.Teams["T-2"] = &model.Team{ID: "T-2", Name: "Support"}
		d.Teams["T-3"] = &model.Team{ID: "T-3", Name: "Web"}
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "db-01", Status: model.CIStatusActive}
		d.ConfigItems["CI-2"] = &model.ConfigItem{ID: "CI-2", Name: "web-01", Status: model.CIStatusActive}
		d.ConfigItems["CI-3"] = &model.ConfigItem{ID: "CI-3", Name: "shared-01", Status: model.CIStatusActive}
		d.Services["S-1"] = &model.Service{ID: "S-1", Name: "Billing", OwnerTeamID: "T-1", TeamIDs: []string{"T-2"}, Status: model.ServiceActive, CIIDs: []string{"CI-1", "CI-3"}}
		d.Services["S-2"] = &model.Service{ID: "S-2", Name: "Shop", OwnerTeamID: "T-3", Status: model.ServiceActive, CIIDs: []string{"CI-2", "CI-3"}}
	})
}

// «Services of my teams»: a member of a supporting team sees the incidents of the services the
// team supports; a limited user cannot silence or show anything outside the scope.
func TestScopeModesAndEnforcement(t *testing.T) {
	f := newConnFixture(t, true)
	scopeCatalog(f.h.st)
	f.h.addRole("support", "incidents:view", "maintenance:edit", "wallboards:edit")
	id := f.h.addLocal("sup", "Support-pass-2026", "support", time.Now())
	f.expect(f.admin, http.MethodPut, "/api/users/"+id, map[string]any{"scope_mode": "bogus"}, http.StatusBadRequest, nil)
	f.expect(f.admin, http.MethodPut, "/api/users/"+id, map[string]any{"scope_mode": "services"}, http.StatusBadRequest, nil)
	f.expect(f.admin, http.MethodPut, "/api/users/"+id, map[string]any{"team_ids": []string{"T-2"}, "scope_mode": "teams"}, http.StatusOK, nil)

	hook := f.webhookConnector()
	f.ingest("hook", `[{"id":"a1","title":"Disk","host":"db-01","signal":"disk","severity":"critical","status":"firing"},
		{"id":"a2","title":"Down","host":"web-01","signal":"http","severity":"error","status":"firing"}]`, hook)
	var page alert.Page
	waitFor(t, "two incidents", func() bool {
		f.admin.call(http.MethodGet, "/api/incidents?status=active", nil, &page)
		return len(page.Alerts) == 2
	})
	sup := f.h.client()
	sup.login("sup", "Support-pass-2026")
	f.expect(sup, http.MethodGet, "/api/incidents?status=active", nil, http.StatusOK, &page)
	if len(page.Alerts) != 1 || page.Alerts[0].CIID != "CI-1" || page.Alerts[0].Route.Team.ID != "T-1" {
		t.Fatalf("supporting team member sees %+v", page.Alerts)
	}

	// Maintenance windows: only targets within the scope; a shared item would silence Shop too.
	window := func(over map[string]any) map[string]any {
		b := map[string]any{"title": "Work", "start": time.Now().Add(time.Hour), "end": time.Now().Add(2 * time.Hour)}
		for k, v := range over {
			b[k] = v
		}
		return b
	}
	f.expect(sup, http.MethodPost, "/api/maintenance", window(map[string]any{"service_ids": []string{"S-2"}}), http.StatusForbidden, nil)
	f.expect(sup, http.MethodPost, "/api/maintenance", window(map[string]any{"ci_ids": []string{"CI-3"}}), http.StatusForbidden, nil)
	var mine app.MaintenanceView
	f.expect(sup, http.MethodPost, "/api/maintenance", window(map[string]any{"service_ids": []string{"S-1"}, "ci_ids": []string{"CI-1"}}), http.StatusCreated, &mine)
	var other app.MaintenanceView
	f.expect(f.admin, http.MethodPost, "/api/maintenance", window(map[string]any{"service_ids": []string{"S-2"}}), http.StatusCreated, &other)
	f.expect(sup, http.MethodDelete, "/api/maintenance/"+other.ID, nil, http.StatusForbidden, nil)
	f.expect(sup, http.MethodPut, "/api/maintenance/"+other.ID, window(map[string]any{"service_ids": []string{"S-1"}}), http.StatusForbidden, nil)
	var list []app.MaintenanceView
	f.expect(sup, http.MethodGet, "/api/maintenance", nil, http.StatusOK, &list)
	for _, m := range list {
		if m.InScope != (m.ID == mine.ID) {
			t.Errorf("in_scope of %s = %v", m.ID, m.InScope)
		}
	}
	var targets map[string][]app.TargetRef
	f.expect(sup, http.MethodGet, "/api/maintenance/targets", nil, http.StatusOK, &targets)
	if len(targets["services"]) != 1 || targets["services"][0].ID != "S-1" || len(targets["cis"]) != 1 || targets["cis"][0].ID != "CI-1" {
		t.Fatalf("targets = %+v", targets)
	}
	f.expect(sup, http.MethodDelete, "/api/maintenance/"+mine.ID, nil, http.StatusNoContent, nil)

	// Wallboards: a limited user cannot make a board of every incident.
	f.expect(sup, http.MethodPost, "/api/wallboards", wallboardBody(nil), http.StatusBadRequest, nil)
	f.expect(sup, http.MethodPost, "/api/wallboards", wallboardBody(map[string]any{"team_ids": []string{"T-3"}}), http.StatusForbidden, nil)
	f.expect(sup, http.MethodPost, "/api/wallboards", wallboardBody(map[string]any{"service_ids": []string{"S-2"}}), http.StatusForbidden, nil)
	f.expect(sup, http.MethodPost, "/api/wallboards", wallboardBody(map[string]any{"team_ids": []string{"T-2"}}), http.StatusCreated, nil)
	var all app.WallboardView
	f.expect(f.admin, http.MethodPost, "/api/wallboards", wallboardBody(map[string]any{"slug": "all"}), http.StatusCreated, &all)
	f.expect(sup, http.MethodDelete, "/api/wallboards/"+all.ID, nil, http.StatusForbidden, nil)
	f.expect(sup, http.MethodPut, "/api/wallboards/"+all.ID, wallboardBody(map[string]any{"slug": "all", "service_ids": []string{"S-1"}}), http.StatusForbidden, nil)

	// A teams scope without teams shows nothing; «all» lifts the limit.
	f.expect(f.admin, http.MethodPut, "/api/users/"+id, map[string]any{"team_ids": []string{}}, http.StatusOK, nil)
	f.expect(sup, http.MethodGet, "/api/incidents?status=active", nil, http.StatusOK, &page)
	if len(page.Alerts) != 0 || page.Counts.Active != 0 {
		t.Fatalf("no teams = %+v", page)
	}
	f.expect(f.admin, http.MethodPut, "/api/users/"+id, map[string]any{"scope_mode": "all"}, http.StatusOK, nil)
	f.expect(sup, http.MethodGet, "/api/incidents?status=active", nil, http.StatusOK, &page)
	if len(page.Alerts) != 2 {
		t.Fatalf("all = %d", len(page.Alerts))
	}
	f.expect(sup, http.MethodDelete, "/api/wallboards/"+all.ID, nil, http.StatusNoContent, nil)
}

// The group table can set the scope mode; it is withdrawn when the group no longer gives it.
func TestGroupMappingSetsScope(t *testing.T) {
	h := newHarness(t)
	srv := ldapServer(t)
	srv.Put(directorytest.Entry{DN: "ou=groups,dc=example,dc=org"})
	srv.Put(directorytest.Entry{DN: ldapOps, Attrs: map[string][]string{"member": {"uid=anna,dc=example,dc=org"}}})
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	h.addRoleAndTeam("owner", "TEAM-1")
	admin := h.client()
	admin.login("admin", "Admin-pass-2026")
	cfg := ldapConfig(srv.URL)
	delete(cfg, "admin_group_dn")
	admin.call(http.MethodPut, "/api/settings/ldap", map[string]any{"config": cfg, "bind_password": ldapPass}, nil)
	var problem map[string]any
	bad := []any{map[string]any{"source": "ldap", "group": ldapOps, "scope": "services"}}
	if code := admin.call(http.MethodPut, "/api/settings/groups", map[string]any{"mappings": bad}, &problem); code != 400 {
		t.Fatalf("services scope by group = %d", code)
	}
	rows := []any{map[string]any{"source": "ldap", "group": ldapOps, "role_id": "owner", "team_id": "TEAM-1", "scope": "teams"}}
	if code := admin.call(http.MethodPut, "/api/settings/groups", map[string]any{"mappings": rows}, nil); code != 200 {
		t.Fatalf("save = %d", code)
	}
	h.client().login("anna", "anna-pass-1")
	if u := h.named("anna"); u.ScopeMode != model.ScopeTeams || u.MappedScope != model.ScopeTeams || !slices.Equal(u.TeamIDs, []string{"TEAM-1"}) {
		t.Fatalf("anna = %+v", u)
	}
	srv.Put(directorytest.Entry{DN: ldapOps, Attrs: map[string][]string{"member": {}}})
	admin.call(http.MethodPost, "/api/settings/groups/sync", nil, nil)
	if u := h.named("anna"); u.ScopeMode != model.ScopeAll || u.MappedScope != "" {
		t.Fatalf("anna out of the group = %+v", u)
	}
}

// Users of older versions keep their service list as the «chosen services» mode.
func TestScopeModeMigration(t *testing.T) {
	re := restarted(t, func(d *store.Data) {
		d.Users["U-1"] = &model.User{ID: "U-1", Username: "a", ServiceIDs: []string{"S-1"}}
		d.Users["U-2"] = &model.User{ID: "U-2", Username: "b"}
	})
	re.Read(func(d *store.Data) {
		if d.Users["U-1"].ScopeMode != model.ScopeServices || d.Users["U-2"].ScopeMode != model.ScopeAll {
			t.Fatalf("modes = %q %q", d.Users["U-1"].ScopeMode, d.Users["U-2"].ScopeMode)
		}
	})
}
