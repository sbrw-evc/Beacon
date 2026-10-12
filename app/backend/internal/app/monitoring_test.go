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

type monitoringFixture struct {
	h     *harness
	admin *client
	zbx   *monitoringtest.Zabbix
	prom  *monitoringtest.Prometheus
	zsrc  app.MonitoringSourceView
	psrc  app.MonitoringSourceView
}

func (f *monitoringFixture) expect(method, path string, body any, want int, out any) {
	f.h.t.Helper()
	var raw map[string]any
	target := out
	if target == nil {
		target = &raw
	}
	if code := f.admin.call(method, path, body, target); code != want {
		f.h.t.Fatalf("%s %s = %d, want %d (%v)", method, path, code, want, raw)
	}
}

func addHostCI(h *harness, name, kind, status string, ips []string, dns string) string {
	var id string
	h.st.Write(func(d *store.Data) {
		id = d.NextID("CI")
		ci := &model.ConfigItem{ID: id, Name: name, Kind: kind, Status: status, Source: model.SourceLocal, IPs: ips}
		if dns != "" {
			ci.Directory = &model.CIDirectory{Status: model.DirectoryMatched, DNSName: dns}
		}
		d.ConfigItems[id] = ci
	})
	return id
}

func newMonitoringFixture(t *testing.T) *monitoringFixture {
	h := newHarness(t)
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	f := &monitoringFixture{h: h, admin: h.client()}
	f.admin.login("admin", "Admin-pass-2026")

	f.zbx = monitoringtest.StartZabbix(t, "7.0.5")
	// By technical name, by the DNS name of the domain, by IP, a short name, an unknown host and
	// a name two items share.
	f.zbx.AddHost(1, "srv-db-01", "Database", false, []string{"Linux servers"}, monitoringtest.Iface("10.0.0.1", "", 1))
	f.zbx.AddHost(2, "APP01", "App", false, nil, monitoringtest.Iface("", "app01.corp.local", 1))
	f.zbx.AddHost(3, "switch", "Core switch", false, nil, monitoringtest.Iface("10.0.0.3", "", 2))
	f.zbx.AddHost(4, "srv-web-01", "", false, nil, monitoringtest.Iface("192.168.7.7", "", 1))
	f.zbx.AddHost(5, "lab-pc", "", false, nil, monitoringtest.Iface("172.16.0.9", "", 1))
	f.zbx.AddHost(6, "dup", "", false, nil)
	f.prom = monitoringtest.StartPrometheus(t)
	f.prom.Target("node", "srv-db-01.corp.local:9100", true)
	f.prom.Target("node", "10.0.0.50:9100", false)
	f.prom.Target("node", "10.0.0.3:9100", true)
	return f
}

func (f *monitoringFixture) credential(name, typ string, fields, secrets map[string]string) string {
	var out app.CredentialView
	f.expect(http.MethodPost, "/api/credentials", app.CredentialInput{Name: name, Type: typ, Fields: fields, Secrets: secrets}, http.StatusCreated, &out)
	return out.ID
}

func (f *monitoringFixture) sources() {
	token := f.credential("Zabbix token", "bearer", nil, map[string]string{"token": "zbx-token"})
	f.expect(http.MethodPost, "/api/monitoring/sources", app.MonitoringSourceInput{Name: "Zabbix", Kind: "zabbix", URL: f.zbx.URL, CredentialID: token, Enabled: true, SyncMinutes: 15},
		http.StatusCreated, &f.zsrc)
	f.expect(http.MethodPost, "/api/monitoring/sources", app.MonitoringSourceInput{Name: "Prometheus", Kind: "prometheus", URL: f.prom.URL, Enabled: true},
		http.StatusCreated, &f.psrc)
	for _, id := range []string{f.zsrc.ID, f.psrc.ID} {
		var st model.MonitoringSync
		f.expect(http.MethodPost, "/api/monitoring/sources/"+id+"/sync", nil, http.StatusOK, &st)
		if !st.OK {
			f.h.t.Fatalf("sync %s: %+v", id, st)
		}
	}
}

func (f *monitoringFixture) hosts(query string) app.HostList {
	var out app.HostList
	f.expect(http.MethodGet, "/api/monitoring/hosts"+query, nil, http.StatusOK, &out)
	return out
}

func byHost(l app.HostList, host string) app.HostView {
	for _, v := range l.Items {
		if v.Host == host {
			return v
		}
	}
	return app.HostView{}
}

func TestMonitoringMatchesHostsWithCIs(t *testing.T) {
	f := newMonitoringFixture(t)
	db := addHostCI(f.h, "srv-db-01.corp.local", "device", "active", []string{"10.0.0.1"}, "")
	app01 := addHostCI(f.h, "APP01", "vm", "active", nil, "app01.corp.local")
	sw := addHostCI(f.h, "sw-core", "device", "active", []string{"10.0.0.3"}, "")
	web := addHostCI(f.h, "srv-web-01.dmz.local", "device", "active", nil, "")
	addHostCI(f.h, "dup", "device", "active", nil, "")
	addHostCI(f.h, "dup", "vm", "active", nil, "")
	lonely := addHostCI(f.h, "srv-file-01", "device", "active", nil, "")
	addHostCI(f.h, "srv-new-01", "device", "planned", nil, "")
	addHostCI(f.h, "payroll", "service", "active", nil, "")

	var list app.CIList
	f.expect(http.MethodGet, "/api/cis", nil, http.StatusOK, &list)
	if list.Summary.MonitoringSources != 0 || list.Summary.NotMonitored != 0 {
		t.Fatalf("without sources coverage is not judged: %+v", list.Summary)
	}

	// One machine both systems see, unknown to the catalog.
	f.zbx.AddHost(7, "kiosk-07", "Lobby kiosk", false, nil, monitoringtest.Iface("172.16.9.7", "", 1))
	f.prom.Target("node", "kiosk-07.corp.local:9100", true)
	f.h.st.Write(func(d *store.Data) { d.Settings.NetBox.Enabled = true })

	f.sources()
	l := f.hosts("")
	if l.Summary.Total != 11 {
		t.Fatalf("hosts: %+v", l.Summary)
	}
	want := map[string][2]string{
		"srv-db-01":  {db, app.MatchShort},
		"APP01":      {app01, app.MatchName},
		"switch":     {sw, app.MatchIP},
		"srv-web-01": {web, app.MatchShort},
		"lab-pc":     {"", ""},
		"dup":        {"", app.MatchAmbiguous},
	}
	for host, w := range want {
		v := byHost(l, host)
		got := ""
		if v.CI != nil {
			got = v.CI.ID
		}
		if got != w[0] || v.Match != w[1] {
			t.Errorf("%s: matched %q by %q, want %q by %q", host, got, v.Match, w[0], w[1])
		}
	}
	if v := byHost(l, "dup"); len(v.Candidates) != 2 {
		t.Errorf("an ambiguous host lists its candidates: %+v", v.Candidates)
	}
	if v := byHost(l, "srv-db-01.corp.local"); v.CI == nil || v.CI.ID != db || v.SourceID != f.psrc.ID || v.State != model.HostUp {
		t.Errorf("prometheus host: %+v", v)
	}
	if v := byHost(l, "10.0.0.3"); v.CI == nil || v.CI.ID != sw || v.Match != app.MatchIP {
		t.Errorf("a host named by its IP address: %+v", v)
	}
	if un := f.hosts("?match=unmatched"); un.Summary.Unmatched != 5 || len(un.Items) != 5 {
		t.Errorf("unmatched: %+v", un)
	}

	var ci app.CIView
	f.expect(http.MethodGet, "/api/cis/"+db, nil, http.StatusOK, &ci)
	if len(ci.Monitoring) != 2 || ci.NotMonitored {
		t.Errorf("db coverage: %+v", ci.Monitoring)
	}
	// Where the item is and where it is not: NetBox is connected but the item is local.
	states := map[string]string{}
	for _, p := range ci.Presence {
		states[p.Name] = p.State
	}
	if len(ci.Presence) != 3 || states["NetBox"] != "missing" || states["Zabbix"] != "present" || states["Prometheus"] != "present" {
		t.Errorf("db presence: %+v", ci.Presence)
	}
	f.expect(http.MethodGet, "/api/cis/"+lonely, nil, http.StatusOK, &ci)
	states = map[string]string{}
	for _, p := range ci.Presence {
		states[p.Name] = p.State
	}
	if states["Zabbix"] != "missing" || states["Prometheus"] != "missing" {
		t.Errorf("lonely presence: %+v", ci.Presence)
	}

	// The kiosk is one machine: creating its item from Zabbix links the Prometheus host too.
	kiosk := byHost(l, "kiosk-07")
	if len(kiosk.AlsoIn) != 1 || kiosk.AlsoIn[0].SourceID != f.psrc.ID {
		t.Fatalf("same machine: %+v", kiosk.AlsoIn)
	}
	var kci app.CIView
	f.expect(http.MethodPost, "/api/monitoring/ci", app.CreateCIInput{SourceID: kiosk.SourceID, Key: kiosk.Key}, http.StatusCreated, &kci)
	if len(kci.Monitoring) != 2 {
		t.Fatalf("one item for both systems: %+v", kci.Monitoring)
	}
	f.expect(http.MethodGet, "/api/cis?flag=not_monitored", nil, http.StatusOK, &list)
	ids := []string{}
	for _, it := range list.Items {
		ids = append(ids, it.ID)
	}
	// Planned items and services are not expected to be monitored; the two "dup" items are.
	if list.Summary.NotMonitored != 3 || !slices.Contains(ids, lonely) || slices.Contains(ids, db) || list.Summary.MonitoringSources != 2 {
		t.Errorf("not monitored: %v %+v", ids, list.Summary)
	}

	// Linking by hand: lab-pc to srv-file-01, switch to nothing, then back.
	lab := byHost(l, "lab-pc")
	var v app.HostView
	f.expect(http.MethodPost, "/api/monitoring/link", app.LinkInput{SourceID: lab.SourceID, Key: lab.Key, Mode: "ci", CIID: lonely}, http.StatusOK, &v)
	if v.CI == nil || v.CI.ID != lonely || v.Match != app.MatchManual {
		t.Fatalf("manual link: %+v", v)
	}
	swh := byHost(l, "switch")
	v = app.HostView{}
	f.expect(http.MethodPost, "/api/monitoring/link", app.LinkInput{SourceID: swh.SourceID, Key: swh.Key, Mode: "none"}, http.StatusOK, &v)
	if v.CI != nil || v.Match != app.MatchExcluded {
		t.Fatalf("excluded: %+v", v)
	}
	v = app.HostView{}
	f.expect(http.MethodGet, "/api/cis/"+sw, nil, http.StatusOK, &ci)
	if len(ci.Monitoring) != 1 || ci.Monitoring[0].Kind != "prometheus" {
		t.Errorf("an excluded host does not cover the item: %+v", ci.Monitoring)
	}
	f.expect(http.MethodPost, "/api/monitoring/link", app.LinkInput{SourceID: swh.SourceID, Key: swh.Key, Mode: "auto"}, http.StatusOK, &v)
	if v.CI == nil || v.CI.ID != sw {
		t.Fatalf("back to automatic: %+v", v)
	}
	if code := f.admin.call(http.MethodPost, "/api/monitoring/link", app.LinkInput{SourceID: swh.SourceID, Key: "nope", Mode: "auto"}, nil); code != http.StatusNotFound {
		t.Errorf("unknown host: %d", code)
	}

	// A host nobody knows becomes a configuration item.
	probe := byHost(l, "10.0.0.50")
	var made app.CIView
	f.expect(http.MethodPost, "/api/monitoring/ci", app.CreateCIInput{SourceID: probe.SourceID, Key: probe.Key, Kind: "vm"}, http.StatusCreated, &made)
	if made.Name != "10.0.0.50" || made.Kind != "vm" || !slices.Equal(made.IPs, []string{"10.0.0.50"}) || len(made.Monitoring) != 1 || made.Monitoring[0].Match != app.MatchManual {
		t.Fatalf("created: %+v", made)
	}

	// Disabled sources do not count.
	f.expect(http.MethodPut, "/api/monitoring/sources/"+f.psrc.ID, app.MonitoringSourceInput{Name: "Prometheus", Kind: "prometheus", URL: f.prom.URL, Enabled: false}, http.StatusOK, nil)
	f.expect(http.MethodGet, "/api/cis/"+db, nil, http.StatusOK, &ci)
	if len(ci.Monitoring) != 1 || ci.Monitoring[0].Kind != "zabbix" {
		t.Errorf("disabled source still covers: %+v", ci.Monitoring)
	}

	// A deleted item takes its hand-made links with it.
	f.expect(http.MethodDelete, "/api/cis/"+lonely, nil, http.StatusNoContent, nil)
	if v := byHost(f.hosts(""), "lab-pc"); v.CI != nil || v.Match != "" {
		t.Errorf("link to a deleted item: %+v", v)
	}
}

func TestMonitoringSourcesAndErrors(t *testing.T) {
	f := newMonitoringFixture(t)
	f.prom.Token = "secret"
	var problem map[string]any
	if code := f.admin.call(http.MethodPost, "/api/monitoring/sources", app.MonitoringSourceInput{Name: "Z", Kind: "zabbix", URL: f.zbx.URL}, &problem); code != http.StatusBadRequest ||
		problem["error"] != "monitoring_credential_required" {
		t.Errorf("zabbix without a credential: %d %v", code, problem)
	}
	if code := f.admin.call(http.MethodPost, "/api/monitoring/sources", app.MonitoringSourceInput{Name: "X", Kind: "nagios", URL: f.zbx.URL}, &problem); code != http.StatusBadRequest {
		t.Errorf("unknown kind: %d", code)
	}
	hmac := f.credential("HMAC", "hmac", nil, map[string]string{"secret": "x"})
	if code := f.admin.call(http.MethodPost, "/api/monitoring/sources", app.MonitoringSourceInput{Name: "P", Kind: "prometheus", URL: f.prom.URL, CredentialID: hmac}, &problem); code != http.StatusBadRequest ||
		problem["error"] != "credential_type" {
		t.Errorf("hmac credential: %d %v", code, problem)
	}

	pw := f.credential("Zabbix admin", "basic", map[string]string{"username": "Admin"}, map[string]string{"password": "zabbix"})
	var rep app.MonitoringTestReport
	f.expect(http.MethodPost, "/api/monitoring/test", app.MonitoringSourceInput{Name: "Z", Kind: "zabbix", URL: f.zbx.URL, CredentialID: pw}, http.StatusOK, &rep)
	if !rep.OK || rep.Version != "7.0.5" || rep.Hosts != 6 || len(rep.Sample) != 5 {
		t.Errorf("test with a password: %+v", rep)
	}
	f.expect(http.MethodPost, "/api/monitoring/test", app.MonitoringSourceInput{Name: "P", Kind: "prometheus", URL: f.prom.URL}, http.StatusOK, &rep)
	if rep.OK || rep.Error == "" {
		t.Errorf("prometheus without its token: %+v", rep)
	}

	// A failed reading is kept with the error, and the hosts read before stay.
	var src app.MonitoringSourceView
	f.expect(http.MethodPost, "/api/monitoring/sources", app.MonitoringSourceInput{Name: "Zabbix", Kind: "zabbix", URL: f.zbx.URL, CredentialID: pw, Enabled: true}, http.StatusCreated, &src)
	var st model.MonitoringSync
	f.expect(http.MethodPost, "/api/monitoring/sources/"+src.ID+"/sync", nil, http.StatusOK, &st)
	if !st.OK || st.Hosts != 6 {
		t.Fatalf("sync: %+v", st)
	}
	f.zbx.Password = "changed"
	f.expect(http.MethodPost, "/api/monitoring/sources/"+src.ID+"/sync", nil, http.StatusOK, &st)
	if st.OK || st.Error == "" {
		t.Fatalf("sync with a wrong password: %+v", st)
	}
	var view app.MonitoringView
	f.expect(http.MethodGet, "/api/monitoring", nil, http.StatusOK, &view)
	if len(view.Sources) != 1 || view.Sources[0].Hosts != 6 || view.Sources[0].Sync.OK || view.Sources[0].CredentialName != "Zabbix admin" || view.Sources[0].Unmatched != 6 {
		t.Errorf("view: %+v", view.Sources)
	}

	// The credential is in use; another address forgets the hosts.
	if code := f.admin.call(http.MethodDelete, "/api/credentials/"+pw, nil, &problem); code != http.StatusConflict {
		t.Errorf("deleting a credential in use: %d", code)
	}
	f.expect(http.MethodPut, "/api/monitoring/sources/"+src.ID, app.MonitoringSourceInput{Name: "Zabbix", Kind: "zabbix", URL: f.zbx.URL + "/other", CredentialID: pw, Enabled: true}, http.StatusOK, &src)
	if src.Hosts != 0 {
		t.Errorf("hosts of the old address: %d", src.Hosts)
	}
	f.expect(http.MethodDelete, "/api/monitoring/sources/"+src.ID, nil, http.StatusNoContent, nil)
	f.expect(http.MethodDelete, "/api/credentials/"+pw, nil, http.StatusNoContent, nil)

	// Permissions: a viewer sees hosts but cannot link or create.
	f.h.addRole("viewer", "monitoring:view")
	f.h.addLocal("viewer", "Viewer-pass-2026", "viewer", time.Now())
	viewer := f.h.client()
	viewer.login("viewer", "Viewer-pass-2026")
	if code := viewer.call(http.MethodGet, "/api/monitoring/hosts", nil, nil); code != http.StatusOK {
		t.Errorf("viewer hosts: %d", code)
	}
	if code := viewer.call(http.MethodPost, "/api/monitoring/link", app.LinkInput{}, nil); code != http.StatusForbidden {
		t.Errorf("viewer link: %d", code)
	}
}
