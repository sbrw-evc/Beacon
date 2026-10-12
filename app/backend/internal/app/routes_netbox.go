package app

import (
	"context"
	"net/http"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
)

func (a *App) registerNetBox(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/netbox", a.authed(a.can("netbox:view", a.netboxView)))
	mux.HandleFunc("PUT /api/netbox", a.authed(a.can("netbox:edit", a.netboxSave)))
	mux.HandleFunc("POST /api/netbox/test", a.authed(a.can("netbox:test", a.netboxTest)))
	mux.HandleFunc("POST /api/netbox/sync", a.authed(a.can("netbox:sync", a.netboxSync)))
	mux.HandleFunc("GET /api/netbox/choices", a.authed(a.can("netbox:view", a.netboxChoices)))

	mux.HandleFunc("GET /api/cis", a.authed(a.can("cis:view", a.listCIs)))
	mux.HandleFunc("GET /api/cis/{id}", a.authed(a.can("cis:view", a.getCI)))
	mux.HandleFunc("POST /api/cis", a.authed(a.can("cis:edit", a.createCI)))
	mux.HandleFunc("PUT /api/cis/{id}", a.authed(a.can("cis:edit", a.updateCI)))
	mux.HandleFunc("DELETE /api/cis/{id}", a.authed(a.can("cis:edit", a.deleteCI)))
	mux.HandleFunc("POST /api/cis/{id}/netbox", a.authed(a.can("cis:edit", a.registerCI)))
	mux.HandleFunc("PUT /api/cis/{id}/aliases", a.authed(a.can("cis:edit", a.setCIAliases)))
}

func (a *App) netboxView(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, a.netbox.View())
}

func (a *App) netboxSave(w http.ResponseWriter, r *http.Request) {
	var in NetBoxRequest
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.netbox.Save(r.Context(), current(r).user.Username, in)
	reply(w, http.StatusOK, out, err)
}

func (a *App) netboxTest(w http.ResponseWriter, r *http.Request) {
	var in NetBoxRequest
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.netbox.Test(r.Context(), in)
	reply(w, http.StatusOK, out, err)
}

func (a *App) netboxSync(w http.ResponseWriter, r *http.Request) {
	// The synchronization finishes even if the browser stops waiting.
	sync := a.netbox.Sync
	// ?confirm=removal: the person saw the warning about missing items and removes them anyway.
	if r.URL.Query().Get("confirm") == "removal" {
		sync = a.netbox.SyncConfirmed
	}
	out, err := sync(context.WithoutCancel(r.Context()), current(r).user.Username)
	reply(w, http.StatusOK, out, err)
}

func (a *App) netboxChoices(w http.ResponseWriter, r *http.Request) {
	out, err := a.netbox.Choices(r.Context())
	reply(w, http.StatusOK, out, err)
}

func (a *App) listCIs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	httpx.JSON(w, http.StatusOK, a.cis.List(CIFilter{Query: q.Get("q"), Kind: q.Get("kind"), Source: q.Get("source"), Status: q.Get("status"),
		Owner: q.Get("owner"), Flag: q.Get("flag")}))
}

func (a *App) getCI(w http.ResponseWriter, r *http.Request) {
	out, err := a.cis.Get(r.PathValue("id"))
	reply(w, http.StatusOK, out, err)
}

func (a *App) createCI(w http.ResponseWriter, r *http.Request) {
	var in CIInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.cis.Create(r.Context(), current(r).user.Username, in)
	reply(w, http.StatusCreated, out, err)
}

func (a *App) updateCI(w http.ResponseWriter, r *http.Request) {
	var in CIInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.cis.Update(r.Context(), current(r).user.Username, r.PathValue("id"), in)
	reply(w, http.StatusOK, out, err)
}

func (a *App) registerCI(w http.ResponseWriter, r *http.Request) {
	out, err := a.cis.Register(r.Context(), current(r).user.Username, r.PathValue("id"))
	reply(w, http.StatusOK, out, err)
}

func (a *App) deleteCI(w http.ResponseWriter, r *http.Request) {
	if err := a.cis.Delete(r.Context(), current(r).user.Username, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
