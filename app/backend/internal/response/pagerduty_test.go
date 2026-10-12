package response_test

import (
	"context"
	"sync"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type fakePD struct {
	e          *alert.Engine
	mu         sync.Mutex
	escalated  []string
	priorities []string
}

func (f *fakePD) Escalate(ctx context.Context, id string) error {
	f.mu.Lock()
	f.escalated = append(f.escalated, id)
	f.mu.Unlock()
	_, err := f.e.EscalatePD(ctx, id, "response", "", nil)
	return err
}

func (f *fakePD) SetPriority(_ context.Context, a alert.Alert, priority string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.priorities = append(f.priorities, a.ID+"="+priority)
	return nil
}

// An escalation step can send the incident to PagerDuty, and the response priority is set on
// the PagerDuty incident once PagerDuty has it (once).
func TestResponsePagerDuty(t *testing.T) {
	v := setup(t, model.ModeLive)
	pd := &fakePD{e: v.e}
	v.r.SetPagerDuty(pd)
	v.st.Write(func(d *store.Data) {
		d.Settings.Alerting.PagerDuty.Enabled = true
		d.Settings.Alerting.PagerDuty.Mode = model.PDModeOff
		for i := range d.Settings.Response.Policies {
			if d.Settings.Response.Policies[i].Priority == model.SeverityLow {
				d.Settings.Response.Policies[i].Steps = []model.EscalationStep{{AfterMinutes: 0, Targets: []string{model.TargetRoute}, Methods: []string{model.CommPagerDuty}}}
			}
		}
	})
	a := v.fire(t, "wiki-01", model.SeverityWarning, model.MethodUSE, alert.SourceFiring)
	if a.PD.State != alert.PDSkipped {
		t.Fatalf("mode off keeps it from PagerDuty: %+v", a.PD)
	}
	v.tick(t)
	st := v.state(t, a.ID)
	if len(pd.escalated) != 1 || len(st.Steps) != 1 || len(st.Steps[0].Reached) != 1 || st.Steps[0].Reached[0] != "pagerduty" {
		t.Fatalf("escalated = %v, steps = %+v", pd.escalated, st.Steps)
	}
	got, _, _ := v.e.Get(context.Background(), a.ID)
	if !got.PD.Escalated || got.PD.State != alert.PDPending {
		t.Fatalf("pd = %+v", got.PD)
	}
	v.e.PDResult(context.Background(), a.ID, alert.PDTrigger, "default", "default", nil)
	v.tick(t)
	v.tick(t)
	if len(pd.priorities) != 1 || pd.priorities[0] != a.ID+"="+model.SeverityLow {
		t.Fatalf("priorities = %v", pd.priorities)
	}
	if st = v.state(t, a.ID); st.PDPriority != model.SeverityLow {
		t.Fatalf("state = %+v", st)
	}
}
