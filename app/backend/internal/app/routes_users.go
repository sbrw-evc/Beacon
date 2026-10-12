package app

import (
	"net/http"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type managedUserView struct {
	userView
	DisplayName string `json:"display_name"`
	// Teams names the teams of the user.
	Teams []userServiceRef `json:"teams"`
	// Services are the business services of the incident scope; Missing marks one deleted since.
	Services []userServiceRef `json:"services"`
	// NetBoxRecreates: a NetBox contact account that the next NetBox synchronization creates
	// again if it is deleted.
	NetBoxRecreates bool `json:"netbox_recreates,omitempty"`
}

type userServiceRef struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Missing bool   `json:"missing,omitempty"`
}

func (a *App) registerUsers(mux *http.ServeMux) {
	a.route(mux, "GET /api/users", "users:view", a.listUsers)
	a.route(mux, "GET /api/users/{id}", "users:view", fetch(func(r *http.Request) (managedUserView, error) {
		return a.managed(a.accounts.Get(r.PathValue("id")))
	}))
	a.route(mux, "POST /api/users", "users:create", submit(http.StatusCreated, func(r *http.Request, in NewUser) (managedUserView, error) {
		return a.managed(a.accounts.Create(r.Context(), actorOf(r), in))
	}))
	a.route(mux, "PUT /api/users/{id}", "users:edit", submit(http.StatusOK, func(r *http.Request, in UserChanges) (managedUserView, error) {
		return a.managed(a.accounts.Update(actorOf(r), r.PathValue("id"), in))
	}))
	a.route(mux, "POST /api/users/{id}/lock", "users:lock", a.lockUser(true))
	a.route(mux, "POST /api/users/{id}/unlock", "users:lock", a.lockUser(false))
	a.route(mux, "PUT /api/users/{id}/password", "users:password", submit(http.StatusOK, func(r *http.Request, in PasswordReset) (managedUserView, error) {
		return a.managed(a.accounts.SetPassword(r.Context(), actorOf(r), r.PathValue("id"), in))
	}))
	a.route(mux, "DELETE /api/users/{id}", "users:delete", remove(func(r *http.Request) error {
		return a.accounts.Delete(r.Context(), actorOf(r), r.PathValue("id"))
	}))
}

func actorOf(r *http.Request) Actor {
	s := current(r)
	return Actor{User: s.user, Session: s.ss.ID}
}

func (a *App) managedViews(users []model.User) []managedUserView {
	teams, services := map[string]string{}, map[string]string{}
	recreates := false
	a.deps.Store.Read(func(d *store.Data) {
		nb := d.Settings.NetBox
		recreates = nb.Enabled && nb.SyncContacts && nb.CreateUsers
		for id, t := range d.Teams {
			teams[id] = t.Name
		}
		for id, s := range d.Services {
			services[id] = s.Name
		}
	})
	out := make([]managedUserView, 0, len(users))
	for _, u := range users {
		v := a.view(u, "")
		v.Permissions, v.Admins = nil, nil
		teamRefs := make([]userServiceRef, 0, len(u.TeamIDs))
		for _, id := range u.TeamIDs {
			name, ok := teams[id]
			teamRefs = append(teamRefs, userServiceRef{ID: id, Name: userOr(name, id), Missing: !ok})
		}
		refs := make([]userServiceRef, 0, len(u.ServiceIDs))
		for _, id := range u.ServiceIDs {
			name, ok := services[id]
			refs = append(refs, userServiceRef{ID: id, Name: userOr(name, id), Missing: !ok})
		}
		out = append(out, managedUserView{userView: v, DisplayName: u.Profile.DisplayName(u.Username), Teams: teamRefs, Services: refs,
			NetBoxRecreates: recreates && u.Source == model.SourceNetBox})
	}
	return out
}

// managed is the view of the user a change returns, or its error.
func (a *App) managed(u model.User, err error) (managedUserView, error) {
	if err != nil {
		return managedUserView{}, err
	}
	return a.managedViews([]model.User{u})[0], nil
}

func (a *App) listUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	users := a.accounts.List(UserFilter{Query: q.Get("q"), Source: q.Get("source"), Role: q.Get("role"), Team: q.Get("team"), Status: q.Get("status")})
	httpx.JSON(w, http.StatusOK, a.managedViews(users))
}

func (a *App) lockUser(locked bool) http.HandlerFunc {
	return fetch(func(r *http.Request) (managedUserView, error) {
		return a.managed(a.accounts.SetLocked(actorOf(r), r.PathValue("id"), locked))
	})
}
