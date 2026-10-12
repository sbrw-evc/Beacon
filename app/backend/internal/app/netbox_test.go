package app_test

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory/directorytest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/netbox/netboxtest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type netboxFixture struct {
	h     *harness
	nb    *netboxtest.Server
	admin *client
}

func device(id int, name, ip string) map[string]any {
	return map[string]any{"id": id, "name": name, "display": name, "status": map[string]any{"value": "active", "label": "Active"},
		"site": map[string]any{"id": 1, "name": "DC-1"}, "role": map[string]any{"id": 2, "name": "Server"},
		"device_type": map[string]any{"id": 3, "model": "R640"}, "primary_ip4": map[string]any{"address": ip + "/24"},
		"tags": []any{map[string]any{"id": 50, "name": "Prod", "slug": "prod"}}}
}

func contact(id int, name, email string) map[string]any {
	return map[string]any{"id": id, "name": name, "email": email, "title": "Engineer"}
}

func assign(id int, objectType string, objectID, contactID int, role string) map[string]any {
	return map[string]any{"id": id, "object_type": objectType, "object_id": objectID, "contact": map[string]any{"id": contactID},
		"role": map[string]any{"id": 1, "name": role}}
}

func newNetBoxFixture(t *testing.T) netboxFixture {
	h := newHarness(t)
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	nb := netboxtest.Start(t)
	nb.Put("extras/tags", map[string]any{"id": 50, "name": "Prod", "slug": "prod"})
	nb.Put("dcim/devices", device(1, "srv-db-01", "10.0.0.1"))
	nb.Put("dcim/devices", device(2, "srv-app-01", "10.0.0.2"))
	nb.Put("dcim/devices", device(3, "sw-core-01", "10.0.0.3"))
	nb.Put("virtualization/virtual-machines", map[string]any{"id": 7, "name": "vm-web-01", "status": map[string]any{"value": "planned"},
		"cluster": map[string]any{"id": 1, "name": "Prod cluster"}})
	nb.Put("ipam/services", map[string]any{"id": 9, "name": "postgres", "protocol": map[string]any{"value": "tcp"}, "ports": []any{5432},
		"device": map[string]any{"id": 1, "name": "srv-db-01"}})
	nb.Put("tenancy/contacts", contact(1, "Ivan Petrov", "ivan@example.org"))
	nb.Put("tenancy/contacts", contact(2, "Admin Person", "admin@example.org"))
	nb.Put("tenancy/contacts", contact(3, "Unassigned Person", "nobody@example.org"))
	nb.Put("tenancy/contact-assignments", assign(1, "dcim.device", 1, 1, "Owner"))
	nb.Put("tenancy/contact-assignments", assign(2, "dcim.device", 1, 2, "On duty"))
	nb.Put("tenancy/contact-assignments", assign(3, "ipam.service", 9, 1, "Owner"))
	// NetBox 3 names the field content_type.
	nb.Put("tenancy/contact-assignments", map[string]any{"id": 4, "content_type": "virtualization.virtualmachine", "object_id": 7,
		"contact": map[string]any{"id": 1}, "role": map[string]any{"id": 1, "name": "Owner"}})
	h.st.Write(func(d *store.Data) {
		for _, u := range d.Users {
			u.Email = "admin@example.org"
		}
	})
	admin := h.client()
	admin.login("admin", "Admin-pass-2026")
	return netboxFixture{h: h, nb: nb, admin: admin}
}

func (f netboxFixture) connect(extra map[string]any) {
	f.h.t.Helper()
	cfg := map[string]any{"enabled": true, "url": f.nb.URL + "/", "sync_minutes": 60, "import_devices": true, "import_vms": true,
		"import_services": true, "sync_contacts": true, "sync_directory": true, "create_users": true}
	for k, v := range extra {
		cfg[k] = v
	}
	var view app.NetBoxView
	if code := f.admin.call(http.MethodPut, "/api/netbox", map[string]any{"config": cfg, "token": netboxtest.Token}, &view); code != 200 || !view.TokenSet || !view.Config.Enabled {
		f.h.t.Fatalf("save = %d %+v", code, view)
	}
}

func (f netboxFixture) sync() app.NetBoxView {
	f.h.t.Helper()
	var st model.SyncState
	if code := f.admin.call(http.MethodPost, "/api/netbox/sync", nil, &st); code != 200 || !st.OK {
		f.h.t.Fatalf("sync = %d %+v", code, st)
	}
	var view app.NetBoxView
	f.admin.call(http.MethodGet, "/api/netbox", nil, &view)
	return view
}

func (f netboxFixture) list(query string) app.CIList {
	f.h.t.Helper()
	var out app.CIList
	if code := f.admin.call(http.MethodGet, "/api/cis"+query, nil, &out); code != 200 {
		f.h.t.Fatalf("list = %d", code)
	}
	return out
}

func (f netboxFixture) byName(name string) app.CIView {
	f.h.t.Helper()
	for _, ci := range f.list("").Items {
		if ci.Name == name {
			return ci
		}
	}
	f.h.t.Fatalf("no item %s", name)
	return app.CIView{}
}

func TestNetBoxSettings(t *testing.T) {
	f := newNetBoxFixture(t)
	var view app.NetBoxView
	if code := f.admin.call(http.MethodGet, "/api/netbox", nil, &view); code != 200 || view.Config.Enabled || view.TokenSet || view.Config.SyncMinutes != 60 {
		t.Fatalf("initial = %d %+v", code, view)
	}
	cfg := map[string]any{"enabled": true, "url": f.nb.URL}
	var problem map[string]any
	if code := f.admin.call(http.MethodPost, "/api/netbox/test", map[string]any{"config": cfg}, &problem); code != 400 || problem["error"] != "netbox_token_required" {
		t.Fatalf("test without token = %d %v", code, problem)
	}
	var report app.NetBoxTestReport
	if code := f.admin.call(http.MethodPost, "/api/netbox/test", map[string]any{"config": cfg, "token": "wrong"}, &report); code != 200 || report.OK || !strings.Contains(report.Error, "403") {
		t.Fatalf("wrong token = %d %+v", code, report)
	}
	if code := f.admin.call(http.MethodPost, "/api/netbox/test", map[string]any{"config": cfg, "token": netboxtest.Token}, &report); code != 200 || !report.OK ||
		report.Probe.Version != "4.1.3" || report.Probe.Devices != 3 || report.Probe.VMs != 1 || report.Probe.Services != 1 || report.Probe.Contacts != 3 {
		t.Fatalf("test = %d %+v", code, report)
	}
	if code := f.admin.call(http.MethodPut, "/api/netbox", map[string]any{"config": cfg, "token": "wrong"}, &problem); code != 400 || problem["error"] != "netbox_unavailable" {
		t.Fatalf("save with a wrong token = %d %v", code, problem)
	}
	f.connect(nil)
	if !slices.ContainsFunc(f.h.bao.Paths(), func(p string) bool { return strings.Contains(p, "netbox") }) {
		t.Fatalf("token is not in OpenBao: %v", f.h.bao.Paths())
	}
	// The stored token works for the same address and is never sent to another one.
	if code := f.admin.call(http.MethodPost, "/api/netbox/test", map[string]any{"config": cfg}, &report); code != 200 || !report.OK {
		t.Fatalf("test with the stored token = %d %+v", code, report)
	}
	other := map[string]any{"enabled": true, "url": "http://127.0.0.1:1"}
	if code := f.admin.call(http.MethodPost, "/api/netbox/test", map[string]any{"config": other}, &problem); code != 400 || problem["error"] != "netbox_token_required" {
		t.Fatalf("stored token for another address = %d %v", code, problem)
	}
	if code := f.admin.call(http.MethodPut, "/api/netbox", map[string]any{"config": map[string]any{"enabled": false}}, &view); code != 200 || view.Config.Enabled || !view.TokenSet {
		t.Fatalf("disable = %d %+v", code, view)
	}
	var st map[string]any
	if code := f.admin.call(http.MethodPost, "/api/netbox/sync", nil, &st); code != 409 || st["error"] != "netbox_off" {
		t.Fatalf("sync while off = %d %v", code, st)
	}
}

func TestNetBoxSync(t *testing.T) {
	f := newNetBoxFixture(t)
	f.connect(nil)
	view := f.sync()
	s := view.Sync.Stats
	if s.Objects != 5 || s.Created != 5 || s.Contacts != 2 || s.UsersCreated != 1 || s.UsersLinked != 1 || view.Items != 5 || view.Users != 1 {
		t.Fatalf("first sync = %+v (view %+v)", s, view)
	}
	db := f.byName("srv-db-01")
	if db.Source != model.SourceNetBox || db.Editable || db.Kind != model.CIKindDevice || db.Attrs.Site != "DC-1" || db.Attrs.DeviceType != "R640" ||
		!slices.Equal(db.IPs, []string{"10.0.0.1"}) || !slices.Equal(db.Tags, []string{"prod"}) || db.NetBox == nil || db.NetBox.URL != f.nb.URL+"/dcim/devices/1/" {
		t.Fatalf("device = %+v", db)
	}
	if len(db.Owners) != 2 {
		t.Fatalf("owners = %+v", db.Owners)
	}
	var ivan app.CIOwnerView
	for _, o := range db.Owners {
		if o.Email == "ivan@example.org" {
			ivan = o
		} else if o.Username != "admin" || o.Role != "On duty" {
			t.Fatalf("existing account must be linked by e-mail: %+v", o)
		}
	}
	if ivan.Username != "ivan@example.org" || ivan.Source != model.SourceNetBox || ivan.Role != "Owner" || ivan.Name != "Petrov Ivan" {
		t.Fatalf("contact account = %+v", ivan)
	}
	svc := f.byName("postgres")
	if svc.Kind != model.CIKindService || svc.Attrs.Parent != "srv-db-01" || svc.Attrs.Ports != "tcp/5432" || len(svc.Owners) != 1 {
		t.Fatalf("service = %+v", svc)
	}
	if vm := f.byName("vm-web-01"); vm.Kind != model.CIKindVM || vm.Status != "planned" || len(vm.Owners) != 1 || vm.Attrs.Cluster != "Prod cluster" {
		t.Fatalf("vm = %+v", vm)
	}
	if l := f.list("?flag=no_owners"); len(l.Items) != 2 || l.Summary.Total != 5 || l.Summary.NetBox != 5 || l.Summary.NoOwners != 2 {
		t.Fatalf("no owners = %+v", l.Summary)
	}
	if l := f.list("?q=10.0.0.2"); len(l.Items) != 1 || l.Items[0].Name != "srv-app-01" {
		t.Fatalf("search by IP = %+v", l.Items)
	}
	if l := f.list("?owner=" + ivan.ID); len(l.Items) != 3 {
		t.Fatalf("by owner = %d", len(l.Items))
	}

	// Nothing changed: nothing counts as updated.
	if s := f.sync().Sync.Stats; s.Created+s.Updated+s.Deleted+s.UsersCreated+s.UsersUpdated != 0 {
		t.Fatalf("idle sync = %+v", s)
	}

	// The contact is renamed, a device is gone and another changes in NetBox.
	f.nb.Put("tenancy/contacts", contact(1, "Petrov Ivan Sergeevich", "ivan@example.org"))
	f.nb.Remove("dcim/devices", 3)
	app01 := device(2, "srv-app-02", "10.0.0.2")
	f.nb.Put("dcim/devices", app01)
	s = f.sync().Sync.Stats
	if s.Deleted != 1 || s.Updated != 1 || s.UsersUpdated != 1 || s.Created != 0 {
		t.Fatalf("second sync = %+v", s)
	}
	var u model.User
	f.h.st.Read(func(d *store.Data) { u = *d.Users[ivan.ID] })
	if u.Name != "Petrov Ivan Sergeevich" || u.LastName != "Petrov" || u.MiddleName != "Sergeevich" || u.Source != model.SourceNetBox {
		t.Fatalf("account after rename = %+v", u)
	}
	if l := f.list(""); len(l.Items) != 4 || slices.ContainsFunc(l.Items, func(v app.CIView) bool { return v.Name == "sw-core-01" || v.Name == "srv-app-01" }) {
		t.Fatalf("after second sync = %+v", l.Items)
	}

	// Imported items change only in NetBox.
	var problem map[string]any
	if code := f.admin.call(http.MethodPut, "/api/cis/"+db.ID, app.CIInput{Name: "x", Kind: "device"}, &problem); code != 409 || problem["error"] != "ci_imported" {
		t.Fatalf("edit imported = %d %v", code, problem)
	}
	// Deleting one deletes it in NetBox too.
	if code := f.admin.call(http.MethodDelete, "/api/cis/"+f.byName("vm-web-01").ID, nil, nil); code != 204 {
		t.Fatalf("delete imported = %d", code)
	}
	if f.nb.Get("virtualization/virtual-machines", 7) != nil {
		t.Fatal("the VM is still in NetBox")
	}
	if s := f.sync().Sync.Stats; s.Created != 0 || s.Objects != 3 {
		t.Fatalf("deleted item came back: %+v", s)
	}
}

func TestLocalConfigItems(t *testing.T) {
	f := newNetBoxFixture(t)
	var adminID string
	f.h.st.Read(func(d *store.Data) { adminID = d.UserByName("admin").ID })
	var ci app.CIView
	in := app.CIInput{Name: "  backup   appliance ", Kind: "device", Description: "Rack 4", OwnerIDs: []string{adminID, adminID},
		IPs: []string{"10.1.0.5", " ", "fe80::1"}, Tags: []string{"Backup"}}
	if code := f.admin.call(http.MethodPost, "/api/cis", in, &ci); code != 201 || ci.Name != "backup appliance" || ci.Source != model.SourceLocal ||
		!ci.Editable || !ci.Registrable || ci.Status != "active" || len(ci.Owners) != 1 || !slices.Equal(ci.IPs, []string{"10.1.0.5", "fe80::1"}) || ci.NetBox != nil {
		t.Fatalf("create = %d %+v", code, ci)
	}
	var problem map[string]any
	for _, bad := range []app.CIInput{{Name: ""}, {Name: "x", Kind: "toaster"}, {Name: "x", IPs: []string{"10.0.0.300"}}, {Name: "x", OwnerIDs: []string{"USR-404"}},
		{Name: "x", Kind: "service", Register: true}} {
		if code := f.admin.call(http.MethodPost, "/api/cis", bad, &problem); code != 400 {
			t.Fatalf("create %+v = %d %v", bad, code, problem)
		}
	}
	// Registration needs a NetBox connection and the defaults for new devices.
	if code := f.admin.call(http.MethodPost, "/api/cis/"+ci.ID+"/netbox", nil, &problem); code != 409 || problem["error"] != "netbox_off" {
		t.Fatalf("register while off = %d %v", code, problem)
	}
	f.connect(nil)
	if code := f.admin.call(http.MethodPost, "/api/cis/"+ci.ID+"/netbox", nil, &problem); code != 400 || problem["error"] != "netbox_defaults" {
		t.Fatalf("register without defaults = %d %v", code, problem)
	}
	f.connect(map[string]any{"site_id": 1, "device_role_id": 2, "device_type_id": 3, "cluster_id": 4})
	if code := f.admin.call(http.MethodPost, "/api/cis/"+ci.ID+"/netbox", nil, &ci); code != 200 || ci.NetBox == nil || ci.NetBox.Kind != "device" || !ci.Editable || ci.Registrable {
		t.Fatalf("register = %d %+v", code, ci)
	}
	obj := f.nb.Get("dcim/devices", ci.NetBox.ID)
	if obj == nil || obj["name"] != "backup appliance" || obj["description"] != "Rack 4" {
		t.Fatalf("NetBox object = %v", obj)
	}
	// Changes of a registered item go to NetBox.
	in = app.CIInput{Name: "backup-01", Kind: "device", Status: "offline", OwnerIDs: []string{adminID}}
	if code := f.admin.call(http.MethodPut, "/api/cis/"+ci.ID, in, &ci); code != 200 || ci.Name != "backup-01" || ci.Status != "offline" {
		t.Fatalf("update = %d %+v", code, ci)
	}
	if obj := f.nb.Get("dcim/devices", ci.NetBox.ID); obj["name"] != "backup-01" || obj["status"].(map[string]any)["value"] != "offline" {
		t.Fatalf("NetBox after update = %v", obj)
	}
	in.Kind = "vm"
	if code := f.admin.call(http.MethodPut, "/api/cis/"+ci.ID, in, &problem); code != 400 || problem["error"] != "ci_kind_fixed" {
		t.Fatalf("kind change = %d %v", code, problem)
	}
	// A virtual machine created with registration goes to NetBox at once.
	var vm app.CIView
	if code := f.admin.call(http.MethodPost, "/api/cis", app.CIInput{Name: "vm-new", Kind: "vm", Register: true}, &vm); code != 201 || vm.NetBox == nil || vm.NetBox.Kind != "virtual-machine" {
		t.Fatalf("create registered vm = %d %+v", code, vm)
	}
	if obj := f.nb.Get("virtualization/virtual-machines", vm.NetBox.ID); obj == nil || obj["cluster"] == nil {
		t.Fatalf("NetBox VM = %v", obj)
	}
	// Sync keeps locally created items local and editable.
	f.sync()
	if got := f.byName("backup-01"); got.Source != model.SourceLocal || !got.Editable || got.ID != ci.ID {
		t.Fatalf("after sync = %+v", got)
	}
	// Deleting removes the item in NetBox too.
	if code := f.admin.call(http.MethodDelete, "/api/cis/"+ci.ID, nil, nil); code != 204 {
		t.Fatalf("delete = %d", code)
	}
	if f.nb.Get("dcim/devices", ci.NetBox.ID) != nil {
		t.Fatal("device is still in NetBox")
	}
	if code := f.admin.call(http.MethodGet, "/api/cis/"+ci.ID, nil, nil); code != 404 {
		t.Fatalf("get deleted = %d", code)
	}
	// An item deleted in NetBox by somebody else loses its link and stays here.
	f.nb.Remove("virtualization/virtual-machines", vm.NetBox.ID)
	if s := f.sync().Sync.Stats; s.Unlinked != 1 {
		t.Fatalf("unlink = %+v", s)
	}
	if got := f.byName("vm-new"); got.NetBox != nil || !got.Registrable {
		t.Fatalf("unlinked = %+v", got)
	}
}

func TestConfigItemsAccess(t *testing.T) {
	f := newNetBoxFixture(t)
	f.h.addLocal("viewer", "Viewer-pass-2026", model.RoleUser, time.Now())
	viewer := f.h.client()
	viewer.login("viewer", "Viewer-pass-2026")
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/cis"}, {http.MethodPost, "/api/cis"}, {http.MethodGet, "/api/netbox"}, {http.MethodPost, "/api/netbox/sync"},
	} {
		if code := viewer.call(c.method, c.path, map[string]any{}, nil); code != 403 {
			t.Fatalf("%s %s = %d", c.method, c.path, code)
		}
	}
}

func TestNetBoxDirectory(t *testing.T) {
	f := newNetBoxFixture(t)
	base := "dc=corp,dc=example"
	bind := "cn=umbrella,dc=corp,dc=example"
	srv := directorytest.Start(t,
		directorytest.Entry{DN: base},
		directorytest.Entry{DN: bind, Password: ldapPass},
		directorytest.Entry{DN: "cn=SRV-DB-01,ou=servers,dc=corp,dc=example", Attrs: map[string][]string{"objectClass": {"computer"}, "cn": {"SRV-DB-01"},
			"dNSHostName": {"srv-db-01.corp.example"}, "operatingSystem": {"Windows Server 2022"}, "userAccountControl": {"4098"},
			"lastLogonTimestamp": {"133700000000000000"}}},
		directorytest.Entry{DN: "cn=petr,dc=corp,dc=example", Password: "Petr-pass-2026", Attrs: map[string][]string{"objectClass": {"user"},
			"sAMAccountName": {"petr"}, "displayName": {"Petr Sidorov"}, "mail": {"petr@example.org"}}},
	)
	ref, err := f.h.vault.PutRef(context.Background(), "ldap", "bind_password", ldapPass)
	if err != nil {
		t.Fatal(err)
	}
	cfg := directory.Defaults(directory.KindAD)
	cfg.Enabled, cfg.URL, cfg.BindDN, cfg.BaseDN, cfg.BindPasswordRef, cfg.PhotoAttr = true, srv.URL, bind, base, ref, ""
	cfg.UserFilter = "(sAMAccountName={username})"
	f.h.st.Write(func(d *store.Data) { d.Settings.LDAP = cfg })
	f.nb.Put("tenancy/contacts", contact(4, "Petr Sidorov", "petr@example.org"))
	f.nb.Put("tenancy/contact-assignments", assign(5, "dcim.device", 2, 4, "Owner"))
	f.connect(nil)
	view := f.sync()
	if s := view.Sync.Stats; !view.DirectoryAvailable || !s.DirectoryChecked || s.DirectoryError != "" || s.DirectoryMatched != 1 || s.DirectoryMissing != 3 {
		t.Fatalf("directory sync = %+v", s)
	}
	db := f.byName("srv-db-01")
	if db.Directory == nil || db.Directory.Status != model.DirectoryMatched || db.Directory.OS != "Windows Server 2022" || !db.Directory.Disabled ||
		db.Directory.LastLogon == nil || db.Directory.LastLogon.Year() != 2024 {
		t.Fatalf("matched computer = %+v", db.Directory)
	}
	if svc := f.byName("postgres"); svc.Directory != nil {
		t.Fatalf("services are not computers: %+v", svc.Directory)
	}
	if l := f.list("?flag=directory_missing"); l.Summary.DirectoryMissing != 3 || len(l.Items) != 3 {
		t.Fatalf("missing = %+v", l.Summary)
	}

	// The person first known as a NetBox contact signs in with the directory and keeps the account.
	owner := f.byName("srv-app-01").Owners[0]
	if owner.Source != model.SourceNetBox || owner.Username != "petr@example.org" {
		t.Fatalf("contact account = %+v", owner)
	}
	petr := f.h.client()
	petr.login("petr", "Petr-pass-2026")
	var u model.User
	f.h.st.Read(func(d *store.Data) { u = *d.Users[owner.ID] })
	if u.Source != model.SourceLDAP || u.Username != "petr" {
		t.Fatalf("adopted account = %+v", u)
	}
	// Later synchronizations keep the account linked without overwriting it.
	if s := f.sync().Sync.Stats; s.UsersCreated != 0 || s.UsersUpdated != 0 {
		t.Fatalf("after adoption = %+v", s)
	}
	if got := f.byName("srv-app-01").Owners[0]; got.ID != owner.ID || got.Source != model.SourceLDAP {
		t.Fatalf("owner after adoption = %+v", got)
	}
}

func (f netboxFixture) syncState(query string) model.SyncState {
	f.h.t.Helper()
	var st model.SyncState
	if code := f.admin.call(http.MethodPost, "/api/netbox/sync"+query, nil, &st); code != 200 || !st.OK {
		f.h.t.Fatalf("sync = %d %+v", code, st)
	}
	return st
}

// syncBindings binds two NetBox items to a service and to monitoring hosts and returns a reader
// of those bindings.
func (f netboxFixture) syncBindings(db, sw app.CIView) func() ([]string, map[string]string) {
	f.h.st.Write(func(d *store.Data) {
		d.Services["SVC-9"] = &model.Service{ID: "SVC-9", Name: "Billing", Status: model.ServiceActive, CIIDs: []string{db.ID, sw.ID}}
		d.MonitoringSources["MON-9"] = &model.MonitoringSource{ID: "MON-9", Name: "Zabbix", Links: map[string]string{"sw-core": sw.ID, "db": db.ID}}
	})
	return func() ([]string, map[string]string) {
		var ids []string
		var links map[string]string
		f.h.st.Read(func(d *store.Data) {
			ids = slices.Clone(d.Services["SVC-9"].CIIDs)
			links = maps.Clone(d.MonitoringSources["MON-9"].Links)
		})
		return ids, links
	}
}

// An item gone from NetBox is removed like a manual deletion: no service or monitoring host keeps
// pointing at it.
func TestNetBoxSyncRemovalDropsBindings(t *testing.T) {
	f := newNetBoxFixture(t)
	f.connect(nil)
	f.sync()
	db, sw := f.byName("srv-db-01"), f.byName("sw-core-01")
	bindings := f.syncBindings(db, sw)

	f.nb.Remove("dcim/devices", 3)
	if st := f.syncState(""); st.Stats.Deleted != 1 || st.Stats.Held != 0 {
		t.Fatalf("one gone = %+v", st.Stats)
	}
	if ids, links := bindings(); !slices.Equal(ids, []string{db.ID}) || len(links) != 1 || links["db"] != db.ID {
		t.Fatalf("bindings of the deleted item must go: services %v, monitoring %v", ids, links)
	}
}

// An answer without all or most of the known objects removes nothing, leaves a warning in the sync
// state, and removes the items only when a person confirms it.
func TestNetBoxSyncKeepsItemsOnIncompleteAnswer(t *testing.T) {
	f := newNetBoxFixture(t)
	f.connect(nil)
	f.sync()
	db, sw := f.byName("srv-db-01"), f.byName("sw-core-01")
	bindings := f.syncBindings(db, sw)
	all := f.list("").Items

	// NetBox answers with nothing (a token that lost its permissions): everything stays.
	objects := map[string][]map[string]any{}
	for _, kind := range []string{"dcim/devices", "virtualization/virtual-machines", "ipam/services"} {
		for id := 1; id <= 20; id++ {
			if o := f.nb.Get(kind, id); o != nil {
				objects[kind] = append(objects[kind], o)
				f.nb.Remove(kind, id)
			}
		}
	}
	st := f.syncState("")
	if st.Stats.Objects != 0 || st.Stats.Deleted != 0 || st.Stats.Unlinked != 0 || st.Stats.Held != len(all) || st.Stats.HeldReason != "empty" {
		t.Fatalf("empty answer = %+v", st.Stats)
	}
	if got := f.byName("srv-db-01"); got.ID != db.ID || got.NetBox == nil {
		t.Fatalf("item after an empty answer = %+v", got)
	}
	if ids, links := bindings(); !slices.Equal(ids, []string{db.ID, sw.ID}) || len(links) != 2 {
		t.Fatalf("bindings after an empty answer: services %v, monitoring %v", ids, links)
	}
	var view app.NetBoxView
	f.admin.call(http.MethodGet, "/api/netbox", nil, &view)
	if view.Sync.Stats.Held != len(all) || view.Sync.Stats.HeldReason != "empty" {
		t.Fatalf("the warning must stay in the sync state: %+v", view.Sync)
	}

	// Back, but all except one are missing: more than half, kept as well.
	for kind, list := range objects {
		for _, o := range list {
			f.nb.Put(kind, o)
		}
	}
	for kind, list := range objects {
		for _, o := range list {
			if id := o["id"].(int); kind != "dcim/devices" || id != db.NetBox.ID {
				f.nb.Remove(kind, id)
			}
		}
	}
	if st := f.syncState(""); st.Stats.Objects != 1 || st.Stats.Deleted != 0 || st.Stats.Held != len(all)-1 || st.Stats.HeldReason != "share" {
		t.Fatalf("partial answer = %+v", st.Stats)
	}
	if got := f.byName("sw-core-01"); got.ID != sw.ID || got.NetBox == nil {
		t.Fatalf("item missing from a partial answer = %+v", got)
	}
	// The person confirms: they are removed, with their bindings.
	if st := f.syncState("?confirm=removal"); st.Stats.Deleted != len(all)-1 || st.Stats.Held != 0 {
		t.Fatalf("confirmed = %+v", st.Stats)
	}
	if l := f.list(""); len(l.Items) != 1 || l.Items[0].ID != db.ID {
		t.Fatalf("after the confirmed removal = %+v", l.Items)
	}
	if ids, links := bindings(); !slices.Equal(ids, []string{db.ID}) || len(links) != 1 {
		t.Fatalf("bindings after the confirmed removal: services %v, monitoring %v", ids, links)
	}
	f.h.st.Read(func(d *store.Data) {
		if d.NetBoxSync.Stats.Held != 0 {
			t.Fatalf("the warning must clear: %+v", d.NetBoxSync.Stats)
		}
	})
}
