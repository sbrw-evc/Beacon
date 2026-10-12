package alert_test

import (
	"context"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// monitored adds a Zabbix system with three hosts; links are host key → CI ID or HostNoCI.
func monitored(st *store.Store, links map[string]string) {
	st.Write(func(d *store.Data) {
		d.MonitoringSources["MON-1"] = &model.MonitoringSource{ID: "MON-1", Name: "Zabbix", Kind: model.MonitoringZabbix, Enabled: true,
			Hosts: []model.MonitoringHost{
				{Key: "10101", Host: "zbx-db-01.corp", Name: "DB 01", IPs: []string{"192.168.5.11"}},
				{Key: "10102", Host: "test-vm-07", Name: "Test VM"},
				{Key: "10103", Host: "zbx-app", Name: "App"},
			},
			Links: links}
	})
}

func setLink(st *store.Store, key, target string) {
	st.Write(func(d *store.Data) {
		if target == "" {
			delete(d.MonitoringSources["MON-1"].Links, key)
			return
		}
		d.MonitoringSources["MON-1"].Links[key] = target
	})
}

func one(t *testing.T, e *alert.Engine) alert.Alert {
	t.Helper()
	list := active(t, e)
	if len(list) != 1 {
		t.Fatalf("want one active alert, got %d", len(list))
	}
	return list[0]
}

// A host linked by hand to an item on the monitoring systems page sends its alerts to that
// item, whatever its own name, address or short name.
func TestLinkedHostResolvesToItsCI(t *testing.T) {
	ctx := context.Background()
	e, st, rec, _ := setup(t)
	monitored(st, map[string]string{"10101": "CI-2"})

	for i, name := range []string{"zbx-db-01.corp", "zbx-db-01", "192.168.5.11:10050"} {
		in := ev("CON-1", "k", name, "cpu", "error", "firing")
		if err := e.Ingest(ctx, []alert.Incoming{in}); err != nil {
			t.Fatal(err)
		}
		a := one(t, e)
		if a.CIID != "CI-2" || a.CIName != "app-01" || a.Route.Team == nil || a.Route.Team.ID != "T-2" {
			t.Fatalf("event %d (%s): alert bound to %q (%q), route %+v", i, name, a.CIID, a.CIName, a.Route)
		}
		if a.EventCI != "zbx-db-01.corp" {
			t.Errorf("the name of the first event is kept: %q", a.EventCI)
		}
	}
	if cmds := rec.take(); len(cmds) != 1 || cmds[0].Action != alert.PDTrigger {
		t.Errorf("one trigger: %+v", cmds)
	}
}

// A host marked «Не является КЕ» opens a suppressed alert that is sent nowhere.
func TestExcludedHostSuppressed(t *testing.T) {
	ctx := context.Background()
	e, st, rec, c := setup(t)
	monitored(st, map[string]string{"10102": model.HostNoCI})
	// Even an item with the same name does not take the event: the hand-made mark wins.
	st.Write(func(d *store.Data) {
		d.ConfigItems["CI-9"] = &model.ConfigItem{ID: "CI-9", Name: "test-vm-07", Kind: model.CIKindVM, Status: model.CIStatusActive}
		d.Services["S-1"].CIIDs = append(d.Services["S-1"].CIIDs, "CI-9")
	})
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", "k", "test-vm-07", "disk", "critical", "firing")}); err != nil {
		t.Fatal(err)
	}
	a := one(t, e)
	if !a.Excluded || !a.Suppressed || a.CIID != "" || a.PD.State != alert.PDSkipped {
		t.Fatalf("excluded alert = %+v", a)
	}
	if cmds := rec.take(); len(cmds) != 0 {
		t.Errorf("nothing goes to PagerDuty: %+v", cmds)
	}
	c.advance(10 * time.Minute)
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	a = one(t, e)
	if !a.Suppressed || !a.Excluded || a.Fallback || len(rec.fallback) != 0 || len(rec.take()) != 0 {
		t.Errorf("no backup notification and no retry, stays suppressed: %+v", a)
	}
	_, entries, err := e.Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, en := range entries {
		found = found || en.Code == "host_excluded"
	}
	if !found {
		t.Errorf("the timeline says why: %+v", entries)
	}
	p, err := e.List(ctx, alert.Filter{Status: "active", Suppressed: true})
	if err != nil || len(p.Alerts) != 1 {
		t.Errorf("listed among suppressed: %v %d", err, len(p.Alerts))
	}
}

// Changing a link moves the open alerts of the host: to the linked item, out of the catalog
// when marked as no item, and back to normal delivery when the mark is taken off.
func TestLinkChangeReresolvesOpenAlert(t *testing.T) {
	ctx := context.Background()
	e, st, rec, _ := setup(t)
	monitored(st, map[string]string{})
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", "k", "zbx-app", "cpu", "error", "firing")}); err != nil {
		t.Fatal(err)
	}
	a := one(t, e)
	if a.CIID != "" {
		t.Fatalf("unknown at first: %+v", a)
	}
	rec.take()

	setLink(st, "10103", "CI-2")
	if err := e.Reresolve(ctx); err != nil {
		t.Fatal(err)
	}
	a = one(t, e)
	if a.CIID != "CI-2" || a.Route.Team == nil || a.Route.Team.ID != "T-2" || a.DedupKey != "ci:CI-2|cpu" {
		t.Fatalf("bound after linking: %+v", a)
	}
	// The next event of the host folds into the same alert.
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", "k2", "zbx-app", "cpu", "error", "firing")}); err != nil {
		t.Fatal(err)
	}
	if b := one(t, e); b.ID != a.ID || b.Count != 2 {
		t.Errorf("same alert: %+v", b)
	}

	setLink(st, "10103", model.HostNoCI)
	if err := e.Reresolve(ctx); err != nil {
		t.Fatal(err)
	}
	a = one(t, e)
	if a.CIID != "" || !a.Excluded || !a.Suppressed || a.DedupKey != "name:zbx-app|cpu" {
		t.Fatalf("excluded after marking: %+v", a)
	}
	rec.take()

	setLink(st, "10103", "")
	if err := e.Reresolve(ctx); err != nil {
		t.Fatal(err)
	}
	a = one(t, e)
	if a.Excluded || a.Suppressed || a.PD.State != alert.PDPending {
		t.Fatalf("delivered again after the mark is taken off: %+v", a)
	}
	if cmds := rec.take(); len(cmds) != 1 || cmds[0].Action != alert.PDTrigger {
		t.Errorf("trigger after inclusion: %+v", cmds)
	}
}

// A test event opens an alert that never goes to PagerDuty and resolves itself.
func TestTestEventNotSentAndExpires(t *testing.T) {
	ctx := context.Background()
	e, _, rec, c := setup(t)
	in := ev("CON-1", "umbrella-test-1", "app-01", "umbrella_test", "critical", "firing")
	in.Labels = map[string]string{alert.TestLabel: "true"}
	if err := e.Ingest(ctx, []alert.Incoming{in}); err != nil {
		t.Fatal(err)
	}
	a := one(t, e)
	if a.CIID != "CI-2" || a.PD.State != alert.PDSkipped {
		t.Fatalf("test alert = %+v", a)
	}
	c.advance(3 * time.Minute)
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if a := one(t, e); a.Fallback || len(rec.fallback) != 0 {
		t.Errorf("no backup notification for a test")
	}
	c.advance(3 * time.Minute)
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(active(t, e)); n != 0 {
		t.Fatalf("resolved after %s, %d active", alert.TestLifetime, n)
	}
	if cmds := rec.take(); len(cmds) != 0 {
		t.Errorf("nothing to PagerDuty: %+v", cmds)
	}
}

// A host linked to an item that already has an alert of the same signal joins that alert
// instead of waiting outside the catalog forever: two active alerts cannot share a key.
func TestLinkMergesIntoExistingAlert(t *testing.T) {
	ctx := context.Background()
	e, st, _, _ := setup(t)
	monitored(st, map[string]string{})
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", "k1", "app-01", "cpu", "error", "firing")}); err != nil {
		t.Fatal(err)
	}
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-2", "k2", "zbx-app", "cpu", "critical", "firing")}); err != nil {
		t.Fatal(err)
	}
	if n := len(active(t, e)); n != 2 {
		t.Fatalf("two alerts before linking, got %d", n)
	}
	setLink(st, "10103", "CI-2")
	if err := e.Reresolve(ctx); err != nil {
		t.Fatal(err)
	}
	a := one(t, e)
	if a.CIID != "CI-2" || len(a.Sources) != 2 || a.Severity != "critical" {
		t.Fatalf("the alert of the item took the other one over: %+v", a)
	}
	// Nothing is left to move: a second pass changes nothing.
	if err := e.Reresolve(ctx); err != nil {
		t.Fatal(err)
	}
	if b := one(t, e); b.ID != a.ID {
		t.Fatalf("stable after a second pass: %+v", b)
	}
}
