package app

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/sbrw-evc/beacon/app/backend/internal/model"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
)

var (
	ErrUsernameTaken = errors.New("the username is already taken")
	ErrSelfAction    = errors.New("the action is not allowed on your own account")
	ErrAdminOnly     = errors.New("only administrators can manage administrators")
	// ErrStillInSPA: the person still has a role in Beacon in Keystone; the next synchronization
	// would create the account again. Take the role away in Keystone first.
	ErrStillInSPA = errors.New("the person still has access in Keystone")
)

func init() {
	statuses = append(statuses,
		orgStatus{ErrUsernameTaken, http.StatusConflict, "username_taken"},
		orgStatus{ErrSelfAction, http.StatusConflict, "self_action"},
		orgStatus{ErrAdminOnly, http.StatusForbidden, "admin_only"},
		orgStatus{ErrStillInSPA, http.StatusConflict, "still_in_spa"},
	)
}

const (
	userStatusActive = "active"
	userStatusLocked = "locked"

	maxUsername = 64
)

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

// UserChanges is what Beacon itself decides about a person: teams and the incident scope.
// Name, e-mail and role come from Keystone.
type UserChanges struct {
	// TeamIDs replaces the teams of the user.
	TeamIDs *[]string `json:"team_ids"`
	// ScopeMode: all, teams (services of the user's teams) or services (ServiceIDs).
	ScopeMode *string `json:"scope_mode"`
	// ServiceIDs replaces the chosen services. Given without ScopeMode, a non-empty list means
	// the services mode and an empty one all services, as before scope modes existed.
	ServiceIDs *[]string `json:"service_ids"`
}

const maxUserServices = 200

type UsersService struct {
	mu       sync.Mutex
	st       *store.Store
	sessions SessionRevoker
	now      func() time.Time
}

func NewUsersService(st *store.Store, sessions SessionRevoker) *UsersService {
	return &UsersService{st: st, sessions: sessions, now: func() time.Time { return time.Now().UTC() }}
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

func (s *UsersService) Update(actor Actor, id string, in UserChanges) (model.User, error) {
	return s.change(actor, id, "user.update", func(d *store.Data, u *model.User) (string, error) {
		var changes []string
		teams := u.TeamIDs
		if in.TeamIDs != nil {
			var err error
			if teams, err = userTeams(d, *in.TeamIDs); err != nil {
				return "", err
			}
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

// Delete removes the record of a person who no longer has access in Keystone (disabled), an account
// of the versions before Keystone that was never bound to Keystone, or a NetBox contact. People who still
// have a role in Beacon in Keystone are removed in Keystone.
func (s *UsersService) Delete(actor Actor, id string) error {
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
		if err := guardUserChange(d, actor, u, nil); err != nil {
			return nil, err
		}
		if u.Source == model.SourceSPA && !u.Disabled {
			return nil, ErrStillInSPA
		}
		return u, nil
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

// guardUserChange: only administrators change administrators (their teams and scope).
func guardUserChange(d *store.Data, actor Actor, before, after *model.User) error {
	if (d.IsAdmin(before) || d.IsAdmin(after)) && !d.IsAdmin(&actor.User) {
		return ErrAdminOnly
	}
	return nil
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
