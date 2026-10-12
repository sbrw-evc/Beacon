package app_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory/directorytest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/entra/entratest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	ldapOps = "cn=ops,ou=groups,dc=example,dc=org"
	ldapEng = "cn=eng,ou=groups,dc=example,dc=org"
)

type groupsReply struct {
	Config struct {
		Mappings    []model.GroupMapping `json:"mappings"`
		SyncOff     bool                 `json:"sync_off"`
		SyncMinutes int                  `json:"sync_minutes"`
	} `json:"config"`
	Sync   model.GroupSyncState `json:"sync"`
	Mapped struct {
		Roles int `json:"roles"`
		Teams int `json:"teams"`
	} `json:"mapped"`
}

func (h *harness) addRoleAndTeam(roleID, teamID string) {
	now := time.Now()
	h.st.Write(func(d *store.Data) {
		d.EnsureSystemRoles(now)
		d.Roles[roleID] = &model.Role{ID: roleID, Name: roleID, Permissions: []string{"status:view"}, CreatedAt: now, UpdatedAt: now}
		d.Teams[teamID] = &model.Team{ID: teamID, Name: teamID, CreatedAt: now, UpdatedAt: now}
	})
}

func teamsOf(u model.User) string { return strings.Join(u.TeamIDs, ",") }

func (h *harness) named(name string) model.User {
	var out model.User
	h.st.Read(func(d *store.Data) {
		if u := d.UserByName(name); u != nil {
			out = *u
		}
	})
	return out
}

func TestGroupMappingLDAP(t *testing.T) {
	h := newHarness(t)
	srv := ldapServer(t)
	// anna is in ops directly and in eng through ops; boris is in no group.
	srv.Put(directorytest.Entry{DN: "ou=groups,dc=example,dc=org"})
	srv.Put(directorytest.Entry{DN: ldapOps, Attrs: map[string][]string{"member": {"uid=anna,dc=example,dc=org"}}})
	srv.Put(directorytest.Entry{DN: ldapEng, Attrs: map[string][]string{"member": {ldapOps}}})
	srv.Put(directorytest.Entry{DN: "uid=boris,dc=example,dc=org", Password: "boris-pass-1", Attrs: map[string][]string{
		"objectClass": {"inetOrgPerson"}, "uid": {"boris"}, "cn": {"Boris Petrov"}}})
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	h.addRoleAndTeam("operator", "TEAM-1")
	h.addRoleAndTeam("viewer", "TEAM-2")
	admin := h.client()
	admin.login("admin", "Admin-pass-2026")
	cfg := ldapConfig(srv.URL)
	delete(cfg, "admin_group_dn")
	if code := admin.call(http.MethodPut, "/api/settings/ldap", map[string]any{"config": cfg, "bind_password": ldapPass}, nil); code != 200 {
		t.Fatalf("ldap save = %d", code)
	}

	var view groupsReply
	if code := admin.call(http.MethodGet, "/api/settings/groups", nil, &view); code != 200 || len(view.Config.Mappings) != 0 || view.Config.SyncMinutes != 60 {
		t.Fatalf("initial = %d %+v", code, view)
	}
	var problem map[string]any
	for _, bad := range []map[string]any{
		{"source": "ldap", "group": "not a dn", "role_id": "operator"},
		{"source": "entra", "group": ldapOps, "role_id": "operator"},
		{"source": "ldap", "group": ldapOps},
		{"source": "ldap", "group": ldapOps, "role_id": "nope"},
		{"source": "ldap", "group": ldapOps, "team_id": "TEAM-9"},
	} {
		if code := admin.call(http.MethodPut, "/api/settings/groups", map[string]any{"mappings": []any{bad}}, &problem); code != 400 || problem["error"] != "group_mapping_invalid" {
			t.Fatalf("bad row %v = %d %v", bad, code, problem)
		}
	}
	dup := map[string]any{"mappings": []any{
		map[string]any{"source": "ldap", "group": ldapOps, "role_id": "operator"},
		map[string]any{"source": "ldap", "group": "CN=Ops, OU=Groups, DC=Example, DC=Org", "team_id": "TEAM-1"},
	}}
	if code := admin.call(http.MethodPut, "/api/settings/groups", dup, &problem); code != 400 {
		t.Fatalf("the same group spelled differently = %d %v", code, problem)
	}

	// The nested group gives the team; the role comes from the first matching row.
	rows := []any{
		map[string]any{"source": "ldap", "group": "CN=Eng,OU=Groups,DC=example,DC=org", "label": "Engineering", "role_id": "operator", "team_id": "TEAM-1"},
		map[string]any{"source": "ldap", "group": ldapOps, "role_id": "viewer"},
	}
	if code := admin.call(http.MethodPut, "/api/settings/groups", map[string]any{"mappings": rows, "sync_minutes": 30}, &view); code != 200 ||
		len(view.Config.Mappings) != 2 || view.Config.Mappings[0].Group != ldapEng || view.Config.Mappings[0].ID == "" || view.Config.SyncMinutes != 30 {
		t.Fatalf("save = %d %+v", code, view)
	}

	anna := h.client()
	anna.login("anna", "anna-pass-1")
	if u := h.named("anna"); u.Role != "operator" || teamsOf(u) != "TEAM-1" || u.MappedRole != "operator" || strings.Join(u.MappedTeams, ",") != "TEAM-1" {
		t.Fatalf("anna after sign-in = %+v", u)
	}
	boris := h.client()
	boris.login("boris", "boris-pass-1")
	if u := h.named("boris"); u.Role != model.RoleUser || teamsOf(u) != "" {
		t.Fatalf("boris = %+v", u)
	}

	// A hand-made change to a mapped value is overridden by the next synchronization...
	annaID, borisID := h.named("anna").ID, h.named("boris").ID
	if code := admin.call(http.MethodPut, "/api/users/"+annaID, map[string]any{"role_id": "viewer", "team_ids": []string{"TEAM-2"}}, nil); code != 200 {
		t.Fatalf("manual edit = %d", code)
	}
	// ...while values set by hand for users no row matches are kept.
	if code := admin.call(http.MethodPut, "/api/users/"+borisID, map[string]any{"role_id": "viewer", "team_ids": []string{"TEAM-2"}}, nil); code != 200 {
		t.Fatalf("manual edit = %d", code)
	}
	if code := admin.call(http.MethodPost, "/api/settings/groups/sync", nil, &view); code != 200 || !view.Sync.OK || view.Sync.LDAP.Users != 2 || view.Sync.LDAP.Changed != 1 {
		t.Fatalf("sync = %d %+v", code, view.Sync)
	}
	// The mapped team is added again next to the team set by hand.
	if u := h.named("anna"); u.Role != "operator" || teamsOf(u) != "TEAM-1,TEAM-2" {
		t.Fatalf("anna after sync = %+v", u)
	}
	if u := h.named("boris"); u.Role != "viewer" || teamsOf(u) != "TEAM-2" {
		t.Fatalf("boris after sync = %+v", u)
	}
	if view.Mapped.Roles != 1 || view.Mapped.Teams != 1 {
		t.Fatalf("mapped = %+v", view.Mapped)
	}

	// Leaving the groups withdraws what the table gave.
	srv.Put(directorytest.Entry{DN: ldapOps, Attrs: map[string][]string{"member": {"uid=boris,dc=example,dc=org"}}})
	admin.call(http.MethodPost, "/api/settings/groups/sync", nil, &view)
	if u := h.named("anna"); u.Role != model.RoleUser || teamsOf(u) != "TEAM-2" || u.MappedRole != "" || len(u.MappedTeams) != 0 {
		t.Fatalf("anna out of the groups = %+v", u)
	}
	// boris is now in ops and eng: the first row wins for the role, over his hand-set value.
	if u := h.named("boris"); u.Role != "operator" || teamsOf(u) != "TEAM-1,TEAM-2" {
		t.Fatalf("boris in the groups = %+v", u)
	}

	// Deleting a team removes it from the table; the row keeps its role.
	if code := admin.call(http.MethodDelete, "/api/teams/TEAM-1", nil, nil); code != 204 && code != 200 {
		t.Fatalf("team delete = %d", code)
	}
	admin.call(http.MethodGet, "/api/settings/groups", nil, &view)
	if m := view.Config.Mappings[0]; m.TeamID != "" || m.RoleID != "operator" {
		t.Fatalf("rows after team delete = %+v", view.Config.Mappings)
	}

	var audit []store.AuditEntry
	h.st.Read(func(d *store.Data) { audit = append(audit, d.Audit...) })
	found := false
	for _, a := range audit {
		if a.Action == "groups.sync" && strings.Contains(a.Detail, "anna role operator → user") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no audit entry for the withdrawn role: %+v", audit)
	}
}

func TestGroupMappingEntra(t *testing.T) {
	h := newHarness(t)
	idp := entratest.Start(t)
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	h.addRoleAndTeam("operator", "TEAM-1")
	admin := h.client()
	admin.login("admin", "Admin-pass-2026")
	cfg := entraConfig(h)
	delete(cfg, "user_group_id")
	if code := admin.call(http.MethodPut, "/api/settings/entra", map[string]any{"config": cfg, "client_secret": entratest.Secret}, nil); code != 200 {
		t.Fatalf("entra save = %d", code)
	}
	rows := []any{map[string]any{"source": "entra", "group": strings.ToUpper(entraOther), "role_id": "operator", "team_id": "TEAM-1"}}
	if code := admin.call(http.MethodPut, "/api/settings/groups", map[string]any{"mappings": rows}, nil); code != 200 {
		t.Fatalf("save = %d", code)
	}

	idp.User = entratest.User{ObjectID: "0b1d0000-0000-0000-0000-000000000001", Username: "anna@contoso.com", Name: "Anna Ivanova",
		Groups: []string{entraUsers, entraOther}}
	if where := h.client().signInEntra(); where != "/" {
		t.Fatalf("sign-in ended at %s", where)
	}
	if u := h.named("anna@contoso.com"); u.Role != "operator" || teamsOf(u) != "TEAM-1" {
		t.Fatalf("anna after sign-in = %+v", u)
	}

	// The synchronization reads the groups with the application token.
	idp.User = entratest.User{}
	idp.Others = []entratest.User{{ObjectID: "0b1d0000-0000-0000-0000-000000000001", Groups: []string{entraUsers}}}
	var view groupsReply
	if code := admin.call(http.MethodPost, "/api/settings/groups/sync", nil, &view); code != 200 || !view.Sync.OK || view.Sync.Entra.Changed != 1 || view.Sync.LDAP.Checked {
		t.Fatalf("sync = %d %+v", code, view.Sync)
	}
	if u := h.named("anna@contoso.com"); u.Role != model.RoleUser || teamsOf(u) != "" {
		t.Fatalf("anna after sync = %+v", u)
	}

	// Without the Graph permission the failure is reported and nothing changes.
	idp.DenyApp = true
	admin.call(http.MethodPost, "/api/settings/groups/sync", nil, &view)
	if view.Sync.OK || !strings.Contains(view.Sync.Entra.Error, "GroupMember.Read.All") {
		t.Fatalf("denied sync = %+v", view.Sync)
	}
	// A deleted user is counted as missing and left alone.
	idp.DenyApp, idp.Others = false, nil
	admin.call(http.MethodPost, "/api/settings/groups/sync", nil, &view)
	if !view.Sync.OK || view.Sync.Entra.Missing != 1 {
		t.Fatalf("sync of a deleted user = %+v", view.Sync)
	}
}
