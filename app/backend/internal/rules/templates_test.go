package rules

import (
	"reflect"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

// legacyTemplates is the template list as it was written in Go before templates.json.
func legacyTemplates() []model.Rule {
	t := func(method, signal, name, query, label, op string, thr float64, hold string, sev, title string) model.Rule {
		return model.Rule{Method: method, Signal: signal, Name: name, Query: query, CILabel: label, Op: op, Threshold: thr, For: hold,
			Interval: "30s", Severity: sev, Title: title, Enabled: true}
	}
	return []model.Rule{
		t(model.MethodUSE, "use.cpu.utilization", "CPU utilization", `100 * (1 - avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[2m])))`, "instance", ">", 90, "5m", "warning", "CPU ${ci}: ${value}% (threshold ${threshold}%)"),
		t(model.MethodUSE, "use.mem.utilization", "Memory utilization", `100 * (1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)`, "instance", ">", 90, "5m", "warning", "Memory ${ci}: ${value}% used"),
		t(model.MethodUSE, "use.disk.utilization", "Disk space", `100 * (1 - node_filesystem_avail_bytes{fstype!~"tmpfs|overlay|squashfs"} / node_filesystem_size_bytes{fstype!~"tmpfs|overlay|squashfs"})`, "instance", ">", 85, "5m", "error", "Disk ${labels.mountpoint} on ${ci}: ${value}%"),
		t(model.MethodUSE, "use.cpu.saturation", "CPU saturation (load per core)", `node_load5 / on (instance) count by (instance) (node_cpu_seconds_total{mode="idle"})`, "instance", ">", 2, "10m", "error", "Load per core ${ci}: ${value}"),
		t(model.MethodUSE, "use.net.errors", "Network errors", `sum by (instance) (increase(node_network_receive_errs_total[5m]) + increase(node_network_transmit_errs_total[5m]))`, "instance", ">", 0, "0s", "warning", "Network errors on ${ci}: ${value} in 5 min"),
		t(model.MethodUSE, "use.container.cpu", "Container CPU", `100 * sum by (name) (rate(container_cpu_usage_seconds_total{name!=""}[2m]))`, "name", ">", 80, "5m", "warning", "CPU of container ${ci}: ${value}%"),
		t(model.MethodRED, "red.rate", "No requests", `sum by (job) (rate(http_requests_total[5m]))`, "job", "<", 0.1, "10m", "error", "Requests to ${ci}: ${value}/s"),
		t(model.MethodRED, "red.errors", "5xx error ratio", `100 * sum by (job) (rate(http_requests_total{code=~"5.."}[5m])) / sum by (job) (rate(http_requests_total[5m]))`, "job", ">", 5, "5m", "critical", "5xx errors ${ci}: ${value}%"),
		t(model.MethodRED, "red.duration", "p99 latency", `histogram_quantile(0.99, sum by (job, le) (rate(http_request_duration_seconds_bucket[5m])))`, "job", ">", 1, "5m", "error", "p99 of ${ci}: ${value} s"),
	}
}

func TestTemplatesFile(t *testing.T) {
	got, want := Templates(), legacyTemplates()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("templates.json differs from the old list:\n got %+v\nwant %+v", got, want)
	}
	for _, r := range got {
		r.SourceID = "s"
		if err := Normalize(&r); err != nil {
			t.Errorf("template %s: %v", r.Name, err)
		}
	}
}

func TestDefaultTitle(t *testing.T) {
	r := model.Rule{Name: "Disk", Method: model.MethodUSE, SourceID: "s", Query: "up", Op: ">", Severity: "warning"}
	if err := Normalize(&r); err != nil {
		t.Fatal(err)
	}
	if r.Title != "Disk: ${ci} = ${value}" || r.For != "0s" || r.Interval != "30s" || r.CILabel != "instance" {
		t.Fatalf("defaults = %+v", r)
	}
}
