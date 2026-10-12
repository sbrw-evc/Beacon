package app

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/ingest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/monitoring"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// Polling the alerts of Graylog. A Graylog event is a moment: an event definition gives an
// event on every run while its condition holds, so the poll makes an alert of the events of
// one definition and key. The alert fires from its first event and is resolved once no event
// came for the quiet time of the system. Each change goes to the connector of the system in
// the body of a Graylog HTTP notification with a status field added, one alert per request, so
// the notification and the poll fold into the same incidents.

// graylogPollSlack widens the read beyond the quiet time so that an event written late still
// counts.
const graylogPollSlack = time.Minute

func (p *alertPoller) pollGraylog(ctx context.Context, src model.MonitoringSource, con model.Connector, auth *monitoring.Auth, st *model.MonitoringPoll) error {
	quiet := monitoring.GraylogQuiet(src)
	reading, err := p.graylog(ctx, src, auth, quiet+time.Duration(max(src.PollSeconds, minPollSeconds))*time.Second+graylogPollSlack)
	if err != nil {
		return err
	}
	now := p.now()
	// The newest event of each alert that is still within the quiet time.
	firing := map[string]monitoring.GraylogEvent{}
	for _, e := range reading.Events {
		if _, seen := firing[e.Series()]; seen || (!e.Timestamp.IsZero() && now.Sub(e.Timestamp) > quiet) {
			continue
		}
		firing[e.Series()] = e
	}
	st.Firing = len(firing)
	type delivery struct {
		series string
		body   []byte
		event  *monitoring.GraylogEvent
	}
	var send []delivery
	for _, s := range slices.Sorted(maps.Keys(firing)) {
		if _, known := src.Polled[s]; !known {
			e := firing[s]
			send = append(send, delivery{s, graylogBody(e.Raw, e.DefinitionID, e.DefinitionType, e.Title, e.Description, "firing"), &e})
		}
	}
	for _, s := range slices.Sorted(maps.Keys(src.Polled)) {
		if _, still := firing[s]; !still {
			prev := src.Polled[s]
			send = append(send, delivery{s, graylogBody(json.RawMessage(prev.Annotations["event"]), prev.Labels["definition_id"], prev.Labels["definition_type"],
				prev.Annotations["title"], prev.Annotations["description"], "resolved"), nil})
		}
	}
	remember := func(done []delivery) {
		if len(done) == 0 {
			return
		}
		p.a.deps.Store.Write(func(d *store.Data) {
			s := d.MonitoringSources[src.ID]
			if s == nil {
				return
			}
			if s.Polled == nil {
				s.Polled = map[string]model.PolledAlert{}
			}
			for _, w := range done {
				if w.event == nil {
					delete(s.Polled, w.series)
					continue
				}
				e := w.event
				s.Polled[w.series] = model.PolledAlert{StartsAt: e.Timestamp,
					Labels:      map[string]string{"definition_id": e.DefinitionID, "definition_type": e.DefinitionType, "key": e.Key},
					Annotations: map[string]string{"event": string(e.Raw), "title": e.Title, "description": e.Description}}
			}
		})
	}
	for i, w := range send {
		if _, _, err := p.a.queue.Enqueue(ctx, ingest.Request{ConnectorID: con.ID, Version: con.Published, RemoteIP: "127.0.0.1",
			Method: http.MethodPost, Headers: map[string]string{"content-type": "application/json", pollHeader: src.ID},
			Body: w.body}, ""); err != nil {
			// What was handed over stays handed over; the rest is sent by the next poll.
			remember(send[:i])
			return fmt.Errorf("%w: %v", ErrIngestDown, err)
		}
		st.Sent++
	}
	remember(send)
	return nil
}

// graylogBody is what a Graylog HTTP notification sends, with the status of the alert added.
func graylogBody(event json.RawMessage, defID, defType, title, description, status string) []byte {
	if len(event) == 0 {
		event = json.RawMessage(`{}`)
	}
	b, _ := json.Marshal(map[string]any{
		"event_definition_id": defID, "event_definition_type": defType, "event_definition_title": title,
		"event_definition_description": description, "job_definition_id": "", "job_trigger_id": "",
		"event": event, "backlog": []any{}, "status": status,
	})
	return b
}
