package notify

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// goldenAlert is an incident that uses every line of a message, with characters HTML escapes.
func goldenAlert() alert.Alert {
	opened := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	return alert.Alert{ID: "INC-7", Title: `HTTP 5xx <b>&"app"</b>`, CIName: "app-01 & co", Signal: "red.errors<1>", Severity: "critical",
		OpenedAt: opened, PD: alert.PD{State: alert.PDFailed, ErrorCode: "no_key", Error: "no key"},
		Route: alert.Route{Services: []alert.Ref{{ID: "s1", Name: "Платежи"}, {ID: "s2", Name: "Cards <EU>"}}, Team: &alert.Ref{ID: "t1", Name: "Payments & SRE"}}}
}

// TestMessagesGolden keeps the messages byte for byte: backup notification, follow-ups and the
// test message, in both languages, for e-mail (subject and text) and Telegram (HTML).
func TestMessagesGolden(t *testing.T) {
	tz, _ := time.LoadLocation("Asia/Novosibirsk")
	at := time.Date(2026, 10, 5, 9, 5, 0, 0, time.UTC)
	type variant struct {
		name    string
		public  string
		grafana bool
		links   bool
		edit    func(a *alert.Alert)
	}
	fallback := []variant{
		{"full", "https://umbrella.example.com/", true, true, func(*alert.Alert) {}},
		{"failed_error", "https://umbrella.example.com", false, true, func(a *alert.Alert) { a.PD = alert.PD{State: alert.PDFailed, Error: "breaker <open>"} }},
		{"failed_bare", "https://umbrella.example.com", false, false, func(a *alert.Alert) { a.PD = alert.PD{State: alert.PDFailed} }},
		{"pending", "https://umbrella.example.com", true, false, func(a *alert.Alert) { a.PD = alert.PD{State: alert.PDPending} }},
		{"off", "", true, true, func(a *alert.Alert) {
			a.PD = alert.PD{State: alert.PDOff}
			a.Signal = a.Title
			a.Route = alert.Route{}
			a.Severity = "info"
		}},
		{"skipped", "https://u.example", false, true, func(a *alert.Alert) { a.PD = alert.PD{State: alert.PDSkipped}; a.Severity = "warning" }},
	}
	followUps := []variant{
		{"acknowledged", "https://umbrella.example.com", false, false, func(a *alert.Alert) {
			a.FollowUp, a.AckedBy, a.AckedAt = alert.StatusAcknowledged, "petrov <p>", &at
		}},
		{"resolved_by", "", false, false, func(a *alert.Alert) {
			a.FollowUp, a.ResolvedBy, a.ResolvedAt = alert.StatusResolved, "ivanov", &at
		}},
		{"resolved_auto", "https://umbrella.example.com", false, false, func(a *alert.Alert) {
			a.FollowUp, a.ResolvedAt = alert.StatusResolved, &at
		}},
	}
	var b strings.Builder
	out := func(name string, m composed) {
		fmt.Fprintf(&b, "=== %s subject\n%s\n=== %s text\n%s=== %s html\n%s\n", name, m.subject, name, m.text, name, m.html)
	}
	for _, locale := range []string{"ru", "en", ""} {
		for _, v := range append(fallback, followUps...) {
			s := New(store.New(), nil)
			s.now = func() time.Time { return at }
			if v.links {
				s.SetLinks(NewLinks([]byte("0123456789abcdef0123456789abcdef")))
			}
			c := config{locale: locale, set: model.Alerting{PublicURL: v.public}}
			if v.grafana {
				c.set.Grafana.DashboardURL = "https://grafana.example/d/x"
			}
			a := goldenAlert()
			v.edit(&a)
			if a.FollowUp != "" {
				out(locale+"/followup/"+v.name, s.composeFollowUp(c, a, tz))
			} else {
				out(locale+"/fallback/"+v.name, s.compose(c, a, target{channel: ChannelEmail, address: "x@example.com", recipient: UserRecipient("u1"), tz: tz}))
			}
		}
		out(locale+"/test", New(store.New(), nil).composeTest(config{locale: locale}))
	}
	path := filepath.Join("testdata", "messages.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != string(want) {
		gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := range min(len(gl), len(wl)) {
			if gl[i] != wl[i] {
				t.Fatalf("line %d:\n got %q\nwant %q", i+1, gl[i], wl[i])
			}
		}
		t.Fatalf("lengths differ: %d lines, want %d", len(gl), len(wl))
	}
}
