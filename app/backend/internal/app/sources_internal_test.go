package app

import (
	"os"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/presets"
)

// The quick connect templates render exactly what the Go code before them did (testdata/quick
// was written by that code), with values that need quoting.
func TestQuickTemplatesGolden(t *testing.T) {
	url, token := "https://umb.example.com/api/ingest/src-1", "tok'en\"x"
	golden := func(name string) string {
		b, err := os.ReadFile("testdata/quick/" + name)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return string(b)
	}
	for _, id := range []string{"zabbix", "alertmanager", "grafana", "webhook"} {
		p, ok := presets.Get(id)
		if !ok || p.Quick == nil {
			t.Fatalf("%s is not quick-connectable", id)
		}
		q, att, err := quickInstructions(p, url, token)
		if err != nil {
			t.Fatal(err)
		}
		if q.Snippet != golden(id+".snippet") {
			t.Errorf("%s snippet:\n%s", id, q.Snippet)
		}
		content := ""
		if att != nil {
			content = att.Content
		}
		if content != golden(id+".attachment") {
			t.Errorf("%s attachment:\n%s", id, content)
		}
		if body := string(testBody(id, "abc123", `db-01 "x"<&>`, nil)); body != golden(id+".test_body.json") {
			t.Errorf("%s test body:\n%s", id, body)
		}
	}
	if got := string(testBody("", "abc123", "x", []byte(`{"own":1}`))); got != `{"own":1}` {
		t.Errorf("a connector without a template sends its first sample: %s", got)
	}
}
