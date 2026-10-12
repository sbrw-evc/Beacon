package app

import (
	"context"
	"net/http"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/config"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	checkTimeout     = 30 * time.Second
	migrationTimeout = 5 * time.Minute
)

func (a *App) registerPostgres(mux *http.ServeMux) {
	a.route(mux, "GET /api/settings/postgres", "settings.postgres:view", check(a.postgres.Overview))
	a.route(mux, "POST /api/settings/postgres/test", "settings.postgres:test", check(a.postgres.Test))
	a.route(mux, "GET /api/settings/postgres/stats", "settings.postgres:view", fetch(func(r *http.Request) (store.PGStats, error) {
		ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
		defer cancel()
		stats, err := a.postgres.Stats(ctx)
		if err != nil {
			return stats, invalid("postgres_stats_failed", err)
		}
		return stats, nil
	}))
	a.route(mux, "POST /api/settings/postgres/probe", "settings.postgres:migrate", submit(http.StatusOK, func(r *http.Request, in config.PostgresTarget) (PostgresProbe, error) {
		ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
		defer cancel()
		return a.postgres.Probe(ctx, in), nil
	}))
	a.route(mux, "POST /api/settings/postgres/migrate", "settings.postgres:migrate", submit(http.StatusOK, func(r *http.Request, in PostgresMigration) (PostgresMigrated, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), migrationTimeout)
		defer cancel()
		return a.postgres.Migrate(ctx, actorName(r), in)
	}))
}
