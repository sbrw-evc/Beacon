package presets

import (
	"context"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
)

// Umbrella Flow events: one object or an array; the method comes from the method field or the
// signal prefix; the item from ci, then ip; the source reference, IP and traffic link become
// labels, and the same id resolves the event it fired.
func TestNetworkFlowEvents(t *testing.T) {
	p, ok := Get("network-flow")
	if !ok {
		t.Fatal("no network-flow preset")
	}
	pl := pipeline(t, p)
	events := map[string][]flow.Event{}
	for _, s := range p.Document.Samples {
		res, err := pl.Run(context.Background(), flow.Input{Body: []byte(s.Body)}, flow.RunOptions{})
		if err != nil || len(res.Failures) > 0 {
			t.Fatalf("%s: failures=%+v err=%v", s.Name, res.Failures, err)
		}
		for _, e := range res.Events {
			events[s.Name] = append(events[s.Name], e.Event)
		}
	}
	u := events["Firing: link utilization (USE)"]
	if len(u) != 1 {
		t.Fatalf("utilization = %+v", u)
	}
	e := u[0]
	if e.CI != "edge-01" || e.Signal != "use.link.utilization" || e.Method != flow.MethodUSE || e.Severity != flow.SeverityWarning ||
		e.Status != flow.StatusFiring || e.ExternalID != "flow-7f3a1c" || e.Value != "93.5" || e.Title == "" {
		t.Errorf("utilization = %+v", e)
	}
	want := map[string]string{"source": "network-flow", "source_ref": "inventory-db:dcim.device:100", "ip": "10.0.0.1",
		"flow_url": "https://flow.example.com/visualize?exporter=10.0.0.1&if=xe-0/0/1", "interface": "xe-0/0/1", "direction": "in"}
	for k, v := range want {
		if e.Labels[k] != v {
			t.Errorf("label %s = %q, want %q (%v)", k, e.Labels[k], v, e.Labels)
		}
	}
	fields := map[string]string{}
	for _, f := range e.Fields {
		fields[f.Name] = f.Value
	}
	if fields["Трафик в Umbrella Flow"] != want["flow_url"] || fields["Inventory DB"] != want["source_ref"] || fields["Окончание"] != "" {
		t.Errorf("fields = %+v", e.Fields)
	}

	d := events["Firing: DDoS (RED), batch of two"]
	if len(d) != 2 {
		t.Fatalf("ddos = %+v", d)
	}
	if d[0].CI != "203.0.113.10" || d[0].Method != flow.MethodRED || d[0].Severity != flow.SeverityCritical || d[0].Labels["source_ref"] != "" {
		t.Errorf("volumetric = %+v", d[0])
	}
	// No method field: the red. prefix of the signal decides.
	if d[1].CI != "web-01" || d[1].Method != flow.MethodRED || d[1].Severity != flow.SeverityError || d[1].Labels["flow_url"] != "" {
		t.Errorf("syn = %+v", d[1])
	}

	r := events["Resolved: link utilization"]
	if len(r) != 1 || r[0].Status != flow.StatusResolved || r[0].Key != e.Key {
		t.Errorf("resolved = %+v, firing key %s", r, e.Key)
	}
	if x := events["Firing: exporter silent"]; len(x) != 1 || x[0].Severity != flow.SeverityLow || x[0].Method != flow.MethodUSE {
		t.Errorf("silent = %+v", x)
	}

	// An unknown method and severity: other and warning.
	res, err := pl.Run(context.Background(), flow.Input{Body: []byte(`{"id":"z","ci":"x","signal":"sec.threat.contact","method":"SEC","severity":"weird","title":"T"}`)}, flow.RunOptions{})
	if err != nil || len(res.Events) != 1 || res.Events[0].Event.Method != flow.MethodOther || res.Events[0].Event.Severity != flow.SeverityWarning {
		t.Errorf("other: %+v %+v %v", res.Events, res.Failures, err)
	}
}
