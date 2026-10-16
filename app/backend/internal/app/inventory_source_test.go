package app_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/sbrw-evc/beacon/app/backend/internal/app"
	"github.com/sbrw-evc/beacon/app/backend/internal/ledger/ledgertest"
	"github.com/sbrw-evc/beacon/app/backend/internal/model"
	"github.com/sbrw-evc/beacon/app/backend/internal/netbox/netboxtest"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
)

// sourcesFixture is one Beacon with a NetBox and a Ledger it could read devices from.
type sourcesFixture struct {
	nb  netboxFixture
	inv inventoryFixture
}

func newSourcesFixture(t *testing.T) sourcesFixture {
	nb := newNetBoxFixture(t)
	inv := ledgertest.Start(t)
	inv.Set(ledgertest.Device(1, "srv-db-01", "10.0.0.1"), ledgertest.Device(4, "fw-edge-01", "10.0.0.4"))
	return sourcesFixture{nb: nb, inv: inventoryFixture{h: nb.h, inv: inv, admin: nb.admin}}
}

func (f sourcesFixture) netboxBody() map[string]any {
	return map[string]any{"config": map[string]any{"enabled": true, "url": f.nb.nb.URL, "sync_minutes": 60, "import_devices": true}, "token": netboxtest.Token}
}

func (f sourcesFixture) ledgerBody() map[string]any {
	return map[string]any{"config": f.inv.config(nil), "token": ledgertest.Token, "secret": ledgertest.Secret}
}

func TestInventorySourcesAreExclusive(t *testing.T) {
	f := newSourcesFixture(t)
	admin := f.nb.admin

	// NetBox on: Ledger cannot be connected.
	f.nb.connect(nil)
	var p map[string]any
	if code := admin.call(http.MethodPut, "/api/inventory-db", f.ledgerBody(), &p); code != http.StatusConflict || p["error"] != "inventory_db_netbox_active" {
		t.Fatalf("Ledger connected next to NetBox: %d %v", code, p)
	}
	var lv app.InventoryDBView
	admin.call(http.MethodGet, "/api/inventory-db", nil, &lv)
	if lv.Config.Enabled || lv.TokenSet || lv.Source.Source != app.SourceNetBox || lv.Source.NetBoxURL == "" {
		t.Fatalf("Ledger view = %+v", lv)
	}

	// NetBox off, Ledger on: NetBox cannot be turned on again.
	if code := admin.call(http.MethodPut, "/api/netbox", map[string]any{"config": map[string]any{"enabled": false}}, nil); code != 200 {
		t.Fatalf("NetBox off = %d", code)
	}
	f.inv.connect()
	p = nil
	if code := admin.call(http.MethodPut, "/api/netbox", f.netboxBody(), &p); code != http.StatusConflict || p["error"] != "netbox_ledger_active" {
		t.Fatalf("NetBox turned on next to Ledger: %d %v", code, p)
	}
	var nv app.NetBoxView
	admin.call(http.MethodGet, "/api/netbox", nil, &nv)
	if nv.Config.Enabled || nv.Source.Source != app.SourceLedger || nv.Source.LedgerNewDeviceURL != f.inv.inv.URL+"/dcim/devices?_sel=new" {
		t.Fatalf("NetBox view = %+v", nv)
	}
	// Turning NetBox off is always allowed.
	if code := admin.call(http.MethodPut, "/api/netbox", map[string]any{"config": map[string]any{"enabled": false}}, nil); code != 200 {
		t.Fatalf("NetBox off next to Ledger = %d", code)
	}
}

func TestBothSourcesAtStartKeepLedger(t *testing.T) {
	f := newSourcesFixture(t)
	admin, h := f.nb.admin, f.nb.h
	// An installation from before the rule: NetBox read its devices, then Ledger was connected too.
	f.nb.connect(nil)
	f.nb.sync()
	h.st.Write(func(d *store.Data) { d.Settings.NetBox.Enabled = false })
	f.inv.connect()
	h.st.Write(func(d *store.Data) { d.Settings.NetBox.Enabled = true })
	imported := len(f.nb.list("?source=netbox").Items)
	if imported == 0 {
		t.Fatal("nothing imported from NetBox")
	}

	if err := app.Migrate(context.Background(), h.st, h.vault); err != nil {
		t.Fatal(err)
	}
	h.st.Read(func(d *store.Data) {
		if d.Settings.NetBox.Enabled || !d.Settings.InventoryDB.Enabled || d.Settings.NetBoxPausedAt == nil {
			t.Fatalf("after start: netbox %v, ledger %v, paused %v", d.Settings.NetBox.Enabled, d.Settings.InventoryDB.Enabled, d.Settings.NetBoxPausedAt)
		}
		if d.Settings.NetBox.URL == "" || d.Settings.NetBox.TokenRef == "" {
			t.Fatal("the NetBox settings must be kept")
		}
		if !slices.ContainsFunc(d.Audit, func(e store.AuditEntry) bool {
			return e.Action == "settings.netbox" && strings.Contains(e.Detail, "Ledger is the inventory source")
		}) {
			t.Fatal("the pause is not in the audit log")
		}
	})
	// The items NetBox imported stay.
	if n := len(f.nb.list("?source=netbox").Items); n != imported {
		t.Fatalf("imported items = %d, want %d", n, imported)
	}
	// The status and the NetBox page say why NetBox is off.
	var sys app.SystemStatus
	if code := admin.call(http.MethodGet, "/api/system", nil, &sys); code != 200 || sys.InventorySource.Source != app.SourceLedger || sys.InventorySource.NetBoxPausedAt == nil {
		t.Fatalf("status = %d %+v", code, sys.InventorySource)
	}
	var nv app.NetBoxView
	admin.call(http.MethodGet, "/api/netbox", nil, &nv)
	if nv.Config.Enabled || nv.Source.NetBoxPausedAt == nil || !nv.TokenSet {
		t.Fatalf("NetBox view = %+v", nv)
	}
	// NetBox is not read; Ledger is.
	if code := admin.call(http.MethodPost, "/api/netbox/sync", nil, nil); code != http.StatusConflict {
		t.Fatalf("NetBox sync while paused = %d", code)
	}
	if st := f.inv.sync(""); st.Stats.Objects != 2 {
		t.Fatalf("Ledger sync = %+v", st.Stats)
	}
	// A second start changes nothing; turning NetBox off by hand clears the warning.
	if err := app.Migrate(context.Background(), h.st, h.vault); err != nil {
		t.Fatal(err)
	}
	var off app.NetBoxView
	if code := admin.call(http.MethodPut, "/api/netbox", map[string]any{"config": map[string]any{"enabled": false}}, &off); code != 200 || off.Source.NetBoxPausedAt != nil {
		t.Fatalf("NetBox off = %d %+v", code, off.Source)
	}
}

func TestLedgerSourceWritesNothingToNetBox(t *testing.T) {
	f := newSourcesFixture(t)
	admin := f.nb.admin
	// A local item registered in NetBox while NetBox was the source.
	f.nb.connect(map[string]any{"site_id": 1, "device_role_id": 2, "device_type_id": 3, "cluster_id": 4})
	var reg app.CIView
	if code := admin.call(http.MethodPost, "/api/cis", app.CIInput{Name: "new-switch", Kind: model.CIKindDevice, Register: true}, &reg); code != http.StatusCreated || reg.NetBox == nil {
		t.Fatalf("register = %d %+v", code, reg)
	}
	if code := admin.call(http.MethodPut, "/api/netbox", map[string]any{"config": map[string]any{"enabled": false}}, nil); code != 200 {
		t.Fatalf("NetBox off = %d", code)
	}
	f.inv.connect()
	before := len(f.nb.nb.Requests)

	// New devices are created in Ledger, not registered in NetBox.
	var p map[string]any
	if code := admin.call(http.MethodPost, "/api/cis", app.CIInput{Name: "fw-new", Kind: model.CIKindDevice, Register: true}, &p); code != http.StatusConflict || p["error"] != "ci_register_ledger" {
		t.Fatalf("register next to Ledger = %d %v", code, p)
	}
	var local app.CIView
	if code := admin.call(http.MethodPost, "/api/cis", app.CIInput{Name: "fw-new", Kind: model.CIKindDevice}, &local); code != http.StatusCreated || local.Registrable {
		t.Fatalf("local item = %d registrable %v", code, local.Registrable)
	}
	if code := admin.call(http.MethodPost, "/api/cis/"+local.ID+"/netbox", nil, &p); code != http.StatusConflict || p["error"] != "ci_register_ledger" {
		t.Fatalf("register button next to Ledger = %d %v", code, p)
	}
	var src app.InventorySource
	if code := admin.call(http.MethodGet, "/api/inventory-source", nil, &src); code != 200 || src.Source != app.SourceLedger || src.LedgerNewDeviceURL == "" {
		t.Fatalf("inventory source = %d %+v", code, src)
	}

	// The item linked to NetBox earlier changes and goes away in Beacon only.
	var upd app.CIView
	if code := admin.call(http.MethodPut, "/api/cis/"+reg.ID, app.CIInput{Name: "new-switch-2", Kind: model.CIKindDevice}, &upd); code != 200 || upd.Name != "new-switch-2" {
		t.Fatalf("update = %d %+v", code, upd)
	}
	if code := admin.call(http.MethodDelete, "/api/cis/"+reg.ID, nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete = %d", code)
	}
	if n := len(f.nb.nb.Requests); n != before {
		t.Fatalf("Beacon called NetBox while Ledger is the source: %v", f.nb.nb.Requests[before:])
	}
	if f.nb.nb.Count("dcim/devices") != 4 {
		t.Fatalf("NetBox devices = %d", f.nb.nb.Count("dcim/devices"))
	}
}
