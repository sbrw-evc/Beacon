package app_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/access"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/spa"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type roleReply struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Description    string           `json:"description"`
	Permissions    []string         `json:"permissions"`
	System         bool             `json:"system"`
	AllPermissions bool             `json:"all_permissions"`
	MemberCount    int              `json:"member_count"`
	Members        []orgMemberReply `json:"members"`
}

func TestCustomRoleGrantsExactPermissions(t *testing.T) {
	f := newOrgFixture(t)
	bob, bobID := f.signedIn("bob", model.RoleUser)

	f.expect(bob, http.MethodGet, "/api/roles", nil, http.StatusForbidden, "forbidden")
	f.expect(bob, http.MethodGet, "/api/settings/ldap", nil, http.StatusForbidden, "forbidden")

	var role roleReply
	f.do(f.admin, http.MethodPost, "/api/roles", map[string]any{
		"name": "Directory readers", "description": "Read the directory settings",
		"permissions": []string{"settings.ldap:view", "teams:edit", "nope:view", "status:unknown", " "},
	}, http.StatusCreated, &role)
	if want := []string{"teams:view", "teams:edit", "settings.ldap:view"}; !slices.Equal(role.Permissions, want) || role.System || role.MemberCount != 0 {
		t.Fatalf("created role = %+v, want permissions %v", role, want)
	}

	f.expect(f.admin, http.MethodPost, "/api/roles", map[string]any{"name": "DIRECTORY readers"}, http.StatusConflict, "role_name_taken")
	f.expect(f.admin, http.MethodPost, "/api/roles", map[string]any{"name": "  "}, http.StatusBadRequest, "invalid_role_name")
	f.expect(f.admin, http.MethodPost, "/api/roles", map[string]any{"name": strings.Repeat("x", 81)}, http.StatusBadRequest, "invalid_role_name")
	f.expect(f.admin, http.MethodPost, "/api/roles", map[string]any{}, http.StatusBadRequest, "invalid_role_name")
	f.expect(f.admin, http.MethodPut, "/api/roles/"+role.ID+"/members", map[string]any{"user_ids": []string{bobID}}, http.StatusNotFound, "not_found")

	// SPA gives the role: the next sign-in carries it.
	f.h.addSPA(spa.User{ID: spaID("bob"), Username: "bob", Name: "bob", Roles: []string{strings.ToLower(role.ID)}})
	bob = f.h.client()
	bob.login("bob", orgUserPass)

	f.expect(bob, http.MethodGet, "/api/settings/ldap", nil, http.StatusOK, "")
	f.expect(bob, http.MethodPut, "/api/settings/ldap", map[string]any{}, http.StatusForbidden, "forbidden")
	f.expect(bob, http.MethodGet, "/api/teams", nil, http.StatusOK, "")
	f.expect(bob, http.MethodGet, "/api/roles", nil, http.StatusForbidden, "forbidden")
	f.expect(bob, http.MethodPost, "/api/roles", map[string]any{"name": "Mine"}, http.StatusForbidden, "forbidden")
	f.expect(bob, http.MethodGet, "/api/system", nil, http.StatusForbidden, "forbidden")

	var me map[string]any
	f.do(bob, http.MethodGet, "/api/auth/me", nil, http.StatusOK, &me)
	if me["role"] != role.ID || len(me["permissions"].([]any)) != 3 {
		t.Fatalf("me = %v", me)
	}

	f.do(f.admin, http.MethodPut, "/api/roles/"+role.ID, map[string]any{"permissions": []string{"teams:edit"}}, http.StatusOK, &role)
	if !slices.Equal(role.Permissions, []string{"teams:view", "teams:edit"}) || role.Name != "Directory readers" {
		t.Fatalf("updated = %+v", role)
	}
	f.expect(bob, http.MethodGet, "/api/settings/ldap", nil, http.StatusForbidden, "forbidden")

	// A role SPA still gives is taken away there first.
	f.expect(f.admin, http.MethodDelete, "/api/roles/"+role.ID, nil, http.StatusConflict, "role_in_use")
	f.h.st.Write(func(d *store.Data) { d.Users[bobID].Role = model.RoleUser })
	f.expect(f.admin, http.MethodDelete, "/api/roles/"+role.ID, nil, http.StatusNoContent, "")
	f.expect(f.admin, http.MethodGet, "/api/roles/"+role.ID, nil, http.StatusNotFound, "not_found")
}

func TestSystemRoleRules(t *testing.T) {
	f := newOrgFixture(t)

	var admin roleReply
	f.do(f.admin, http.MethodGet, "/api/roles/"+model.RoleAdmin, nil, http.StatusOK, &admin)
	if !admin.System || !admin.AllPermissions || !slices.Equal(admin.Permissions, access.All()) || admin.MemberCount != 1 {
		t.Fatalf("admin role = %+v", admin)
	}
	original := admin.Description

	f.do(f.admin, http.MethodPut, "/api/roles/"+model.RoleAdmin, map[string]any{"description": "Everything"}, http.StatusOK, &admin)
	if admin.Description != "Everything" {
		t.Fatalf("admin description = %q", admin.Description)
	}
	f.expect(f.admin, http.MethodPut, "/api/roles/"+model.RoleAdmin, map[string]any{"name": "Root"}, http.StatusConflict, "role_system")
	f.expect(f.admin, http.MethodPut, "/api/roles/"+model.RoleAdmin, map[string]any{"permissions": []string{}}, http.StatusConflict, "role_system")
	f.expect(f.admin, http.MethodPut, "/api/roles/"+model.RoleUser, map[string]any{"name": "Everyone"}, http.StatusConflict, "role_system")
	f.expect(f.admin, http.MethodDelete, "/api/roles/"+model.RoleAdmin, nil, http.StatusConflict, "role_system")
	f.expect(f.admin, http.MethodDelete, "/api/roles/"+model.RoleUser+"?reassign_to="+model.RoleAdmin, nil, http.StatusConflict, "role_system")
	f.expect(f.admin, http.MethodPost, "/api/roles", map[string]any{"name": "administrator"}, http.StatusConflict, "role_name_taken")

	var user roleReply
	f.do(f.admin, http.MethodPut, "/api/roles/"+model.RoleUser, map[string]any{"name": "User", "permissions": []string{"status:defaults"}}, http.StatusOK, &user)
	if !slices.Equal(user.Permissions, []string{"status:view", "status:defaults"}) || user.AllPermissions {
		t.Fatalf("user role = %+v", user)
	}

	var list []roleReply
	f.do(f.admin, http.MethodGet, "/api/roles", nil, http.StatusOK, &list)
	if len(list) != 2 || list[0].ID != model.RoleAdmin || list[1].ID != model.RoleUser {
		t.Fatalf("list = %+v", list)
	}

	f.do(f.admin, http.MethodPut, "/api/roles/"+model.RoleUser, map[string]any{"permissions": []string{}}, http.StatusOK, &user)
	f.do(f.admin, http.MethodPut, "/api/roles/"+model.RoleAdmin, map[string]any{"description": original}, http.StatusOK, &admin)
}
