package monitoring_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/monitoring"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/monitoring/monitoringtest"
)

func TestPrometheusMetrics(t *testing.T) {
	p := monitoringtest.StartPrometheus(t)
	start := time.Unix(1_700_000_000, 0)
	p.Range(map[string]string{"instance": "web-01.corp:9100", "job": "node"}, [2]float64{1_700_000_000, 12.5}, [2]float64{1_700_000_060, 40})
	src := model.MonitoringSource{Kind: model.MonitoringPrometheus, URL: p.URL}
	host := model.MonitoringHost{Key: "web-01.corp", Host: "web-01.corp", Endpoints: []string{"web-01.corp:9100"}}
	panel := model.DefaultHostPanels()[0]
	out, err := monitoring.Metrics(context.Background(), src, nil, host, monitoring.MetricQuery{Panel: panel, From: start, To: start.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Name != "web-01.corp:9100" || out[0].Unit != "%" || len(out[0].Points) != 2 || out[0].Points[1] != [2]float64{1_700_000_060_000, 40} {
		t.Fatalf("series %+v", out)
	}
	q := p.Queries[0]
	if !strings.Contains(q, `instance=~"web-01\\.corp:9100"`) || strings.Contains(q, "$selector") {
		t.Fatalf("query %s", q)
	}
	if _, err := monitoring.Metrics(context.Background(), src, nil, host, monitoring.MetricQuery{Panel: model.HostPanel{ZabbixKey: "x"}, From: start, To: start.Add(time.Hour)}); err != monitoring.ErrNoQuery {
		t.Fatalf("a panel without PromQL: %v", err)
	}
}

func TestHostSelectorWithoutEndpoints(t *testing.T) {
	src := model.MonitoringSource{HostLabel: "host"}
	got := monitoring.ExpandPromQL(`up{$selector} or node_uname_info{nodename="$host"}`, src, model.MonitoringHost{Host: "db.local"})
	if got != `up{host=~"db\\.local(:[0-9]+)?"} or node_uname_info{nodename="db.local"}` {
		t.Fatal(got)
	}
}

func TestZabbixMetrics(t *testing.T) {
	for _, version := range []string{"8.0.0", "7.0.5"} {
		t.Run(version, func(t *testing.T) {
			z := monitoringtest.StartZabbix(t, version)
			z.AddHost(10101, "srv-db-01", "DB server", false, nil)
			z.AddItem(10101, 1, "CPU utilization", "system.cpu.util", "%", 0, [2]float64{1_700_000_000, 10}, [2]float64{1_700_000_060, 95}, [2]float64{1_600_000_000, 1})
			z.AddItem(10101, 2, "CPU iowait", "system.cpu.util[,iowait]", "%", 0, [2]float64{1_700_000_000, 3})
			z.AddItem(10101, 3, "/: Space utilization", "vfs.fs.dependent.size[/,pused]", "%", 0, [2]float64{1_700_000_000, 70})
			z.AddItem(10101, 4, "/var: Space utilization", "vfs.fs.dependent.size[/var,pused]", "%", 0, [2]float64{1_700_000_030, 80})
			z.AddItem(10102, 5, "CPU utilization", "system.cpu.util", "%", 0, [2]float64{1_700_000_000, 50})
			src := model.MonitoringSource{Kind: model.MonitoringZabbix, URL: z.URL}
			auth := &monitoring.Auth{Type: "bearer", Secrets: map[string]string{"token": "zbx-token"}}
			host := model.MonitoringHost{Key: "10101", Host: "srv-db-01"}
			from := time.Unix(1_699_999_000, 0)
			panels := model.DefaultHostPanels()
			cpu, err := monitoring.Metrics(context.Background(), src, auth, host, monitoring.MetricQuery{Panel: panels[0], From: from, To: from.Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			if len(cpu) != 1 || cpu[0].Name != "CPU utilization" || len(cpu[0].Points) != 2 || cpu[0].Points[1][1] != 95 {
				t.Fatalf("cpu %+v", cpu)
			}
			disk, err := monitoring.Metrics(context.Background(), src, auth, host, monitoring.MetricQuery{Panel: panels[3], From: from, To: from.Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			if len(disk) != 2 || disk[0].Name != "/: Space utilization" || disk[1].Name != "/var: Space utilization" {
				t.Fatalf("disk %+v", disk)
			}
			// Three days are read from trends.
			long, err := monitoring.Metrics(context.Background(), src, auth, host, monitoring.MetricQuery{Panel: panels[0], From: from, To: from.Add(72 * time.Hour)})
			if err != nil || len(long) != 1 || len(long[0].Points) != 2 || z.Calls[len(z.Calls)-1] != "trend.get" {
				t.Fatalf("trends %+v %v %v", long, err, z.Calls)
			}
		})
	}
}

func TestDownsample(t *testing.T) {
	var pts [][2]float64
	for i := range 1000 {
		pts = append(pts, [2]float64{float64(i * 1000), float64(i % 10)})
	}
	out := monitoring.Downsample(pts, 100)
	if len(out) > 100 || len(out) < 90 {
		t.Fatalf("%d points", len(out))
	}
	if out[0][1] != 4.5 {
		t.Fatalf("first bucket %v", out[0])
	}
}

func TestSplitThreshold(t *testing.T) {
	for _, c := range []struct {
		in, expr, op string
		th           float64
		split        bool
	}{
		{`100 - avg(rate(cpu{mode="idle"}[5m])) * 100 > 90`, `100 - avg(rate(cpu{mode="idle"}[5m])) * 100`, ">", 90, true},
		{`up == 0`, `up`, "==", 0, true},
		{`disk_free_ratio < bool 0.1`, `disk_free_ratio`, "<", 0.1, true},
		{`(x > 5)`, `(x > 5)`, "", 0, false},
		{`rate(errors[5m])`, `rate(errors[5m])`, "", 0, false},
	} {
		expr, op, th := monitoring.SplitThreshold(c.in)
		if expr != c.expr || op != c.op || (th != nil) != c.split || (th != nil && *th != c.th) {
			t.Errorf("%s: %q %q %v", c.in, expr, op, th)
		}
	}
}
