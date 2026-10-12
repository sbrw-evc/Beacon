package app

import (
	"context"
	"net/http"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
)

func (a *App) registerServices(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services", a.authed(a.can("services:view", a.listServices)))
	mux.HandleFunc("GET /api/services/{id}", a.authed(a.can("services:view", a.getService)))
	mux.HandleFunc("POST /api/services", a.authed(a.can("services:edit", a.createService)))
	mux.HandleFunc("PUT /api/services/{id}", a.authed(a.can("services:edit", a.updateService)))
	mux.HandleFunc("DELETE /api/services/{id}", a.authed(a.can("services:edit", a.deleteService)))
	mux.HandleFunc("POST /api/services/{id}/cis", a.authed(a.can("services:edit", a.bindCIs)))
	mux.HandleFunc("POST /api/services/bulk/cis", a.authed(a.can("services:edit", a.bulkCIs)))
	mux.HandleFunc("DELETE /api/services/{id}/cis/{ci}", a.authed(a.can("services:edit", a.unbindCI)))
	mux.HandleFunc("POST /api/services/{id}/dependencies", a.authed(a.can("services:edit", a.bindDependencies)))
	mux.HandleFunc("DELETE /api/services/{id}/dependencies/{dep}", a.authed(a.can("services:edit", a.unbindDependency)))
	mux.HandleFunc("POST /api/services/{id}/netbox", a.authed(a.can("services:edit", a.linkServiceNetBox)))
	mux.HandleFunc("DELETE /api/services/{id}/netbox", a.authed(a.can("services:edit", a.unlinkServiceNetBox)))
	mux.HandleFunc("GET /api/teams/{id}/services", a.authed(a.can("services:view", a.teamServices)))
}

func (a *App) listServices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	httpx.JSON(w, http.StatusOK, a.services.List(ServiceFilter{
		Query:       q.Get("q"),
		Team:        q.Get("team"),
		Owner:       q.Get("owner"),
		Descendants: q.Get("descendants") != "false",
		Criticality: q.Get("criticality"),
		Status:      q.Get("status"),
		Tag:         q.Get("tag"),
	}))
}

func (a *App) getService(w http.ResponseWriter, r *http.Request) {
	out, err := a.services.Get(r.PathValue("id"))
	reply(w, http.StatusOK, out, err)
}

func (a *App) createService(w http.ResponseWriter, r *http.Request) {
	var in ServiceInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.services.Create(current(r).user.Username, in)
	reply(w, http.StatusCreated, out, err)
}

func (a *App) updateService(w http.ResponseWriter, r *http.Request) {
	var in ServiceInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.services.Update(r.Context(), current(r).user.Username, r.PathValue("id"), in)
	reply(w, http.StatusOK, out, err)
}

func (a *App) deleteService(w http.ResponseWriter, r *http.Request) {
	if err := a.services.Delete(r.Context(), current(r).user.Username, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) teamServices(w http.ResponseWriter, r *http.Request) {
	out, err := a.services.ForTeam(r.PathValue("id"), r.URL.Query().Get("descendants") == "true")
	reply(w, http.StatusOK, out, err)
}

type serviceIDsInput struct {
	IDs []string `json:"ids"`
}

func (a *App) bindCIs(w http.ResponseWriter, r *http.Request) {
	var in serviceIDsInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.services.BindCIs(r.Context(), current(r).user.Username, r.PathValue("id"), in.IDs)
	reply(w, http.StatusOK, out, err)
}

func (a *App) unbindCI(w http.ResponseWriter, r *http.Request) {
	out, err := a.services.UnbindCI(r.Context(), current(r).user.Username, r.PathValue("id"), r.PathValue("ci"))
	reply(w, http.StatusOK, out, err)
}

func (a *App) bindDependencies(w http.ResponseWriter, r *http.Request) {
	var in serviceIDsInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.services.SetDependencies(current(r).user.Username, r.PathValue("id"), in.IDs, nil)
	reply(w, http.StatusOK, out, err)
}

func (a *App) unbindDependency(w http.ResponseWriter, r *http.Request) {
	out, err := a.services.SetDependencies(current(r).user.Username, r.PathValue("id"), nil, []string{r.PathValue("dep")})
	reply(w, http.StatusOK, out, err)
}

func (a *App) linkServiceNetBox(w http.ResponseWriter, r *http.Request) {
	out, err := a.services.LinkNetBox(r.Context(), current(r).user.Username, r.PathValue("id"))
	reply(w, http.StatusOK, out, err)
}

func (a *App) unlinkServiceNetBox(w http.ResponseWriter, r *http.Request) {
	out, err := a.services.UnlinkNetBox(r.Context(), current(r).user.Username, r.PathValue("id"))
	reply(w, http.StatusOK, out, err)
}

func (a *App) bulkCIs(w http.ResponseWriter, r *http.Request) {
	var in BulkCIsInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	// The batch finishes even if the browser stops waiting.
	out, err := a.services.BulkCIs(context.WithoutCancel(r.Context()), current(r).user.Username, in)
	reply(w, http.StatusOK, out, err)
}
