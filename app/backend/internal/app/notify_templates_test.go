package app_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/notify"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// Message templates: checked when saved, kept when a form does not send them, previewed with a
// sample incident before they are saved.
func TestNotifyTemplates(t *testing.T) {
	f := newConnFixture(t, false)
	var v app.NotifyView
	f.expect(f.admin, http.MethodGet, "/api/notifications", nil, http.StatusOK, &v)
	if v.Templates != nil || len(v.DefaultTemplates) != len(notify.TemplateNames()) || !strings.Contains(v.DefaultTemplates["fallback.text"], `{{t "why"}}`) {
		t.Fatalf("defaults: %+v", v)
	}

	var problem struct {
		Code   string `json:"error"`
		Detail string `json:"detail"`
	}
	for _, bad := range []map[string]string{
		{"fallback.subject": "{{.Title"},
		{"fallback.subject": "{{.NoSuchField}}"},
		{"nope.text": "x"},
		{"fallback.text": strings.Repeat("x", 17<<10)},
	} {
		f.expect(f.admin, http.MethodPut, "/api/notifications", map[string]any{"templates": bad}, http.StatusBadRequest, &problem)
		if problem.Code != "template_invalid" {
			t.Fatalf("%v: %+v", bad, problem)
		}
	}

	settings := map[string]any{"templates": map[string]string{
		"fallback.subject": "[{{upper .Severity}}] {{.ID}}",
		"fallback.text":    v.DefaultTemplates["fallback.text"],
	}}
	f.expect(f.admin, http.MethodPut, "/api/notifications", settings, http.StatusOK, &v)
	if len(v.Templates) != 1 || v.Templates["fallback.subject"] != "[{{upper .Severity}}] {{.ID}}" {
		t.Fatalf("saved: %+v", v.Templates)
	}
	var stored map[string]string
	f.h.st.Read(func(d *store.Data) { stored = d.Settings.Alerting.Notify.Templates })
	if len(stored) != 1 {
		t.Fatalf("stored: %v", stored)
	}
	// A form without templates keeps them; {} goes back to the built-in ones.
	f.expect(f.admin, http.MethodPut, "/api/notifications", map[string]any{"min_severity": "warning"}, http.StatusOK, &v)
	if len(v.Templates) != 1 {
		t.Fatalf("kept: %+v", v.Templates)
	}

	var preview struct {
		Messages []notify.Preview `json:"messages"`
	}
	f.expect(f.admin, http.MethodPost, "/api/notifications/preview", map[string]any{"locale": "en"}, http.StatusOK, &preview)
	if len(preview.Messages) != 4 || preview.Messages[0].Subject != "[CRITICAL] INC-1042" || !strings.Contains(preview.Messages[0].Text, "Severity: P1 · critical") {
		t.Fatalf("preview saved: %+v", preview.Messages)
	}
	f.expect(f.admin, http.MethodPost, "/api/notifications/preview", map[string]any{"locale": "ru", "templates": map[string]string{"fallback.subject": "{{t \"severity\"}}"}}, http.StatusOK, &preview)
	if preview.Messages[0].Subject != "Важность" {
		t.Fatalf("preview draft: %+v", preview.Messages[0])
	}
	f.expect(f.admin, http.MethodPost, "/api/notifications/preview", map[string]any{"templates": map[string]string{"fallback.html": "{{"}}, http.StatusBadRequest, &problem)
	if problem.Code != "template_invalid" || !strings.Contains(problem.Detail, "fallback.html") {
		t.Fatalf("preview broken: %+v", problem)
	}

	var reset app.NotifyView
	f.expect(f.admin, http.MethodPut, "/api/notifications", map[string]any{"templates": map[string]string{}}, http.StatusOK, &reset)
	if reset.Templates != nil {
		t.Fatalf("reset: %+v", reset.Templates)
	}
}
