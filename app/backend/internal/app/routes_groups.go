package app

import (
	"net/http"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

func (a *App) registerGroups(mux *http.ServeMux) {
	a.route(mux, "GET /api/settings/groups", "settings.ldap:view", show(a.groups.View))
	a.route(mux, "PUT /api/settings/groups", "settings.ldap:edit", submit(http.StatusOK, func(r *http.Request, in model.GroupMappings) (GroupsView, error) {
		return a.groups.Save(actorName(r), in)
	}))
	a.route(mux, "POST /api/settings/groups/sync", "settings.ldap:edit", fetch(func(r *http.Request) (GroupsView, error) {
		return a.groups.Sync(r.Context(), actorName(r))
	}))
}
