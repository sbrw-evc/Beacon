package monitoring_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/monitoring"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/monitoring/monitoringtest"
)

func zabbixWithHosts(t *testing.T, version string) *monitoringtest.Zabbix {
	z := monitoringtest.StartZabbix(t, version)
	z.AddHost(10101, "srv-db-01", "DB server", false, []string{"Linux servers", "Databases"},
		monitoringtest.Iface("10.0.0.1", "srv-db-01.corp.local", 1))
	z.AddHost(10102, "sw-core-01", "sw-core-01", false, []string{"Network"},
		monitoringtest.Iface("10.0.0.3", "", 1), monitoringtest.Iface("10.0.0.3", "", 2))
	z.AddHost(10103, "old-box", "", true, nil, monitoringtest.Iface("10.9.9.9", "", 0))
	z.AddHost(10104, "printer", "printer", false, nil, monitoringtest.Iface("10.0.5.5", "", 2))
	return z
}

func TestZabbixToken(t *testing.T) {
	for _, version := range []string{"8.0.0", "7.2.1", "6.0.30"} {
		t.Run(version, func(t *testing.T) {
			z := zabbixWithHosts(t, version)
			src := model.MonitoringSource{Kind: model.MonitoringZabbix, URL: z.URL + "/"}
			res, err := monitoring.Fetch(context.Background(), src, &monitoring.Auth{Type: "bearer", Secrets: map[string]string{"token": "zbx-token"}})
			if err != nil {
				t.Fatal(err)
			}
			if res.Version != version || len(res.Hosts) != 4 {
				t.Fatalf("got %+v", res)
			}
			db := res.Hosts[slices.IndexFunc(res.Hosts, func(h model.MonitoringHost) bool { return h.Key == "10101" })]
			if db.Host != "srv-db-01" || db.Name != "DB server" || !slices.Equal(db.IPs, []string{"10.0.0.1"}) || !slices.Equal(db.DNS, []string{"srv-db-01.corp.local"}) ||
				!slices.Equal(db.Groups, []string{"Databases", "Linux servers"}) || db.State != model.HostUp || !strings.Contains(db.URL, "filter_name=srv-db-01") {
				t.Fatalf("db host %+v", db)
			}
			states := map[string]string{}
			for _, h := range res.Hosts {
				states[h.Host] = h.State
			}
			want := map[string]string{"srv-db-01": "up", "sw-core-01": "partial", "old-box": "disabled", "printer": "down"}
			for k, v := range want {
				if states[k] != v {
					t.Errorf("%s: state %q, want %q", k, states[k], v)
				}
			}
			if h := res.Hosts[slices.IndexFunc(res.Hosts, func(h model.MonitoringHost) bool { return h.Key == "10103" })]; h.Name != "old-box" {
				t.Errorf("a host without a visible name keeps its technical name, got %q", h.Name)
			}
		})
	}
}

func TestZabbixPasswordAndErrors(t *testing.T) {
	z := zabbixWithHosts(t, "7.0.5")
	src := model.MonitoringSource{Kind: model.MonitoringZabbix, URL: z.URL + "/api_jsonrpc.php"}
	res, err := monitoring.Fetch(context.Background(), src, &monitoring.Auth{Type: "basic", Fields: map[string]string{"username": "Admin"}, Secrets: map[string]string{"password": "zabbix"}})
	if err != nil || len(res.Hosts) != 4 {
		t.Fatalf("password login: %v %d", err, len(res.Hosts))
	}
	if z.LoggedOut != 1 {
		t.Errorf("the session is not closed: %d", z.LoggedOut)
	}
	_, err = monitoring.Fetch(context.Background(), src, &monitoring.Auth{Type: "basic", Fields: map[string]string{"username": "Admin"}, Secrets: map[string]string{"password": "wrong"}})
	if err == nil || !strings.Contains(err.Error(), "Incorrect user name") {
		t.Errorf("wrong password: %v", err)
	}
	_, err = monitoring.Fetch(context.Background(), src, &monitoring.Auth{Type: "bearer", Secrets: map[string]string{"token": "nope"}})
	if err == nil || !strings.Contains(err.Error(), "Not authorized") {
		t.Errorf("wrong token: %v", err)
	}
	if _, err = monitoring.Fetch(context.Background(), src, nil); err == nil {
		t.Error("no credential is accepted")
	}
	if _, err = monitoring.Fetch(context.Background(), model.MonitoringSource{Kind: model.MonitoringZabbix, URL: z.URL + "/nothing/"}, nil); err == nil {
		t.Error("a wrong address is accepted")
	}
}

func TestPrometheusTargets(t *testing.T) {
	p := monitoringtest.StartPrometheus(t)
	p.Token = "prom"
	p.Target("node", "srv-db-01.corp.local:9100", true)
	p.Target("postgres", "srv-db-01.corp.local:9187", false)
	p.Target("node", "10.0.0.2:9100", true)
	p.Target("blackbox", "https://portal.example.com/health", false)
	p.Target("node", "", true)
	src := model.MonitoringSource{Kind: model.MonitoringPrometheus, URL: p.URL}
	if _, err := monitoring.Fetch(context.Background(), src, nil); err == nil {
		t.Fatal("no token is accepted")
	}
	res, err := monitoring.Fetch(context.Background(), src, &monitoring.Auth{Type: "bearer", Secrets: map[string]string{"token": "prom"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hosts) != 3 || p.Queries[len(p.Queries)-1] != "up" {
		t.Fatalf("hosts %+v queries %v", res.Hosts, p.Queries)
	}
	byKey := map[string]model.MonitoringHost{}
	for _, h := range res.Hosts {
		byKey[h.Key] = h
	}
	db := byKey["srv-db-01.corp.local"]
	if db.State != model.HostPartial || !slices.Equal(db.Groups, []string{"node", "postgres"}) || len(db.Endpoints) != 2 || !slices.Equal(db.DNS, []string{"srv-db-01.corp.local"}) {
		t.Errorf("db %+v", db)
	}
	if h := byKey["10.0.0.2"]; h.State != model.HostUp || !slices.Equal(h.IPs, []string{"10.0.0.2"}) {
		t.Errorf("ip host %+v", h)
	}
	if h := byKey["portal.example.com"]; h.State != model.HostDown {
		t.Errorf("probe %+v", h)
	}
}

// Grafana hosts come from the instances of its alert rules: a host per instance label, down
// while all its instances alert, partial while some do.
func TestGrafanaHosts(t *testing.T) {
	g := monitoringtest.StartGrafana(t)
	g.SetRule("cpu", "High CPU", map[string]string{"summary": "CPU"},
		monitoringtest.GrafanaInstance{Labels: map[string]string{"instance": "db-01:9100"}, State: "Alerting"},
		monitoringtest.GrafanaInstance{Labels: map[string]string{"instance": "app-01:9100"}, State: "Normal"})
	g.SetRule("disk", "Disk", nil,
		monitoringtest.GrafanaInstance{Labels: map[string]string{"host": "db-01"}, State: "Normal"},
		monitoringtest.GrafanaInstance{Labels: map[string]string{"host": "10.0.0.7"}, State: "Alerting (NoData)"},
		monitoringtest.GrafanaInstance{Labels: map[string]string{"service": "no-host"}, State: "Alerting"})
	src := model.MonitoringSource{Kind: model.MonitoringGrafana, URL: g.URL}
	auth := &monitoring.Auth{Type: "bearer", Secrets: map[string]string{"token": g.Token}}
	res, err := monitoring.Fetch(context.Background(), src, auth)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, h := range res.Hosts {
		states[h.Key] = h.State
	}
	want := map[string]string{"db-01": "partial", "app-01": "up", "10.0.0.7": "down"}
	if res.Version != "11.3.0" || len(states) != 3 {
		t.Fatalf("got %+v", res)
	}
	for k, v := range want {
		if states[k] != v {
			t.Errorf("%s: %q, want %q", k, states[k], v)
		}
	}
	if _, err := monitoring.Fetch(context.Background(), src, &monitoring.Auth{Type: "bearer", Secrets: map[string]string{"token": "bad"}}); err == nil ||
		!strings.Contains(err.Error(), "401") {
		t.Errorf("a wrong token: %v", err)
	}
	src.HostLabel = "service"
	if res, _ := monitoring.Fetch(context.Background(), src, auth); len(res.Hosts) != 1 || res.Hosts[0].Key != "no-host" {
		t.Errorf("by the service label: %+v", res.Hosts)
	}
	reading, _ := monitoring.ReadGrafana(context.Background(), src, auth)
	if len(reading.Alerts) != 5 || reading.Alerts[0].Fingerprint == "" || reading.Alerts[0].Labels["alertname"] == "" ||
		reading.Alerts[0].Labels["grafana_folder"] != "Infra" {
		t.Errorf("alerts = %+v", reading.Alerts)
	}
}

// Graylog hosts are the sources of its messages; a host with an alert event within the quiet
// time is down.
func TestGraylogHosts(t *testing.T) {
	g := monitoringtest.StartGraylog(t)
	g.SetSources(map[string]int{"web-01": 120, "WEB-01.example.com": 3, "10.0.0.5": 9})
	g.AddEvent(monitoringtest.GraylogEvent{ID: "e1", DefinitionID: "d1", Title: "5xx", Key: "web-01", Priority: 3, At: time.Now().Add(-time.Minute),
		GroupBy: map[string]string{"source": "web-01"}})
	g.AddEvent(monitoringtest.GraylogEvent{ID: "e0", DefinitionID: "d1", Title: "5xx", Key: "10.0.0.5", Priority: 3, At: time.Now().Add(-time.Hour),
		GroupBy: map[string]string{"source": "10.0.0.5"}})
	src := model.MonitoringSource{Kind: model.MonitoringGraylog, URL: g.URL}
	auth := &monitoring.Auth{Type: "bearer", Secrets: map[string]string{"token": g.Token}}
	res, err := monitoring.Fetch(context.Background(), src, auth)
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "6.1.2" || len(res.Hosts) != 3 {
		t.Fatalf("result = %+v", res)
	}
	states := map[string]string{}
	for _, h := range res.Hosts {
		states[h.Key] = h.State
	}
	if states["web-01"] != model.HostDown || states["10.0.0.5"] != model.HostUp || states["web-01.example.com"] != model.HostUp {
		t.Errorf("states = %v", states)
	}
	if _, err := monitoring.Fetch(context.Background(), src, &monitoring.Auth{Type: "bearer", Secrets: map[string]string{"token": "x"}}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("wrong token: %v", err)
	}
}
