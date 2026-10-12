package app

import (
	"errors"
	"net/http"
	"slices"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// noService is a service ID nothing has: the incident scope of a user whose scope has no service.
const noService = "-none-"

var (
	ErrOutOfScope    = errors.New("the targets are outside your scope")
	ErrScopeRequired = errors.New("choose configuration items, services or teams within your scope")
)

func init() {
	statuses = append(statuses,
		orgStatus{ErrOutOfScope, http.StatusForbidden, "out_of_scope"},
		orgStatus{ErrScopeRequired, http.StatusBadRequest, "scope_required"},
	)
}

// viewScope is what a user sees and may act on: every service, or the services in the set.
type viewScope struct {
	all      bool
	services map[string]bool
}

var everything = viewScope{all: true}

// scopeOf: administrators and users with ScopeAll see everything; ScopeTeams gives the services
// the user's teams own or support; ScopeServices the chosen services.
func scopeOf(d *store.Data, u *model.User) viewScope {
	if u == nil || d.IsAdmin(u) {
		return everything
	}
	switch u.ScopeMode {
	case model.ScopeTeams:
		teams := map[string]bool{}
		for _, t := range u.TeamIDs {
			teams[t] = true
		}
		out := viewScope{services: map[string]bool{}}
		for _, s := range d.Services {
			if s.Involves(teams) {
				out.services[s.ID] = true
			}
		}
		return out
	case model.ScopeServices:
		out := viewScope{services: map[string]bool{}}
		for _, id := range u.ServiceIDs {
			out.services[id] = true
		}
		return out
	case "":
		if len(u.ServiceIDs) > 0 {
			c := *u
			c.ScopeMode = model.ScopeServices
			return scopeOf(d, &c)
		}
	}
	return everything
}

// userScope reads the scope of a user from the store.
func (a *App) userScope(u model.User) viewScope {
	var out viewScope
	a.deps.Store.Read(func(d *store.Data) { out = scopeOf(d, &u) })
	return out
}

// ids is the scope as the alert engine takes it: nil for everything, never empty otherwise.
func (s viewScope) ids() []string {
	if s.all {
		return nil
	}
	out := make([]string, 0, len(s.services))
	for id := range s.services {
		out = append(out, id)
	}
	if len(out) == 0 {
		return []string{noService}
	}
	slices.Sort(out)
	return out
}

func (s viewScope) hasService(id string) bool { return s.all || s.services[id] }

// hasCI: an item is in the scope when every active or planned service it belongs to is, and it
// belongs to at least one; a maintenance window on a shared item would silence other services.
func (s viewScope) hasCI(d *store.Data, id string) bool {
	if s.all {
		return true
	}
	found := false
	for _, svc := range d.Services {
		if svc.Status == model.ServiceRetired || !slices.Contains(svc.CIIDs, id) {
			continue
		}
		if !s.services[svc.ID] {
			return false
		}
		found = true
	}
	return found
}

// hasTeam: a team is in the scope when it owns or supports at least one service and all of them
// are in the scope.
func (s viewScope) hasTeam(d *store.Data, id string) bool {
	if s.all {
		return true
	}
	ids := teamServiceIDs(d, []string{id})
	if len(ids) == 0 {
		return false
	}
	for _, sid := range ids {
		if !s.services[sid] {
			return false
		}
	}
	return true
}

// checkTargets: a limited user may only choose items, services and teams inside the scope and
// must choose at least one, because no targets means everything.
func (s viewScope) checkTargets(d *store.Data, cis, services, teams []string) error {
	if s.all {
		return nil
	}
	if len(cis)+len(services)+len(teams) == 0 {
		return ErrScopeRequired
	}
	for _, id := range cis {
		if !s.hasCI(d, id) {
			return ErrOutOfScope
		}
	}
	for _, id := range services {
		if !s.hasService(id) {
			return ErrOutOfScope
		}
	}
	for _, id := range teams {
		if !s.hasTeam(d, id) {
			return ErrOutOfScope
		}
	}
	return nil
}

// covers: an existing window or wallboard may be changed by the user only when all its targets
// are within the scope; one without targets covers everything.
func (s viewScope) covers(d *store.Data, cis, services, teams []string) error {
	if s.checkTargets(d, cis, services, teams) != nil {
		return ErrOutOfScope
	}
	return nil
}
