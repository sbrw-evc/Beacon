package notify_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/notify"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/telegram"
)

type onCall []alert.Person

func (o onCall) OnCallPeople(alert.Alert) []alert.Person { return o }

// With the bot on, a Telegram notification has buttons that acknowledge and resolve the
// incident; the follow-up answers it and takes its buttons away.
func TestTelegramButtonsAndThread(t *testing.T) {
	s, st, _, tg, res := setup(t)
	st.Write(func(d *store.Data) { d.Settings.Alerting.Notify.Telegram.Bot = true })
	s.Deliver(context.Background(), incident())
	msgs := tg.Messages()
	if len(msgs) != 2 {
		t.Fatalf("messages: %+v", msgs)
	}
	var k telegram.Keyboard
	if err := json.Unmarshal(msgs[0].Keyboard, &k); err != nil {
		t.Fatal(err)
	}
	if len(k.Rows) != 2 || k.Rows[0][0].Data != notify.CallbackAck+"INC-7" || k.Rows[0][1].Data != notify.CallbackResolve+"INC-7" ||
		k.Rows[0][1].Text != "✔️ Решить" || k.Rows[1][0].Text != "Открыть в Umbrella" {
		t.Fatalf("keyboard: %+v", k)
	}
	var ref string
	for _, n := range res.reached {
		if n.Channel == "telegram" && n.Address == "4242" {
			ref = n.Ref
		}
	}
	if ref != "1" {
		t.Fatalf("reached: %+v", res.reached)
	}

	a := incident()
	at := time.Date(2026, 10, 5, 9, 5, 0, 0, time.UTC)
	a.Status, a.AckedBy, a.AckedAt, a.FollowUp = alert.StatusAcknowledged, "petrov", &at, alert.StatusAcknowledged
	a.Notified = []alert.Notified{{Channel: "telegram", Address: "4242", Recipient: "u:u1", Ref: ref}}
	s.DeliverFollowUp(context.Background(), a)
	msgs = tg.Messages()
	if last := msgs[len(msgs)-1]; last.ReplyTo != 1 || !strings.Contains(last.Text, "petrov") {
		t.Fatalf("follow-up: %+v", last)
	}
	edits := tg.Edits()
	if len(edits) != 1 || edits[0].ID != 1 || strings.Contains(string(edits[0].Keyboard), "ack:") {
		t.Fatalf("edits: %+v", edits)
	}
}

// Without the bot the message keeps the signed acknowledgement link as a button.
func TestTelegramWithoutBot(t *testing.T) {
	s, _, _, tg, _ := setup(t)
	s.Deliver(context.Background(), incident())
	var k telegram.Keyboard
	_ = json.Unmarshal(tg.Messages()[0].Keyboard, &k)
	if len(k.Rows) != 1 || !strings.Contains(k.Rows[0][0].URL, "/ack/") {
		t.Fatalf("keyboard: %+v", k)
	}
}

// The people on call in PagerDuty get the notification too.
func TestOnCallRecipients(t *testing.T) {
	s, st, smtp, _, _ := setup(t)
	st.Write(func(d *store.Data) {
		d.Settings.Alerting.Notify.ExtraEmails = nil
		d.Settings.Alerting.Notify.Telegram.Enabled = false
	})
	s.SetOnCall(onCall{{Name: "Ann", Email: "ann@example.com", Role: "on-call"}})
	s.Deliver(context.Background(), incident())
	var to []string
	for _, m := range smtp.Mails() {
		to = append(to, m.To)
	}
	if strings.Join(to, ",") != "ivanov@example.com,ann@example.com" {
		t.Fatalf("mails to %v", to)
	}
	if got := s.PreviewTargets(incident().Route); len(got) != 2 {
		t.Fatalf("preview: %+v", got)
	}
}
