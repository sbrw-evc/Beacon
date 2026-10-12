package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/credentials"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

var (
	ErrUsernameTaken = errors.New("the username is already taken")
	ErrSelfAction    = errors.New("the action is not allowed on your own account")
	ErrAdminOnly     = errors.New("only administrators can manage administrators")
	ErrLastAdmin     = errors.New("the administrator role would have no active member")
)

func init() {
	statuses = append(statuses,
		orgStatus{ErrUsernameTaken, http.StatusConflict, "username_taken"},
		orgStatus{ErrSelfAction, http.StatusConflict, "self_action"},
		orgStatus{ErrAdminOnly, http.StatusForbidden, "admin_only"},
		orgStatus{ErrLastAdmin, http.StatusConflict, "last_admin"},
	)
}

const (
	userStatusActive = "active"
	userStatusLocked = "locked"

	maxUsername = 64
)

type SecretRemover interface {
	Delete(ctx context.Context, path string) error
}

type Actor struct {
	User    model.User
	Session string
}

type UserFilter struct {
	Query  string
	Source string
	Role   string
	Team   string
	Status string
}

type NewUser struct {
	model.Profile
	Username           string   `json:"username"`
	Password           string   `json:"password"`
	RoleID             string   `json:"role_id"`
	TeamIDs            []string `json:"team_ids"`
	ScopeMode          string   `json:"scope_mode"`
	MustChangePassword *bool    `json:"must_change_password"`
}

type UserChanges struct {
	Profile *model.Profile `json:"profile"`
	RoleID  *string        `json:"role_id"`
	// TeamIDs replaces the teams of the user.
	TeamIDs *[]string `json:"team_ids"`
	// ScopeMode: all, teams (services of the user's teams) or services (ServiceIDs).
	ScopeMode *string `json:"scope_mode"`
	// ServiceIDs replaces the chosen services. Given without ScopeMode, a non-empty list means
	// the services mode and an empty one all services, as before scope modes existed.
	ServiceIDs *[]string `json:"service_ids"`
}

const maxUserServices = 200

type PasswordReset struct {
	Password           string `json:"password"`
	MustChangePassword bool   `json:"must_change_password"`
}

type UsersService struct {
	mu        sync.Mutex
	st        *store.Store
	passwords *credentials.Passwords
	secrets   SecretRemover
	sessions  SessionRevoker
	now       func() time.Time
}

func NewUsersService(st *store.Store, passwords *credentials.Passwords, secrets SecretRemover, sessions SessionRevoker) *UsersService {
	return &UsersService{st: st, passwords: passwords, secrets: secrets, sessions: sessions, now: func() time.Time { return time.Now().UTC() }}
}

func ValidUsername(v string) bool {
	n := len([]rune(v))
	return n >= 1 && n <= maxUsername && !strings.ContainsFunc(v, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}

func (s *UsersService) List(f UserFilter) []model.User {
	q := strings.ToLower(strings.TrimSpace(f.Query))
	out := []model.User{}
	s.st.Read(func(d *store.Data) {
		teams := userTeamScope(d, f.Team)
		for _, u := range d.Users {
			if f.Source != "" && u.Source != f.Source ||
				f.Role != "" && d.RoleOf(u).ID != f.Role ||
				f.Team != "" && !slices.ContainsFunc(u.TeamIDs, func(t string) bool { return teams[t] }) ||
				f.Status == userStatusActive && u.Disabled ||
				f.Status == userStatusLocked && !u.Disabled ||
				q != "" && !userMatches(u, q) {
				continue
			}
			out = append(out, *u)
		}
	})
	slices.SortFunc(out, func(x, y model.User) int {
		return byName(x.Profile.DisplayName(x.Username), y.Profile.DisplayName(y.Username))
	})
	return out
}

func userMatches(u *model.User, q string) bool {
	for _, v := range []string{u.Username, u.Name, u.Profile.DisplayName(u.Username), u.Email, u.Title, u.Department} {
		if strings.Contains(strings.ToLower(v), q) {
			return true
		}
	}
	return false
}

func userTeamScope(d *store.Data, root string) map[string]bool {
	out := map[string]bool{}
	if root == "" {
		return out
	}
	out[root] = true
	for grew := true; grew; {
		grew = false
		for _, t := range d.Teams {
			if !out[t.ID] && t.ParentID != "" && out[t.ParentID] {
				out[t.ID], grew = true, true
			}
		}
	}
	return out
}

func (s *UsersService) Get(id string) (model.User, error) {
	var out model.User
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		if u := d.Users[id]; u != nil {
			out, err = *u, nil
		}
	})
	return out, err
}

func (s *UsersService) Create(ctx context.Context, actor Actor, in NewUser) (model.User, error) {
	username := strings.TrimSpace(in.Username)
	if !ValidUsername(username) {
		return model.User{}, invalid("invalid_username", nil)
	}
	profile, err := normalUserProfile(in.Profile)
	if err != nil {
		return model.User{}, err
	}
	role := in.RoleID
	if role == "" {
		s.st.Read(func(d *store.Data) { role = d.NewUserRole() })
	}
	scope := userOr(in.ScopeMode, model.ScopeAll)
	if scope != model.ScopeAll && scope != model.ScopeTeams {
		return model.User{}, invalid("invalid_scope", nil)
	}
	draft := model.User{Username: username, Name: profile.DisplayName(username), Profile: profile, Source: model.SourceLocal,
		Role: role, TeamIDs: in.TeamIDs, ScopeMode: scope, MustChangePassword: in.MustChangePassword == nil || *in.MustChangePassword}

	s.mu.Lock()
	defer s.mu.Unlock()
	var policy model.PasswordPolicy
	s.st.Read(func(d *store.Data) {
		policy, err = d.Settings.Password, checkNewUser(d, actor, draft)
		if err == nil {
			draft.TeamIDs, err = userTeams(d, draft.TeamIDs)
		}
	})
	if err != nil {
		return model.User{}, err
	}
	if err := policy.Validate(in.Password, username); err != nil {
		return model.User{}, err
	}
	s.st.Write(func(d *store.Data) { draft.ID = d.NextID("USR") })
	ref, err := s.passwords.Set(ctx, draft.ID, in.Password)
	if err != nil {
		return model.User{}, err
	}
	now := s.now()
	draft.PasswordRef, draft.PasswordChangedAt, draft.CreatedAt = ref, now, now
	s.st.Write(func(d *store.Data) {
		if err = checkNewUser(d, actor, draft); err != nil {
			return
		}
		u := draft
		d.Users[u.ID] = &u
		d.AddAudit(store.AuditEntry{At: now, Actor: actor.User.Username, Action: "user.create", Object: u.ID, Detail: describeUser(d, &u)})
	})
	if err != nil {
		_ = s.secrets.Delete(ctx, credentials.Path(draft.ID))
		return model.User{}, err
	}
	return draft, nil
}

func checkNewUser(d *store.Data, actor Actor, u model.User) error {
	if d.UserByName(u.Username) != nil {
		return ErrUsernameTaken
	}
	if d.Roles[u.Role] == nil {
		return invalid("unknown_role", nil)
	}
	return guardUserChange(d, actor, nil, &u)
}

func normalUserProfile(in model.Profile) (model.Profile, error) {
	p, err := in.Normalize()
	if err != nil {
		return p, invalid("invalid_name", err)
	}
	if !model.ValidEmail(p.Email) {
		return p, invalid("invalid_email", nil)
	}
	return p, nil
}

func (s *UsersService) Update(actor Actor, id string, in UserChanges) (model.User, error) {
	var profile model.Profile
	if in.Profile != nil {
		p, err := normalUserProfile(*in.Profile)
		if err != nil {
			return model.User{}, err
		}
		profile = p
	}
	return s.change(actor, id, "user.update", func(d *store.Data, u *model.User) (string, error) {
		var changes []string
		if in.Profile != nil && profile != u.Profile {
			if u.Source != model.SourceLocal {
				return "", ErrManagedByDirectory
			}
			u.Profile, u.Name = profile, profile.DisplayName(u.Username)
			changes = append(changes, "profile")
		}
		role, teams := d.RoleOf(u).ID, u.TeamIDs
		if in.RoleID != nil {
			role = *in.RoleID
		}
		if d.Roles[role] == nil {
			return "", invalid("unknown_role", nil)
		}
		if in.TeamIDs != nil {
			var err error
			if teams, err = userTeams(d, *in.TeamIDs); err != nil {
				return "", err
			}
		}
		if role != d.RoleOf(u).ID {
			if u.ID == actor.User.ID {
				return "", ErrSelfAction
			}
			u.Role = role
			changes = append(changes, "role "+role)
		}
		if !slices.Equal(teams, u.TeamIDs) {
			u.TeamIDs = teams
			changes = append(changes, "teams "+userOr(strings.Join(teams, " "), "none"))
		}
		mode := u.ScopeMode
		if mode == "" {
			mode = model.ScopeAll
			if len(u.ServiceIDs) > 0 {
				mode = model.ScopeServices
			}
		}
		if in.ServiceIDs != nil {
			scope, err := userServiceScope(d, *in.ServiceIDs)
			if err != nil {
				return "", err
			}
			if !slices.Equal(scope, u.ServiceIDs) {
				u.ServiceIDs = scope
				changes = append(changes, "services "+userOr(strings.Join(scope, " "), "none"))
			}
			if in.ScopeMode == nil {
				mode = model.ScopeAll
				if len(scope) > 0 {
					mode = model.ScopeServices
				}
			}
		}
		if in.ScopeMode != nil {
			if !model.ValidScope(*in.ScopeMode) {
				return "", invalid("invalid_scope", nil)
			}
			mode = *in.ScopeMode
		}
		if mode == model.ScopeServices && len(u.ServiceIDs) == 0 {
			return "", invalid("scope_services_required", nil)
		}
		if mode != u.ScopeMode {
			u.ScopeMode = mode
			changes = append(changes, "scope "+mode)
		}
		return strings.Join(changes, ", "), nil
	})
}

// userServiceScope checks and normalizes the business services of a user's incident scope:
// every one must exist; duplicates and blanks are dropped; nil means no limit.
func userServiceScope(d *store.Data, ids []string) ([]string, error) {
	var out []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || slices.Contains(out, id) {
			continue
		}
		if d.Services[id] == nil {
			return nil, invalid("service_not_found", nil)
		}
		out = append(out, id)
	}
	if len(out) > maxUserServices {
		return nil, invalid("too_many_services", nil)
	}
	slices.Sort(out)
	return out, nil
}

func (s *UsersService) SetLocked(actor Actor, id string, locked bool) (model.User, error) {
	action := "user.unlock"
	if locked {
		action = "user.lock"
	}
	u, err := s.change(actor, id, action, func(d *store.Data, u *model.User) (string, error) {
		if u.ID == actor.User.ID {
			return "", ErrSelfAction
		}
		if u.Disabled == locked {
			return "", nil
		}
		u.Disabled = locked
		return u.Username, nil
	})
	if err == nil && locked {
		s.sessions.DeleteUser(id, "")
	}
	return u, err
}

func (s *UsersService) SetPassword(ctx context.Context, actor Actor, id string, in PasswordReset) (model.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var target model.User
	var policy model.PasswordPolicy
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		u := d.Users[id]
		if u == nil {
			return
		}
		target, policy, err = *u, d.Settings.Password, nil
		if u.Source != model.SourceLocal {
			err = ErrManagedByDirectory
			return
		}
		err = guardUserChange(d, actor, u, u)
	})
	if err != nil {
		return model.User{}, err
	}
	if err := policy.Validate(in.Password, target.Username); err != nil {
		return model.User{}, err
	}
	ref, err := s.passwords.Set(ctx, id, in.Password)
	if err != nil {
		return model.User{}, err
	}
	now := s.now()
	out, err := s.mutate(actor, id, "user.password_reset", func(d *store.Data, u *model.User) (string, error) {
		u.PasswordRef, u.PasswordChangedAt, u.MustChangePassword = ref, now, in.MustChangePassword
		if in.MustChangePassword {
			return "must change at next sign-in", nil
		}
		return "set", nil
	})
	if err != nil {
		return out, err
	}
	keep := ""
	if id == actor.User.ID {
		keep = actor.Session
	}
	s.sessions.DeleteUser(id, keep)
	return out, nil
}

func (s *UsersService) Delete(ctx context.Context, actor Actor, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	check := func(d *store.Data) (*model.User, error) {
		u := d.Users[id]
		if u == nil {
			return nil, ErrNotFound
		}
		if u.ID == actor.User.ID {
			return nil, ErrSelfAction
		}
		return u, guardUserChange(d, actor, u, nil)
	}
	var target model.User
	var err error
	s.st.Read(func(d *store.Data) {
		var u *model.User
		if u, err = check(d); err == nil {
			target = *u
		}
	})
	if err != nil {
		return err
	}
	if target.Source == model.SourceLocal {
		if err := s.secrets.Delete(ctx, credentials.Path(id)); err != nil {
			return fmt.Errorf("%w: %v", credentials.ErrUnavailable, err)
		}
	}
	s.st.Write(func(d *store.Data) {
		if _, err = check(d); err != nil {
			return
		}
		delete(d.Users, id)
		for _, t := range d.Teams {
			if t.LeadID == id {
				t.LeadID = ""
			}
		}
		d.AddAudit(store.AuditEntry{Actor: actor.User.Username, Action: "user.delete", Object: id, Detail: target.Username + " (" + target.Source + ")"})
	})
	if err != nil {
		return err
	}
	s.sessions.DeleteUser(id, "")
	return nil
}

func (s *UsersService) change(actor Actor, id, action string, fn func(d *store.Data, u *model.User) (string, error)) (model.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(actor, id, action, fn)
}

func (s *UsersService) mutate(actor Actor, id, action string, fn func(d *store.Data, u *model.User) (string, error)) (model.User, error) {
	var out model.User
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		u := d.Users[id]
		if u == nil {
			return
		}
		next := *u
		detail, e := fn(d, &next)
		if e == nil {
			e = guardUserChange(d, actor, u, &next)
		}
		if err = e; err != nil {
			return
		}
		if detail != "" {
			*u = next
			d.AddAudit(store.AuditEntry{Actor: actor.User.Username, Action: action, Object: u.ID, Detail: detail})
		}
		out = *u
	})
	return out, err
}

func guardUserChange(d *store.Data, actor Actor, before, after *model.User) error {
	if (d.IsAdmin(before) || d.IsAdmin(after)) && !d.IsAdmin(&actor.User) {
		return ErrAdminOnly
	}
	if before == nil {
		return nil
	}
	active, local := adminCounts(d, map[string]bool{before.ID: true}, after)
	switch {
	case active == 0 && d.IsAdmin(before) && !before.Disabled:
		return ErrLastAdmin
	case local == 0 && localAdmins(d) > 0:
		return ErrNoLocalAdmin
	}
	return nil
}

func adminCounts(d *store.Data, skip map[string]bool, extra ...*model.User) (active, local int) {
	count := func(u *model.User) {
		if u == nil || u.Disabled || !d.IsAdmin(u) {
			return
		}
		active++
		if u.Source == model.SourceLocal && u.PasswordRef != "" {
			local++
		}
	}
	for id, u := range d.Users {
		if !skip[id] {
			count(u)
		}
	}
	for _, u := range extra {
		count(u)
	}
	return active, local
}

func describeUser(d *store.Data, u *model.User) string {
	out := u.Username + ", role " + d.RoleOf(u).ID
	if len(u.TeamIDs) > 0 {
		out += ", teams " + strings.Join(u.TeamIDs, " ")
	}
	return out
}

func userOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
