package app

import (
	"context"
	"net/http"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory"
)

func (a *App) registerDirectory(mux *http.ServeMux) {
	a.route(mux, "GET /api/settings/ldap", "settings.ldap:view", show(a.directory.View))
	a.route(mux, "PUT /api/settings/ldap", "settings.ldap:edit", submit(http.StatusOK, func(r *http.Request, in directory.TestRequest) (DirectoryView, error) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		return a.directory.Save(ctx, actorName(r), in)
	}))
	a.route(mux, "POST /api/settings/ldap/test", "settings.ldap:test", submit(http.StatusOK, func(r *http.Request, in directory.TestRequest) (directory.TestReport, error) {
		return a.directory.Test(in)
	}))
}
