package presets

import (
	"context"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
)

// A Graylog HTTP notification: the host comes from the group-by values, the priority is the
// severity, the alert is the definition and key, and a body the poll marks resolved resolves it.
func TestGraylogNotification(t *testing.T) {
	p, _ := Get("graylog")
	events := map[string]flow.Event{}
	for _, s := range p.Document.Samples {
		res, err := pipeline(t, p).Run(context.Background(), flow.Input{Body: []byte(s.Body)}, flow.RunOptions{})
		if err != nil || len(res.Events) != 1 {
			t.Fatalf("%s: events=%+v failures=%+v err=%v", s.Name, res.Events, res.Failures, err)
		}
		events[s.Name] = res.Events[0].Event
	}
	e := events["Event: too many 5xx"]
	if e.Title != "Too many 5xx on nginx" || e.CI != "web-01" || e.Severity != flow.SeverityError || e.Status != flow.StatusFiring ||
		e.ExternalID != "6720a1b2c3d4e5f601234567web-01" || e.Value != "Too many 5xx on nginx: count()=73.0" {
		t.Errorf("event = %+v", e)
	}
	names := map[string]string{}
	for _, f := range e.Fields {
		names[f.Name] = f.Value
	}
	if names["Пример сообщения"] == "" || names["Ключ"] != "web-01" {
		t.Errorf("fields = %+v", e.Fields)
	}
	r := events["Resolved by the poll: database errors"]
	if r.Status != flow.StatusResolved || r.CI != "db-01" || r.Severity != flow.SeverityCritical || r.Title != "Database errors" {
		t.Errorf("resolved = %+v", r)
	}
}
