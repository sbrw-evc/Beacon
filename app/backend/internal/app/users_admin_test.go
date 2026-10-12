package app_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/access"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
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
	Disabled           bool `json:"disabled"`
	MustChangePassword bool `json:"must_change_password"`
	PasswordExpired    bool `json:"password_expired"`
	Permissions        []string
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
	h.addLocal("viewer", "Viewer-pass-2026", "viewers", time.Now())
	h.addLocal("plain", "Plain-pass-2026", model.RoleUser, time.Now())

	viewer := h.client()
	viewer.login("viewer", "Viewer-pass-2026")
	var list []managed
	expect(t, "viewer list", viewer.call(http.MethodGet, "/api/users", nil, &list), 200, list)
	if len(list) != 3 || list[0].Permissions != nil {
		t.Fatalf("list = %+v", list)
	}
	var p problem
	expect(t, "viewer create", viewer.call(http.MethodPost, "/api/users", map[string]any{"username": "x", "password": "Xx-pass-20261"}, &p), 403, p)
	expect(t, "viewer lock", viewer.call(http.MethodPost, "/api/users/"+adminID+"/lock", nil, &p), 403, p)
	expect(t, "viewer edit", viewer.call(http.MethodPut, "/api/users/"+adminID, map[string]any{}, &p), 403, p)
	expect(t, "viewer password", viewer.call(http.MethodPut, "/api/users/"+adminID+"/password", map[string]any{"password": "x"}, &p), 403, p)
	expect(t, "viewer delete", viewer.call(http.MethodDelete, "/api/users/"+adminID, nil, &p), 403, p)

	plain := h.client()
	plain.login("plain", "Plain-pass-2026")
	expect(t, "plain list", plain.call(http.MethodGet, "/api/users", nil, &p), 403, p)
	expect(t, "plain get", plain.call(http.MethodGet, "/api/users/"+adminID, nil, &p), 403, p)

	var one managed
	expect(t, "admin get", admin.call(http.MethodGet, "/api/users/"+adminID, nil, &one), 200, one)
	if one.Role != model.RoleAdmin || one.RoleName != "Administrator" || one.Username != "admin" {
		t.Fatalf("get = %+v", one)
	}
	expect(t, "missing", admin.call(http.MethodGet, "/api/users/USR-404", nil, &p), 404, p)
}

func TestUsersLifecycle(t *testing.T) {
	h, admin, _ := adminSession(t)
	h.addTeam("TEAM-1", "")
	h.addTeam("TEAM-2", "TEAM-1")

	var p problem
	create := func(body map[string]any, want int, out any) {
		t.Helper()
		expect(t, "create "+strings.TrimSpace(body["username"].(string)), admin.call(http.MethodPost, "/api/users", body, out), want, out)
	}
	create(map[string]any{"username": "has space", "password": "Valid-pass-2026"}, 400, &p)
	if p.Error != "invalid_username" {
		t.Fatalf("username problem = %+v", p)
	}
	create(map[string]any{"username": strings.Repeat("a", 65), "password": "Valid-pass-2026"}, 400, &p)
	create(map[string]any{"username": "ADMIN", "password": "Valid-pass-2026"}, 409, &p)
	if p.Error != "username_taken" {
		t.Fatalf("taken problem = %+v", p)
	}
	create(map[string]any{"username": "ivan", "password": "short"}, 400, &p)
	if p.Error != "weak_password" || len(p.Violations) == 0 {
		t.Fatalf("weak problem = %+v", p)
	}
	create(map[string]any{"username": "ivan", "password": "Valid-pass-2026", "role_id": "ghost"}, 400, &p)
	create(map[string]any{"username": "ivan", "password": "Valid-pass-2026", "team_ids": []string{"ghost"}}, 400, &p)
	create(map[string]any{"username": "ivan", "password": "Valid-pass-2026", "email": "not-mail"}, 400, &p)

	var u managed
	create(map[string]any{"username": " ivan ", "password": "Valid-pass-2026", "last_name": "Petrov", "first_name": "Ivan", "team_ids": []string{"TEAM-2"}}, 201, &u)
	if u.Username != "ivan" || u.Source != "local" || u.Role != model.RoleUser || len(u.Teams) != 1 || u.Teams[0].Name != "Team TEAM-2" || !u.MustChangePassword || !u.PasswordExpired ||
		u.DisplayName != "Petrov Ivan" {
		t.Fatalf("created = %+v", u)
	}
	if h.bao.Get("umbrella/users/" + u.ID)["password_hash"] == nil || !h.audited("user.create", u.ID) {
		t.Fatal("password secret or audit entry missing")
	}

	var list []managed
	for query, want := range map[string]int{"?q=petrov": 1, "?team=TEAM-1": 1, "?team=TEAM-2": 1, "?role=admin": 1, "?source=ldap": 0, "?status=locked": 0, "?status=active": 2} {
		expect(t, "list "+query, admin.call(http.MethodGet, "/api/users"+query, nil, &list), 200, list)
		if len(list) != want {
			t.Fatalf("list %s = %d users, want %d", query, len(list), want)
		}
	}

	expect(t, "edit", admin.call(http.MethodPut, "/api/users/"+u.ID, map[string]any{
		"profile": map[string]any{"last_name": "Sidorov", "first_name": "Ivan", "email": "ivan@example.org"}, "team_ids": []string{}, "role_id": model.RoleAdmin}, &u), 200, u)
	if u.LastName != "Sidorov" || len(u.TeamIDs) != 0 || u.Role != model.RoleAdmin || !h.audited("user.update", u.ID) {
		t.Fatalf("edited = %+v", u)
	}

	expect(t, "password", admin.call(http.MethodPut, "/api/users/"+u.ID+"/password", map[string]any{"password": "Fresh-pass-2026", "must_change_password": false}, &u), 200, u)
	if u.MustChangePassword || u.PasswordExpired {
		t.Fatalf("after password = %+v", u)
	}
	ivan := h.client()
	ivan.login("ivan", "Fresh-pass-2026")
	expect(t, "ivan me", ivan.call(http.MethodGet, "/api/auth/me", nil, nil), 200, nil)

	expect(t, "lock", admin.call(http.MethodPost, "/api/users/"+u.ID+"/lock", nil, &u), 200, u)
	if !u.Disabled || !h.audited("user.lock", u.ID) {
		t.Fatalf("locked = %+v", u)
	}
	expect(t, "locked session", ivan.call(http.MethodGet, "/api/auth/me", nil, nil), 401, nil)
	expect(t, "locked login", h.client().call(http.MethodPost, "/api/auth/login", map[string]string{"username": "ivan", "password": "Fresh-pass-2026"}, nil), 401, nil)
	expect(t, "locked filter", admin.call(http.MethodGet, "/api/users?status=locked", nil, &list), 200, list)
	if len(list) != 1 {
		t.Fatalf("locked list = %+v", list)
	}

	expect(t, "unlock", admin.call(http.MethodPost, "/api/users/"+u.ID+"/unlock", nil, &u), 200, u)
	if u.Disabled || !h.audited("user.unlock", u.ID) {
		t.Fatalf("unlocked = %+v", u)
	}
	ivan.login("ivan", "Fresh-pass-2026")

	expect(t, "reset with must change", admin.call(http.MethodPut, "/api/users/"+u.ID+"/password", map[string]any{"password": "Other-pass-2026", "must_change_password": true}, &u), 200, u)
	if !u.MustChangePassword {
		t.Fatalf("must change = %+v", u)
	}
	expect(t, "reset signs out", ivan.call(http.MethodGet, "/api/auth/me", nil, nil), 401, nil)

	expect(t, "delete", admin.call(http.MethodDelete, "/api/users/"+u.ID, nil, nil), 204, nil)
	if h.user(u.ID) != nil || h.bao.Get("umbrella/users/"+u.ID) != nil || !h.audited("user.delete", u.ID) {
		t.Fatal("user, secret or audit entry left after delete")
	}
	expect(t, "deleted get", admin.call(http.MethodGet, "/api/users/"+u.ID, nil, &p), 404, p)
	expect(t, "deleted again", admin.call(http.MethodDelete, "/api/users/"+u.ID, nil, &p), 404, p)
}

func TestUsersSafetyRules(t *testing.T) {
	h, admin, adminID := adminSession(t)
	var p problem
	expect(t, "lock self", admin.call(http.MethodPost, "/api/users/"+adminID+"/lock", nil, &p), 409, p)
	if p.Error != "self_action" {
		t.Fatalf("lock self = %+v", p)
	}
	expect(t, "delete self", admin.call(http.MethodDelete, "/api/users/"+adminID, nil, &p), 409, p)
	expect(t, "own role", admin.call(http.MethodPut, "/api/users/"+adminID, map[string]any{"role_id": model.RoleUser}, &p), 409, p)
	if h.user(adminID).Disabled || h.user(adminID).Role != model.RoleAdmin {
		t.Fatal("admin changed by a refused request")
	}
	var u managed
	expect(t, "own team", admin.call(http.MethodPut, "/api/users/"+adminID, map[string]any{"role_id": model.RoleAdmin, "profile": map[string]any{"last_name": "Root"}}, &u), 200, u)

	h.addRole("managers", "users:create", "users:edit", "users:lock", "users:password", "users:delete")
	h.addLocal("manager", "Manager-pass-2026", "managers", time.Now())
	manager := h.client()
	manager.login("manager", "Manager-pass-2026")
	expect(t, "manager creates admin", manager.call(http.MethodPost, "/api/users", map[string]any{"username": "boss", "password": "Boss-pass-2026", "role_id": model.RoleAdmin}, &p), 403, p)
	if p.Error != "admin_only" {
		t.Fatalf("admin only = %+v", p)
	}
	expect(t, "manager locks admin", manager.call(http.MethodPost, "/api/users/"+adminID+"/lock", nil, &p), 403, p)
	expect(t, "manager resets admin", manager.call(http.MethodPut, "/api/users/"+adminID+"/password", map[string]any{"password": "Taken-pass-2026"}, &p), 403, p)
	expect(t, "manager deletes admin", manager.call(http.MethodDelete, "/api/users/"+adminID, nil, &p), 403, p)
	expect(t, "manager creates user", manager.call(http.MethodPost, "/api/users", map[string]any{"username": "worker", "password": "Worker-pass-2026"}, &u), 201, u)
	expect(t, "manager promotes", manager.call(http.MethodPut, "/api/users/"+u.ID, map[string]any{"role_id": model.RoleAdmin}, &p), 403, p)
	expect(t, "manager deletes user", manager.call(http.MethodDelete, "/api/users/"+u.ID, nil, nil), 204, nil)
}

func TestUsersDirectoryAccounts(t *testing.T) {
	h, admin, _ := adminSession(t)
	h.addTeam("TEAM-1", "")
	id := h.addLDAP("anna", model.RoleUser)
	var p problem
	expect(t, "ldap profile", admin.call(http.MethodPut, "/api/users/"+id, map[string]any{"profile": map[string]any{"last_name": "Changed"}}, &p), 409, p)
	if p.Error != "managed_by_directory" {
		t.Fatalf("ldap profile = %+v", p)
	}
	var u managed
	expect(t, "ldap same profile", admin.call(http.MethodPut, "/api/users/"+id, map[string]any{"profile": map[string]any{}, "team_ids": []string{"TEAM-1"}, "role_id": model.RoleAdmin}, &u), 200, u)
	if len(u.TeamIDs) != 1 || u.TeamIDs[0] != "TEAM-1" || u.Role != model.RoleAdmin || u.Source != "ldap" {
		t.Fatalf("ldap edit = %+v", u)
	}
	expect(t, "ldap password", admin.call(http.MethodPut, "/api/users/"+id+"/password", map[string]any{"password": "Valid-pass-2026"}, &p), 409, p)
	expect(t, "ldap lock", admin.call(http.MethodPost, "/api/users/"+id+"/lock", nil, &u), 200, u)
	expect(t, "ldap delete", admin.call(http.MethodDelete, "/api/users/"+id, nil, nil), 204, nil)
	if h.user(id) != nil {
		t.Fatal("ldap user left after delete")
	}
}

func TestUsersVaultFailure(t *testing.T) {
	h, admin, _ := adminSession(t)
	victim := h.addLocal("victim", "Victim-pass-2026", model.RoleUser, time.Now())
	h.bao.Server.Close()
	var p problem
	expect(t, "delete without vault", admin.call(http.MethodDelete, "/api/users/"+victim, nil, &p), 503, p)
	if h.user(victim) == nil {
		t.Fatal("user removed although the password secret was kept")
	}
	expect(t, "create without vault", admin.call(http.MethodPost, "/api/users", map[string]any{"username": "late", "password": "Late-pass-2026"}, &p), 503, p)
	h.st.Read(func(d *store.Data) {
		if d.UserByName("late") != nil {
			t.Fatal("user created without a password")
		}
	})
}
