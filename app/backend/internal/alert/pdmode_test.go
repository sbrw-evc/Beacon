package alert_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func pdMode(st *store.Store, mode string, modes map[string]string) {
	st.Write(func(d *store.Data) {
		d.Settings.Alerting.PagerDuty.Mode = mode
		d.Settings.Alerting.PagerDuty.Modes = modes
	})
}

// In backup mode the channels of Umbrella go first: the incident waits in standby and goes to
// PagerDuty only when nobody has taken it within the backup delay.
func TestPagerDutyBackupMode(t *testing.T) {
	ctx := context.Background()
	e, st, rec, c := setup(t)
	rec.channels = true
	pdMode(st, model.PDModeBackup, nil)
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "app-01", "errors", "critical", "firing")}); err != nil {
		t.Fatal(err)
	}
	a := active(t, e)[0]
	if a.PD.State != alert.PDStandby {
		t.Fatalf("pd = %+v", a.PD)
	}
	if n, fb, _ := rec.counts(); n != 0 || fb != 1 {
		t.Fatalf("channels go at once and nothing to PagerDuty: cmds=%d fallback=%d", n, fb)
	}
	if cs := codes(t, e, a.ID); cs["pd_standby"] != 1 || cs["fallback"] != 1 {
		t.Fatalf("timeline = %v", cs)
	}
	c.advance(4 * time.Minute)
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := rec.counts(); n != 0 {
		t.Fatalf("still within the backup delay: %+v", rec.take())
	}
	c.advance(2 * time.Minute)
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	cmds := rec.take()
	if len(cmds) != 1 || cmds[0].Action != alert.PDTrigger {
		t.Fatalf("not taken in 5 minutes: escalated to PagerDuty, got %+v", cmds)
	}
	if a = active(t, e)[0]; !a.PD.Escalated || a.PD.State != alert.PDPending {
		t.Fatalf("pd = %+v", a.PD)
	}
	if cs := codes(t, e, a.ID); cs["pd_handover"] != 1 {
		t.Fatalf("timeline = %v", cs)
	}
	// A repeated event does not put it back in standby.
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "app-01", "errors", "critical", "firing")}); err != nil {
		t.Fatal(err)
	}
	if a = active(t, e)[0]; a.PD.State == alert.PDStandby {
		t.Fatalf("pd = %+v", a.PD)
	}
}

// An incident taken in Umbrella while PagerDuty waits never goes there, and nothing is
// acknowledged or resolved in PagerDuty for it.
func TestPagerDutyBackupTakenInTime(t *testing.T) {
	ctx := context.Background()
	e, st, rec, c := setup(t)
	rec.channels = true
	pdMode(st, model.PDModeBackup, nil)
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "app-01", "errors", "critical", "firing")}); err != nil {
		t.Fatal(err)
	}
	a := active(t, e)[0]
	if _, err := e.Act(ctx, a.ID, "ack", "eng", ""); err != nil {
		t.Fatal(err)
	}
	c.advance(10 * time.Minute)
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Act(ctx, a.ID, "resolve", "eng", ""); err != nil {
		t.Fatal(err)
	}
	if cmds := rec.take(); len(cmds) != 0 {
		t.Fatalf("PagerDuty is not involved: %+v", cmds)
	}
}

// Backup notification that reached nobody sends the incident to PagerDuty without waiting.
func TestPagerDutyBackupNobodyReached(t *testing.T) {
	ctx := context.Background()
	e, st, rec, c := setup(t)
	rec.channels = true
	pdMode(st, model.PDModeBackup, nil)
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "app-01", "errors", "critical", "firing")}); err != nil {
		t.Fatal(err)
	}
	a := active(t, e)[0]
	if due, err := e.FallbackDue(ctx, a.ID); err != nil || !due {
		t.Fatalf("due=%v err=%v", due, err)
	}
	if err := e.FallbackDone(ctx, a.ID, nil); err != nil {
		t.Fatal(err)
	}
	c.advance(10 * time.Second)
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if cmds := rec.take(); len(cmds) != 1 || cmds[0].Action != alert.PDTrigger {
		t.Fatalf("cmds = %+v", cmds)
	}
	_, entries, _ := e.Get(ctx, a.ID)
	if last := entries[len(entries)-1]; last.Code != "pd_handover" || last.Args["reason"] != "nobody_reached" {
		t.Fatalf("last = %+v", last)
	}
}

// Without a channel to take it first, backup mode sends the incident to PagerDuty at once.
func TestPagerDutyBackupWithoutChannels(t *testing.T) {
	ctx := context.Background()
	e, st, rec, _ := setup(t)
	pdMode(st, model.PDModeBackup, nil)
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "app-01", "errors", "critical", "firing")}); err != nil {
		t.Fatal(err)
	}
	if cmds := rec.take(); len(cmds) != 1 || cmds[0].Action != alert.PDTrigger {
		t.Fatalf("cmds = %+v", cmds)
	}
}

// Parallel mode: PagerDuty and the channels at once. A severity can have its own mode: off
// keeps it from PagerDuty, and the channels take it.
func TestPagerDutyParallelAndPerSeverity(t *testing.T) {
	ctx := context.Background()
	e, st, rec, _ := setup(t)
	rec.channels = true
	pdMode(st, model.PDModeParallel, map[string]string{model.SeverityError: model.PDModeOff})
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "app-01", "errors", "critical", "firing")}); err != nil {
		t.Fatal(err)
	}
	if n, fb, _ := rec.counts(); n != 1 || fb != 1 {
		t.Fatalf("parallel: cmds=%d fallback=%d", n, fb)
	}
	rec.take()
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", "b", "app-01", "latency", "error", "firing")}); err != nil {
		t.Fatal(err)
	}
	if cmds := rec.take(); len(cmds) != 0 {
		t.Fatalf("error is off for PagerDuty: %+v", cmds)
	}
	var b alert.Alert
	for _, a := range active(t, e) {
		if a.Signal == "latency" {
			b = a
		}
	}
	if b.PD.State != alert.PDSkipped || b.PD.ErrorCode != "mode_off" {
		t.Fatalf("pd = %+v", b.PD)
	}
	if cs := codes(t, e, b.ID); cs["pd_skipped"] != 1 || cs["fallback"] != 1 {
		t.Fatalf("timeline = %v", cs)
	}
}

// A person (or an escalation step) can send an incident in standby to PagerDuty at once; a
// comment on an incident PagerDuty has becomes a note there.
func TestEscalatePDAndNotes(t *testing.T) {
	ctx := context.Background()
	e, st, rec, _ := setup(t)
	rec.channels = true
	pdMode(st, model.PDModeBackup, nil)
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "app-01", "errors", "critical", "firing")}); err != nil {
		t.Fatal(err)
	}
	a := active(t, e)[0]
	if _, err := e.Act(ctx, a.ID, "comment", "eng", "looking"); err != nil {
		t.Fatal(err)
	}
	if cmds := rec.take(); len(cmds) != 0 {
		t.Fatalf("PagerDuty has no incident to note: %+v", cmds)
	}
	if _, err := e.EscalatePD(ctx, a.ID, "manual", "eng", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.EscalatePD(ctx, a.ID, "manual", "eng", nil); !errors.Is(err, alert.ErrPDHas) {
		t.Fatalf("second escalation: %v", err)
	}
	cmds := rec.take()
	if len(cmds) != 1 || cmds[0].Action != alert.PDTrigger {
		t.Fatalf("cmds = %+v", cmds)
	}
	e.PDResult(ctx, a.ID, alert.PDTrigger, "default", "default", nil)
	if _, err := e.Act(ctx, a.ID, "comment", "eng", "rolling back"); err != nil {
		t.Fatal(err)
	}
	cmds = rec.take()
	if len(cmds) != 1 || cmds[0].Action != alert.PDNote || cmds[0].Text != "rolling back" || cmds[0].Actor != "eng" {
		t.Fatalf("cmds = %+v", cmds)
	}
}
