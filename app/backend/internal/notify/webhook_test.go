package notify_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/beacon/app/backend/internal/alert"
	"github.com/sbrw-evc/beacon/app/backend/internal/model"
	"github.com/sbrw-evc/beacon/app/backend/internal/notify"
	"github.com/sbrw-evc/beacon/app/backend/internal/notify/notifytest"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
)

// withWebhooks turns Teams and Zoom on: the extra Teams channel answers 400, the team has its
// own Teams channel and Zoom chat.
func withWebhooks(t *testing.T, st *store.Store) (*notifytest.Webhook, alert.Alert) {
	hook := notifytest.NewWebhook(t)
	hook.Fail["/teams/extra"] = 400
	st.Write(func(d *store.Data) {
		n := &d.Settings.Alerting.Notify
		n.Teams = model.TeamsChannel{Enabled: true}
		n.Zoom = model.ZoomChannel{Enabled: true, TokenRef: "zm"}
		n.ExtraTeams = []string{hook.URL("/teams/extra")}
		n.ExtraZoom = []string{hook.URL("/zoom/duty")}
	})
	a := incident()
	a.Route.People = a.Route.People[:1]
	a.Route.Channel = &alert.Channel{Teams: hook.URL("/teams/sre-a1b2"), Zoom: hook.URL("/zoom/sre-c3d4?format=message")}
	return hook, a
}

func byPath(reqs []notifytest.Request) map[string][]notifytest.Request {
	out := map[string][]notifytest.Request{}
	for _, r := range reqs {
		out[r.Path] = append(out[r.Path], r)
	}
	return out
}

func TestTeamsAndZoomBackupNotification(t *testing.T) {
	s, st, smtp, tg, res := setup(t)
	hook, a := withWebhooks(t, st)
	s.Deliver(context.Background(), a)

	// The other channels are still sent.
	if len(smtp.Mails()) != 2 || len(tg.Messages()) != 2 {
		t.Fatalf("mails %d, telegram %d", len(smtp.Mails()), len(tg.Messages()))
	}
	got := byPath(hook.Requests())
	if len(got) != 3 || len(got["/teams/sre-a1b2"]) != 1 || len(got["/zoom/sre-c3d4"]) != 1 || len(got["/zoom/duty"]) != 1 {
		t.Fatalf("webhooks: %+v", got)
	}

	// Teams: an Adaptive Card with the subject, the text and buttons for the links.
	tr := got["/teams/sre-a1b2"][0]
	if tr.Body["type"] != "message" || tr.ContentType != "application/json" {
		t.Fatalf("teams: %+v", tr)
	}
	att := tr.Body["attachments"].([]any)[0].(map[string]any)
	card := att["content"].(map[string]any)
	if att["contentType"] != "application/vnd.microsoft.card.adaptive" || card["type"] != "AdaptiveCard" || card["version"] != "1.4" {
		t.Fatalf("card: %+v", att)
	}
	body := card["body"].([]any)
	head := body[0].(map[string]any)
	if !strings.Contains(head["text"].(string), "INC-7 · HTTP 5xx на app-01") || head["weight"] != "Bolder" || head["wrap"] != true {
		t.Fatalf("head: %+v", head)
	}
	var text []string
	for _, b := range body[1:] {
		text = append(text, b.(map[string]any)["text"].(string))
	}
	if all := strings.Join(text, "\n"); !strings.Contains(all, "Payments SRE") || !strings.Contains(all, "Платежи") {
		t.Fatalf("text: %s", all)
	}
	actions := card["actions"].([]any)
	var titles []string
	var ack string
	for _, x := range actions {
		act := x.(map[string]any)
		if act["type"] != "Action.OpenUrl" {
			t.Fatalf("action: %+v", act)
		}
		titles = append(titles, act["title"].(string))
		if strings.Contains(act["url"].(string), "/ack/") {
			ack = act["url"].(string)
		}
	}
	if strings.Join(titles, ",") != "Подтвердить,Открыть в Beacon,Grafana,Трафик" {
		t.Fatalf("actions: %v", titles)
	}
	// The acknowledgement link names the channel, redacted.
	_, who, err := s.Links().Verify(ack[strings.Index(ack, "/ack/")+5:], time.Now())
	if err != nil || who != notify.TeamsRecipient(hook.URL("/teams/sre-a1b2")) || strings.Contains(who, "sre-a1b2") {
		t.Fatalf("ack link: %q %v", who, err)
	}

	// Zoom: the verification token, the full format, a head and a message.
	zr := got["/zoom/sre-c3d4"][0]
	if zr.Authorization != "zoom-verification-token" || zr.Query != "format=full" {
		t.Fatalf("zoom: %+v", zr)
	}
	if zr.Body["head"].(map[string]any)["text"] != head["text"] {
		t.Fatalf("zoom head: %+v", zr.Body)
	}
	msg := zr.Body["body"].([]any)[0].(map[string]any)
	if msg["type"] != "message" || !strings.Contains(msg["text"].(string), "Payments SRE") {
		t.Fatalf("zoom body: %+v", msg)
	}

	// Outcomes are recorded with redacted URLs; the reached addresses keep the URL for the
	// follow-up.
	codes := map[string][]map[string]string{}
	for _, n := range res.notes {
		codes[n.code] = append(codes[n.code], n.args)
	}
	if f := codes["notify_failed"]; len(f) != 1 || f[0]["channel"] != "teams" || f[0]["to"] != model.RedactURL(hook.URL("/teams/extra")) || !strings.Contains(f[0]["error"], "400") {
		t.Fatalf("failed: %+v", f)
	}
	for _, n := range res.notes {
		for _, v := range n.args {
			if strings.Contains(v, "/teams/") || strings.Contains(v, "/zoom/") {
				t.Fatalf("a webhook URL is recorded: %+v", n)
			}
		}
	}
	var teamsReached []alert.Notified
	for _, n := range res.reached {
		if n.Channel == "teams" || n.Channel == "zoom" {
			teamsReached = append(teamsReached, n)
		}
	}
	if len(teamsReached) != 3 || teamsReached[0].Address != hook.URL("/teams/sre-a1b2") || !strings.HasPrefix(teamsReached[0].Recipient, "ms:") {
		t.Fatalf("reached: %+v", teamsReached)
	}

	// The follow-up goes to the webhooks too.
	at := time.Date(2026, 10, 5, 9, 5, 0, 0, time.UTC)
	a.Status, a.AckedBy, a.AckedAt, a.FollowUp = alert.StatusAcknowledged, "petrov", &at, alert.StatusAcknowledged
	a.Notified = res.reached
	s.DeliverFollowUp(context.Background(), a)
	got = byPath(hook.Requests())
	if len(got["/teams/sre-a1b2"]) != 2 || len(got["/zoom/sre-c3d4"]) != 2 || len(got["/zoom/duty"]) != 2 {
		t.Fatalf("follow-ups: %+v", got)
	}
	fu := got["/zoom/sre-c3d4"][1]
	if fu.Authorization != "zoom-verification-token" || !strings.Contains(fu.Body["body"].([]any)[0].(map[string]any)["text"].(string), "petrov") {
		t.Fatalf("zoom follow-up: %+v", fu)
	}
}

// A team with only a Teams or Zoom channel gets backup notification there; Zoom without its
// token fails alone.
func TestWebhookOnlyTeamChannel(t *testing.T) {
	s, st, smtp, _, res := setup(t)
	hook, a := withWebhooks(t, st)
	st.Write(func(d *store.Data) {
		d.Settings.Alerting.Notify.Zoom.TokenRef = ""
		d.Settings.Alerting.Notify.ExtraTeams = nil
	})
	a.Route.Channel = &alert.Channel{Teams: hook.URL("/teams/only")}
	s.Deliver(context.Background(), a)
	got := byPath(hook.Requests())
	if len(got["/teams/only"]) != 1 || len(got) != 1 || len(smtp.Mails()) != 2 {
		t.Fatalf("webhooks %+v, mails %d", got, len(smtp.Mails()))
	}
	var failed []string
	for _, n := range res.notes {
		if n.code == "notify_failed" {
			failed = append(failed, n.args["channel"]+": "+n.args["error"])
		}
	}
	if len(failed) != 1 || !strings.Contains(failed[0], "zoom: the Zoom verification token is not set") {
		t.Fatalf("failed: %v", failed)
	}
}

func TestWebhookTestAndPreview(t *testing.T) {
	s, st, _, _, _ := setup(t)
	hook, a := withWebhooks(t, st)
	if _, err := s.Test(context.Background(), notify.ChannelZoom, hook.URL("/zoom/test")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Test(context.Background(), notify.ChannelTeams, hook.URL("/teams/extra")); err == nil || strings.Contains(err.Error(), "/teams/") {
		t.Fatalf("a failing test: %v", err)
	}
	for _, tg := range s.PreviewTargets(a.Route) {
		if (tg.Channel == "teams" || tg.Channel == "zoom") && (strings.Contains(tg.Address, "sre") || !strings.Contains(tg.Address, "/…")) {
			t.Fatalf("preview shows %q", tg.Address)
		}
	}
	if !notify.Reaches(model.Notify{Teams: model.TeamsChannel{Enabled: true}}, nil, []*model.Team{{Teams: "https://example.webhook.office.com/x"}}) {
		t.Fatal("a team with a Teams channel is not reached")
	}
	if notify.Reaches(model.Notify{Zoom: model.ZoomChannel{Enabled: true}}, nil, []*model.Team{{Zoom: "https://integrations.zoom.us/x"}}) {
		t.Fatal("Zoom without its token reaches")
	}
}
