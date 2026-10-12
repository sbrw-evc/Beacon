package notify_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
)

// Without PagerDuty the message is about a new incident: it does not say PagerDuty failed.
func TestBackupNotificationWithoutPagerDuty(t *testing.T) {
	s, _, smtp, _, res := setup(t)
	a := incident()
	a.PD = alert.PD{State: alert.PDOff}
	s.Deliver(context.Background(), a)
	m := smtp.Mails()
	if len(m) == 0 || strings.Contains(m[0].Body, "PagerDuty") || !strings.Contains(m[0].Body, "Новый инцидент") {
		t.Fatalf("mail: %+v", m)
	}
	if len(res.reached) != 4 || res.reached[0] != (alert.Notified{Channel: "email", Address: "ivanov@example.com", Recipient: "u:u1"}) {
		t.Fatalf("reached: %+v", res.reached)
	}

	// A delivery error is told by its code, in the language of the message.
	a.PD = alert.PD{State: alert.PDFailed, ErrorCode: "no_key", Error: "no Events API v2 integration key is set"}
	s.Deliver(context.Background(), a)
	if m := smtp.Mails(); !strings.Contains(m[len(m)-1].Body, "ошибка доставки: не задан ключ интеграции") {
		t.Fatalf("mail: %s", m[len(m)-1].Body)
	}
}

// An incident taken since it was queued is not sent.
func TestBackupNotificationTakenMeanwhile(t *testing.T) {
	s, _, smtp, tg, res := setup(t)
	res.taken = true
	s.Deliver(context.Background(), incident())
	if len(smtp.Mails()) != 0 || len(tg.Messages()) != 0 || len(res.done) != 0 {
		t.Fatalf("sent: %+v %+v %+v", smtp.Mails(), tg.Messages(), res.done)
	}
}

func TestFollowUp(t *testing.T) {
	s, _, smtp, tg, res := setup(t)
	a := incident()
	at := time.Date(2026, 10, 5, 9, 5, 0, 0, time.UTC)
	a.Status, a.AckedBy, a.AckedAt, a.FollowUp = alert.StatusAcknowledged, "petrov", &at, alert.StatusAcknowledged
	a.Notified = []alert.Notified{{Channel: "email", Address: "ivanov@example.com", Recipient: "u:u1"}, {Channel: "telegram", Address: "4242", Recipient: "u:u1"},
		{Channel: "email", Address: "duty@example.com", Recipient: "e:duty@example.com"}}
	s.DeliverFollowUp(context.Background(), a)
	m := smtp.Mails()
	if len(m) != 2 || m[0].To != "ivanov@example.com" || m[1].To != "duty@example.com" {
		t.Fatalf("mails: %+v", m)
	}
	for _, want := range []string{"Инцидент взят в работу", "INC-7 · HTTP 5xx на app-01", "Взял(а): petrov, 05.10.2026 16:05 +07",
		"Открыть в Umbrella: https://umbrella.example.com/incidents?id=INC-7"} {
		if !strings.Contains(m[0].Body, want) {
			t.Fatalf("body has no %q:\n%s", want, m[0].Body)
		}
	}
	if !strings.Contains(m[1].Body, "05.10.2026 12:05 MSK") || !strings.Contains(m[0].Subject, "взят в работу") {
		t.Fatalf("duty mail: %s %s", m[1].Subject, m[1].Body)
	}
	if msgs := tg.Messages(); len(msgs) != 1 || msgs[0].ChatID != "4242" || !strings.Contains(msgs[0].Text, "petrov") {
		t.Fatalf("telegram: %+v", msgs)
	}
	if len(res.followUps) != 1 || res.followUps[0] != "INC-7:acknowledged" {
		t.Fatalf("reported: %+v", res.followUps)
	}
	var sent int
	for _, n := range res.notes {
		if n.code == "notify_followup" {
			sent++
		}
	}
	if sent != 2 {
		t.Fatalf("notes: %+v", res.notes)
	}

	// Resolved by the sources: no person, the reason instead.
	a.Status, a.FollowUp, a.ResolvedAt = alert.StatusResolved, alert.StatusResolved, &at
	s.DeliverFollowUp(context.Background(), a)
	if m := smtp.Mails(); len(m) != 4 || !strings.Contains(m[2].Body, "Инцидент решён") || !strings.Contains(m[2].Body, "источники вернулись в норму") {
		t.Fatalf("resolved: %+v", m)
	}
}
