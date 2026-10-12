package pagerduty_test

import (
	"context"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/pagerduty"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/pagerduty/pdtest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func syncGateway(t *testing.T, sync model.PDSync) (*pagerduty.Gateway, *pdtest.Fake, *results) {
	t.Helper()
	fake := pdtest.New(t)
	st := store.New()
	st.Write(func(d *store.Data) {
		d.Settings.Alerting.PagerDuty = model.PagerDuty{Enabled: true, EventsURL: fake.EventsURL(), APIURL: fake.APIURL(), RoutingKeyRef: "ref",
			APITokenRef: "tok", ServiceID: pdtest.ServiceID, Sync: sync}
	})
	g := pagerduty.New(st, secrets{"ref": "key-1", "tok": pdtest.Token})
	g.Backoff = time.Millisecond
	res := &results{}
	g.SetResults(res)
	return g, fake, res
}

func pdAlert(id, status, pdState, incident string) alert.Alert {
	return alert.Alert{ID: id, Status: status, PD: alert.PD{Key: "umb-" + id, State: pdState, IncidentID: incident, Queue: pdtest.ServiceID}}
}

// The read-back brings what happened in PagerDuty when no webhook did: an acknowledgement, a
// resolution (the incident is no longer open), an unacknowledgement and the incident itself.
func TestSyncReadsBack(t *testing.T) {
	g, fake, res := syncGateway(t, model.PDSync{})
	fake.SetIncident(pdtest.Incident{ID: "PI1", Key: "umb-A-1", Status: "acknowledged", By: "Ann"})
	fake.SetIncident(pdtest.Incident{ID: "PI2", Key: "umb-A-2", Status: "resolved", By: "Bob"})
	fake.SetIncident(pdtest.Incident{ID: "PI3", Key: "umb-A-3", Status: "triggered"})
	fake.SetIncident(pdtest.Incident{ID: "PI4", Key: "umb-A-4", Status: "triggered"})
	fake.SetIncident(pdtest.Incident{ID: "PI5", Key: "umb-A-5", Status: "acknowledged", By: "Ann"})
	res.active = []alert.Alert{
		pdAlert("A-1", alert.StatusOpen, alert.PDAccepted, ""),
		pdAlert("A-2", alert.StatusOpen, alert.PDAccepted, "PI2"),
		pdAlert("A-3", alert.StatusAcknowledged, alert.PDAcked, "PI3"),
		pdAlert("A-4", alert.StatusOpen, alert.PDAccepted, ""),
		pdAlert("A-5", alert.StatusAcknowledged, alert.PDAcked, "PI5"), // in step: nothing
	}
	out, err := g.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if out.Checked != 5 || out.Applied != 4 {
		t.Fatalf("out = %+v", out)
	}
	want := map[string]string{"umb-A-1": "incident.acknowledged/Ann", "umb-A-2": "incident.resolved/Bob", "umb-A-3": "incident.unacknowledged/", "umb-A-4": "incident.triggered/"}
	for _, u := range res.inbound {
		if want[u.DedupKey] != u.EventType+"/"+u.Actor {
			t.Fatalf("%s: %s/%s", u.DedupKey, u.EventType, u.Actor)
		}
		if u.IncidentID == "" || u.IncidentURL == "" {
			t.Fatalf("incident not named: %+v", u)
		}
	}
	if st := g.Status(); st.LastSyncAt == nil || st.SyncApplied != 4 {
		t.Fatalf("status = %+v", st)
	}
}

// Comments become notes of the incident, written as the From user; the priority is set by name.
func TestNotesAndPriority(t *testing.T) {
	g, fake, res := syncGateway(t, model.PDSync{FromEmail: "bot@example.com", Notes: true, Priority: true})
	fake.SetIncident(pdtest.Incident{ID: "PI1", Key: "umb-A-1", Status: "triggered"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)
	a := pdAlert("A-1", alert.StatusOpen, alert.PDAccepted, "")
	g.Send(alert.Command{Action: alert.PDNote, Alert: a, Text: "rolling back", Actor: "eng"})
	deadline := time.Now().Add(5 * time.Second)
	for {
		res.mu.Lock()
		n := len(res.notes)
		res.mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := g.SetPriority(ctx, a, "P2"); err != nil {
		t.Fatal(err)
	}
	notes, prios, from := fake.Snapshot()
	if len(notes["PI1"]) != 1 || notes["PI1"][0] != "[Umbrella] eng: rolling back" || prios["PI1"] != "PRI2" {
		t.Fatalf("notes=%v priorities=%v", notes, prios)
	}
	if len(from) != 2 || from[0] != "bot@example.com" {
		t.Fatalf("from = %v", from)
	}
	if res.notes[0] != "pd_note " {
		t.Fatalf("timeline = %v", res.notes)
	}
	if err := g.SetPriority(ctx, a, "P5"); err != pagerduty.ErrNoPriority {
		t.Fatalf("unknown priority: %v", err)
	}
}

// On-call people of the route are read and given the notifications, matched with Umbrella
// users by e-mail.
func TestOnCall(t *testing.T) {
	g, fake, _ := syncGateway(t, model.PDSync{OnCall: true})
	fake.OnCall = []pdtest.OnCallUser{{Name: "Ann", Email: "ann@example.com", Level: 1}, {Name: "Lead", Email: "lead@example.com", Level: 2}}
	g.SetUsers(func(email string) *model.User {
		if email == "ann@example.com" {
			return &model.User{ID: "U-1", Telegram: "1001"}
		}
		return nil
	})
	if err := g.RefreshOnCall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := g.OnCallByRoute()[pagerduty.DefaultRoute]; len(got) != 2 || got[0].Name != "Ann" || got[0].UserID != "U-1" {
		t.Fatalf("on call = %+v", got)
	}
	people := g.OnCallPeople(alert.Alert{})
	if len(people) != 1 || people[0].UserID != "U-1" || people[0].Telegram != "1001" || people[0].Role != "on-call" {
		t.Fatalf("people = %+v", people)
	}
}
