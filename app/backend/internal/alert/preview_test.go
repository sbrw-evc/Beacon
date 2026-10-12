package alert_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// The preview in the CI card must be exactly the route the engine gives an incident.
func TestPreviewEqualsEngineRoute(t *testing.T) {
	ctx := context.Background()
	e, st, _, c := setup(t)
	for _, ci := range []string{"db-01.example.com", "app-01", "lonely"} {
		if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", ci, ci, "cpu", "critical", "firing")}); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range active(t, e) {
		p, ok := alert.PreviewCI(st, a.CIID, c.now())
		if !ok {
			t.Fatalf("no preview for %s", a.CIID)
		}
		p.At = a.Route.At
		if !reflect.DeepEqual(p, a.Route) {
			t.Errorf("%s: preview %+v != engine %+v", a.CIName, p, a.Route)
		}
	}
	if _, ok := alert.PreviewCI(st, "CI-404", c.now()); ok {
		t.Error("unknown item")
	}

	// db-01 is in Payments (critical) and Reports (low): the primary service is Payments.
	p, _ := alert.PreviewCI(st, "CI-1", c.now())
	if p.Service == nil || p.Service.ID != "S-1" || p.Team == nil || p.Team.ID != "T-2" || len(p.Services) != 2 {
		t.Fatalf("primary = %+v", p)
	}

	// Reports alone goes to its empty team's parent; its item db-01 goes by Payments instead.
	r, elsewhere, ok := alert.PreviewService(st, "S-2", c.now())
	if !ok || r.Team == nil || r.Team.ID != "T-3" || len(r.People) != 2 || r.Via != alert.ViaService {
		t.Fatalf("service preview = %+v", r)
	}
	if len(elsewhere) != 1 || elsewhere[0].CI.ID != "CI-1" || elsewhere[0].Service.ID != "S-1" || elsewhere[0].Team.ID != "T-2" {
		t.Fatalf("elsewhere = %+v", elsewhere)
	}
	if _, elsewhere, _ := alert.PreviewService(st, "S-1", c.now()); len(elsewhere) != 0 {
		t.Fatalf("Payments routes its own items: %+v", elsewhere)
	}
}

// A person in two teams gets the incidents of both teams' services.
func TestMemberOfTwoTeamsGetsBoth(t *testing.T) {
	ctx := context.Background()
	e, st, _, _ := setup(t)
	st.Write(func(d *store.Data) {
		d.Users["U-5"] = &model.User{ID: "U-5", Username: "both", Name: "Both Teams", TeamIDs: []string{"T-2", "T-4"}, Profile: model.Profile{Email: "both@example.com"}}
		d.Teams["T-4"] = &model.Team{ID: "T-4", Name: "Storage"}
		d.ConfigItems["CI-4"] = &model.ConfigItem{ID: "CI-4", Name: "nas-01", Kind: model.CIKindDevice, Status: model.CIStatusActive}
		d.Services["S-4"] = &model.Service{ID: "S-4", Name: "Files", OwnerTeamID: "T-4", Criticality: model.CriticalityHigh, Status: model.ServiceActive, CIIDs: []string{"CI-4"}}
	})
	for _, ci := range []string{"db-01.example.com", "nas-01"} {
		if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", ci, ci, "cpu", "critical", "firing")}); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]string{}
	for _, a := range active(t, e) {
		for _, p := range a.Route.People {
			if p.UserID == "U-5" {
				got[a.CIID] = a.Route.Team.ID
			}
		}
	}
	if got["CI-1"] != "T-2" || got["CI-4"] != "T-4" {
		t.Fatalf("U-5 got %v, want the incidents of both teams", got)
	}
}

// A team with a channel: the route keeps the lead and the channel instead of every member; a
// channel without a lead still takes the incident instead of going up to the parent team.
func TestTeamChannelRoute(t *testing.T) {
	ctx := context.Background()
	e, st, _, c := setup(t)
	st.Write(func(d *store.Data) {
		d.Teams["T-2"].Email = "sre-duty@example.com"
		d.Teams["T-3"].Telegram = "-100500"
	})
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "db-01.example.com", "cpu", "critical", "firing")}); err != nil {
		t.Fatal(err)
	}
	r := active(t, e)[0].Route
	if r.Channel == nil || r.Channel.Email != "sre-duty@example.com" || len(r.People) != 1 || r.People[0].UserID != "U-1" || r.Via != alert.ViaService {
		t.Fatalf("route = %+v", r)
	}
	if len(r.Recipients()) != 1 {
		t.Fatalf("recipients = %+v", r.Recipients())
	}
	p, _, _ := alert.PreviewService(st, "S-2", c.now())
	if p.Channel == nil || p.Channel.Telegram != "-100500" || len(p.People) != 0 || p.Via != alert.ViaService {
		t.Fatalf("empty team with a channel = %+v", p)
	}
}

// A team whose only channel is a Teams or Zoom webhook is a team with its own channel too; the
// webhook URLs are redacted where the alert is shown.
func TestWebhookOnlyTeamChannelRoute(t *testing.T) {
	ctx := context.Background()
	e, st, _, c := setup(t)
	const hook = "https://example.webhook.office.com/webhookb2/x/IncomingWebhook/y/secret-a1b2"
	st.Write(func(d *store.Data) {
		d.Teams["T-2"].Teams = hook
		d.Teams["T-3"].Zoom = "https://integrations.zoom.us/chat/webhooks/incomingwebhook/secret-c3d4"
	})
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "db-01.example.com", "cpu", "critical", "firing")}); err != nil {
		t.Fatal(err)
	}
	a := active(t, e)[0]
	r := a.Route
	if r.Channel == nil || r.Channel.Teams != hook || len(r.People) != 1 || r.People[0].UserID != "U-1" {
		t.Fatalf("route = %+v", r)
	}
	a.Notified = []alert.Notified{{Channel: "teams", Address: hook}, {Channel: "email", Address: "lead@example.com"}}
	red := a.Redacted()
	if red.Route.Channel.Teams != model.RedactURL(hook) || red.Notified[0].Address != model.RedactURL(hook) || red.Notified[1].Address != "lead@example.com" {
		t.Fatalf("redacted = %+v %+v", red.Route.Channel, red.Notified)
	}
	if a.Route.Channel.Teams != hook || a.Notified[0].Address != hook {
		t.Fatal("redaction changed the alert")
	}
	p, _, _ := alert.PreviewService(st, "S-2", c.now())
	if p.Channel == nil || p.Channel.Zoom == "" || len(p.People) != 0 {
		t.Fatalf("zoom-only team = %+v", p)
	}
}
