package app

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/sbrw-evc/beacon/app/backend/internal/httpx"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
)

// Beacon reads devices from one inventory source. Inventory lives in Ledger and Beacon reads
// its devices as configuration items; the NetBox synchronization is for sites that run a real
// NetBox instead of Ledger. The two are never on together: turning one on while the other is on
// is refused, and an installation that had both keeps Ledger and pauses NetBox at start.

var (
	ErrLedgerIsSource = errors.New("Ledger is the inventory source: turn the Ledger connection off before turning NetBox on")
	ErrNetBoxIsSource = errors.New("NetBox is the inventory source: turn the NetBox synchronization off before connecting Ledger")
	// ErrRegisterInLedger: with Ledger as the source, devices are created in Ledger, not in NetBox.
	ErrRegisterInLedger = errors.New("devices are kept in Ledger: create the device there")
)

func init() {
	statuses = append(statuses,
		orgStatus{ErrLedgerIsSource, http.StatusConflict, "netbox_ledger_active"},
		orgStatus{ErrNetBoxIsSource, http.StatusConflict, "inventory_db_netbox_active"},
		orgStatus{ErrRegisterInLedger, http.StatusConflict, "ci_register_ledger"},
	)
}

const (
	SourceLedger = "ledger"
	SourceNetBox = "netbox"
)

// InventorySource is which system Beacon reads devices from, for the pages that create items.
type InventorySource struct {
	// Source: ledger, netbox or "" (none: items are made by hand).
	Source string `json:"source"`
	// LedgerURL is the address of Ledger while it is the source; LedgerNewDeviceURL opens its
	// form for a new device.
	LedgerURL          string `json:"ledger_url,omitempty"`
	LedgerNewDeviceURL string `json:"ledger_new_device_url,omitempty"`
	NetBoxURL          string `json:"netbox_url,omitempty"`
	// NetBoxPausedAt: NetBox was turned off at start because Ledger was on too.
	NetBoxPausedAt *time.Time `json:"netbox_paused_at,omitempty"`
}

func inventorySourceOf(d *store.Data) InventorySource {
	out := InventorySource{NetBoxPausedAt: d.Settings.NetBoxPausedAt}
	switch {
	case d.Settings.InventoryDB.Enabled:
		out.Source, out.LedgerURL = SourceLedger, d.Settings.InventoryDB.URL
		out.LedgerNewDeviceURL = ledgerNewDeviceURL(out.LedgerURL)
	case d.Settings.NetBox.Enabled:
		out.Source, out.NetBoxURL = SourceNetBox, d.Settings.NetBox.URL
	}
	return out
}

// ledgerNewDeviceURL is the devices list of Ledger with the form for a new device open. Ledger
// does not fill the form from the address, so the person types the name and address there.
func ledgerNewDeviceURL(base string) string {
	if base == "" {
		return ""
	}
	return base + "/dcim/devices?_sel=new"
}

// ledgerIsSource reports whether devices come from Ledger.
func ledgerIsSource(d *store.Data) bool { return d.Settings.InventoryDB.Enabled }

// pauseNetBoxForLedger keeps one inventory source in an installation that has both on: Ledger
// stays, NetBox is turned off with its settings, token and the items it imported kept.
func pauseNetBoxForLedger(d *store.Data, now time.Time) bool {
	if !d.Settings.NetBox.Enabled || !d.Settings.InventoryDB.Enabled {
		return false
	}
	d.Settings.NetBox.Enabled = false
	at := now
	d.Settings.NetBoxPausedAt = &at
	d.AddAudit(store.AuditEntry{Actor: "system", Action: "settings.netbox",
		Detail: "paused: Ledger is the inventory source, NetBox and Ledger are not read together"})
	slog.Warn("netbox: synchronization paused, Ledger is the inventory source")
	return true
}

func (a *App) registerInventorySource(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/inventory-source", a.authed(a.can("cis:view", a.inventorySource)))
}

func (a *App) inventorySource(w http.ResponseWriter, r *http.Request) {
	var out InventorySource
	a.deps.Store.Read(func(d *store.Data) { out = inventorySourceOf(d) })
	httpx.JSON(w, http.StatusOK, out)
}
