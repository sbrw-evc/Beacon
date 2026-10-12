// Package pdtest is a fake PagerDuty: the Events API v2 and the part of the REST API Umbrella
// uses.
package pdtest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/pagerduty"
)

const (
	Token     = "pd-token"
	ServiceID = "PSVC1"
	// ServiceKey is the Events API key of the integration the fake creates for ServiceID.
	ServiceKey = "svc-key-1"
	Secret     = "whsec-1"
)

type Fake struct {
	Server *httptest.Server

	mu     sync.Mutex
	events []pagerduty.Event
	// EventsStatus answers Events API calls with this status when not zero.
	EventsStatus   int
	Subscriptions  map[string]string
	integration    bool
	IncidentAlerts map[string][]string
	// Incidents of the REST API by ID; Notes and Priorities written to them, with the From
	// header of each write in From.
	Incidents  map[string]*Incident
	Notes      map[string][]string
	Priorities map[string]string
	From       []string
	// OnCall are the people on call for the escalation policy of ServiceID.
	OnCall []OnCallUser
	// Extra are services listed after ServiceID (owned by team PT1 «Payments»); created holds
	// the services an Events API integration was created in.
	Extra   []Service
	created map[string]bool
}

// Service is an extra service of the fake.
type Service struct {
	ID, Name, Status, TeamID, Team string
}

// Incident is an incident of the fake REST API.
type Incident struct {
	ID, Key, Status, By string
	// Service is the service (queue) of the incident; empty is ServiceID.
	Service string
}

// OnCallUser is a person on call in the fake.
type OnCallUser struct {
	Name, Email string
	Level       int
}

// SetIncident adds or changes an incident.
func (f *Fake) SetIncident(in Incident) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Incidents[in.ID] = &in
}

// Snapshot returns the notes, priorities and From headers written so far.
func (f *Fake) Snapshot() (notes map[string][]string, priorities map[string]string, from []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	notes, priorities = map[string][]string{}, map[string]string{}
	for k, v := range f.Notes {
		notes[k] = append([]string(nil), v...)
	}
	for k, v := range f.Priorities {
		priorities[k] = v
	}
	return notes, priorities, append([]string(nil), f.From...)
}

func (in *Incident) json() map[string]any {
	svc := in.Service
	if svc == "" {
		svc = ServiceID
	}
	out := map[string]any{"id": in.ID, "incident_key": in.Key, "status": in.Status, "html_url": "https://pd/incidents/" + in.ID,
		"service": map[string]string{"id": svc, "summary": svc}}
	if in.By != "" {
		out["last_status_change_by"] = map[string]string{"summary": in.By}
		if in.Status == "acknowledged" {
			out["acknowledgements"] = []map[string]any{{"acknowledger": map[string]string{"summary": in.By}}}
		}
	}
	return out
}

func New(t *testing.T) *Fake {
	t.Helper()
	f := &Fake{Subscriptions: map[string]string{}, IncidentAlerts: map[string][]string{}, Incidents: map[string]*Incident{},
		Notes: map[string][]string{}, Priorities: map[string]string{}, created: map[string]bool{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Server.Close)
	return f
}

func (f *Fake) EventsURL() string { return f.Server.URL + "/v2/enqueue" }

func (f *Fake) APIURL() string { return f.Server.URL + "/api" }

func (f *Fake) Events() []pagerduty.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]pagerduty.Event(nil), f.events...)
}

func (f *Fake) SetEventsStatus(code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.EventsStatus = code
}

// Sign signs a webhook body the way PagerDuty does.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func write(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/v2/enqueue" {
		if f.EventsStatus != 0 {
			write(w, f.EventsStatus, map[string]any{"status": "error", "message": "unavailable"})
			return
		}
		var ev pagerduty.Event
		if err := json.NewDecoder(r.Body).Decode(&ev); err != nil || ev.RoutingKey == "" || ev.DedupKey == "" {
			write(w, http.StatusBadRequest, map[string]any{"status": "invalid event", "message": "Event object is invalid", "errors": []string{"bad"}})
			return
		}
		f.events = append(f.events, ev)
		write(w, http.StatusAccepted, map[string]any{"status": "success", "dedup_key": ev.DedupKey})
		return
	}
	path, ok := strings.CutPrefix(r.URL.Path, "/api")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Token token="+Token {
		write(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"message": "Unauthorized"}})
		return
	}
	integrations := func() []map[string]string {
		if !f.integration {
			return []map[string]string{}
		}
		return []map[string]string{{"id": "PINT1", "type": "events_api_v2_inbound_integration", "integration_key": ServiceKey}}
	}
	switch {
	case path == "/abilities":
		write(w, http.StatusOK, map[string]any{"abilities": []string{"teams", "urgencies"}})
	case path == "/services":
		list := []map[string]any{{"id": ServiceID, "name": "Payments", "status": "active", "html_url": "https://pd/services/" + ServiceID,
			"integrations": integrations(), "escalation_policy": map[string]string{"id": "PEP1", "summary": "Payments on-call"},
			"teams": []map[string]string{{"id": "PT1", "summary": "Payments"}}}}
		for _, x := range f.Extra {
			list = append(list, map[string]any{"id": x.ID, "name": x.Name, "status": x.Status, "html_url": "https://pd/services/" + x.ID,
				"integrations": []map[string]string{}, "escalation_policy": map[string]string{"id": "PEP-" + x.ID, "summary": x.Name + " policy"},
				"teams": []map[string]string{{"id": x.TeamID, "summary": x.Team}}})
		}
		write(w, http.StatusOK, map[string]any{"services": list, "more": false})
	case path == "/services/"+ServiceID && r.Method == http.MethodGet:
		write(w, http.StatusOK, map[string]any{"service": map[string]any{"id": ServiceID, "name": "Payments", "integrations": integrations(),
			"escalation_policy": map[string]string{"id": "PEP1", "summary": "Payments on-call"}}})
	case path == "/oncalls":
		var list []map[string]any
		if r.URL.Query().Get("escalation_policy_ids[]") == "PEP1" {
			for _, u := range f.OnCall {
				list = append(list, map[string]any{"escalation_level": u.Level, "escalation_policy": map[string]string{"id": "PEP1", "summary": "Payments on-call"},
					"user": map[string]string{"name": u.Name, "email": u.Email}})
			}
		}
		write(w, http.StatusOK, map[string]any{"oncalls": list})
	case path == "/priorities":
		write(w, http.StatusOK, map[string]any{"priorities": []map[string]string{{"id": "PRI1", "name": "P1"}, {"id": "PRI2", "name": "P2"}, {"id": "PRI3", "name": "P3"}}})
	case path == "/incidents" && r.Method == http.MethodGet:
		q := r.URL.Query()
		var list []map[string]any
		for _, in := range f.Incidents {
			if k := q.Get("incident_key"); k != "" && in.Key != k {
				continue
			}
			if st := q["statuses[]"]; len(st) > 0 && !slices.Contains(st, in.Status) {
				continue
			}
			list = append(list, in.json())
		}
		write(w, http.StatusOK, map[string]any{"incidents": list, "more": false})
	case strings.HasPrefix(path, "/incidents/") && strings.HasSuffix(path, "/notes") && r.Method == http.MethodPost:
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/incidents/"), "/notes")
		var body struct {
			Note struct {
				Content string `json:"content"`
			} `json:"note"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.From = append(f.From, r.Header.Get("From"))
		f.Notes[id] = append(f.Notes[id], body.Note.Content)
		write(w, http.StatusCreated, map[string]any{"note": map[string]string{"id": "PN1"}})
	case strings.HasPrefix(path, "/incidents/") && strings.Count(path, "/") == 2 && r.Method == http.MethodPut:
		id := strings.TrimPrefix(path, "/incidents/")
		var body struct {
			Incident struct {
				Priority struct {
					ID string `json:"id"`
				} `json:"priority"`
			} `json:"incident"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.From = append(f.From, r.Header.Get("From"))
		f.Priorities[id] = body.Incident.Priority.ID
		write(w, http.StatusOK, map[string]any{"incident": map[string]string{"id": id}})
	case strings.HasPrefix(path, "/incidents/") && strings.Count(path, "/") == 2 && r.Method == http.MethodGet:
		in := f.Incidents[strings.TrimPrefix(path, "/incidents/")]
		if in == nil {
			write(w, http.StatusNotFound, map[string]any{"error": map[string]any{"message": "Not Found"}})
			return
		}
		write(w, http.StatusOK, map[string]any{"incident": in.json()})
	case path == "/services/"+ServiceID+"/integrations" && r.Method == http.MethodPost:
		f.integration = true
		write(w, http.StatusCreated, map[string]any{"integration": integrations()[0]})
	case strings.HasPrefix(path, "/services/") && f.extra(strings.Split(path, "/")[2]) != nil:
		x := f.extra(strings.Split(path, "/")[2])
		ints := []map[string]string{}
		if f.created[x.ID] {
			ints = append(ints, map[string]string{"id": "PINT-" + x.ID, "type": "events_api_v2_inbound_integration", "integration_key": "key-" + x.ID})
		}
		if strings.HasSuffix(path, "/integrations") && r.Method == http.MethodPost {
			f.created[x.ID] = true
			write(w, http.StatusCreated, map[string]any{"integration": map[string]string{"id": "PINT-" + x.ID, "type": "events_api_v2_inbound_integration", "integration_key": "key-" + x.ID}})
			return
		}
		write(w, http.StatusOK, map[string]any{"service": map[string]any{"id": x.ID, "name": x.Name, "integrations": ints}})
	case path == "/webhook_subscriptions" && r.Method == http.MethodPost:
		var body struct {
			Sub struct {
				DeliveryMethod struct {
					URL string `json:"url"`
				} `json:"delivery_method"`
			} `json:"webhook_subscription"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		id := "PSUB1"
		f.Subscriptions[id] = body.Sub.DeliveryMethod.URL
		write(w, http.StatusCreated, map[string]any{"webhook_subscription": map[string]any{"id": id, "delivery_method": map[string]any{"secret": Secret}}})
	case strings.HasPrefix(path, "/webhook_subscriptions/") && r.Method == http.MethodDelete:
		delete(f.Subscriptions, strings.TrimPrefix(path, "/webhook_subscriptions/"))
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(path, "/incidents/") && strings.HasSuffix(path, "/alerts"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/incidents/"), "/alerts")
		var alerts []map[string]string
		for _, k := range f.IncidentAlerts[id] {
			alerts = append(alerts, map[string]string{"alert_key": k})
		}
		write(w, http.StatusOK, map[string]any{"alerts": alerts})
	default:
		write(w, http.StatusNotFound, map[string]any{"error": map[string]any{"message": "Not Found"}})
	}
}

func (f *Fake) extra(id string) *Service {
	for i := range f.Extra {
		if f.Extra[i].ID == id {
			return &f.Extra[i]
		}
	}
	return nil
}

// Created tells whether an Events API integration was created in an extra service.
func (f *Fake) Created(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.created[id]
}
