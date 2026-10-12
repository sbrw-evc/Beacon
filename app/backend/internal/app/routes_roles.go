package app

import (
	"net/http"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
)

type membersInput struct {
	UserIDs []string `json:"user_ids"`
}

type roleDeleteInput struct {
	ReassignTo string `json:"reassign_to"`
}

func (a *App) registerRoles(mux *http.ServeMux) {
	a.route(mux, "GET /api/roles", "roles:view", show(a.roles.List))
	a.route(mux, "GET /api/roles/{id}", "roles:view", fetch(func(r *http.Request) (RoleView, error) {
		return a.roles.Get(r.PathValue("id"))
	}))
	a.route(mux, "POST /api/roles", "roles:edit", submit(http.StatusCreated, func(r *http.Request, in RoleInput) (RoleView, error) {
		return a.roles.Create(actorName(r), in)
	}))
	a.route(mux, "PUT /api/roles/{id}", "roles:edit", submit(http.StatusOK, func(r *http.Request, in RoleInput) (RoleView, error) {
		return a.roles.Update(actorName(r), r.PathValue("id"), in)
	}))
	a.route(mux, "DELETE /api/roles/{id}", "roles:edit", a.deleteRole)
	a.route(mux, "PUT /api/roles/{id}/members", "roles:edit", submit(http.StatusOK, func(r *http.Request, in membersInput) (RoleView, error) {
		return a.roles.AddMembers(current(r).user, r.PathValue("id"), in.UserIDs)
	}))
	a.route(mux, "PUT /api/roles/{id}/new-users", "roles:edit", fetch(func(r *http.Request) (RoleView, error) {
		return a.roles.SetNewUserRole(actorName(r), r.PathValue("id"))
	}))
}

// deleteRole takes the role that receives the members from the query or, failing that, from
// the body, which is optional.
func (a *App) deleteRole(w http.ResponseWriter, r *http.Request) {
	in := roleDeleteInput{ReassignTo: r.URL.Query().Get("reassign_to")}
	if in.ReassignTo == "" && r.ContentLength > 0 && !httpx.Decode(w, r, &in) {
		return
	}
	remove(func(r *http.Request) error {
		return a.roles.Delete(current(r).user, r.PathValue("id"), in.ReassignTo)
	})(w, r)
}
