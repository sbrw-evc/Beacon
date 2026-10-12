package alert_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func (r *recorder) counts() (cmds, fallback, followUp int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cmds), len(r.fallback), len(r.followUp)
}

func (r *recorder) lastFollowUp() alert.Alert {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.followUp[len(r.followUp)-1]
}

func pdOff(st *store.Store) {
	st.Write(func(d *store.Data) { d.Settings.Alerting.PagerDuty.Enabled = false })
}

func codes(t *testing.T, e *alert.Engine, id string) map[string]int {
	t.Helper()
	_, entries, err := e.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, en := range entries {
		out[en.Code]++
		if en.Kind == alert.KindPagerDuty {
			out["kind:pagerduty"]++
		}
	}
	return out
}

// Without PagerDuty an incident is not "not delivered": its PagerDuty state is off, it is not
// counted as not taken, nothing about PagerDuty is on its timeline, and backup notification
// goes out at once instead of after 2 minutes.
func TestPagerDutyOff(t *testing.T) {
	ctx := context.Background()
	e, st, rec, c := setup(t)
	pdOff(st)
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "app-01", "errors", "critical", "firing")}); err != nil {
		t.Fatal(err)
	}
	a := active(t, e)[0]
	if a.PD.State != alert.PDOff || a.PD.Error != "" || a.PD.Retry != "" {
		t.Fatalf("pd = %+v", a.PD)
	}
	if n, _, _ := rec.counts(); n != 0 {
		t.Fatalf("nothing is sent to PagerDuty: %+v", rec.take())
	}
	page, err := e.List(ctx, alert.Filter{Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Counts.PDNotTaken != 0 || page.Counts.PDEnabled {
		t.Errorf("counts = %+v", page.Counts)
	}
	if p, _ := e.List(ctx, alert.Filter{Status: "active", PD: "failed"}); len(p.Alerts) != 0 {
		t.Errorf("not listed as not taken: %+v", p.Alerts)
	}
	if _, n, _ := rec.counts(); n != 1 || rec.fallback[0].ID != a.ID {
		t.Fatalf("backup notification at once: %d", n)
	}
	got, _, _ := e.Get(ctx, a.ID)
	if !got.Fallback || got.FallbackState != alert.FallbackPending {
		t.Fatalf("fallback = %+v", got)
	}
	cs := codes(t, e, a.ID)
	if cs["kind:pagerduty"] != 0 || cs["fallback"] != 1 {
		t.Errorf("timeline = %v", cs)
	}

	// Ticks while PagerDuty is off change nothing and do not log failures.
	c.advance(10 * time.Minute)
	e.Tick(ctx)
	if n, _, _ := rec.counts(); n != 0 {
		t.Fatalf("still nothing sent: %+v", rec.take())
	}
	if got, _, _ := e.Get(ctx, a.ID); got.PD.State != alert.PDOff {
		t.Fatalf("pd = %+v", got.PD)
	}

	// A late answer of the gateway (PagerDuty turned off after the command was made) is not
	// a failure either.
	e.PDResult(ctx, a.ID, alert.PDTrigger, "", "", alert.ErrPDOff)
	if got, _, _ := e.Get(ctx, a.ID); got.PD.State != alert.PDOff || got.PD.Error != "" {
		t.Fatalf("pd = %+v", got.PD)
	}
	if cs := codes(t, e, a.ID); cs["kind:pagerduty"] != 0 {
		t.Errorf("timeline = %v", cs)
	}

	// Turned on: the open incident is sent to PagerDuty.
	st.Write(func(d *store.Data) { d.Settings.Alerting.PagerDuty.Enabled = true })
	e.Tick(ctx)
	cmds := rec.take()
	if len(cmds) != 1 || cmds[0].Action != alert.PDTrigger || cmds[0].Alert.ID != a.ID {
		t.Fatalf("sent once PagerDuty is on: %+v", cmds)
	}
	page, _ = e.List(ctx, alert.Filter{Status: "active"})
	if !page.Counts.PDEnabled {
		t.Errorf("counts = %+v", page.Counts)
	}
}

// Delivery errors are on the timeline as codes the interface translates, not as English text.
func TestPagerDutyErrorCodes(t *testing.T) {
	ctx := context.Background()
	e, _, _, _ := setup(t)
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "app-01", "errors", "critical", "firing")})
	a := active(t, e)[0]
	e.PDResult(ctx, a.ID, alert.PDTrigger, "default", "", &alert.DeliveryError{Code: "no_key", Msg: "no key"})
	got, entries, _ := e.Get(ctx, a.ID)
	if got.PD.State != alert.PDFailed || got.PD.ErrorCode != "no_key" {
		t.Fatalf("pd = %+v", got.PD)
	}
	last := entries[len(entries)-1]
	if last.Code != "pd_failed" || last.Args["code"] != "no_key" || last.Args["action"] != "trigger" {
		t.Fatalf("entry = %+v", last)
	}
	e.PDResult(ctx, a.ID, alert.PDTrigger, "", "", &alert.DeliveryError{Code: "below_threshold", Detail: "error", Err: alert.ErrPDSkipped})
	_, entries, _ = e.Get(ctx, a.ID)
	if last := entries[len(entries)-1]; last.Code != "pd_skipped" || last.Args["code"] != "below_threshold" || last.Args["min"] != "error" {
		t.Fatalf("entry = %+v", last)
	}
}

// Incidents that were marked failed by an earlier version only because PagerDuty was off are
// shown as off, and the false failures leave their timelines.
func TestPagerDutyOffMigration(t *testing.T) {
	ctx := context.Background()
	e, _, _, _ := setup(t)
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "app-01", "errors", "critical", "firing")})
	a := active(t, e)[0]
	e.PDResult(ctx, a.ID, alert.PDTrigger, "", "", errors.New("PagerDuty is not enabled"))
	if _, err := e.DB().Exec(ctx, `INSERT INTO alert_timeline (alert_id, at, kind, code, args) VALUES ($1, now(), 'pagerduty', 'pd_failed',
		'{"action": "trigger", "error": "PagerDuty is not enabled"}')`, a.ID); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := e.Get(ctx, a.ID); got.PD.State != alert.PDFailed {
		t.Fatalf("legacy state = %+v", got.PD)
	}
	if err := alert.EnsureSchema(ctx, e.DB()); err != nil {
		t.Fatal(err)
	}
	got, _, _ := e.Get(ctx, a.ID)
	if got.PD.State != alert.PDOff || got.PD.Error != "" || got.PD.Retry != "" {
		t.Fatalf("migrated = %+v", got.PD)
	}
	if cs := codes(t, e, a.ID); cs["pd_failed"] != 0 {
		t.Errorf("timeline = %v", cs)
	}
	if p, _ := e.List(ctx, alert.Filter{Status: "active"}); p.Counts.PDNotTaken != 0 {
		t.Errorf("counts = %+v", p.Counts)
	}
}

// An acknowledged incident gets no backup notification, also one that was queued before.
func TestFallbackNotForAcknowledged(t *testing.T) {
	ctx := context.Background()
	e, _, rec, c := setup(t)
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "app-01", "errors", "critical", "firing")})
	a := active(t, e)[0]
	c.advance(time.Minute)
	if _, err := e.Act(ctx, a.ID, "ack", "jane", ""); err != nil {
		t.Fatal(err)
	}
	c.advance(alert.DefaultFallbackAfter)
	e.Tick(ctx)
	if _, n, _ := rec.counts(); n != 0 {
		t.Fatalf("no backup notification for an acknowledged incident: %d", n)
	}

	// Queued, then acknowledged before the notifier got to it: it is not sent.
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "b", "app-01", "latency", "critical", "firing")})
	b := active(t, e)[0]
	if b.ID == a.ID {
		b = active(t, e)[1]
	}
	c.advance(alert.DefaultFallbackAfter)
	e.Tick(ctx)
	if _, n, _ := rec.counts(); n != 1 {
		t.Fatalf("due: %d", n)
	}
	if _, err := e.Act(ctx, b.ID, "ack", "jane", ""); err != nil {
		t.Fatal(err)
	}
	due, err := e.FallbackDue(ctx, b.ID)
	if err != nil || due {
		t.Fatalf("due = %v %v", due, err)
	}
	got, _, _ := e.Get(ctx, b.ID)
	if got.Fallback || got.FallbackState != "" {
		t.Fatalf("cancelled: %+v", got)
	}
	if cs := codes(t, e, b.ID); cs["fallback_cancelled"] != 1 {
		t.Errorf("timeline = %v", cs)
	}
	c.advance(2 * alert.DefaultFallbackRetry)
	e.Tick(ctx)
	if _, n, _ := rec.counts(); n != 1 {
		t.Fatalf("not handed over again: %d", n)
	}
	if p, _ := e.List(ctx, alert.Filter{Status: "active"}); p.Counts.Fallback != 0 {
		t.Errorf("not counted: %+v", p.Counts)
	}
}

// The delay and the minimum severity of backup notification come from the settings.
func TestFallbackSettings(t *testing.T) {
	ctx := context.Background()
	e, st, rec, c := setup(t)
	delay := 30
	st.Write(func(d *store.Data) {
		d.Settings.Alerting.Notify.DelaySeconds = &delay
		d.Settings.Alerting.Notify.MinSeverity = "warning"
	})
	e.Ingest(ctx, []alert.Incoming{
		ev("CON-1", "w", "app-01", "disk", "warning", "firing"),
		ev("CON-1", "i", "app-01", "info", "info", "firing"),
	})
	c.advance(29 * time.Second)
	e.Tick(ctx)
	if _, n, _ := rec.counts(); n != 0 {
		t.Fatalf("not before the delay: %d", n)
	}
	c.advance(time.Second)
	e.Tick(ctx)
	if _, n, _ := rec.counts(); n != 1 || rec.fallback[0].Severity != "warning" {
		t.Fatalf("a warning goes after 30 s, info never: %+v", rec.fallback)
	}
	e.FallbackDue(ctx, rec.fallback[0].ID)
	e.FallbackDone(ctx, rec.fallback[0].ID, nil)
	c.advance(time.Hour)
	e.Tick(ctx)
	if _, n, _ := rec.counts(); n != 1 {
		t.Fatalf("info is below the threshold: %d", n)
	}

	// 0 is at once, also with PagerDuty on.
	zero := 0
	st.Write(func(d *store.Data) {
		d.Settings.Alerting.Notify.DelaySeconds = &zero
		d.Settings.Alerting.Notify.MinSeverity = ""
	})
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "e", "app-01", "errors", "error", "firing"), ev("CON-1", "w2", "db-01.example.com", "disk", "warning", "firing")})
	if _, n, _ := rec.counts(); n != 2 || rec.fallback[1].Severity != "error" {
		t.Fatalf("at once, error and above by default: %+v", rec.fallback)
	}
	_, entries, _ := e.Get(ctx, rec.fallback[1].ID)
	for _, en := range entries {
		if en.Code == "fallback" && (en.Args["after_s"] != "0" || en.Args["reason"] != "pd_not_taken") {
			t.Errorf("entry = %+v", en)
		}
	}

	// Without a delay of its own, backup notification waits 2 minutes with PagerDuty only.
	st.Write(func(d *store.Data) { d.Settings.Alerting.Notify.DelaySeconds = nil })
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "c", "lonely", "down", "critical", "firing")})
	if _, n, _ := rec.counts(); n != 2 {
		t.Fatalf("waits for PagerDuty: %d", n)
	}
	c.advance(alert.DefaultFallbackAfter)
	e.Tick(ctx)
	if _, n, _ := rec.counts(); n != 3 {
		t.Fatalf("after 2 minutes: %d", n)
	}
}

// The people backup notification reached learn that the incident was taken or resolved.
func TestFollowUpAfterAckAndResolve(t *testing.T) {
	ctx := context.Background()
	e, st, rec, c := setup(t)
	pdOff(st)
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "app-01", "errors", "critical", "firing")})
	a := active(t, e)[0]
	if due, err := e.FallbackDue(ctx, a.ID); !due || err != nil {
		t.Fatalf("due = %v %v", due, err)
	}
	sent := []alert.Notified{{Channel: "email", Address: "lead@example.com", Recipient: "u:U-1"}, {Channel: "telegram", Address: "1001", Recipient: "u:U-1"}}
	if err := e.FallbackDone(ctx, a.ID, sent); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Minute)
	if _, err := e.Act(ctx, a.ID, "ack", "jane", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, n := rec.counts(); n != 1 {
		t.Fatalf("follow-up on ack: %d", n)
	}
	fu := rec.lastFollowUp()
	if fu.FollowUp != alert.StatusAcknowledged || fu.AckedBy != "jane" || len(fu.Notified) != 2 {
		t.Fatalf("follow-up = %+v", fu)
	}
	// Not reported yet: a later tick hands it over again.
	c.advance(alert.DefaultFallbackRetry)
	e.Tick(ctx)
	if _, _, n := rec.counts(); n != 2 {
		t.Fatalf("handed over again: %d", n)
	}
	if err := e.FollowUpDone(ctx, a.ID, alert.StatusAcknowledged); err != nil {
		t.Fatal(err)
	}
	c.advance(alert.DefaultFallbackRetry)
	e.Tick(ctx)
	if _, _, n := rec.counts(); n != 2 {
		t.Fatalf("done: %d", n)
	}

	resolved := ev("CON-1", "a", "app-01", "errors", "critical", "resolved")
	e.Ingest(ctx, []alert.Incoming{resolved})
	if _, _, n := rec.counts(); n != 3 || rec.lastFollowUp().FollowUp != alert.StatusResolved {
		t.Fatalf("follow-up on resolve: %d", n)
	}
	e.FollowUpDone(ctx, a.ID, alert.StatusResolved)
	if got, _, _ := e.Get(ctx, a.ID); got.FollowUp != "" {
		t.Fatalf("done: %+v", got)
	}

	// Nobody was reached: no follow-up.
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "b", "app-01", "latency", "critical", "firing")})
	b := active(t, e)[0]
	e.FallbackDone(ctx, b.ID, nil)
	e.Act(ctx, b.ID, "resolve", "jane", "")
	if _, _, n := rec.counts(); n != 3 {
		t.Fatalf("nobody to tell: %d", n)
	}

	// Acknowledged while the messages were going out: the follow-up follows them.
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "d", "db-01.example.com", "disk", "critical", "firing")})
	d := active(t, e)[0]
	if due, _ := e.FallbackDue(ctx, d.ID); !due {
		t.Fatal("due")
	}
	e.Act(ctx, d.ID, "ack", "jane", "")
	e.FallbackDone(ctx, d.ID, sent[:1])
	if _, _, n := rec.counts(); n != 4 || rec.lastFollowUp().ID != d.ID {
		t.Fatalf("follow-up after a late report: %d", n)
	}
}

// A test event (label umbrella_test=true) goes neither to PagerDuty nor to backup notification.
func TestTestLabelGoesNowhere(t *testing.T) {
	ctx := context.Background()
	e, st, rec, c := setup(t)
	in := ev("CON-1", "t", "app-01", "errors", "critical", "firing")
	in.Labels = map[string]string{alert.TestLabel: "true"}
	e.Ingest(ctx, []alert.Incoming{in})
	a := active(t, e)[0]
	if a.PD.State != alert.PDSkipped {
		t.Fatalf("pd = %+v", a.PD)
	}
	in2 := in
	in2.Severity = "error"
	in2.Key = "t2"
	e.Ingest(ctx, []alert.Incoming{in2})
	c.advance(time.Hour)
	e.Tick(ctx)
	if n, f, _ := rec.counts(); n != 0 || f != 0 {
		t.Fatalf("sent: %+v %+v", rec.cmds, rec.fallback)
	}
	if cs := codes(t, e, a.ID); cs["pd_skipped"] != 1 {
		t.Errorf("timeline = %v", cs)
	}
	pdOff(st)
	zero := 0
	st.Write(func(d *store.Data) { d.Settings.Alerting.Notify.DelaySeconds = &zero })
	in3 := in
	in3.Key, in3.Signal = "t3", "latency"
	e.Ingest(ctx, []alert.Incoming{in3})
	c.advance(time.Hour)
	e.Tick(ctx)
	if n, f, _ := rec.counts(); n != 0 || f != 0 {
		t.Fatalf("sent without PagerDuty: %+v %+v", rec.cmds, rec.fallback)
	}
}
