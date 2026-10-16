package app

import (
	"net/http"
	"slices"
	"strings"

	"github.com/sbrw-evc/beacon/app/backend/internal/access"
	"github.com/sbrw-evc/beacon/app/backend/internal/httpx"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
)

type RoleRef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	System bool   `json:"system"`
}

type TeamRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
}

type UserRef struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Name     string   `json:"name"`
	Source   string   `json:"source"`
	Disabled bool     `json:"disabled"`
	RoleID   string   `json:"role_id"`
	TeamIDs  []string `json:"team_ids"`
}

type Refs struct {
	Roles    []RoleRef    `json:"roles"`
	Teams    []TeamRef    `json:"teams"`
	Users    []UserRef    `json:"users"`
	Services []ServiceRef `json:"services"`
	// NewUserRole is the role accounts created by a directory or NetBox get without a mapped role.
	NewUserRole string `json:"new_user_role"`
}

type CatalogView struct {
	Groups []access.Group `json:"groups"`
	Pages  []access.Page  `json:"pages"`
}

func (a *App) registerRefs(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/refs", a.authed(a.refs))
	mux.HandleFunc("GET /api/access/catalog", a.authed(a.catalog))
}

// refsAudience lists, per section of GET /api/refs, the permissions whose
// pages read that section. A caller gets a section when holding any of them;
// other sections come back as empty arrays so pages degrade instead of failing.
//
//	users    — Users (count), Teams and Roles (member pickers), CI editor and service accounts (owners)
//	roles    — Users (role filter/editor), LDAP/Entra group mapping, Service accounts
//	teams    — Users, Teams, Services (owner tree), Incidents (team filter),
//	           Alerting (PagerDuty routing), LDAP/Entra group mapping, Service accounts
//	services — Alerting (PagerDuty routing), Service accounts (scope)
var refsAudience = struct{ users, roles, teams, services []string }{
	users:    []string{"users:view", "teams:view", "roles:view", "cis:edit", "serviceaccounts:edit"},
	roles:    []string{"users:view", "roles:view", "settings.ldap:view", "serviceaccounts:view"},
	teams:    []string{"users:view", "teams:view", "services:view", "incidents:view", "settings.alerting:view", "settings.ldap:view", "serviceaccounts:view"},
	services: []string{"settings.alerting:view", "serviceaccounts:view"},
}

func hasAny(perms access.Set, want []string) bool {
	return slices.ContainsFunc(want, perms.Has)
}

func (a *App) refs(w http.ResponseWriter, r *http.Request) {
	perms := a.access.Permissions(current(r).user)
	out := a.collectRefs()
	if !hasAny(perms, refsAudience.users) {
		out.Users = []UserRef{}
	}
	if !hasAny(perms, refsAudience.roles) {
		out.Roles = []RoleRef{}
	}
	if !hasAny(perms, refsAudience.teams) {
		out.Teams = []TeamRef{}
	}
	if !hasAny(perms, refsAudience.services) {
		out.Services = []ServiceRef{}
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (a *App) catalog(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, CatalogView{Groups: access.Groups(), Pages: access.Pages()})
}

func byName(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) }

func (a *App) collectRefs() Refs {
	out := Refs{Roles: []RoleRef{}, Teams: []TeamRef{}, Users: []UserRef{}, Services: []ServiceRef{}}
	a.deps.Store.Read(func(d *store.Data) {
		out.NewUserRole = d.NewUserRole()
		for _, r := range d.Roles {
			out.Roles = append(out.Roles, RoleRef{ID: r.ID, Name: r.Name, System: r.System})
		}
		for _, t := range d.Teams {
			out.Teams = append(out.Teams, TeamRef{ID: t.ID, Name: t.Name, ParentID: t.ParentID})
		}
		for _, u := range d.Users {
			out.Users = append(out.Users, UserRef{ID: u.ID, Username: u.Username, Name: u.Profile.DisplayName(u.Username), Source: u.Source,
				Disabled: u.Disabled, RoleID: d.RoleOf(u).ID, TeamIDs: teamList(u.TeamIDs)})
		}
		for _, s := range d.Services {
			out.Services = append(out.Services, ServiceRef{ID: s.ID, Name: s.Name})
		}
	})
	slices.SortFunc(out.Roles, func(x, y RoleRef) int { return byName(x.Name, y.Name) })
	slices.SortFunc(out.Teams, func(x, y TeamRef) int { return byName(x.Name, y.Name) })
	slices.SortFunc(out.Users, func(x, y UserRef) int { return byName(x.Name, y.Name) })
	slices.SortFunc(out.Services, func(x, y ServiceRef) int { return byName(x.Name, y.Name) })
	return out
}
