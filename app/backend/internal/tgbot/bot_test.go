package tgbot_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/notify/notifytest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/telegram"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/tgbot"
)

type action struct{ user, id, action, text string }

// backend is a fake Umbrella: Telegram user 501 is linked to «eng», 502 is not linked.
type backend struct {
	api, token string
	mu         sync.Mutex
	acts       []action
	linked     map[int64]string
	status     map[string]string
}

func (b *backend) Config() (tgbot.Config, error) {
	return tgbot.Config{On: true, Token: b.token, API: b.api, Locale: "ru"}, nil
}

func (b *backend) User(tg telegram.User) (tgbot.Person, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	name, ok := b.linked[tg.ID]
	return tgbot.Person{ID: "U-" + name, Username: name, Name: name, CanAck: true, CanView: true}, ok
}

func (b *backend) Link(_ context.Context, token string, tg telegram.User) (tgbot.Person, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if token != "good" {
		return tgbot.Person{}, context.Canceled
	}
	b.linked[tg.ID] = "new"
	return tgbot.Person{ID: "U-new", Username: "new", Name: "New One"}, nil
}

func (b *backend) Unlink(_ context.Context, p tgbot.Person) error { return nil }

func (b *backend) Act(_ context.Context, p tgbot.Person, id, act, text string) (alert.Alert, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.acts = append(b.acts, action{p.Username, id, act, text})
	switch {
	case b.status[id] == "":
		return alert.Alert{}, alert.ErrNotFound
	case act == "ack" && b.status[id] != alert.StatusOpen:
		return alert.Alert{}, alert.ErrNotOpen
	case act == "ack":
		b.status[id] = alert.StatusAcknowledged
	case act == "resolve":
		b.status[id] = alert.StatusResolved
	}
	return alert.Alert{ID: id, Status: b.status[id]}, nil
}

func (b *backend) Active(context.Context, tgbot.Person, int) ([]alert.Alert, error) {
	return []alert.Alert{{ID: "INC-7", Title: "Disk full", Severity: "critical", Status: alert.StatusOpen}}, nil
}

func (b *backend) IncidentURL(id string) string { return "https://umbrella/incidents?id=" + id }

func (b *backend) actions() []action {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]action(nil), b.acts...)
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func start(t *testing.T) (*notifytest.Telegram, *backend) {
	t.Helper()
	fake := notifytest.NewTelegram(t)
	be := &backend{api: fake.URL(), token: fake.Token, linked: map[int64]string{501: "eng"}, status: map[string]string{"INC-7": alert.StatusOpen}}
	bot := tgbot.New(be, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { bot.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	eventually(t, "polling", func() bool { return bot.Status().State == "polling" && bot.Username() == "umbrella_test_bot" })
	return fake, be
}

func keyboard(id string) map[string]any {
	return map[string]any{"inline_keyboard": [][]map[string]string{
		{{"text": "ack", "callback_data": "ack:" + id}, {"text": "res", "callback_data": "res:" + id}},
		{{"text": "open", "url": "https://umbrella/incidents?id=" + id}},
	}}
}

func press(fake *notifytest.Telegram, from int64, data string) {
	fake.Push(map[string]any{"callback_query": map[string]any{"id": "cb-" + data, "data": data, "from": map[string]any{"id": from, "language_code": "en"},
		"message": map[string]any{"message_id": 42, "chat": map[string]any{"id": -100, "type": "group"}, "reply_markup": keyboard("INC-7")}}})
}

func say(fake *notifytest.Telegram, from int64, text string, extra map[string]any) {
	m := map[string]any{"message_id": 50, "text": text, "from": map[string]any{"id": from}, "chat": map[string]any{"id": from, "type": "private"}}
	for k, v := range extra {
		m[k] = v
	}
	fake.Push(map[string]any{"message": m})
}

// The buttons under a notification acknowledge and resolve the incident in the name of the
// linked user, and the buttons follow its state.
func TestButtons(t *testing.T) {
	fake, be := start(t)
	press(fake, 501, "ack:INC-7")
	eventually(t, "ack", func() bool { return len(fake.Edits()) == 1 })
	if got := be.actions(); len(got) != 1 || got[0] != (action{"eng", "INC-7", "ack", ""}) {
		t.Fatalf("actions = %+v", got)
	}
	var k telegram.Keyboard
	_ = json.Unmarshal(fake.Edits()[0].Keyboard, &k)
	if len(k.Rows) != 2 || k.Rows[0][0].Data != "res:INC-7" || k.Rows[1][0].URL == "" {
		t.Fatalf("after ack: %+v", k)
	}
	press(fake, 501, "res:INC-7")
	eventually(t, "resolve", func() bool { return len(fake.Edits()) == 2 })
	_ = json.Unmarshal(fake.Edits()[1].Keyboard, &k)
	if len(k.Rows) != 1 || k.Rows[0][0].URL == "" {
		t.Fatalf("after resolve only the link stays: %+v", k)
	}
	if a := fake.Answers(); a[0] != "Acknowledged: the incident is taken" || a[1] != "The incident is resolved" {
		t.Fatalf("answers = %v", a)
	}
	// Somebody whose account is not linked is told how to link it; nothing is done.
	press(fake, 502, "ack:INC-7")
	eventually(t, "answer", func() bool { return len(fake.Answers()) == 3 })
	if a := fake.Answers()[2]; !strings.Contains(a, "not linked") || len(be.actions()) != 2 {
		t.Fatalf("answer = %q, actions = %+v", a, be.actions())
	}
}

// Commands, linking and replies to a notification.
func TestCommands(t *testing.T) {
	fake, be := start(t)
	say(fake, 502, "/start good", nil)
	eventually(t, "linked", func() bool { return len(fake.Messages()) == 1 })
	if m := fake.Messages()[0]; !strings.Contains(m.Text, "New One") || m.ChatID != "502" {
		t.Fatalf("link answer = %+v", m)
	}
	say(fake, 501, "/incidents", nil)
	eventually(t, "list", func() bool { return len(fake.Messages()) == 2 })
	if m := fake.Messages()[1]; !strings.Contains(m.Text, "INC-7") || !strings.Contains(m.Text, "Disk full") {
		t.Fatalf("list = %q", m.Text)
	}
	say(fake, 501, "/ack 7", nil)
	eventually(t, "ack", func() bool { return len(fake.Messages()) == 3 })
	say(fake, 501, "rolled back the release", map[string]any{"reply_to_message": map[string]any{"message_id": 42, "chat": map[string]any{"id": 501, "type": "private"},
		"reply_markup": keyboard("INC-7")}})
	eventually(t, "comment", func() bool { return len(fake.Messages()) == 4 })
	want := []action{{"eng", "INC-7", "ack", ""}, {"eng", "INC-7", "comment", "rolled back the release"}}
	if got := be.actions(); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("actions = %+v", got)
	}
	say(fake, 501, "/ack@umbrella_test_bot INC-7", nil)
	eventually(t, "already", func() bool { return len(fake.Messages()) == 5 })
	if m := fake.Messages()[4]; !strings.Contains(m.Text, "уже взят") {
		t.Fatalf("already taken = %q", m.Text)
	}
	eventually(t, "offsets confirmed", func() bool { return fake.Pending() == 0 })
}
