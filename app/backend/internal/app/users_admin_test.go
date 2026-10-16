package app_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/sbrw-evc/beacon/app/backend/internal/access"
	"github.com/sbrw-evc/beacon/app/backend/internal/keystone"
	"github.com/sbrw-evc/beacon/app/backend/internal/model"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
)

const adminPass = "Admin-pass-2026"

type managed struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	LastName    string   `json:"last_name"`
	Source      string   `json:"source"`
	Role        string   `json:"role"`
	RoleName    string   `json:"role_name"`
	TeamIDs     []string `json:"team_ids"`
	Teams       []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"teams"`
	Disabled    bool `json:"disabled"`
	Permissions []string
}

type problem struct {
	Error      string   `json:"error"`
	Violations []string `json:"violations"`
}

func (h *harness) addRole(id string, perms ...string) {
	h.st.Write(func(d *store.Data) {
		d.Roles[id] = &model.Role{ID: id, Name: id, Permissions: access.Normalize(perms)}
	})
}

func (h *harness) addTeam(id, parent string) {
	h.st.Write(func(d *store.Data) { d.Teams[id] = &model.Team{ID: id, Name: "Team " + id, ParentID: parent} })
}

func (h *harness) addLDAP(username, role string) string {
	var id string
	h.st.Write(func(d *store.Data) {
		id = d.NextID("USR")
		d.Users[id] = &model.User{ID: id, Username: username, Name: username, Source: model.SourceLDAP, Role: role, CreatedAt: time.Now()}
	})
	return id
}

func (h *harness) user(id string) *model.User {
	var out *model.User
	h.st.Read(func(d *store.Data) {
		if u := d.Users[id]; u != nil {
			c := *u
			out = &c
		}
	})
	return out
}

func (h *harness) audited(action, object string) bool {
	found := false
	h.st.Read(func(d *store.Data) {
		for _, a := range d.Audit {
			found = found || a.Action == action && a.Object == object
		}
	})
	return found
}

func expect(t *testing.T, what string, got, want int, body any) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %d, want %d (%+v)", what, got, want, body)
	}
}

func adminSession(t *testing.T) (*harness, *client, string) {
	h := newHarness(t)
	h.st.Write(func(d *store.Data) { d.EnsureSystemRoles(time.Now()) })
	id := h.addLocal("admin", adminPass, model.RoleAdmin, time.Now())
	c := h.client()
	c.login("admin", adminPass)
	return h, c, id
}

func TestUsersPermissions(t *testing.T) {
	h, admin, adminID := adminSession(t)
	h.addRole("viewers", "users:view")
	h.addLocal("viewer", "", "viewers", time.Now())
	h.addLocal("plain", "", model.RoleUser, time.Now())

	viewer := h.client()
	viewer.login("viewer", "")
	var list []managed
	expect(t, "viewer list", viewer.call(http.MethodGet, "/api/users", nil, &list), 200, list)
	if len(list) != 3 || list[0].Permissions != nil {
		t.Fatalf("list = %+v", list)
	}
	var p problem
	expect(t, "viewer edit", viewer.call(http.MethodPut, "/api/users/"+adminID, map[string]any{}, &p), 403, p)
	expect(t, "viewer delete", viewer.call(http.MethodDelete, "/api/users/"+adminID, nil, &p), 403, p)
	// People are created, locked and given passwords in Keystone: Beacon has no such routes.
	for _, path := range []string{"/api/users", "/api/users/" + adminID + "/lock", "/api/auth/login"} {
		if code := admin.call(http.MethodPost, path, map[string]any{"username": "x", "password": "Xx-pass-20261"}, &p); code != 404 && code != 405 {
			t.Fatalf("POST %s = %d, want no route", path, code)
		}
	}

	plain := h.client()
	plain.login("plain", "")
	expect(t, "plain list", plain.call(http.MethodGet, "/api/users", nil, &p), 403, p)
	expect(t, "plain get", plain.call(http.MethodGet, "/api/users/"+adminID, nil, &p), 403, p)

	var one managed
	expect(t, "admin get", admin.call(http.MethodGet, "/api/users/"+adminID, nil, &one), 200, one)
	if one.Role != model.RoleAdmin || one.RoleName != "Administrator" || one.Username != "admin" || one.Source != model.SourceSPA {
		t.Fatalf("get = %+v", one)
	}
	expect(t, "missing", admin.call(http.MethodGet, "/api/users/USR-404", nil, &p), 404, p)
}

func TestUsersLifecycle(t *testing.T) {
	h, admin, _ := adminSession(t)
	h.addTeam("TEAM-1", "")
	h.addTeam("TEAM-2", "TEAM-1")
	id := h.addLocal("ivan", "", model.RoleUser, time.Now())
	ivan := h.client()
	ivan.login("ivan", "")

	var list []managed
	for query, want := range map[string]int{"?q=iva": 1, "?role=admin": 1, "?source=ldap": 0, "?status=locked": 0, "?status=active": 2} {
		expect(t, "list "+query, admin.call(http.MethodGet, "/api/users"+query, nil, &list), 200, list)
		if len(list) != want {
			t.Fatalf("list %s = %d users, want %d", query, len(list), want)
		}
	}

	var p problem
	expect(t, "ghost team", admin.call(http.MethodPut, "/api/users/"+id, map[string]any{"team_ids": []string{"ghost"}}, &p), 400, p)
	var u managed
	expect(t, "edit", admin.call(http.MethodPut, "/api/users/"+id, map[string]any{"team_ids": []string{"TEAM-2"}}, &u), 200, u)
	if len(u.Teams) != 1 || u.Teams[0].Name != "Team TEAM-2" || u.Role != model.RoleUser || !h.audited("user.update", id) {
		t.Fatalf("edited = %+v", u)
	}
	expect(t, "team filter", admin.call(http.MethodGet, "/api/users?team=TEAM-1", nil, &list), 200, list)
	if len(list) != 1 {
		t.Fatalf("team list = %+v", list)
	}

	expect(t, "delete active", admin.call(http.MethodDelete, "/api/users/"+id, nil, &p), 409, p)
	if p.Error != "still_in_spa" {
		t.Fatalf("delete active = %+v", p)
	}

	// Keystone disables the person: the next synchronization locks the record and signs them out.
	h.addSPA(keystone.User{ID: spaID("ivan"), Username: "ivan", Name: "ivan", Disabled: true, Roles: []string{"user"}})
	expect(t, "sync", admin.call(http.MethodPost, "/api/settings/spa/sync", nil, nil), 200, nil)
	if !h.user(id).Disabled {
		t.Fatal("the person disabled in Keystone stays active")
	}
	expect(t, "disabled session", ivan.call(http.MethodGet, "/api/auth/me", nil, nil), 401, nil)
	expect(t, "locked filter", admin.call(http.MethodGet, "/api/users?status=locked", nil, &list), 200, list)
	if len(list) != 1 {
		t.Fatalf("locked list = %+v", list)
	}

	expect(t, "delete", admin.call(http.MethodDelete, "/api/users/"+id, nil, nil), 204, nil)
	if h.user(id) != nil || !h.audited("user.delete", id) {
		t.Fatal("user or audit entry left after delete")
	}
	expect(t, "deleted get", admin.call(http.MethodGet, "/api/users/"+id, nil, &p), 404, p)
	expect(t, "deleted again", admin.call(http.MethodDelete, "/api/users/"+id, nil, &p), 404, p)
}

func TestUsersSafetyRules(t *testing.T) {
	h, admin, adminID := adminSession(t)
	var p problem
	expect(t, "delete self", admin.call(http.MethodDelete, "/api/users/"+adminID, nil, &p), 409, p)
	var u managed
	expect(t, "own team", admin.call(http.MethodPut, "/api/users/"+adminID, map[string]any{"team_ids": []string{}}, &u), 200, u)

	h.addRole("managers", "users:view", "users:edit", "users:delete")
	h.addLocal("manager", "", "managers", time.Now())
	worker := h.addLocal("worker", "", model.RoleUser, time.Now())
	manager := h.client()
	manager.login("manager", "")
	expect(t, "manager edits admin", manager.call(http.MethodPut, "/api/users/"+adminID, map[string]any{"scope_mode": "all"}, &p), 403, p)
	if p.Error != "admin_only" {
		t.Fatalf("admin only = %+v", p)
	}
	expect(t, "manager deletes admin", manager.call(http.MethodDelete, "/api/users/"+adminID, nil, &p), 403, p)
	expect(t, "manager edits user", manager.call(http.MethodPut, "/api/users/"+worker, map[string]any{"scope_mode": "all"}, &u), 200, u)
}

func TestUsersDirectoryAccounts(t *testing.T) {
	h, admin, _ := adminSession(t)
	h.addTeam("TEAM-1", "")
	// An account of the versions before Keystone, never bound to Keystone: it is locked by the first
	// synchronization and can be removed.
	id := h.addLDAP("anna", model.RoleUser)
	var u managed
	expect(t, "ldap teams", admin.call(http.MethodPut, "/api/users/"+id, map[string]any{"team_ids": []string{"TEAM-1"}}, &u), 200, u)
	if len(u.TeamIDs) != 1 || u.Source != "ldap" {
		t.Fatalf("ldap edit = %+v", u)
	}
	expect(t, "sync", admin.call(http.MethodPost, "/api/settings/spa/sync", nil, nil), 200, nil)
	if !h.user(id).Disabled {
		t.Fatal("an account Keystone does not know stays active")
	}
	expect(t, "ldap delete", admin.call(http.MethodDelete, "/api/users/"+id, nil, nil), 204, nil)
	if h.user(id) != nil {
		t.Fatal("ldap user left after delete")
	}
}
