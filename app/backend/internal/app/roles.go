package app

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/access"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

var (
	ErrRoleNameTaken = errors.New("a role with this name already exists")
	ErrRoleSystem    = errors.New("system roles cannot be changed this way")
	ErrRoleInUse     = errors.New("the role still has members")
	ErrOwnAdmin      = errors.New("you cannot remove your own administrator role")
)

func init() {
	statuses = append(statuses,
		orgStatus{ErrRoleNameTaken, http.StatusConflict, "role_name_taken"},
		orgStatus{ErrRoleSystem, http.StatusConflict, "role_system"},
		orgStatus{ErrRoleInUse, http.StatusConflict, "role_in_use"},
		orgStatus{ErrOwnAdmin, http.StatusConflict, "own_admin"},
	)
}

type RoleInput struct {
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	Permissions *[]string `json:"permissions"`
}

type RoleView struct {
	model.Role
	AllPermissions bool `json:"all_permissions"`
	// NewUsers: accounts created by a directory or NetBox without a mapped role get this role.
	NewUsers    bool        `json:"new_users"`
	MemberCount int         `json:"member_count"`
	Members     []OrgMember `json:"members"`
}

type RolesService struct {
	st  *store.Store
	now func() time.Time
}

func NewRolesService(st *store.Store) *RolesService {
	return &RolesService{st: st, now: func() time.Time { return time.Now().UTC() }}
}

func (s *RolesService) List() []RoleView {
	var out []RoleView
	s.st.Read(func(d *store.Data) {
		members := roleMembers(d)
		out = make([]RoleView, 0, len(d.Roles))
		for _, r := range d.Roles {
			out = append(out, roleView(d, r, members[r.ID]))
		}
	})
	slices.SortFunc(out, func(x, y RoleView) int {
		if x.System != y.System {
			if x.System {
				return -1
			}
			return 1
		}
		if x.System {
			return strings.Compare(x.ID, y.ID)
		}
		return byName(x.Name, y.Name)
	})
	return out
}

func (s *RolesService) Get(id string) (RoleView, error) {
	var out RoleView
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		if r := d.Roles[id]; r != nil {
			out, err = roleView(d, r, roleMembers(d)[id]), nil
		}
	})
	return out, err
}

func (s *RolesService) Create(actor string, in RoleInput) (RoleView, error) {
	if in.Name == nil {
		return RoleView{}, invalid("invalid_role_name", errOrgNameEmpty)
	}
	r := &model.Role{Permissions: []string{}}
	if err := applyRoleText(r, in); err != nil {
		return RoleView{}, err
	}
	if in.Permissions != nil {
		r.Permissions = access.Normalize(*in.Permissions)
	}
	var out RoleView
	var err error
	s.st.Write(func(d *store.Data) {
		if roleNameTaken(d, r.Name, "") {
			err = ErrRoleNameTaken
			return
		}
		r.ID = d.NextID("ROL")
		r.CreatedAt, r.UpdatedAt = s.now(), s.now()
		d.Roles[r.ID] = r
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "role.create", Object: r.ID, Detail: describeRole(r)})
		out = roleView(d, r, nil)
	})
	return out, err
}

func (s *RolesService) Update(actor, id string, in RoleInput) (RoleView, error) {
	var out RoleView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		cur := d.Roles[id]
		if cur == nil {
			return
		}
		next := *cur
		next.Permissions = slices.Clone(cur.Permissions)
		if err = applyRoleText(&next, in); err != nil {
			return
		}
		if in.Permissions != nil {
			next.Permissions = access.Normalize(*in.Permissions)
		}
		if err = checkSystemRoleChange(cur, &next, in); err != nil {
			return
		}
		if !strings.EqualFold(next.Name, cur.Name) && roleNameTaken(d, next.Name, id) {
			err = ErrRoleNameTaken
			return
		}
		if detail := diffRole(cur, &next); detail != "" {
			next.UpdatedAt = s.now()
			*cur = next
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "role.update", Object: id, Detail: detail})
		}
		out, err = roleView(d, cur, roleMembers(d)[id]), nil
	})
	return out, err
}

func (s *RolesService) Delete(actor model.User, id, reassignTo string) error {
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		r := d.Roles[id]
		if r == nil {
			return
		}
		if r.System {
			err = ErrRoleSystem
			return
		}
		members := roleUsers(d, id)
		target := d.Roles[reassignTo]
		switch {
		case len(members) > 0 && reassignTo == "":
			err = ErrRoleInUse
			return
		case reassignTo != "" && (target == nil || target.ID == id):
			err = invalid("invalid_reassign", fmt.Errorf("unknown role %q", reassignTo))
			return
		}
		if len(members) > 0 {
			if err = checkRoleMove(d, actor, target.ID, members); err != nil {
				return
			}
		}
		for _, u := range members {
			u.Role = target.ID
		}
		delete(d.Roles, id)
		dropMappingRefs(d, id, "")
		if d.Settings.NewUserRole == id {
			d.Settings.NewUserRole = ""
		}
		detail := r.Name
		if len(members) > 0 {
			detail = fmt.Sprintf("%s; %d member(s) moved to %s", r.Name, len(members), target.Name)
		}
		d.AddAudit(store.AuditEntry{Actor: actor.Username, Action: "role.delete", Object: id, Detail: detail})
		err = nil
	})
	return err
}

func (s *RolesService) AddMembers(actor model.User, id string, userIDs []string) (RoleView, error) {
	var out RoleView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		r := d.Roles[id]
		if r == nil {
			return
		}
		var users []*model.User
		if users, err = orgUsers(d, userIDs); err != nil {
			return
		}
		if err = checkRoleMove(d, actor, id, users); err != nil {
			return
		}
		moved := []*model.User{}
		for _, u := range users {
			if u.Role != id {
				u.Role = id
				moved = append(moved, u)
			}
		}
		if len(moved) > 0 {
			d.AddAudit(store.AuditEntry{Actor: actor.Username, Action: "role.members", Object: id, Detail: r.Name + ": " + usernames(moved)})
		}
		out = roleView(d, r, roleMembers(d)[id])
	})
	return out, err
}

func checkRoleMove(d *store.Data, actor model.User, target string, users []*model.User) error {
	leaving := map[string]bool{}
	for _, u := range users {
		if d.IsAdmin(u) == (target == model.RoleAdmin) {
			continue
		}
		if !d.IsAdmin(&actor) {
			return ErrAdminOnly
		}
		if u.ID == actor.ID {
			return ErrOwnAdmin
		}
		if d.IsAdmin(u) {
			leaving[u.ID] = true
		}
	}
	if len(leaving) == 0 {
		return nil
	}
	active, local := adminCounts(d, leaving)
	switch {
	case active == 0:
		return ErrLastAdmin
	case local == 0 && localAdmins(d) > 0:
		return ErrNoLocalAdmin
	}
	return nil
}

func checkSystemRoleChange(cur, next *model.Role, in RoleInput) error {
	if !cur.System {
		return nil
	}
	if next.Name != cur.Name {
		return ErrRoleSystem
	}
	if cur.ID == model.RoleAdmin && in.Permissions != nil {
		return ErrRoleSystem
	}
	return nil
}

func applyRoleText(r *model.Role, in RoleInput) error {
	if in.Name != nil {
		name, err := orgName(*in.Name)
		if err != nil {
			return invalid("invalid_role_name", err)
		}
		r.Name = name
	}
	if in.Description != nil {
		desc, err := orgDescription(*in.Description)
		if err != nil {
			return invalid("invalid_description", err)
		}
		r.Description = desc
	}
	return nil
}

func roleNameTaken(d *store.Data, name, except string) bool {
	for _, r := range d.Roles {
		if r.ID != except && strings.EqualFold(r.Name, name) {
			return true
		}
	}
	return false
}

func roleUsers(d *store.Data, id string) []*model.User {
	out := []*model.User{}
	for _, u := range d.Users {
		if d.RoleOf(u).ID == id {
			out = append(out, u)
		}
	}
	return out
}

func roleMembers(d *store.Data) map[string][]OrgMember {
	out := map[string][]OrgMember{}
	for _, u := range d.Users {
		m := orgMemberOf(d, u)
		out[m.RoleID] = append(out[m.RoleID], m)
	}
	return out
}

func roleView(d *store.Data, r *model.Role, members []OrgMember) RoleView {
	v := RoleView{Role: *r, Members: slices.Clone(members), MemberCount: len(members), NewUsers: d.NewUserRole() == r.ID}
	if v.Members == nil {
		v.Members = []OrgMember{}
	}
	sortMembers(v.Members)
	v.Permissions = slices.Clone(r.Permissions)
	if r.ID == model.RoleAdmin {
		v.AllPermissions, v.Permissions = true, access.All()
	}
	if v.Permissions == nil {
		v.Permissions = []string{}
	}
	return v
}

func describeRole(r *model.Role) string {
	return fmt.Sprintf("%s; permissions: %s", r.Name, permList(r.Permissions))
}

func permList(perms []string) string {
	if len(perms) == 0 {
		return "none"
	}
	return strings.Join(perms, ", ")
}

func diffRole(cur, next *model.Role) string {
	var parts []string
	if cur.Name != next.Name {
		parts = append(parts, fmt.Sprintf("name %q -> %q", cur.Name, next.Name))
	}
	if cur.Description != next.Description {
		parts = append(parts, "description changed")
	}
	was, now := access.NewSet(cur.Permissions), access.NewSet(next.Permissions)
	var added, removed []string
	for _, p := range access.All() {
		switch {
		case now[p] && !was[p]:
			added = append(added, p)
		case was[p] && !now[p]:
			removed = append(removed, p)
		}
	}
	if len(added) > 0 {
		parts = append(parts, "granted "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		parts = append(parts, "revoked "+strings.Join(removed, ", "))
	}
	return strings.Join(parts, "; ")
}

// SetNewUserRole chooses the role of accounts a directory sign-in or synchronization or NetBox
// creates without a mapped role. The administrator role cannot be chosen; existing users keep
// their roles.
func (s *RolesService) SetNewUserRole(actor, id string) (RoleView, error) {
	var out RoleView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		r := d.Roles[id]
		if r == nil {
			return
		}
		if id == model.RoleAdmin {
			err = invalid("new_user_role_admin", errors.New("new users cannot be administrators"))
			return
		}
		err = nil
		if d.Settings.NewUserRole != id {
			d.Settings.NewUserRole = id
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "roles.new_users", Object: id, Detail: r.Name})
		}
		out = roleView(d, r, roleMembers(d)[id])
	})
	return out, err
}
