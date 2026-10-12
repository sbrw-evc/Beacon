package pagerduty_test

import (
	"context"
	"slices"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/pagerduty"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/pagerduty/pdtest"
)

// The queues are the services of PagerDuty with their policy, teams, open incidents and the
// routes sending to them; the hook gets them after each read.
func TestQueues(t *testing.T) {
	g, fake, _ := syncGateway(t, model.PDSync{})
	fake.Extra = []pdtest.Service{{ID: "PSVC2", Name: "Billing", Status: "maintenance", TeamID: "PT2", Team: "SRE"}}
	fake.SetIncident(pdtest.Incident{ID: "PI1", Key: "k1", Status: "triggered"})
	fake.SetIncident(pdtest.Incident{ID: "PI2", Key: "k2", Status: "acknowledged", Service: "PSVC2"})
	fake.SetIncident(pdtest.Incident{ID: "PI3", Key: "k3", Status: "triggered", Service: "PSVC2"})
	var hooked []pagerduty.Queue
	g.SetQueueHook(func(_ context.Context, qs []pagerduty.Queue) { hooked = qs })
	if err := g.RefreshQueues(context.Background()); err != nil {
		t.Fatal(err)
	}
	qs := g.Queues()
	if len(qs) != 2 || len(hooked) != 2 {
		t.Fatalf("queues = %+v", qs)
	}
	pay, bill := qs[0], qs[1]
	if pay.ID != pdtest.ServiceID || pay.Policy != "Payments on-call" || pay.Triggered != 1 || pay.Acknowledged != 0 ||
		!slices.Equal(pay.Routes, []string{pagerduty.DefaultRoute}) || len(pay.Teams) != 1 || pay.Teams[0].Name != "Payments" {
		t.Fatalf("payments = %+v", pay)
	}
	if bill.Status != "maintenance" || bill.Triggered != 1 || bill.Acknowledged != 1 || len(bill.Routes) != 0 || bill.Teams[0].Name != "SRE" || bill.EventsKey {
		t.Fatalf("billing = %+v", bill)
	}
	if st := g.Status(); st.QueuesAt == nil || st.QueuesError != "" {
		t.Fatalf("status = %+v", st)
	}
}

// An incident moved to another queue in PagerDuty is read back as a queue change alone.
func TestSyncQueueMoved(t *testing.T) {
	g, fake, res := syncGateway(t, model.PDSync{})
	fake.SetIncident(pdtest.Incident{ID: "PI1", Key: "umb-A-1", Status: "triggered", Service: "PSVC2"})
	res.active = []alert.Alert{pdAlert("A-1", alert.StatusOpen, alert.PDAccepted, "PI1")}
	out, err := g.Sync(context.Background())
	if err != nil || out.Applied != 1 {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
	if u := res.inbound[0]; u.EventType != "" || u.Queue != "PSVC2" {
		t.Fatalf("update = %+v", u)
	}
}
