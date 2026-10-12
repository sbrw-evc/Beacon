package app_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/netbox"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type incidentCIResult struct {
	CI    app.CIView  `json:"ci"`
	Alert alert.Alert `json:"alert"`
	Bound []string    `json:"bound"`
}

type incidentDetail struct {
	Alert alert.Alert `json:"alert"`
	CI    *struct {
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		NetBoxURL string   `json:"netbox_url"`
		Aliases   []string `json:"aliases"`
	} `json:"ci"`
	Services []struct {
		ID    string       `json:"id"`
		Name  string       `json:"name"`
		Links []model.Link `json:"links"`
	} `json:"services"`
	Maintenance *struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"maintenance"`
}

func TestIncidentCreateAndBindCI(t *testing.T) {
	f := newConnFixture(t, true)
	f.h.st.Write(func(d *store.Data) {
		d.EnsureSystemRoles(time.Now())
		d.Teams["T-1"] = &model.Team{ID: "T-1", Name: "Web"}
		d.ConfigItems["CI-901"] = &model.ConfigItem{ID: "CI-901", Name: "web-01", Status: model.CIStatusActive, Source: model.SourceNetBox,
			NetBox: &model.NetBoxRef{Kind: netbox.KindDevice, ID: 7, URL: "https://netbox.example/dcim/devices/7/"}}
		d.ConfigItems["CI-902"] = &model.ConfigItem{ID: "CI-902", Name: "db-01", Status: model.CIStatusActive}
		d.Services["S-1"] = &model.Service{ID: "S-1", Name: "Shop", OwnerTeamID: "T-1", Status: model.ServiceActive, CIIDs: []string{"CI-901"},
			Links: []model.Link{{Title: "Runbook", URL: "https://wiki.example/shop"}}}
		d.Services["S-2"] = &model.Service{ID: "S-2", Name: "Billing", OwnerTeamID: "T-1", Status: model.ServiceActive}
	})
	lead := f.h.addLocal("lead", "Lead-pass-2026-x", model.RoleUser, time.Now())
	f.h.st.Write(func(d *store.Data) { d.Users[lead].TeamIDs = []string{"T-1"} })
	f.h.addRole("duty", "incidents:view", "incidents:ack")
	f.h.addRole("catalog", "incidents:view", "cis:edit")
	f.h.addLocal("duty", "Duty-pass-2026-x", "duty", time.Now())
	f.h.addLocal("catalog", "Catalog-pass-2026-x", "catalog", time.Now())
	scopedID := f.h.addLocal("scoped", "Scoped-pass-2026-x", "catalog", time.Now())
	f.expect(f.admin, http.MethodPut, "/api/users/"+scopedID, map[string]any{"service_ids": []string{"S-1"}}, http.StatusOK, nil)

	cred := f.credential(flow.CredBearer, nil, map[string]string{"token": "tkn-1"})
	var c app.ConnectorView
	f.expect(f.admin, http.MethodPost, "/api/connectors", map[string]any{"name": "Webhook", "slug": "hook", "preset": "webhook",
		"credentials": map[string]string{"webhook.credential": cred.ID}}, http.StatusCreated, &c)
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/publish", map[string]any{}, http.StatusOK, nil)
	auth := map[string]string{"Authorization": "Bearer tkn-1", "Content-Type": "application/json"}
	f.ingest("hook", `[{"id":"a1","title":"Down","host":"new-host","signal":"ping","severity":"critical","status":"firing"},
		{"id":"a2","title":"Disk","host":"new-host","signal":"disk","severity":"warning","status":"firing"},
		{"id":"a3","title":"Slow","host":"edge-77.dc:9100","signal":"latency","severity":"error","status":"firing"},
		{"id":"a4","title":"Up","host":"web-01","signal":"http","severity":"error","status":"firing"}]`, auth)
	var page alert.Page
	waitFor(t, "four incidents", func() bool {
		f.admin.call(http.MethodGet, "/api/incidents?status=active", nil, &page)
		return len(page.Alerts) == 4
	})
	byName := map[string]alert.Alert{}
	for _, a := range page.Alerts {
		byName[a.CIName+"/"+a.Signal] = a
	}
	ping, disk, slow, bound := byName["new-host/ping"], byName["new-host/disk"], byName["edge-77.dc:9100/latency"], byName["web-01/http"]
	if ping.CIID != "" || slow.CIID != "" || bound.CIID != "CI-901" {
		t.Fatalf("incidents = %+v", page.Alerts)
	}

	// The card of a bound incident leads to the item (with its NetBox link) and the services with their links.
	var detail incidentDetail
	f.expect(f.admin, http.MethodGet, "/api/incidents/"+bound.ID, nil, http.StatusOK, &detail)
	if detail.CI == nil || detail.CI.ID != "CI-901" || detail.CI.NetBoxURL != "https://netbox.example/dcim/devices/7/" ||
		len(detail.Services) != 1 || detail.Services[0].ID != "S-1" || len(detail.Services[0].Links) != 1 || detail.Maintenance != nil {
		t.Fatalf("detail = %+v", detail)
	}

	// Permissions: creating items needs cis:edit, binding the new item to a service services:edit,
	// and a user limited to some services does not see incidents outside them.
	duty, catalog, scoped := f.h.client(), f.h.client(), f.h.client()
	duty.login("duty", "Duty-pass-2026-x")
	catalog.login("catalog", "Catalog-pass-2026-x")
	scoped.login("scoped", "Scoped-pass-2026-x")
	f.expect(duty, http.MethodPost, "/api/incidents/"+ping.ID+"/create-ci", map[string]any{"kind": "vm"}, http.StatusForbidden, nil)
	f.expect(duty, http.MethodPost, "/api/incidents/"+slow.ID+"/bind-ci", map[string]any{"ci_id": "CI-901"}, http.StatusForbidden, nil)
	f.expect(catalog, http.MethodPost, "/api/incidents/"+ping.ID+"/create-ci", map[string]any{"kind": "vm", "service_id": "S-2"}, http.StatusForbidden, nil)
	f.expect(scoped, http.MethodPost, "/api/incidents/"+ping.ID+"/create-ci", map[string]any{"kind": "vm"}, http.StatusNotFound, nil)
	f.expect(scoped, http.MethodPost, "/api/incidents/"+slow.ID+"/bind-ci", map[string]any{"ci_id": "CI-901"}, http.StatusNotFound, nil)
	f.expect(f.admin, http.MethodPost, "/api/incidents/"+bound.ID+"/create-ci", map[string]any{"kind": "vm"}, http.StatusConflict, nil)
	f.expect(f.admin, http.MethodPost, "/api/incidents/"+ping.ID+"/create-ci", map[string]any{"kind": "vm", "service_id": "S-404"}, http.StatusBadRequest, nil)
	f.h.st.Read(func(d *store.Data) {
		if len(d.ConfigItems) != 2 {
			t.Fatalf("nothing created by refused requests: %d items", len(d.ConfigItems))
		}
	})

	// Creating the item from the incident binds it (and the other incident of the same host) at once,
	// routed through the chosen service.
	var created incidentCIResult
	f.expect(f.admin, http.MethodPost, "/api/incidents/"+ping.ID+"/create-ci", map[string]any{"kind": "vm", "ips": []string{"10.1.1.5"}, "service_id": "S-2"},
		http.StatusCreated, &created)
	if created.CI.Name != "new-host" || created.CI.Kind != "vm" || len(created.CI.Services) != 1 || created.CI.Services[0].ID != "S-2" {
		t.Fatalf("created item = %+v", created.CI)
	}
	if a := created.Alert; a.CIID != created.CI.ID || a.Route.Team == nil || a.Route.Team.ID != "T-1" || len(a.Route.People) != 1 ||
		len(a.Route.Services) != 1 || a.Route.Services[0].ID != "S-2" {
		t.Fatalf("bound incident = %+v", a)
	}
	if !slices.Contains(created.Bound, ping.ID) || !slices.Contains(created.Bound, disk.ID) {
		t.Errorf("bound = %v", created.Bound)
	}

	f.expect(duty, http.MethodPut, "/api/cis/CI-902/aliases", map[string]any{"aliases": []string{"db-01.old"}}, http.StatusForbidden, nil)
	// A name taken by another item cannot become an alias.
	f.expect(f.admin, http.MethodPut, "/api/cis/CI-902/aliases", map[string]any{"aliases": []string{"WEB-01"}}, http.StatusConflict, nil)

	// Binding to an existing item (an imported one too) keeps the event name as an alias: the
	// incident is bound now and later events of that name fold into it.
	var bind incidentCIResult
	f.expect(catalog, http.MethodPost, "/api/incidents/"+slow.ID+"/bind-ci", map[string]any{"ci_id": "CI-901"}, http.StatusOK, &bind)
	if bind.Alert.CIID != "CI-901" || bind.Alert.Route.Team == nil || !slices.Equal(bind.CI.Aliases, []string{"edge-77.dc:9100"}) {
		t.Fatalf("bind = %+v", bind)
	}
	f.ingest("hook", `{"id":"a5","title":"Slow","host":"edge-77.dc:9100","signal":"latency","severity":"critical","status":"firing"}`, auth)
	waitFor(t, "the later event folded", func() bool {
		var d incidentDetail
		f.admin.call(http.MethodGet, "/api/incidents/"+slow.ID, nil, &d)
		return d.Alert.Severity == "critical" && len(d.Alert.Sources) == 2
	})
	f.expect(f.admin, http.MethodGet, "/api/incidents?status=active", nil, http.StatusOK, &page)
	if len(page.Alerts) != 4 {
		t.Errorf("no new incident: %d", len(page.Alerts))
	}
	f.expect(f.admin, http.MethodGet, "/api/incidents/"+slow.ID, nil, http.StatusOK, &detail)
	if detail.CI == nil || !slices.Equal(detail.CI.Aliases, []string{"edge-77.dc:9100"}) {
		t.Errorf("detail ci = %+v", detail.CI)
	}

	// An incident held by a maintenance window names the window.
	var mw struct {
		ID string `json:"id"`
	}
	f.expect(f.admin, http.MethodPost, "/api/maintenance", map[string]any{"title": "Disk swap", "ci_ids": []string{"CI-901"},
		"start": time.Now().Add(-time.Minute).Format(time.RFC3339), "end": time.Now().Add(time.Hour).Format(time.RFC3339)}, http.StatusCreated, &mw)
	f.ingest("hook", `{"id":"a4","title":"Up","host":"web-01","signal":"http","severity":"critical","status":"firing"}`, auth)
	waitFor(t, "the incident held by the window", func() bool {
		var d incidentDetail
		f.admin.call(http.MethodGet, "/api/incidents/"+bound.ID, nil, &d)
		return d.Maintenance != nil && d.Maintenance.ID == mw.ID && d.Maintenance.Title == "Disk swap"
	})
}
