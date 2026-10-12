package presets

import (
	"context"
	"strings"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
)

// A Grafana alert whose description is a stack trace of a hundred lines written with \n as
// text: the title is one line, the description is cleaned and cut in the middle, the fields
// lead to Grafana.
func TestGrafanaStackTrace(t *testing.T) {
	p, _ := Get("grafana")
	var body string
	for _, s := range p.Document.Samples {
		if strings.Contains(s.Name, "stack trace") {
			body = s.Body
		}
	}
	res, err := pipeline(t, p).Run(context.Background(), flow.Input{Body: []byte(body)}, flow.RunOptions{})
	if err != nil || len(res.Events) != 1 {
		t.Fatalf("events=%+v failures=%+v err=%v", res.Events, res.Failures, err)
	}
	e := res.Events[0].Event
	if e.Title != "java.lang.IllegalStateException: Connection pool exhausted" {
		t.Errorf("title = %q", e.Title)
	}
	lines := strings.Split(e.Description, "\n")
	if len(lines) != 81 || !strings.Contains(e.Description, "lines skipped") || !strings.HasPrefix(lines[len(lines)-1], "\tat java.base") {
		t.Errorf("description has %d lines:\n%s", len(lines), e.Description)
	}
	names := map[string]string{}
	for _, f := range e.Fields {
		names[f.Name] = f.Value
	}
	if names["Правило в Grafana"] != "http://grafana/alerting/grafana/def/view" || names["Папка"] != "Payments" || names["Runbook"] != "" {
		t.Errorf("fields = %+v", e.Fields)
	}
	if e.CI != "pay-01" || e.Severity != flow.SeverityCritical {
		t.Errorf("event = %+v", e)
	}
}
