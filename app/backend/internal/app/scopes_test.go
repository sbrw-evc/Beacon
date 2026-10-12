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
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type scopedView struct {
	managed
	ServiceIDs []string `json:"service_ids"`
	Services   []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Missing bool   `json:"missing"`
	} `json:"services"`
}

func TestUserServiceScopeEdit(t *testing.T) {
	h := newHarness(t)
	h.addLocal("admin", adminPass, model.RoleAdmin, time.Now())
	id := h.addLocal("owner", "Owner-pass-2026-x", model.RoleUser, time.Now())
	h.st.Write(func(d *store.Data) {
		d.EnsureSystemRoles(time.Now())
		d.Services["S-1"] = &model.Service{ID: "S-1", Name: "Billing", Status: model.ServiceActive}
		d.Services["S-2"] = &model.Service{ID: "S-2", Name: "Shop", Status: model.ServiceActive}
	})
	admin := h.client()
	admin.login("admin", adminPass)

	var p problem
	code := admin.call(http.MethodPut, "/api/users/"+id, map[string]any{"service_ids": []string{"S-1", "S-404"}}, &p)
	if code != http.StatusBadRequest || p.Error != "service_not_found" {
		t.Fatalf("unknown service = %d %+v", code, p)
	}
	if u := h.user(id); len(u.ServiceIDs) != 0 {
		t.Fatalf("scope changed on error: %v", u.ServiceIDs)
	}

	var v scopedView
	code = admin.call(http.MethodPut, "/api/users/"+id, map[string]any{"service_ids": []string{"S-2", " ", "S-1", "S-2"}}, &v)
	expect(t, "set scope", code, http.StatusOK, v)
	if !slices.Equal(v.ServiceIDs, []string{"S-1", "S-2"}) || len(v.Services) != 2 || v.Services[0].Name != "Billing" || v.Services[1].Name != "Shop" {
		t.Fatalf("view = %+v", v)
	}
	if u := h.user(id); !slices.Equal(u.ServiceIDs, []string{"S-1", "S-2"}) || !h.audited("user.update", id) {
		t.Fatalf("stored = %v", u.ServiceIDs)
	}

	// Other changes keep the scope; a deleted service shows as missing.
	code = admin.call(http.MethodPut, "/api/users/"+id, map[string]any{"team_ids": []string{}}, &v)
	expect(t, "other change", code, http.StatusOK, v)
	h.st.Write(func(d *store.Data) { delete(d.Services, "S-2") })
	var list []scopedView
	expect(t, "list", admin.call(http.MethodGet, "/api/users", nil, &list), http.StatusOK, list)
	for _, u := range list {
		if u.ID == id && (len(u.Services) != 2 || !u.Services[1].Missing || u.Services[1].Name != "S-2") {
			t.Fatalf("list view = %+v", u)
		}
	}

	// The user sees their own scope; an empty list lifts it.
	owner := h.client()
	me := owner.login("owner", "Owner-pass-2026-x")
	if ids, _ := me["service_ids"].([]any); len(ids) != 2 {
		t.Fatalf("me = %v", me)
	}
	code = admin.call(http.MethodPut, "/api/users/"+id, map[string]any{"service_ids": []string{}}, &v)
	expect(t, "clear scope", code, http.StatusOK, v)
	if u := h.user(id); u.ServiceIDs != nil || len(v.Services) != 0 {
		t.Fatalf("cleared = %v %+v", u.ServiceIDs, v)
	}
}

func TestIncidentScopes(t *testing.T) {
	f := newConnFixture(t, true)
	f.h.st.Write(func(d *store.Data) {
		d.EnsureSystemRoles(time.Now())
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "db-01", Status: model.CIStatusActive}
		d.ConfigItems["CI-2"] = &model.ConfigItem{ID: "CI-2", Name: "web-01", Status: model.CIStatusActive}
		d.ConfigItems["CI-3"] = &model.ConfigItem{ID: "CI-3", Name: "lab-01", Status: model.CIStatusActive}
		d.Services["S-1"] = &model.Service{ID: "S-1", Name: "Billing", Status: model.ServiceActive, CIIDs: []string{"CI-1"}}
		d.Services["S-2"] = &model.Service{ID: "S-2", Name: "Shop", Status: model.ServiceActive, CIIDs: []string{"CI-2"}}
	})
	f.h.addRole("ops", "incidents:view", "incidents:ack", "cmdb:view")
	ownerID := f.h.addLocal("owner", "Owner-pass-2026-x", "ops", time.Now())
	f.h.addLocal("ops", "Ops-pass-2026-x", "ops", time.Now())
	f.expect(f.admin, http.MethodPut, "/api/users/"+ownerID, map[string]any{"service_ids": []string{"S-1"}}, http.StatusOK, nil)
	adminID := ""
	f.h.st.Read(func(d *store.Data) { adminID = d.UserByName("admin").ID })
	f.expect(f.admin, http.MethodPut, "/api/users/"+adminID, map[string]any{"service_ids": []string{"S-2"}}, http.StatusOK, nil)

	cred := f.credential(flow.CredBearer, nil, map[string]string{"token": "tkn-1"})
	var c app.ConnectorView
	f.expect(f.admin, http.MethodPost, "/api/connectors", map[string]any{"name": "Webhook", "slug": "hook", "preset": "webhook",
		"credentials": map[string]string{"webhook.credential": cred.ID}}, http.StatusCreated, &c)
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/publish", map[string]any{}, http.StatusOK, nil)
	auth := map[string]string{"Authorization": "Bearer tkn-1", "Content-Type": "application/json"}
	f.ingest("hook", `[{"id":"a1","title":"Disk full","host":"db-01","signal":"disk","severity":"critical","status":"firing"},
		{"id":"a2","title":"Down","host":"web-01","signal":"http","severity":"error","status":"firing"},
		{"id":"a3","title":"Lab","host":"lab-01","signal":"cpu","severity":"warning","status":"firing"}]`, auth)

	// The administrator is never limited, even with a scope set.
	var page alert.Page
	waitFor(t, "three incidents", func() bool {
		f.admin.call(http.MethodGet, "/api/incidents?status=active", nil, &page)
		return len(page.Alerts) == 3
	})
	if page.Counts.Active != 3 {
		t.Fatalf("admin counts = %+v", page.Counts)
	}
	byCI := map[string]string{}
	for _, a := range page.Alerts {
		byCI[a.CIID] = a.ID
	}
	in, out, none := byCI["CI-1"], byCI["CI-2"], byCI["CI-3"]

	owner := f.h.client()
	owner.login("owner", "Owner-pass-2026-x")
	page = alert.Page{}
	f.expect(owner, http.MethodGet, "/api/incidents?status=active", nil, http.StatusOK, &page)
	if len(page.Alerts) != 1 || page.Alerts[0].ID != in || page.Counts.Active != 1 || page.Counts.BySeverity["error"] != 0 || page.Counts.BySeverity["critical"] != 1 {
		t.Fatalf("owner list = %+v", page)
	}
	f.expect(owner, http.MethodGet, "/api/incidents?service=S-2", nil, http.StatusOK, &page)
	if len(page.Alerts) != 0 {
		t.Fatalf("service outside the scope = %+v", page.Alerts)
	}
	f.expect(owner, http.MethodGet, "/api/incidents?service=S-1", nil, http.StatusOK, &page)
	if len(page.Alerts) != 1 {
		t.Fatalf("service in the scope = %+v", page.Alerts)
	}
	for _, id := range []string{out, none} {
		f.expect(owner, http.MethodGet, "/api/incidents/"+id, nil, http.StatusNotFound, nil)
		f.expect(owner, http.MethodPost, "/api/incidents/"+id+"/ack", nil, http.StatusNotFound, nil)
		f.expect(owner, http.MethodPost, "/api/incidents/"+id+"/comment", map[string]string{"text": "x"}, http.StatusNotFound, nil)
	}
	f.expect(owner, http.MethodGet, "/api/incidents/"+in, nil, http.StatusOK, nil)
	var bulk struct {
		Done   []string          `json:"done"`
		Failed map[string]string `json:"failed"`
	}
	f.expect(owner, http.MethodPost, "/api/incidents/bulk", map[string]any{"ids": []string{in, out, none}, "action": "ack"}, http.StatusOK, &bulk)
	if !slices.Equal(bulk.Done, []string{in}) || bulk.Failed[out] != "not_found" || bulk.Failed[none] != "not_found" {
		t.Fatalf("bulk = %+v", bulk)
	}

	// The Grafana link of an incident outside the scope leads back to the incident page.
	f.h.st.Write(func(d *store.Data) {
		d.Settings.Alerting.Grafana = model.Grafana{DashboardURL: "https://grafana.example/d/x"}
	})
	noFollow := *owner.http
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for id, grafana := range map[string]bool{in: true, out: false} {
		resp, err := noFollow.Get(f.h.srv.URL + "/go/incidents/" + id + "/grafana")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if loc := resp.Header.Get("Location"); (len(loc) > 4 && loc[:5] == "https") != grafana {
			t.Errorf("grafana %s -> %s", id, loc)
		}
	}

	// Without a scope, a user sees every incident.
	ops := f.h.client()
	ops.login("ops", "Ops-pass-2026-x")
	f.expect(ops, http.MethodGet, "/api/incidents?status=active", nil, http.StatusOK, &page)
	if len(page.Alerts) != 3 || page.Counts.Active != 3 {
		t.Fatalf("unscoped = %d %+v", len(page.Alerts), page.Counts)
	}
	f.expect(ops, http.MethodPost, "/api/incidents/"+out+"/ack", nil, http.StatusOK, nil)
}
