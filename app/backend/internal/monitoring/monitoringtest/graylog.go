package monitoringtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Graylog answers /api/system, the events search and the aggregation API. Requests need the
// access Token as the user name with the password "token" and the X-Requested-By header.
type Graylog struct {
	URL   string
	Token string

	mu      sync.Mutex
	events  []GraylogEvent
	sources map[string]int
}

// GraylogEvent is an alert event of a definition.
type GraylogEvent struct {
	ID, DefinitionID, Title, Key, Message string
	Priority                              int
	At                                    time.Time
	GroupBy                               map[string]string
}

func StartGraylog(t *testing.T) *Graylog {
	g := &Graylog{Token: "gl-token", sources: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(srv.Close)
	g.URL = srv.URL
	return g
}

// AddEvent adds an alert event.
func (g *Graylog) AddEvent(e GraylogEvent) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.events = append(g.events, e)
}

// ClearEvents forgets every event.
func (g *Graylog) ClearEvents() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.events = nil
}

// SetSources sets the message counts per source of the last day.
func (g *Graylog) SetSources(counts map[string]int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sources = counts
}

func (g *Graylog) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if u, p, ok := r.BasicAuth(); !ok || u != g.Token || p != "token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPost && r.Header.Get("X-Requested-By") == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	switch r.URL.Path {
	case "/api/system":
		_ = json.NewEncoder(w).Encode(map[string]any{"version": "6.1.2+a1b2c3d", "hostname": "graylog"})
	case "/api/events/search":
		var in struct {
			Page      int `json:"page"`
			PerPage   int `json:"per_page"`
			Timerange struct {
				Range int `json:"range"`
			} `json:"timerange"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		since := time.Now().Add(-time.Duration(in.Timerange.Range) * time.Second)
		events, defs := []any{}, map[string]any{}
		for _, e := range g.events {
			if e.At.Before(since) {
				continue
			}
			events = append(events, map[string]any{"event": map[string]any{
				"id": e.ID, "event_definition_id": e.DefinitionID, "event_definition_type": "aggregation-v1",
				"timestamp": e.At.UTC().Format("2006-01-02T15:04:05.000Z"), "message": e.Message, "source": "graylog",
				"key": e.Key, "key_tuple": []string{e.Key}, "priority": e.Priority, "alert": true, "fields": map[string]any{}, "group_by_fields": e.GroupBy,
			}, "index_name": "gl-events_0"})
			defs[e.DefinitionID] = map[string]any{"id": e.DefinitionID, "title": e.Title, "description": ""}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"events": events, "total_events": len(events), "context": map[string]any{"event_definitions": defs}})
	case "/api/search/aggregate":
		rows := []any{}
		for s, n := range g.sources {
			rows = append(rows, []any{s, n})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"schema": []any{}, "datarows": rows})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}
