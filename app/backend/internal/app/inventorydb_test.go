package app_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/inventorydb/inventorydbtest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type inventoryFixture struct {
	h     *harness
	inv   *inventorydbtest.Server
	admin *client
}

func newInventoryFixture(t *testing.T) inventoryFixture {
	h := newHarness(t)
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	inv := inventorydbtest.Start(t)
	inv.Set(inventorydbtest.Device(1, "srv-db-01", "10.0.0.1"), inventorydbtest.Device(2, "srv-app-01", "10.0.0.2"),
		inventorydbtest.Device(3, "sw-core-01", "10.0.0.3"),
		map[string]any{"source_ref": "inventory-db:dcim.site:1", "type": "site", "name": "DC1", "status": "active"})
	admin := h.client()
	admin.login("admin", "Admin-pass-2026")
	return inventoryFixture{h: h, inv: inv, admin: admin}
}

func (f inventoryFixture) config(extra map[string]any) map[string]any {
	cfg := map[string]any{"enabled": true, "url": f.inv.URL + "/", "integration_id": inventorydbtest.Integration, "sync_minutes": 15,
		"import_devices": true, "push_status": true}
	for k, v := range extra {
		cfg[k] = v
	}
	return cfg
}

func (f inventoryFixture) connect() {
	f.h.t.Helper()
	var view app.InventoryDBView
	body := map[string]any{"config": f.config(nil), "token": inventorydbtest.Token, "secret": inventorydbtest.Secret}
	if code := f.admin.call(http.MethodPut, "/api/inventory-db", body, &view); code != 200 || !view.TokenSet || !view.SecretSet || !view.Config.Enabled {
		f.h.t.Fatalf("save = %d %+v", code, view)
	}
}

func (f inventoryFixture) sync(query string) model.SyncState {
	f.h.t.Helper()
	var st model.SyncState
	if code := f.admin.call(http.MethodPost, "/api/inventory-db/sync"+query, nil, &st); code != 200 || !st.OK {
		f.h.t.Fatalf("sync = %d %+v", code, st)
	}
	return st
}

func (f inventoryFixture) byName() map[string]*model.ConfigItem {
	out := map[string]*model.ConfigItem{}
	f.h.st.Read(func(d *store.Data) {
		for _, ci := range d.ConfigItems {
			c := *ci
			out[ci.Name] = &c
		}
	})
	return out
}

func TestInventoryDBIsOffByDefault(t *testing.T) {
	f := newInventoryFixture(t)
	var view app.InventoryDBView
	if code := f.admin.call(http.MethodGet, "/api/inventory-db", nil, &view); code != 200 || view.Config.Enabled || view.TokenSet {
		t.Fatalf("view = %d %+v", code, view)
	}
	if !view.Config.ImportDevices || !view.Config.PushStatus || view.Config.SyncMinutes != 15 {
		t.Fatalf("defaults = %+v", view.Config)
	}
	if code := f.admin.call(http.MethodPost, "/api/inventory-db/sync", nil, nil); code != http.StatusConflict {
		t.Fatalf("sync while off = %d", code)
	}
}

func TestInventoryDBSaveChecksConnection(t *testing.T) {
	f := newInventoryFixture(t)
	var report app.InventoryDBTestReport
	if code := f.admin.call(http.MethodPost, "/api/inventory-db/test", map[string]any{"config": f.config(nil), "token": inventorydbtest.Token}, &report); code != 200 || !report.OK || report.Probe.Items != 4 {
		t.Fatalf("test = %d %+v", code, report)
	}
	if code := f.admin.call(http.MethodPut, "/api/inventory-db", map[string]any{"config": f.config(nil), "token": "wrong", "secret": "s"}, nil); code != http.StatusBadRequest {
		t.Fatalf("wrong token saved: %d", code)
	}
	if code := f.admin.call(http.MethodPut, "/api/inventory-db", map[string]any{"config": f.config(nil), "token": inventorydbtest.Token}, nil); code != http.StatusBadRequest {
		t.Fatalf("saved without a signing secret: %d", code)
	}
	f.connect()
	if got := f.h.bao.Get("umbrella/inventory-db"); got["token"] != inventorydbtest.Token || got["signing_secret"] != inventorydbtest.Secret {
		t.Fatalf("secrets not in OpenBao: %v", f.h.bao.Paths())
	}
	// Saving again keeps the stored token and secret; another address needs them again.
	if code := f.admin.call(http.MethodPut, "/api/inventory-db", map[string]any{"config": f.config(map[string]any{"sync_minutes": 30})}, nil); code != 200 {
		t.Fatalf("resave = %d", code)
	}
	if code := f.admin.call(http.MethodPut, "/api/inventory-db", map[string]any{"config": f.config(map[string]any{"url": "http://127.0.0.1:1"})}, nil); code != http.StatusBadRequest {
		t.Fatalf("stored token sent to another address: %d", code)
	}
}

func TestInventoryDBSyncCreatesLinksAndRemovesItems(t *testing.T) {
	f := newInventoryFixture(t)
	// An item made by hand with the name of a device is linked, not duplicated.
	f.h.st.Write(func(d *store.Data) {
		d.ConfigItems["CI-9"] = &model.ConfigItem{ID: "CI-9", Name: "SW-CORE-01", Kind: model.CIKindDevice, Status: model.CIStatusActive, Source: model.SourceLocal}
	})
	f.connect()
	st := f.sync("")
	if s := st.Stats; s.Objects != 3 || s.Created != 2 || s.Linked != 1 {
		t.Fatalf("stats = %+v", s)
	}
	items := f.byName()
	db := items["srv-db-01"]
	if db == nil || db.Source != model.SourceInventoryDB || db.InventoryDB == nil || db.InventoryDB.Ref != "inventory-db:dcim.device:1" ||
		db.Attrs.Site != "DC1" || db.Attrs.Parent != "R01" || db.Attrs.DeviceType != "Dell R650" || db.Attrs.Serial != "SN1" ||
		len(db.IPs) != 1 || db.IPs[0] != "10.0.0.1" || db.InventoryDB.URL != "https://inventory.example/dcim/devices/1" {
		t.Fatalf("item = %+v %+v", db, db.InventoryDB)
	}
	if sw := items["SW-CORE-01"]; sw.Source != model.SourceLocal || sw.InventoryDB == nil || sw.InventoryDB.ID != 3 {
		t.Fatalf("linked = %+v", sw)
	}
	// Imported items are read-only.
	if code := f.admin.call(http.MethodPut, "/api/cis/"+db.ID, map[string]any{"name": "x", "kind": "device", "status": "active"}, nil); code == 200 {
		t.Fatal("an Inventory DB item was edited")
	}
	// A second run changes nothing.
	if s := f.sync("").Stats; s.Created+s.Updated+s.Linked+s.Deleted+s.Unlinked != 0 {
		t.Fatalf("second sync = %+v", s)
	}
	// Device 2 is gone: its item is deleted; device 3 is gone: the hand-made item is unlinked.
	f.inv.Set(inventorydbtest.Device(1, "srv-db-01", "10.0.0.9"))
	if s := f.sync("?confirm=removal").Stats; s.Deleted != 1 || s.Unlinked != 1 || s.Updated != 1 {
		t.Fatalf("removal = %+v", s)
	}
	items = f.byName()
	if items["srv-app-01"] != nil || items["SW-CORE-01"] == nil || items["SW-CORE-01"].InventoryDB != nil || items["srv-db-01"].IPs[0] != "10.0.0.9" {
		t.Fatalf("after removal: %v", items)
	}
}

func TestInventoryDBKeepsItemsWhenTheAnswerLooksEmpty(t *testing.T) {
	f := newInventoryFixture(t)
	f.connect()
	f.sync("")
	f.inv.Set()
	if s := f.sync("").Stats; s.Held != 3 || s.Deleted != 0 {
		t.Fatalf("held = %+v", s)
	}
	if len(f.byName()) != 3 {
		t.Fatal("items were removed")
	}
}

func TestInventoryDBNeedsPermissions(t *testing.T) {
	f := newInventoryFixture(t)
	f.h.addLocal("viewer", "Viewer-pass-2026", model.RoleUser, time.Now())
	c := f.h.client()
	c.login("viewer", "Viewer-pass-2026")
	if code := c.call(http.MethodPut, "/api/inventory-db", map[string]any{"config": f.config(nil)}, nil); code != http.StatusForbidden {
		t.Fatalf("user saved settings: %d", code)
	}
}
