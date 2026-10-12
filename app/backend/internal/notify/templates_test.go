package notify

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func TestTemplateOverrides(t *testing.T) {
	s := New(store.New(), nil)
	a := goldenAlert()
	tgt := target{channel: ChannelEmail, address: "x@example.com", recipient: UserRecipient("u1"), tz: time.UTC}
	overrides := map[string]string{
		"fallback.subject": "{{upper .Severity}}: {{.Title}}\n",
		// Works for the sample incident, fails for an incident without a team.
		"fallback.text": "{{t \"team\"}} {{.Team.Name}}\n",
		"fallback.html": "<i>{{html .Title}}</i> {{t \"severity\"}}",
	}
	c := config{locale: "en", set: model.Alerting{PublicURL: "https://u.example"}}
	c.msgs = newMessages(c.locale, overrides)
	m := s.compose(c, a, tgt)
	if m.subject != `CRITICAL: HTTP 5xx <b>&"app"</b>` || m.text != "Team Payments & SRE\n" || m.html != "<i>HTTP 5xx &lt;b&gt;&amp;&#34;app&#34;&lt;/b&gt;</i> Severity" {
		t.Fatalf("custom: %+v", m)
	}
	// A template that fails for an incident falls back to the built-in one.
	a.Route.Team = nil
	builtin := s.compose(config{locale: "en", set: c.set}, a, tgt)
	if m := s.compose(c, a, tgt); m.text != builtin.text || m.subject == builtin.subject {
		t.Fatalf("fallback: %+v", m)
	}
}

// A Telegram HTML override that forgets html still sends escaped text, one that calls it does
// not escape twice, and the other parts keep the text as it is.
func TestHTMLOverridesEscape(t *testing.T) {
	s := New(store.New(), nil)
	a := goldenAlert()
	a.Route.Team.Name = "Ops <&> SRE"
	tgt := target{channel: ChannelTelegram, address: "1", recipient: UserRecipient("u1"), tz: time.UTC}
	c := config{locale: "en", set: model.Alerting{PublicURL: "https://u.example"}}
	want := "<b>HTTP 5xx &lt;b&gt;&amp;&#34;app&#34;&lt;/b&gt;</b> Ops &lt;&amp;&gt; SRE"
	for _, body := range []string{
		"<b>{{.Title}}</b> {{.Team.Name}}",
		"<b>{{html .Title}}</b> {{html .Team.Name}}",
		`<b>{{include "title" .}}</b> {{with .Team}}{{.Name}}{{end}}{{define "title"}}{{.Title}}{{end}}`,
	} {
		c.msgs = newMessages(c.locale, map[string]string{"fallback.html": body, "fallback.text": "{{.Title}}"})
		m := s.compose(c, a, tgt)
		if m.html != want {
			t.Errorf("%s:\n got %q\nwant %q", body, m.html, want)
		}
		if m.text != `HTTP 5xx <b>&"app"</b>` {
			t.Errorf("text escaped: %q", m.text)
		}
	}
	if a.Title != `HTTP 5xx <b>&"app"</b>` || a.Route.Team.Name != "Ops <&> SRE" {
		t.Fatalf("the incident was changed: %q %q", a.Title, a.Route.Team.Name)
	}
}

func TestCleanTemplates(t *testing.T) {
	out, err := CleanTemplates(map[string]string{
		"fallback.subject": "  ",
		"fallback.text":    DefaultTemplates()["fallback.text"],
		"followup.subject": DefaultTemplates()["followup.subject"] + "\n",
		"test.text":        "Hi {{t \"test_body\"}}\r\nbye\r\n",
		"followup.html":    "{{.Event}}",
	})
	if err != nil || len(out) != 2 || out["test.text"] != "Hi {{t \"test_body\"}}\nbye\n" || out["followup.html"] != "{{.Event}}" {
		t.Fatalf("clean: %v %q", err, out)
	}
	if out, err := CleanTemplates(map[string]string{"fallback.text": ""}); err != nil || out != nil {
		t.Fatalf("empty: %v %v", out, err)
	}
	for name, body := range map[string]string{
		"fallback.text":    "{{.Title",
		"fallback.html":    "{{.NoSuchField}}",
		"followup.subject": "{{template \"nowhere\" .}}",
		"partials":         "x",
		"nope.text":        "x",
		"test.subject":     `{{include "test.subject" .}}`,
		"test.text":        `{{define "loop"}}{{template "loop" .}}{{end}}{{template "loop" .}}`,
		"test.html":        `{{define "loop2"}}{{include "loop2" .}}{{end}}{{include "loop2" .}}`,
	} {
		_, err := CleanTemplates(map[string]string{name: body})
		var te *TemplateError
		if !errors.As(err, &te) || te.Name != name {
			t.Errorf("%s %q: %v", name, body, err)
		}
	}
}

func TestPreviewTemplates(t *testing.T) {
	out, err := PreviewTemplates("en", map[string]string{"followup.subject": "Done: {{.ID}} {{t (print \"fu.subj.\" .Event)}}"})
	if err != nil || len(out) != 4 {
		t.Fatalf("preview: %v %+v", err, out)
	}
	names := []string{}
	for _, p := range out {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "fallback,followup.acknowledged,followup.resolved,test" {
		t.Fatalf("names: %v", names)
	}
	if out[1].Subject != "Done: INC-1042 being handled" || out[2].Subject != "Done: INC-1042 resolved" ||
		!strings.Contains(out[0].Text, "Payments SRE") || !strings.Contains(out[0].HTML, "<b>") || out[3].Subject != "Umbrella backup notification test" {
		t.Fatalf("preview: %+v", out)
	}
	if ru, _ := PreviewTemplates("", nil); !strings.Contains(ru[0].Text, "Важность") {
		t.Fatalf("ru: %+v", ru[0])
	}
	if _, err := PreviewTemplates("en", map[string]string{"fallback.text": "{{"}); err == nil {
		t.Fatal("a broken template previews")
	}
}
