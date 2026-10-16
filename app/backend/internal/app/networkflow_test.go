package app_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/beacon/app/backend/internal/alert"
	"github.com/sbrw-evc/beacon/app/backend/internal/app"
	"github.com/sbrw-evc/beacon/app/backend/internal/model"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
)

func TestFlowLink(t *testing.T) {
	opened := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	now := opened.Add(time.Hour)
	al := alert.Alert{ID: "INC-3", CIName: "edge-01", OpenedAt: opened,
		Labels: map[string]string{"ip": "10.0.0.9", "source_ref": "inventory-db:dcim.device:7"}}
	if app.FlowLink("", al, nil, now) != "" {
		t.Fatal("a link without the Current address")
	}
	// Without an item: the name, ip and source_ref of the events; active: until now.
	u, _ := url.Parse(app.FlowLink("https://flow.example/", al, nil, now))
	q := u.Query()
	if u.Host != "flow.example" || u.Path != "/go/incident" || q.Get("ci") != "edge-01" || q.Get("ip") != "10.0.0.9" ||
		q.Get("ref") != "inventory-db:dcim.device:7" || q.Get("from") != "2026-10-05T09:30:00Z" || q.Get("to") != "2026-10-05T11:00:00Z" {
		t.Fatalf("link = %s", u)
	}
	// The catalog item wins; resolved: until 10 minutes after the resolution.
	ci := &model.ConfigItem{ID: "CI-1", Name: "edge-01.corp", IPs: []string{"10.0.0.1", "10.0.0.2"}, InventoryDB: &model.InventoryRef{Ref: "inventory-db:dcim.device:100"}}
	resolved := opened.Add(20 * time.Minute)
	al.ResolvedAt = &resolved
	u, _ = url.Parse(app.FlowLink("https://flow.example", al, ci, now))
	q = u.Query()
	if q.Get("ci") != "edge-01.corp" || q.Get("ip") != "10.0.0.1" || q.Get("ref") != "inventory-db:dcim.device:100" || q.Get("to") != "2026-10-05T10:30:00Z" {
		t.Fatalf("resolved link = %s", u)
	}
	// Resolved a moment ago: not past now.
	if u, _ = url.Parse(app.FlowLink("https://flow.example", al, ci, opened.Add(25*time.Minute))); u.Query().Get("to") != "2026-10-05T10:25:00Z" {
		t.Fatalf("just resolved = %s", u)
	}
	// An item without addresses or references leaves them out.
	u, _ = url.Parse(app.CIFlowLink("https://flow.example", &model.ConfigItem{ID: "CI-2", Name: "svc"}, now))
	if q = u.Query(); q.Get("ci") != "svc" || q.Has("ip") || q.Has("ref") || q.Get("from") != "2026-10-05T10:00:00Z" || q.Get("to") != "2026-10-05T11:00:00Z" {
		t.Fatalf("item link = %s", u)
	}
}

func TestFlowRedirect(t *testing.T) {
	f := newConnFixture(t, true)
	f.h.st.Write(func(d *store.Data) {
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "db-01", Status: model.CIStatusActive, IPs: []string{"10.0.0.11"}}
	})
	auth := f.webhookConnector()
	f.ingest("hook", `{"id":"a1","title":"Traffic","host":"db-01","signal":"use.link.utilization","severity":"warning","status":"firing"}`, auth)
	var page struct {
		Alerts []alert.Alert `json:"alerts"`
	}
	waitFor(t, "the incident", func() bool {
		f.admin.call(http.MethodGet, "/api/incidents", nil, &page)
		return len(page.Alerts) == 1
	})
	id := page.Alerts[0].ID
	var detail struct {
		Flow string `json:"flow_url"`
	}
	var ci struct {
		Flow string `json:"flow_url"`
	}
	f.admin.call(http.MethodGet, "/api/incidents/"+id, nil, &detail)
	f.admin.call(http.MethodGet, "/api/cis/CI-1", nil, &ci)
	if detail.Flow != "" || ci.Flow != "" {
		t.Fatalf("links before the Current address is set: %q %q", detail.Flow, ci.Flow)
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	signedIn := func(path string) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, f.h.srv.URL+path, nil)
		for _, c := range f.admin.http.Jar.Cookies(req.URL) {
			req.AddCookie(c)
		}
		resp, err := noFollow.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	// Not set: the link goes to the incident.
	if resp := signedIn("/go/incidents/" + id + "/flow"); resp.Header.Get("Location") != "/incidents?id="+id {
		t.Fatalf("not set: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	f.expect(f.admin, http.MethodPut, "/api/flow", map[string]any{"url": "ftp://x"}, http.StatusBadRequest, nil)
	var set model.NetworkFlow
	f.expect(f.admin, http.MethodPut, "/api/flow", map[string]any{"url": " https://flow.example/ "}, http.StatusOK, &set)
	if set.URL != "https://flow.example" || set.UpdatedBy == "" {
		t.Fatalf("flow = %+v", set)
	}
	f.expect(f.admin, http.MethodGet, "/api/flow", nil, http.StatusOK, &set)
	if set.URL != "https://flow.example" {
		t.Fatalf("flow = %+v", set)
	}
	f.admin.call(http.MethodGet, "/api/incidents/"+id, nil, &detail)
	f.admin.call(http.MethodGet, "/api/cis/CI-1", nil, &ci)
	if detail.Flow != "/go/incidents/"+id+"/flow" || ci.Flow != "/go/cis/CI-1/flow" {
		t.Fatalf("links: %q %q", detail.Flow, ci.Flow)
	}

	// Cross-site without the cookie: the hop page, as for Grafana.
	resp, err := noFollow.Get(f.h.srv.URL + "/go/incidents/" + id + "/flow")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	hop := "/go/incidents/" + id + "/flow?same=1"
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `content="0;url=`+hop+`"`) {
		t.Fatalf("cross-site, no cookie: %d %s", resp.StatusCode, body)
	}
	resp = signedIn(hop)
	u, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusFound || u.Host != "flow.example" || u.Path != "/go/incident" || u.Query().Get("ci") != "db-01" ||
		u.Query().Get("ip") != "10.0.0.11" || u.Query().Get("from") == "" || u.Query().Get("to") == "" {
		t.Fatalf("signed in: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, err = noFollow.Get(f.h.srv.URL + hop)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/incidents?id="+id {
		t.Fatalf("anonymous: %s", loc)
	}

	// The item: its recent traffic; an unknown item or nobody signed in goes to the catalog.
	resp = signedIn("/go/cis/CI-1/flow?same=1")
	if u, _ := url.Parse(resp.Header.Get("Location")); resp.StatusCode != http.StatusFound || u.Host != "flow.example" || u.Query().Get("ip") != "10.0.0.11" {
		t.Fatalf("item: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp = signedIn("/go/cis/CI-9/flow"); resp.Header.Get("Location") != "/cis?id=CI-9" {
		t.Fatalf("unknown item: %s", resp.Header.Get("Location"))
	}
	resp, err = noFollow.Get(f.h.srv.URL + "/go/cis/CI-1/flow?same=1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/cis?id=CI-1" {
		t.Fatalf("anonymous item: %s", loc)
	}
}
