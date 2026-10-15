package app

import (
	"net/http"
)

type membersInput struct {
	UserIDs []string `json:"user_ids"`
}

func (a *App) registerRoles(mux *http.ServeMux) {
	a.route(mux, "GET /api/roles", "roles:view", show(a.roles.List))
	a.route(mux, "GET /api/roles/{id}", "roles:view", fetch(func(r *http.Request) (RoleView, error) {
		return a.roles.Get(r.PathValue("id"))
	}))
	// The roles are the catalogue SPA gives out: every change is published to SPA.
	a.route(mux, "POST /api/roles", "roles:edit", submit(http.StatusCreated, func(r *http.Request, in RoleInput) (RoleView, error) {
		return published(a)(a.roles.Create(actorName(r), in))
	}))
	a.route(mux, "PUT /api/roles/{id}", "roles:edit", submit(http.StatusOK, func(r *http.Request, in RoleInput) (RoleView, error) {
		return published(a)(a.roles.Update(actorName(r), r.PathValue("id"), in))
	}))
	a.route(mux, "DELETE /api/roles/{id}", "roles:edit", remove(func(r *http.Request) error {
		err := a.roles.Delete(current(r).user, r.PathValue("id"))
		if err == nil {
			a.rolesChanged()
		}
		return err
	}))
	a.route(mux, "PUT /api/roles/{id}/new-users", "roles:edit", fetch(func(r *http.Request) (RoleView, error) {
		return published(a)(a.roles.SetNewUserRole(actorName(r), r.PathValue("id")))
	}))
}

// published publishes the catalogue after a change that succeeded.
func published(a *App) func(RoleView, error) (RoleView, error) {
	return func(v RoleView, err error) (RoleView, error) {
		if err == nil {
			a.rolesChanged()
		}
		return v, err
	}
}
