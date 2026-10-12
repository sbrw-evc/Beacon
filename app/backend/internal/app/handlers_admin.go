package app

import (
	"context"
	"net/http"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
)

func (a *App) updateSettings(w http.ResponseWriter, r *http.Request) {
	var in Defaults
	if !httpx.Decode(w, r, &in) {
		return
	}
	s, err := a.settings.UpdateDefaults(current(r).user.Username, in)
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, defaultsOf(s))
}

func (a *App) system(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	httpx.JSON(w, http.StatusOK, a.status.Collect(ctx))
}
