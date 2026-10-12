package app

import (
	"context"
	"net/http"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
)

func (a *App) registerInventoryDB(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/inventory-db", a.authed(a.can("inventorydb:view", a.inventoryView)))
	mux.HandleFunc("PUT /api/inventory-db", a.authed(a.can("inventorydb:edit", a.inventorySave)))
	mux.HandleFunc("POST /api/inventory-db/test", a.authed(a.can("inventorydb:test", a.inventoryTest)))
	mux.HandleFunc("POST /api/inventory-db/sync", a.authed(a.can("inventorydb:sync", a.inventorySync)))
}

func (a *App) inventoryView(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, a.inventory.View())
}

func (a *App) inventorySave(w http.ResponseWriter, r *http.Request) {
	var in InventoryDBRequest
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.inventory.Save(r.Context(), current(r).user.Username, in)
	reply(w, http.StatusOK, out, err)
}

func (a *App) inventoryTest(w http.ResponseWriter, r *http.Request) {
	var in InventoryDBRequest
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.inventory.Test(r.Context(), in)
	reply(w, http.StatusOK, out, err)
}

func (a *App) inventorySync(w http.ResponseWriter, r *http.Request) {
	// The synchronization finishes even if the browser stops waiting.
	sync := a.inventory.Sync
	if r.URL.Query().Get("confirm") == "removal" {
		sync = a.inventory.SyncConfirmed
	}
	out, err := sync(context.WithoutCancel(r.Context()), current(r).user.Username)
	reply(w, http.StatusOK, out, err)
}
