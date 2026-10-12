package app_test

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/access"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory/directorytest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets/secretstest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func TestPresetRolePermissionsAreInTheCatalog(t *testing.T) {
	want := []string{model.RoleViewer, model.RoleOnCall, model.RoleServiceOwner, model.RoleIntegrations, model.RoleNOCLead}
	var ids []string
	for _, r := range model.PresetRoles(model.LocaleEN, time.Now()) {
		ids = append(ids, r.ID)
		if r.Name == "" || r.Description == "" || len(r.Permissions) == 0 {
			t.Fatalf("preset %s is incomplete: %+v", r.ID, r)
		}
		for _, p := range r.Permissions {
			if !access.Valid(p) {
				t.Fatalf("preset %s has unknown permission %q", r.ID, p)
			}
		}
	}
	if !slices.Equal(ids, want) {
		t.Fatalf("preset ids = %v, want %v", ids, want)
	}
	for _, r := range model.PresetRoles(model.LocaleRU, time.Now()) {
		if r.Name == "" || r.Description == "" {
			t.Fatalf("preset %s has no Russian text", r.ID)
		}
	}
}

func TestPresetRolesCreatedOnceAndEditable(t *testing.T) {
	_, vault := secretstest.New(t)
	st := store.New()
	st.Write(func(d *store.Data) {
		d.Settings.DefaultLocale = model.LocaleRU
		d.Users["USR-1"] = &model.User{ID: "USR-1", Username: "old", Source: model.SourceLDAP, Role: model.RoleUser}
	})
	if err := app.Migrate(context.Background(), st, vault); err != nil {
		t.Fatal(err)
	}
	ids := []string{model.RoleViewer, model.RoleOnCall, model.RoleServiceOwner, model.RoleIntegrations, model.RoleNOCLead}
	st.Read(func(d *store.Data) {
		for _, id := range ids {
			r := d.Roles[id]
			if r == nil || r.System {
				t.Fatalf("preset %s = %+v", id, r)
			}
		}
		if d.Roles[model.RoleViewer].Name != "Наблюдатель" {
			t.Errorf("presets are named in the language of the installation: %q", d.Roles[model.RoleViewer].Name)
		}
		viewer := d.Roles[model.RoleViewer].Permissions
		for _, p := range []string{"incidents:view", "cmdb:view", "cis:view", "services:view", "maintenance:view", "wallboards:view"} {
			if !slices.Contains(viewer, p) {
				t.Errorf("viewer lacks %s: %v", p, viewer)
			}
		}
		if slices.Contains(viewer, "incidents:ack") {
			t.Error("the viewer changes nothing")
		}
		oncall := d.Roles[model.RoleOnCall].Permissions
		if !slices.Contains(oncall, "incidents:ack") || !slices.Contains(oncall, "maintenance:edit") {
			t.Errorf("on-call = %v", oncall)
		}
		integ := d.Roles[model.RoleIntegrations].Permissions
		for _, p := range []string{"connectors:view", "connectors:publish", "credentials:edit", "netbox:sync", "monitoring:link", "cis:edit"} {
			if !slices.Contains(integ, p) {
				t.Errorf("integrations lacks %s (view permissions of granted pages are added)", p)
			}
		}
		noc := d.Roles[model.RoleNOCLead].Permissions
		if !slices.Contains(noc, "users:lock") || !slices.Contains(noc, "wallboards:edit") || slices.Contains(noc, "users:delete") {
			t.Errorf("noc lead = %v", noc)
		}
		if d.NewUserRole() != model.RoleViewer {
			t.Errorf("new users get %q, want the viewer preset", d.NewUserRole())
		}
		if d.Users["USR-1"].Role != model.RoleUser {
			t.Error("existing users keep their role")
		}
	})

	// The presets are ordinary roles: changed or deleted, they are not created again.
	st.Write(func(d *store.Data) {
		d.Roles[model.RoleOnCall].Name = "Дежурные"
		d.Roles[model.RoleOnCall].Permissions = []string{"incidents:view"}
		delete(d.Roles, model.RoleNOCLead)
	})
	if err := app.Migrate(context.Background(), st, vault); err != nil {
		t.Fatal(err)
	}
	st.Read(func(d *store.Data) {
		if r := d.Roles[model.RoleOnCall]; r.Name != "Дежурные" || len(r.Permissions) != 1 {
			t.Errorf("an edited preset is kept as edited: %+v", r)
		}
		if d.Roles[model.RoleNOCLead] != nil {
			t.Error("a deleted preset must not come back")
		}
	})
}

func TestPresetRolesSkipTakenNames(t *testing.T) {
	st := store.New()
	st.Write(func(d *store.Data) {
		d.Roles["ROL-1"] = &model.Role{ID: "ROL-1", Name: "viewer"}
		if !d.EnsurePresetRoles(model.LocaleEN, time.Now()) {
			t.Fatal("first run seeds")
		}
		if d.Roles[model.RoleViewer] != nil {
			t.Error("a preset whose name is taken is skipped")
		}
		if d.NewUserRole() != model.RoleUser {
			t.Errorf("without the viewer preset new users get %q", d.NewUserRole())
		}
		if d.EnsurePresetRoles(model.LocaleEN, time.Now()) {
			t.Error("second run does nothing")
		}
	})
}

func TestNewUserRoleForDirectoryUsers(t *testing.T) {
	h := newHarness(t)
	srv := ldapServer(t)
	srv.Put(directorytest.Entry{DN: "uid=boris,dc=example,dc=org", Password: "boris-pass-1", Attrs: map[string][]string{
		"objectClass": {"inetOrgPerson"}, "uid": {"boris"}, "cn": {"Boris Petrov"}}})
	srv.Put(directorytest.Entry{DN: "uid=vera,dc=example,dc=org", Password: "vera-pass-1", Attrs: map[string][]string{
		"objectClass": {"inetOrgPerson"}, "uid": {"vera"}, "cn": {"Vera Orlova"}}})
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	h.st.Write(func(d *store.Data) {
		d.EnsureSystemRoles(time.Now())
		d.EnsurePresetRoles(model.LocaleEN, time.Now())
	})
	admin := h.client()
	admin.login("admin", "Admin-pass-2026")
	if code := admin.call(http.MethodPut, "/api/settings/ldap", map[string]any{"config": ldapConfig(srv.URL), "bind_password": ldapPass}, nil); code != 200 {
		t.Fatalf("ldap save = %d", code)
	}

	// The viewer preset is the default: a new directory user can see incidents at once.
	boris := h.client()
	boris.login("boris", "boris-pass-1")
	if u := h.named("boris"); u.Role != model.RoleViewer {
		t.Fatalf("boris = %q, want the viewer preset", u.Role)
	}
	_, me := boris.me()
	if me["admins"] != nil {
		t.Errorf("a user with permissions gets no access banner: %v", me["admins"])
	}

	var problem map[string]any
	if code := admin.call(http.MethodPut, "/api/roles/admin/new-users", nil, &problem); code != 400 || problem["error"] != "new_user_role_admin" {
		t.Fatalf("admin as the new user role = %d %v", code, problem)
	}
	var role map[string]any
	if code := admin.call(http.MethodPut, "/api/roles/"+model.RoleOnCall+"/new-users", nil, &role); code != 200 || role["new_users"] != true {
		t.Fatalf("set new user role = %d %v", code, role)
	}
	vera := h.client()
	vera.login("vera", "vera-pass-1")
	if u := h.named("vera"); u.Role != model.RoleOnCall {
		t.Fatalf("vera = %q, want the chosen role", u.Role)
	}
	if u := h.named("boris"); u.Role != model.RoleViewer {
		t.Fatalf("existing users are not changed: boris = %q", u.Role)
	}

	// Deleting the chosen role falls back to the role without permissions.
	if code := admin.call(http.MethodDelete, "/api/roles/"+model.RoleOnCall+"?reassign_to="+model.RoleViewer, nil, nil); code != 204 {
		t.Fatalf("delete = %d", code)
	}
	var refs app.Refs
	admin.call(http.MethodGet, "/api/refs", nil, &refs)
	if refs.NewUserRole != model.RoleUser {
		t.Fatalf("new user role after delete = %q", refs.NewUserRole)
	}
}

func TestNoAccessBannerNamesAdministrators(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	h.st.Write(func(d *store.Data) { d.EnsureSystemRoles(now) })
	adminID := h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, now)
	h.addLocal("root2", "Admin-pass-2026", model.RoleAdmin, now)
	h.addLocal("nobody", "Nobody-pass-2026", model.RoleUser, now)
	h.st.Write(func(d *store.Data) {
		d.Users[adminID].Profile = model.Profile{LastName: "Petrova", FirstName: "Olga", Email: "olga@example.com"}
		d.UserByName("root2").Disabled = true
	})
	c := h.client()
	c.login("nobody", "Nobody-pass-2026")
	_, me := c.me()
	admins, _ := me["admins"].([]any)
	if len(admins) != 1 {
		t.Fatalf("admins = %v (locked administrators are not listed)", me["admins"])
	}
	if a := admins[0].(map[string]any); a["name"] != "Petrova Olga" || a["email"] != "olga@example.com" {
		t.Fatalf("admin = %v", a)
	}
	a := h.client()
	a.login("admin", "Admin-pass-2026")
	if _, me := a.me(); me["admins"] != nil {
		t.Fatal("administrators get no banner")
	}
}

// Accounts for NetBox contacts: off for new settings (contacts link to existing accounts by
// e-mail only), on for settings saved before the switch existed; created accounts get the role
// for new users.
func TestNetBoxContactAccountsSwitch(t *testing.T) {
	f := newNetBoxFixture(t)
	f.h.st.Write(func(d *store.Data) { d.EnsurePresetRoles(model.LocaleEN, time.Now()) })
	f.connect(map[string]any{"create_users": false})
	view := f.sync()
	if s := view.Sync.Stats; s.UsersCreated != 0 || s.UsersSkipped != 1 || s.UsersLinked != 1 || view.Users != 0 {
		t.Fatalf("switch off = %+v (users %d)", s, view.Users)
	}
	if len(f.byName("srv-db-01").Owners) != 1 {
		t.Fatal("the contact with an account is still a responsible person")
	}
	f.connect(map[string]any{"create_users": true})
	if s := f.sync().Sync.Stats; s.UsersCreated != 1 {
		t.Fatalf("switch on = %+v", s)
	}
	if u := f.h.named("ivan@example.org"); u.Source != model.SourceNetBox || u.Role != model.RoleViewer {
		t.Fatalf("contact account = %+v", u)
	}
}

func TestNetBoxContactAccountsKeptForExistingSettings(t *testing.T) {
	_, vault := secretstest.New(t)
	st := store.New()
	st.Write(func(d *store.Data) {
		d.Settings.NetBox.URL, d.Settings.NetBox.SyncContacts = "https://netbox.example.org", true
	})
	if err := app.Migrate(context.Background(), st, vault); err != nil {
		t.Fatal(err)
	}
	st.Read(func(d *store.Data) {
		if !d.Settings.NetBox.CreateUsers {
			t.Fatal("an existing NetBox connection keeps creating accounts")
		}
	})
	st.Write(func(d *store.Data) { d.Settings.NetBox.CreateUsers = false })
	if err := app.Migrate(context.Background(), st, vault); err != nil {
		t.Fatal(err)
	}
	st.Read(func(d *store.Data) {
		if d.Settings.NetBox.CreateUsers {
			t.Fatal("a choice made later is not overridden")
		}
	})
	fresh := store.New()
	if err := app.Migrate(context.Background(), fresh, vault); err != nil {
		t.Fatal(err)
	}
	fresh.Read(func(d *store.Data) {
		if d.Settings.NetBox.CreateUsers || !d.Settings.NetBoxUsersDecided {
			t.Fatal("a new installation starts with the switch off")
		}
	})
}

// Every matching row of the group table adds its team; teams set by hand stay.
func TestGroupMappingGivesSeveralTeams(t *testing.T) {
	h := newHarness(t)
	srv := ldapServer(t)
	srv.Put(directorytest.Entry{DN: "ou=groups,dc=example,dc=org"})
	srv.Put(directorytest.Entry{DN: ldapOps, Attrs: map[string][]string{"member": {"uid=anna,dc=example,dc=org"}}})
	srv.Put(directorytest.Entry{DN: ldapEng, Attrs: map[string][]string{"member": {"uid=anna,dc=example,dc=org"}}})
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	h.addRoleAndTeam("operator", "TEAM-1")
	h.addRoleAndTeam("viewer", "TEAM-2")
	h.addRoleAndTeam("other", "TEAM-3")
	admin := h.client()
	admin.login("admin", "Admin-pass-2026")
	cfg := ldapConfig(srv.URL)
	delete(cfg, "admin_group_dn")
	admin.call(http.MethodPut, "/api/settings/ldap", map[string]any{"config": cfg, "bind_password": ldapPass}, nil)
	rows := []any{
		map[string]any{"source": "ldap", "group": ldapOps, "role_id": "operator", "team_id": "TEAM-1"},
		map[string]any{"source": "ldap", "group": ldapEng, "team_id": "TEAM-2"},
	}
	if code := admin.call(http.MethodPut, "/api/settings/groups", map[string]any{"mappings": rows}, nil); code != 200 {
		t.Fatalf("save = %d", code)
	}
	h.client().login("anna", "anna-pass-1")
	if u := h.named("anna"); teamsOf(u) != "TEAM-1,TEAM-2" || u.Role != "operator" {
		t.Fatalf("anna = %+v", u)
	}
	id := h.named("anna").ID
	if code := admin.call(http.MethodPut, "/api/users/"+id, map[string]any{"team_ids": []string{"TEAM-1", "TEAM-2", "TEAM-3"}}, nil); code != 200 {
		t.Fatalf("hand-made team = %d", code)
	}
	// anna leaves eng: TEAM-2 is withdrawn, the hand-made TEAM-3 stays.
	srv.Put(directorytest.Entry{DN: ldapEng, Attrs: map[string][]string{"member": {}}})
	var view groupsReply
	admin.call(http.MethodPost, "/api/settings/groups/sync", nil, &view)
	if u := h.named("anna"); teamsOf(u) != "TEAM-1,TEAM-3" {
		t.Fatalf("anna after leaving eng = %v", u.TeamIDs)
	}
	if view.Mapped.Teams != 1 {
		t.Fatalf("mapped = %+v", view.Mapped)
	}
}
