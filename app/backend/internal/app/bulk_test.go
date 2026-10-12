package app_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/monitoring/monitoringtest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func bulkResults[T any](items []T, key func(T) string, result func(T) string) map[string]string {
	out := map[string]string{}
	for _, it := range items {
		out[key(it)] = result(it)
	}
	return out
}

func TestBulkServiceCIs(t *testing.T) {
	f := newNetBoxFixture(t)
	f.connect(nil)
	f.sync()
	f.h.st.Write(func(d *store.Data) {
		now := time.Now()
		d.Teams["TEAM-1"] = &model.Team{ID: "TEAM-1", Name: "Payments", CreatedAt: now, UpdatedAt: now}
	})
	db, pg := f.byName("srv-db-01"), f.byName("postgres")
	local := addCI(f.h, "laptop-01", model.CIKindOther)

	var billing, shop app.ServiceView
	f.admin.call(http.MethodPost, "/api/services", app.ServiceInput{Name: "Billing", OwnerTeamID: "TEAM-1"}, &billing)
	f.admin.call(http.MethodPost, "/api/services", app.ServiceInput{Name: "Shop", OwnerTeamID: "TEAM-1"}, &shop)
	f.admin.call(http.MethodPost, "/api/services/"+billing.ID+"/netbox", nil, &billing)
	f.admin.call(http.MethodPost, "/api/services/"+shop.ID+"/cis", map[string]any{"ids": []string{local}}, &shop)
	slug := billing.NetBox.Slug

	key := func(it app.BulkCIItem) string { return it.ServiceID + "/" + it.CIID }
	res := func(it app.BulkCIItem) string { return it.Result }
	var out app.BulkCIsResult
	in := app.BulkCIsInput{ServiceIDs: []string{billing.ID, shop.ID}, CIIDs: []string{db.ID, pg.ID, local, db.ID}, Action: "bind"}
	if code := f.admin.call(http.MethodPost, "/api/services/bulk/cis", in, &out); code != 200 {
		t.Fatalf("bulk bind = %d", code)
	}
	got := bulkResults(out.Items, key, res)
	want := map[string]string{
		billing.ID + "/" + db.ID: app.BulkBound, billing.ID + "/" + pg.ID: app.BulkBound, billing.ID + "/" + local: app.BulkBound,
		shop.ID + "/" + db.ID: app.BulkBound, shop.ID + "/" + pg.ID: app.BulkBound, shop.ID + "/" + local: app.BulkAlready,
	}
	if len(out.Items) != 6 || out.Summary[app.BulkBound] != 5 || out.Summary[app.BulkAlready] != 1 {
		t.Fatalf("bulk bind = %+v", out)
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q, want %q", k, got[k], w)
		}
	}
	// The linked service tagged its NetBox objects.
	if tags := f.nb.Tagged("dcim/devices", 1); !slices.Contains(tags, slug) {
		t.Errorf("device tags = %v", tags)
	}
	if tags := f.nb.Tagged("ipam/services", 9); !slices.Equal(tags, []string{slug}) {
		t.Errorf("service tags = %v", tags)
	}

	// Unbinding takes the tag off; items never bound are reported as such.
	out = app.BulkCIsResult{}
	in = app.BulkCIsInput{ServiceIDs: []string{billing.ID}, CIIDs: []string{pg.ID, local}, Action: "unbind"}
	f.admin.call(http.MethodPost, "/api/services/bulk/cis", in, &out)
	if out.Summary[app.BulkUnbound] != 2 || len(f.nb.Tagged("ipam/services", 9)) != 0 {
		t.Fatalf("bulk unbind = %+v", out)
	}
	f.admin.call(http.MethodPost, "/api/services/bulk/cis", in, &out)
	if out.Summary[app.BulkNotBound] != 2 {
		t.Fatalf("unbind again = %+v", out)
	}

	// NetBox off: items kept in NetBox fail one by one, the local one is still bound.
	f.h.st.Write(func(d *store.Data) { d.Settings.NetBox.Enabled = false })
	out = app.BulkCIsResult{}
	in = app.BulkCIsInput{ServiceIDs: []string{billing.ID}, CIIDs: []string{pg.ID, local}, Action: "bind"}
	f.admin.call(http.MethodPost, "/api/services/bulk/cis", in, &out)
	got = bulkResults(out.Items, key, res)
	if got[billing.ID+"/"+pg.ID] != app.BulkFailed || out.Items[0].Error != "netbox_off" || got[billing.ID+"/"+local] != app.BulkBound {
		t.Fatalf("netbox off = %+v", out.Items)
	}
	var v app.ServiceView
	f.admin.call(http.MethodGet, "/api/services/"+billing.ID, nil, &v)
	if ids := ciIDs(v); slices.Contains(ids, pg.ID) || !slices.Contains(ids, local) {
		t.Fatalf("after partial = %v", ids)
	}

	for _, c := range []struct {
		in   app.BulkCIsInput
		code string
	}{
		{app.BulkCIsInput{ServiceIDs: []string{billing.ID}, CIIDs: []string{local}, Action: "swap"}, "bulk_action"},
		{app.BulkCIsInput{ServiceIDs: []string{"SVC-404"}, CIIDs: []string{local}, Action: "bind"}, "unknown_service"},
		{app.BulkCIsInput{ServiceIDs: []string{billing.ID}, CIIDs: []string{"CI-404"}, Action: "bind"}, "unknown_ci"},
		{app.BulkCIsInput{CIIDs: []string{local}, Action: "bind"}, "no_services"},
		{app.BulkCIsInput{ServiceIDs: []string{billing.ID}, Action: "bind"}, "no_cis"},
	} {
		var p map[string]any
		if code := f.admin.call(http.MethodPost, "/api/services/bulk/cis", c.in, &p); code != http.StatusBadRequest || p["error"] != c.code {
			t.Errorf("%+v = %d %v, want %s", c.in, code, p, c.code)
		}
	}
}

func TestBulkCreateCIsFromHosts(t *testing.T) {
	f := newMonitoringFixture(t)
	db := addHostCI(f.h, "srv-db-01.corp.local", "device", "active", []string{"10.0.0.1"}, "")
	f.zbx.AddHost(7, "kiosk-07", "Lobby kiosk", false, nil, monitoringtest.Iface("172.16.9.7", "", 1))
	f.prom.Target("node", "kiosk-07.corp.local:9100", true)
	f.sources()
	l := f.hosts("")
	kz, kp := byHost(l, "kiosk-07"), byHost(l, "kiosk-07.corp.local")
	lab, srv := byHost(l, "lab-pc"), byHost(l, "srv-db-01")
	if kz.Key == "" || kp.Key == "" || lab.Key == "" || srv.CI == nil {
		t.Fatalf("fixture: %+v %+v %+v %+v", kz, kp, lab, srv)
	}

	var out app.BulkHostsResult
	in := app.BulkHostsInput{Kind: "vm", Hosts: []app.HostKey{
		{SourceID: kp.SourceID, Key: kp.Key}, {SourceID: kz.SourceID, Key: kz.Key}, {SourceID: lab.SourceID, Key: lab.Key},
		{SourceID: srv.SourceID, Key: srv.Key}, {SourceID: lab.SourceID, Key: "nope"}, {SourceID: kz.SourceID, Key: kz.Key},
	}}
	f.expect(http.MethodPost, "/api/monitoring/ci/bulk", in, http.StatusOK, &out)
	if len(out.Items) != 5 {
		t.Fatalf("items = %+v", out.Items)
	}
	results := []string{}
	for _, it := range out.Items {
		results = append(results, it.Result)
	}
	if !slices.Equal(results, []string{app.BulkLinked, app.BulkCreated, app.BulkCreated, app.BulkMatched, app.BulkFailed}) {
		t.Fatalf("results = %v (%+v)", results, out.Items)
	}
	// One machine in both systems is one item, made from Zabbix whatever the order.
	if out.Items[0].CI.ID != out.Items[1].CI.ID || out.Items[3].CI.ID != db || out.Items[4].Error != "host_not_found" {
		t.Fatalf("items = %+v", out.Items)
	}
	if out.Items[1].SourceName != "Zabbix" || out.Items[1].Host != "Lobby kiosk" {
		t.Errorf("names = %+v", out.Items[1])
	}
	var ci app.CIView
	f.expect(http.MethodGet, "/api/cis/"+out.Items[0].CI.ID, nil, http.StatusOK, &ci)
	if ci.Name != "kiosk-07" || ci.Kind != "vm" || len(ci.Monitoring) != 2 {
		t.Fatalf("kiosk = %+v", ci)
	}
	if s := f.hosts("").Summary; s.Unmatched != l.Summary.Unmatched-3 {
		t.Errorf("unmatched %d -> %d", l.Summary.Unmatched, s.Unmatched)
	}

	// NetBox is off: registering fails per host, and the rest is not tried after a few in a row.
	f.zbx.AddHost(8, "pc-1", "", false, nil)
	f.zbx.AddHost(9, "pc-2", "", false, nil)
	f.zbx.AddHost(10, "pc-3", "", false, nil)
	f.zbx.AddHost(11, "pc-4", "", false, nil)
	f.expect(http.MethodPost, "/api/monitoring/sources/"+f.zsrc.ID+"/sync", nil, http.StatusOK, nil)
	l = f.hosts("?source=" + f.zsrc.ID)
	in = app.BulkHostsInput{Register: true}
	for _, h := range []string{"pc-1", "pc-2", "pc-3", "pc-4"} {
		v := byHost(l, h)
		in.Hosts = append(in.Hosts, app.HostKey{SourceID: v.SourceID, Key: v.Key})
	}
	out = app.BulkHostsResult{}
	f.expect(http.MethodPost, "/api/monitoring/ci/bulk", in, http.StatusOK, &out)
	if out.Summary[app.BulkFailed] != 3 || out.Summary[app.BulkSkipped] != 1 || out.Items[0].Error != "netbox_off" || out.Items[3].Error != "stopped" {
		t.Fatalf("netbox off = %+v", out)
	}

	var p map[string]any
	if code := f.admin.call(http.MethodPost, "/api/monitoring/ci/bulk", app.BulkHostsInput{}, &p); code != http.StatusBadRequest || p["error"] != "no_hosts" {
		t.Errorf("empty = %d %v", code, p)
	}
}
