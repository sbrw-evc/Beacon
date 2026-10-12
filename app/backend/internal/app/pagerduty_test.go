package app_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/pagerduty"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/pagerduty/pdtest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func (f connFixture) webhookConnector() map[string]string {
	cred := f.credential(flow.CredBearer, nil, map[string]string{"token": "tkn-1"})
	var c app.ConnectorView
	f.expect(f.admin, http.MethodPost, "/api/connectors", map[string]any{"name": "Webhook", "slug": "hook", "preset": "webhook",
		"credentials": map[string]string{"webhook.credential": cred.ID}}, http.StatusCreated, &c)
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/publish", map[string]any{}, http.StatusOK, nil)
	return map[string]string{"Authorization": "Bearer tkn-1", "Content-Type": "application/json"}
}

func (f connFixture) post(path, body string, headers map[string]string) int {
	f.h.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, f.h.srv.URL+path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.h.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestPagerDutyRoundTrip(t *testing.T) {
	f := newConnFixture(t, true)
	pd := pdtest.New(t)
	f.h.st.Write(func(d *store.Data) {
		d.Teams["T-1"] = &model.Team{ID: "T-1", Name: "DBA"}
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "db-01", Status: model.CIStatusActive}
		d.Services["S-1"] = &model.Service{ID: "S-1", Name: "Billing", OwnerTeamID: "T-1", Status: model.ServiceActive, CIIDs: []string{"CI-1"}}
	})
	auth := f.webhookConnector()

	var v app.PagerDutyView
	f.expect(f.admin, http.MethodPut, "/api/pagerduty", map[string]any{"enabled": true, "region": "eu", "public_url": "http://umbrella.example/",
		"events_url": pd.EventsURL(), "api_url": pd.APIURL(), "api_token": pdtest.Token, "pd_service_id": pdtest.ServiceID, "min_severity": "warning",
		"routes": []map[string]any{{"name": "DBA", "team_id": "T-1", "routing_key": "dba-key"}}}, http.StatusOK, &v)
	if !v.HasRoutingKey || !v.HasAPIToken || v.ServiceName != "Payments" || len(v.Routes) != 1 || !v.Routes[0].HasKey ||
		v.PublicURL != "http://umbrella.example" || v.WebhookURL != "http://umbrella.example/api/pagerduty/webhook" {
		t.Fatalf("view = %+v", v)
	}
	if got := f.h.bao.Get("umbrella/pagerduty"); got == nil {
		t.Errorf("secrets are in OpenBao: %v", f.h.bao.Paths())
	}
	// Keeping the route without its key keeps the key.
	f.expect(f.admin, http.MethodPut, "/api/pagerduty", map[string]any{"enabled": true, "public_url": "http://umbrella.example",
		"events_url": pd.EventsURL(), "api_url": pd.APIURL(), "routes": []map[string]any{{"id": v.Routes[0].ID, "name": "DBA", "team_id": "T-1"}}}, http.StatusOK, &v)
	if !v.HasRoutingKey || len(v.Routes) != 1 || !v.Routes[0].HasKey || v.ServiceName != "Payments" {
		t.Fatalf("kept = %+v", v)
	}
	f.expect(f.admin, http.MethodPut, "/api/pagerduty", map[string]any{"enabled": true, "routes": []map[string]any{{"name": "x"}}}, http.StatusBadRequest, nil)
	f.expect(f.admin, http.MethodPost, "/api/pagerduty/subscription", nil, http.StatusOK, &v)
	if !v.HasWebhookSecret || v.WebhookSubscriptionID == "" || pd.Subscriptions[v.WebhookSubscriptionID] != v.WebhookURL {
		t.Fatalf("subscription = %+v", v)
	}
	f.expect(f.admin, http.MethodPost, "/api/pagerduty/check", nil, http.StatusOK, nil)

	f.ingest("hook", `{"id":"a1","title":"Disk full","host":"db-01","signal":"disk","severity":"critical","status":"firing"}`, auth)
	waitFor(t, "the trigger in PagerDuty", func() bool { return len(pd.Events()) == 1 })
	ev := pd.Events()[0]
	if ev.EventAction != "trigger" || ev.RoutingKey != "dba-key" || ev.Payload == nil || ev.Payload.Severity != "critical" ||
		ev.Payload.Group != "Billing" || !strings.HasPrefix(ev.DedupKey, "umb-INC-") || len(ev.Links) != 1 ||
		ev.Links[0].Href != "http://umbrella.example/incidents?id="+strings.TrimPrefix(ev.DedupKey, "umb-") {
		t.Fatalf("event = %+v %+v", ev, ev.Payload)
	}
	id := strings.TrimPrefix(ev.DedupKey, "umb-")
	var view struct {
		Alert alert.Alert `json:"alert"`
	}
	waitFor(t, "accepted", func() bool {
		f.admin.call(http.MethodGet, "/api/incidents/"+id, nil, &view)
		return view.Alert.PD.State == alert.PDAccepted
	})
	if view.Alert.PD.Route != "DBA" {
		t.Errorf("route = %q", view.Alert.PD.Route)
	}

	hook := fmt.Sprintf(`{"event":{"id":"E1","event_type":"incident.acknowledged","agent":{"summary":"Jane"},"data":{"id":"Q1","type":"incident","html_url":"https://pd/incidents/Q1","incident_key":%q,"service":{"id":"PSVC1","summary":"Payments"}}}}`, ev.DedupKey)
	if code := f.post("/api/pagerduty/webhook", hook, map[string]string{"X-PagerDuty-Signature": "v1=bad"}); code != http.StatusUnauthorized {
		t.Errorf("bad signature = %d", code)
	}
	if code := f.post("/api/pagerduty/webhook", hook, map[string]string{"X-PagerDuty-Signature": pdtest.Sign(pdtest.Secret, []byte(hook))}); code != http.StatusOK {
		t.Fatalf("webhook = %d", code)
	}
	f.admin.call(http.MethodGet, "/api/incidents/"+id, nil, &view)
	if view.Alert.Status != alert.StatusAcknowledged || view.Alert.AckedBy != "Jane" || view.Alert.PD.IncidentURL != "https://pd/incidents/Q1" {
		t.Errorf("acknowledged in PagerDuty: %+v", view.Alert)
	}

	// Moved to another queue (service) in PagerDuty: the incident follows it, on the timeline too.
	moved := fmt.Sprintf(`{"event":{"id":"E2","event_type":"incident.reassigned","agent":{"summary":"Jane"},"data":{"id":"Q1","type":"incident","incident_key":%q,"service":{"id":"PSVC2","summary":"Billing"}}}}`, ev.DedupKey)
	if code := f.post("/api/pagerduty/webhook", moved, map[string]string{"X-PagerDuty-Signature": pdtest.Sign(pdtest.Secret, []byte(moved))}); code != http.StatusOK {
		t.Fatalf("webhook = %d", code)
	}
	var detail struct {
		Alert    alert.Alert   `json:"alert"`
		Timeline []alert.Entry `json:"timeline"`
	}
	f.admin.call(http.MethodGet, "/api/incidents/"+id, nil, &detail)
	if detail.Alert.PD.Queue != "PSVC2" || detail.Alert.PD.QueueName != "Billing" ||
		!slices.ContainsFunc(detail.Timeline, func(e alert.Entry) bool {
			return e.Code == "pd_queue_moved" && e.Args["from"] == "Payments" && e.Args["to"] == "Billing"
		}) {
		t.Errorf("queue = %+v, timeline = %+v", detail.Alert.PD, detail.Timeline)
	}

	f.expect(f.admin, http.MethodPost, "/api/incidents/"+id+"/resolve", nil, http.StatusOK, nil)
	waitFor(t, "the resolve in PagerDuty", func() bool { return len(pd.Events()) == 2 })
	if ev := pd.Events()[1]; ev.EventAction != "resolve" || ev.DedupKey != "umb-"+id || ev.Payload != nil {
		t.Errorf("resolve = %+v", ev)
	}

	// A warning below the threshold of a new setting is not sent.
	f.expect(f.admin, http.MethodPost, "/api/pagerduty/test", nil, http.StatusOK, nil)
	if n := len(pd.Events()); n != 4 {
		t.Errorf("the test sends trigger and resolve, events = %d", n)
	}
	pd.SetEventsStatus(http.StatusBadRequest)
	f.expect(f.admin, http.MethodPost, "/api/pagerduty/test", nil, http.StatusBadGateway, nil)
}

func TestPagerDutyRoute(t *testing.T) {
	set := model.PagerDuty{RoutingKeyRef: "default", Routes: []model.PDRoute{
		{Name: "Billing", ServiceID: "S-2", RoutingKeyRef: "billing"},
		{Name: "DBA", TeamID: "T-1", RoutingKeyRef: "dba"},
	}}
	a := alert.Alert{Route: alert.Route{Team: &alert.Ref{ID: "T-1"}, Services: []alert.Ref{{ID: "S-1"}, {ID: "S-2"}}}}
	if name, ref := pagerduty.Route(set, a); name != "Billing" || ref != "billing" {
		t.Errorf("first matching route: %s %s", name, ref)
	}
	a.Route.Services = nil
	if name, _ := pagerduty.Route(set, a); name != "DBA" {
		t.Errorf("team route: %s", name)
	}
	if name, ref := pagerduty.Route(set, alert.Alert{}); name != pagerduty.DefaultRoute || ref != "default" {
		t.Errorf("default: %s %s", name, ref)
	}
}

// The queues of PagerDuty are linked to the teams of Umbrella by the name of their PagerDuty
// team: a queue no route sends to gets a route of that team, once; a disabled queue, one of a
// team Umbrella does not have, and one already linked are left alone.
func TestPagerDutyQueues(t *testing.T) {
	f := newConnFixture(t, false)
	pd := pdtest.New(t)
	pd.Extra = []pdtest.Service{
		{ID: "PSVC2", Name: "Billing API", Status: "active", TeamID: "PT2", Team: "sre"},
		{ID: "PSVC3", Name: "Old", Status: "disabled", TeamID: "PT2", Team: "SRE"},
		{ID: "PSVC4", Name: "Mobile", Status: "active", TeamID: "PT4", Team: "Mobile"},
	}
	f.h.st.Write(func(d *store.Data) {
		d.Teams["T-1"] = &model.Team{ID: "T-1", Name: "SRE"}
		d.Teams["T-2"] = &model.Team{ID: "T-2", Name: "Payments"}
	})
	var v app.PagerDutyView
	f.expect(f.admin, http.MethodPut, "/api/pagerduty", map[string]any{"enabled": true, "events_url": pd.EventsURL(), "api_url": pd.APIURL(),
		"api_token": pdtest.Token, "pd_service_id": pdtest.ServiceID}, http.StatusOK, &v)
	var out struct {
		Links  app.QueueLinks `json:"links"`
		Queues app.QueuesView `json:"queues"`
	}
	f.expect(f.admin, http.MethodPost, "/api/pagerduty/queues/link", nil, http.StatusOK, &out)
	if !slices.Equal(out.Links.Created, []string{"Billing API"}) || len(out.Queues.Queues) != 4 || !pd.Created("PSVC2") {
		t.Fatalf("links = %+v", out)
	}
	f.expect(f.admin, http.MethodGet, "/api/pagerduty", nil, http.StatusOK, &v)
	if len(v.Routes) != 1 || v.Routes[0].TeamID != "T-1" || v.Routes[0].PDServiceID != "PSVC2" || !v.Routes[0].HasKey {
		t.Fatalf("routes = %+v", v.Routes)
	}
	f.expect(f.admin, http.MethodPost, "/api/pagerduty/queues/link", nil, http.StatusOK, &out)
	if len(out.Links.Created) != 0 {
		t.Fatalf("linked twice: %+v", out.Links)
	}
	// A queue gone from PagerDuty is shown, its route kept.
	pd.Extra = pd.Extra[1:]
	f.expect(f.admin, http.MethodPost, "/api/pagerduty/queues/link", nil, http.StatusOK, &out)
	if !slices.Equal(out.Queues.Gone, []string{v.Routes[0].ID}) {
		t.Fatalf("gone = %+v", out.Queues.Gone)
	}
	f.h.st.Write(func(d *store.Data) {
		d.Roles["viewer"] = &model.Role{ID: "viewer", Name: "Viewer", Permissions: []string{"settings.alerting:view"}}
	})
	f.h.addLocal("vi", "Viewer-pass-2026", "viewer", time.Now())
	vi := f.h.client()
	vi.login("vi", "Viewer-pass-2026")
	f.expect(vi, http.MethodGet, "/api/pagerduty/queues", nil, http.StatusOK, nil)
	f.expect(vi, http.MethodPost, "/api/pagerduty/queues/link", nil, http.StatusForbidden, nil)
}
