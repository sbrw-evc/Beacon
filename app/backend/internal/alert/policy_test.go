package alert

import (
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func TestPolicyDefaultsAndOverrides(t *testing.T) {
	st := store.New()
	e := New(nil, st)
	p := e.policy()
	if p.window != DefaultWindow || p.fallbackRetry != DefaultFallbackRetry || p.retention != Retention || p.testLifetime != TestLifetime || p.delay != 0 {
		t.Fatalf("defaults = %+v", p)
	}
	st.Write(func(d *store.Data) { d.Settings.Alerting.PagerDuty.Enabled = true })
	if p := e.policy(); p.delay != DefaultFallbackAfter {
		t.Fatalf("delay while PagerDuty is on = %s", p.delay)
	}
	// The fields of the engine stay the defaults tests and callers may change.
	e.Window = time.Minute
	if p := e.policy(); p.window != time.Minute {
		t.Fatalf("engine window = %s", p.window)
	}
	// A custom reopen window and the rest of the policy replace the defaults.
	st.Write(func(d *store.Data) {
		d.Settings.Alerting.Policy = &model.AlertPolicy{ReopenWindowSeconds: 1800, FallbackDelaySeconds: 30, FallbackRetrySeconds: 60,
			RetentionDays: 7, TestLifetimeSeconds: 120}
	})
	p = e.policy()
	if p.window != 30*time.Minute || p.delay != 30*time.Second || p.fallbackRetry != time.Minute || p.retention != 7*24*time.Hour ||
		p.testLifetime != 2*time.Minute {
		t.Fatalf("custom policy = %+v", p)
	}
	// The delay of backup notification set in its own settings still wins.
	zero := 0
	st.Write(func(d *store.Data) { d.Settings.Alerting.Notify.DelaySeconds = &zero })
	if p := e.policy(); p.delay != 0 {
		t.Fatalf("explicit delay = %s", p.delay)
	}
	// Values beyond the limits are capped.
	st.Write(func(d *store.Data) { d.Settings.Alerting.Policy = &model.AlertPolicy{ReopenWindowSeconds: 1 << 30} })
	if p := e.policy(); p.window != MaxWindow {
		t.Fatalf("capped window = %s", p.window)
	}
}

func TestReopen(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	resolved := now.Add(-time.Minute)
	a := &Alert{Status: StatusResolved, ResolvedAt: &resolved, ResolvedBy: "x", AckedBy: "y", Fallback: true, FallbackState: FallbackSent,
		Notified: []Notified{{Channel: "email", Address: "a@b"}}, FollowUp: "resolved",
		PD: PD{State: PDAccepted, Route: "r", RouteID: "R-1", IncidentID: "Q1", IncidentURL: "u"}}
	a.reopen(now)
	if a.Status != StatusOpen || a.ResolvedAt != nil || a.ResolvedBy != "" || a.AckedBy != "" || a.Fallback || a.FallbackState != "" ||
		a.Notified != nil || a.FollowUp != "" || !a.OpenedAt.Equal(now) {
		t.Fatalf("reopened = %+v", a)
	}
	if a.PD.State != PDPending || a.PD.Route != "" || a.PD.RouteID != "" || a.PD.IncidentID != "" || len(a.PD.OldIncidents) != 1 || a.PD.OldIncidents[0] != "Q1" {
		t.Fatalf("pd = %+v", a.PD)
	}
}
