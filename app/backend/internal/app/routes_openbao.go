package app

import (
	"context"
	"net/http"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/config"
)

func (a *App) registerOpenBao(mux *http.ServeMux) {
	a.route(mux, "GET /api/settings/openbao", "settings.openbao:view", check(a.openbao.Overview))
	a.route(mux, "POST /api/settings/openbao/test", "settings.openbao:test", check(a.openbao.Test))
	a.route(mux, "POST /api/settings/openbao/probe", "settings.openbao:migrate", submit(http.StatusOK, func(r *http.Request, in config.OpenBao) (OpenBaoProbe, error) {
		ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
		defer cancel()
		return a.openbao.Probe(ctx, in), nil
	}))
	a.route(mux, "POST /api/settings/openbao/migrate", "settings.openbao:migrate", submit(http.StatusOK, func(r *http.Request, in OpenBaoMigration) (OpenBaoMigrated, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), migrationTimeout)
		defer cancel()
		return a.openbao.Migrate(ctx, actorName(r), in)
	}))
}
