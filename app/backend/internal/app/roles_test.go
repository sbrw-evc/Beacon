package app_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/access"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
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
	f.expect(bob, http.MethodGet, "/api/settings/password-policy", nil, http.StatusForbidden, "forbidden")

	var role roleReply
	f.do(f.admin, http.MethodPost, "/api/roles", map[string]any{
		"name": "Policy readers", "description": "Read the password policy",
		"permissions": []string{"settings.policy:view", "teams:edit", "nope:view", "status:unknown", " "},
	}, http.StatusCreated, &role)
	if want := []string{"teams:view", "teams:edit", "settings.policy:view"}; !slices.Equal(role.Permissions, want) || role.System || role.MemberCount != 0 {
		t.Fatalf("created role = %+v, want permissions %v", role, want)
	}

	f.expect(f.admin, http.MethodPost, "/api/roles", map[string]any{"name": "POLICY readers"}, http.StatusConflict, "role_name_taken")
	f.expect(f.admin, http.MethodPost, "/api/roles", map[string]any{"name": "  "}, http.StatusBadRequest, "invalid_role_name")
	f.expect(f.admin, http.MethodPost, "/api/roles", map[string]any{"name": strings.Repeat("x", 81)}, http.StatusBadRequest, "invalid_role_name")
	f.expect(f.admin, http.MethodPost, "/api/roles", map[string]any{}, http.StatusBadRequest, "invalid_role_name")

	f.do(f.admin, http.MethodPut, "/api/roles/"+role.ID+"/members", map[string]any{"user_ids": []string{bobID}}, http.StatusOK, &role)
	if role.MemberCount != 1 || role.Members[0].ID != bobID {
		t.Fatalf("members = %+v", role)
	}
	f.expect(f.admin, http.MethodPut, "/api/roles/"+role.ID+"/members", map[string]any{"user_ids": []string{"USR-404"}}, http.StatusBadRequest, "unknown_user")

	f.expect(bob, http.MethodGet, "/api/settings/password-policy", nil, http.StatusOK, "")
	f.expect(bob, http.MethodPut, "/api/settings/password-policy", map[string]any{}, http.StatusForbidden, "forbidden")
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
	if !slices.Equal(role.Permissions, []string{"teams:view", "teams:edit"}) || role.Name != "Policy readers" {
		t.Fatalf("updated = %+v", role)
	}
	f.expect(bob, http.MethodGet, "/api/settings/password-policy", nil, http.StatusForbidden, "forbidden")

	f.expect(f.admin, http.MethodDelete, "/api/roles/"+role.ID, nil, http.StatusConflict, "role_in_use")
	f.expect(f.admin, http.MethodDelete, "/api/roles/"+role.ID+"?reassign_to="+role.ID, nil, http.StatusBadRequest, "invalid_reassign")
	f.expect(f.admin, http.MethodDelete, "/api/roles/"+role.ID+"?reassign_to=ROL-404", nil, http.StatusBadRequest, "invalid_reassign")
	f.expect(f.admin, http.MethodDelete, "/api/roles/"+role.ID, map[string]any{"reassign_to": model.RoleUser}, http.StatusNoContent, "")
	if got := f.userField(bobID, func(u *model.User) string { return u.Role }); got != model.RoleUser {
		t.Fatalf("bob role after delete = %s", got)
	}
	f.expect(f.admin, http.MethodGet, "/api/roles/"+role.ID, nil, http.StatusNotFound, "not_found")
	f.expect(bob, http.MethodGet, "/api/teams", nil, http.StatusForbidden, "forbidden")
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

func TestRoleMembershipMovesAndAdminGuards(t *testing.T) {
	f := newOrgFixture(t)
	dave := f.user("dave")
	erin := f.user("erin")
	defer f.dropUsers(dave, erin)

	var ops roleReply
	f.do(f.admin, http.MethodPost, "/api/roles", map[string]any{"name": "Operators", "permissions": []string{"roles:edit"}}, http.StatusCreated, &ops)
	var dev roleReply
	f.do(f.admin, http.MethodPost, "/api/roles", map[string]any{"name": "Developers"}, http.StatusCreated, &dev)

	f.do(f.admin, http.MethodPut, "/api/roles/"+ops.ID+"/members", map[string]any{"user_ids": []string{dave, erin, dave}}, http.StatusOK, &ops)
	if ops.MemberCount != 2 {
		t.Fatalf("ops = %+v", ops)
	}
	f.do(f.admin, http.MethodPut, "/api/roles/"+dev.ID+"/members", map[string]any{"user_ids": []string{erin}}, http.StatusOK, &dev)
	f.do(f.admin, http.MethodGet, "/api/roles/"+ops.ID, nil, http.StatusOK, &ops)
	if dev.MemberCount != 1 || ops.MemberCount != 1 || ops.Members[0].ID != dave || dev.Members[0].RoleID != dev.ID {
		t.Fatalf("after move ops=%+v dev=%+v", ops, dev)
	}

	f.expect(f.admin, http.MethodPut, "/api/roles/"+model.RoleUser+"/members", map[string]any{"user_ids": []string{f.adminID}}, http.StatusConflict, "own_admin")

	frank, frankID := f.signedIn("frank", ops.ID)
	f.expect(frank, http.MethodPut, "/api/roles/"+model.RoleAdmin+"/members", map[string]any{"user_ids": []string{frankID}}, http.StatusForbidden, "admin_only")
	f.expect(frank, http.MethodPut, "/api/roles/"+dev.ID+"/members", map[string]any{"user_ids": []string{f.adminID}}, http.StatusForbidden, "admin_only")
	f.expect(frank, http.MethodDelete, "/api/roles/"+dev.ID+"?reassign_to="+model.RoleAdmin, nil, http.StatusForbidden, "admin_only")
	f.expect(frank, http.MethodPut, "/api/roles/"+dev.ID+"/members", map[string]any{"user_ids": []string{dave}}, http.StatusOK, "")

	var admin roleReply
	f.do(f.admin, http.MethodPut, "/api/roles/"+model.RoleAdmin+"/members", map[string]any{"user_ids": []string{dave}}, http.StatusOK, &admin)
	if admin.MemberCount != 2 {
		t.Fatalf("admin members = %+v", admin.Members)
	}
	f.expect(frank, http.MethodPut, "/api/roles/"+model.RoleUser+"/members", map[string]any{"user_ids": []string{dave}}, http.StatusForbidden, "admin_only")
	f.do(f.admin, http.MethodPut, "/api/roles/"+model.RoleUser+"/members", map[string]any{"user_ids": []string{dave}}, http.StatusOK, nil)
	f.expect(f.admin, http.MethodPut, "/api/roles/"+model.RoleUser+"/members", map[string]any{"user_ids": []string{dave, f.adminID}}, http.StatusConflict, "own_admin")
	if got := f.userField(dave, func(u *model.User) string { return u.Role }); got != model.RoleUser {
		t.Fatalf("dave role = %s", got)
	}

	f.expect(frank, http.MethodDelete, "/api/roles/"+dev.ID+"?reassign_to="+model.RoleUser, nil, http.StatusNoContent, "")
	f.expect(f.admin, http.MethodDelete, "/api/roles/"+ops.ID+"?reassign_to="+model.RoleUser, nil, http.StatusNoContent, "")
	var list []roleReply
	f.do(f.admin, http.MethodGet, "/api/roles", nil, http.StatusOK, &list)
	if len(list) != 2 {
		t.Fatalf("roles left = %+v", list)
	}
}
