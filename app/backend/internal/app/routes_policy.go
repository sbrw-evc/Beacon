package app

import (
	"net/http"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

func (a *App) registerPolicy(mux *http.ServeMux) {
	a.route(mux, "GET /api/settings/password-policy", "settings.policy:view", show(a.policy.Summary))
	a.route(mux, "PUT /api/settings/password-policy", "settings.policy:edit", submit(http.StatusOK, func(r *http.Request, in model.PasswordPolicy) (PolicySummary, error) {
		return a.policy.Update(actorName(r), in)
	}))
}
