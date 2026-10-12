package pagerduty_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/pagerduty"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/pagerduty/pdtest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store/storetest"
)

// The incident is acknowledged and resolved in the PagerDuty service its trigger went to, even
// after the alert is routed to another team; a reopened alert goes by the current route.
func TestCloseFollowsTheTrigger(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := storetest.TempDatabase(t)
	db, err := pgxpool.New(ctx, cfg.DSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := alert.EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	fake := pdtest.New(t)
	st := store.New()
	st.Write(func(d *store.Data) {
		d.Teams["T-1"] = &model.Team{ID: "T-1", Name: "DBA"}
		d.Teams["T-2"] = &model.Team{ID: "T-2", Name: "Ops"}
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "db-01", Status: model.CIStatusActive}
		d.Services["S-1"] = &model.Service{ID: "S-1", Name: "Billing", OwnerTeamID: "T-1", Status: model.ServiceActive, CIIDs: []string{"CI-1"}}
		d.Settings.Alerting.PagerDuty = model.PagerDuty{Enabled: true, EventsURL: fake.EventsURL(), RoutingKeyRef: "default", Routes: []model.PDRoute{
			{ID: "PDR-1", Name: "DBA", TeamID: "T-1", RoutingKeyRef: "dba"},
			{ID: "PDR-2", Name: "Ops", TeamID: "T-2", RoutingKeyRef: "ops"},
		}}
	})
	e := alert.New(db, st)
	g := pagerduty.New(st, secrets{"default": "default-key", "dba": "dba-key", "ops": "ops-key"})
	g.Backoff = time.Millisecond
	e.SetSender(g)
	g.SetResults(e)
	go g.Run(ctx)

	wait := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("waiting for %s", what)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	in := alert.Incoming{ConnectorID: "CON-1", Key: "k", Title: "Disk full", CI: "db-01", Signal: "disk", Severity: "critical", Status: alert.SourceFiring}
	if err := e.Ingest(ctx, []alert.Incoming{in}); err != nil {
		t.Fatal(err)
	}
	wait("the trigger", func() bool { return len(fake.Events()) == 1 })
	id := fake.Events()[0].DedupKey[len("umb-"):]
	if k := fake.Events()[0].RoutingKey; k != "dba-key" {
		t.Fatalf("trigger key = %s", k)
	}
	wait("accepted", func() bool {
		a, _, _ := e.Get(ctx, id)
		return a.PD.State == alert.PDAccepted && a.PD.RouteID == "PDR-1"
	})

	// The service moves to another team while the incident is open.
	st.Write(func(d *store.Data) { d.Services["S-1"].OwnerTeamID = "T-2" })
	if err := e.Reroute(ctx); err != nil {
		t.Fatal(err)
	}
	if a, _, _ := e.Get(ctx, id); a.Route.Team == nil || a.Route.Team.ID != "T-2" {
		t.Fatalf("routed again: %+v", a.Route)
	}
	if _, err := e.Act(ctx, id, "ack", "eng", ""); err != nil {
		t.Fatal(err)
	}
	wait("the acknowledgement", func() bool { return len(fake.Events()) == 2 })
	in.Status = alert.SourceResolved
	if err := e.Ingest(ctx, []alert.Incoming{in}); err != nil {
		t.Fatal(err)
	}
	wait("the resolve", func() bool { return len(fake.Events()) == 3 })
	for _, ev := range fake.Events()[1:] {
		if ev.RoutingKey != "dba-key" || ev.DedupKey != "umb-"+id {
			t.Errorf("%s went to %s, not to the service of the trigger", ev.EventAction, ev.RoutingKey)
		}
	}

	// Firing again within the window reopens the alert: a new PagerDuty incident by the current route.
	in.Status = alert.SourceFiring
	if err := e.Ingest(ctx, []alert.Incoming{in}); err != nil {
		t.Fatal(err)
	}
	wait("the new trigger", func() bool { return len(fake.Events()) == 4 })
	if ev := fake.Events()[3]; ev.EventAction != "trigger" || ev.RoutingKey != "ops-key" {
		t.Errorf("reopened: %s via %s", ev.EventAction, ev.RoutingKey)
	}
}
