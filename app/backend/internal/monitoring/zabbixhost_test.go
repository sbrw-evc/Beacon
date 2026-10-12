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

func TestZabbixHostEventsAndLogs(t *testing.T) {
	for _, version := range []string{"8.0.0", "7.0.5", "6.0.30"} {
		t.Run(version, func(t *testing.T) {
			z := monitoringtest.StartZabbix(t, version)
			z.AddHost(10101, "srv-db-01", "DB server", false, nil)
			// In the window: one ended, one going on; before it: one still open, one long over; another host.
			z.AddEvent(monitoringtest.Event{HostID: "10101", ID: "20", TriggerID: "300", Name: "High CPU", Clock: 1_700_000_100, Severity: 4,
				Recovery: "21", RecoveryClock: 1_700_000_400, Tags: map[string]string{"scope": "performance"}})
			z.AddEvent(monitoringtest.Event{HostID: "10101", ID: "22", TriggerID: "301", Name: "Disk full", Clock: 1_700_000_200, Severity: 5})
			z.AddEvent(monitoringtest.Event{HostID: "10101", ID: "10", TriggerID: "302", Name: "Agent unreachable", Clock: 1_699_990_000, Severity: 2})
			z.AddEvent(monitoringtest.Event{HostID: "10101", ID: "5", TriggerID: "303", Name: "Old", Clock: 1_699_000_000, Severity: 3, Recovery: "6", RecoveryClock: 1_699_000_100})
			z.AddEvent(monitoringtest.Event{HostID: "10102", ID: "23", TriggerID: "304", Name: "Other host", Clock: 1_700_000_150, Severity: 3})
			z.AddLogItem(10101, 1, "App log", "log[/var/log/app.log]", 2,
				monitoringtest.LogValue{Clock: 1_700_000_110, Value: "ERROR db: connection refused\n"},
				monitoringtest.LogValue{Clock: 1_700_000_300, Value: "INFO db: reconnected"},
				monitoringtest.LogValue{Clock: 1_600_000_000, Value: "ERROR too old"})
			z.AddLogItem(10101, 2, "Syslog", "logrt[/var/log/syslog.*]", 4, monitoringtest.LogValue{Clock: 1_700_000_120, Value: "kernel: Out of memory"})
			z.AddLogItem(10101, 3, "Agent version", "agent.version", 1, monitoringtest.LogValue{Clock: 1_700_000_130, Value: "7.0.0"})
			z.AddLogItem(10101, 4, "Errors per minute", "log.count[/var/log/app.log,ERROR]", 4, monitoringtest.LogValue{Clock: 1_700_000_140, Value: "3"})

			src := model.MonitoringSource{Kind: model.MonitoringZabbix, URL: z.URL}
			auth := &monitoring.Auth{Type: "bearer", Secrets: map[string]string{"token": "zbx-token"}}
			host := model.MonitoringHost{Key: "10101", Host: "srv-db-01"}
			from, to := time.Unix(1_700_000_000, 0), time.Unix(1_700_000_500, 0)

			events, truncated, err := monitoring.HostEvents(context.Background(), src, auth, host, from, to, 100)
			if err != nil {
				t.Fatal(err)
			}
			if truncated || len(events) != 3 {
				t.Fatalf("events %+v", events)
			}
			disk, cpu, agent := events[0], events[1], events[2]
			if disk.Title != "Disk full" || disk.Severity != model.SeverityCritical || disk.Level != "Disaster" || disk.Status != "firing" || disk.ResolvedAt != nil {
				t.Errorf("disk %+v", disk)
			}
			if cpu.Status != "resolved" || cpu.ResolvedAt == nil || cpu.ResolvedAt.Unix() != 1_700_000_400 || cpu.Severity != model.SeverityError ||
				len(cpu.Tags) != 1 || cpu.Tags[0] != "scope: performance" || !strings.HasSuffix(cpu.URL, "/tr_events.php?triggerid=300&eventid=20") {
				t.Errorf("cpu %+v", cpu)
			}
			if agent.Title != "Agent unreachable" || agent.Status != "firing" || agent.Severity != model.SeverityLow {
				t.Errorf("a problem still open from before the window: %+v", agent)
			}
			if _, truncated, err := monitoring.HostEvents(context.Background(), src, auth, host, from, to, 1); err != nil || !truncated {
				t.Errorf("the limit is not reported: %v", err)
			}

			lines, err := monitoring.HostLogLines(context.Background(), src, auth, host, from, to, 100, "")
			if err != nil {
				t.Fatal(err)
			}
			if lines.Items != 2 || len(lines.Lines) != 3 {
				t.Fatalf("logs %+v", lines)
			}
			if l := lines.Lines[0]; l.Text != "INFO db: reconnected" || l.Level != "info" || l.Labels["item"] != "App log" {
				t.Errorf("newest %+v", l)
			}
			if l := lines.Lines[2]; l.Text != "ERROR db: connection refused" || l.Level != "error" {
				t.Errorf("oldest %+v", l)
			}
			found, err := monitoring.HostLogLines(context.Background(), src, auth, host, from, to, 100, "memory")
			if err != nil || len(found.Lines) != 1 || found.Lines[0].Labels["key"] != "logrt[/var/log/syslog.*]" {
				t.Fatalf("search %+v %v", found, err)
			}
			few, err := monitoring.HostLogLines(context.Background(), src, auth, host, from, to, 2, "")
			if err != nil || len(few.Lines) != 2 || !few.Truncated {
				t.Fatalf("limit %+v %v", few, err)
			}
		})
	}
}
