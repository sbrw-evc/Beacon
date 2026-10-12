package app_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets/secretstest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// newProxyHarness runs the app without PostgreSQL behind the given trusted proxies.
func newProxyHarness(t *testing.T, proxies string) *harness {
	bao, vault := secretstest.New(t)
	st := store.New()
	st.Write(func(d *store.Data) { d.Settings.Password = model.DefaultPasswordPolicy() })
	tp := app.ParseTrustedProxies(proxies)
	if tp == nil {
		tp = app.TrustedProxies{}
	}
	a := app.New(app.Options{Version: "test", TrustedProxies: tp}, app.Deps{Vault: vault, Store: st, Sessions: auth.NewSessions()})
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	return &harness{t: t, srv: srv, st: st, bao: bao, vault: vault, app: a}
}

type fetched struct {
	status int
	header http.Header
	body   string
}

func fetch(t *testing.T, h *harness, path string, headers map[string]string) fetched {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return fetched{resp.StatusCode, resp.Header, string(b)}
}

func wallboardBody(over map[string]any) map[string]any {
	b := map[string]any{"slug": "noc", "title": "NOC", "enabled": true, "show_acknowledged": true, "allowed_networks": []string{"127.0.0.1"}}
	for k, v := range over {
		b[k] = v
	}
	return b
}

func seedCatalog(st *store.Store) {
	st.Write(func(d *store.Data) {
		d.Teams["T-1"] = &model.Team{ID: "T-1", Name: "DBA"}
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "db-01", Status: model.CIStatusActive, IPs: []string{"10.9.0.1"}}
		d.Services["S-1"] = &model.Service{ID: "S-1", Name: "Billing", OwnerTeamID: "T-1", Status: model.ServiceActive, CIIDs: []string{"CI-1"}}
	})
}

func TestWallboardsCRUD(t *testing.T) {
	f := newConnFixture(t, false)
	seedCatalog(f.h.st)
	bad := []struct {
		over map[string]any
		code string
	}{
		{map[string]any{"title": " "}, "title_invalid"},
		{map[string]any{"description": strings.Repeat("я", 2001)}, "description_too_long"},
		{map[string]any{"slug": "-x"}, "slug_invalid"},
		{map[string]any{"slug": "a"}, "slug_invalid"},
		{map[string]any{"slug": "a/b"}, "slug_invalid"},
		{map[string]any{"allowed_networks": []string{}}, "networks_required"},
		{map[string]any{"allowed_networks": []string{" ", ""}}, "networks_required"},
		{map[string]any{"allowed_networks": []string{"10.0.0.0/33"}}, "network_invalid"},
		{map[string]any{"allowed_networks": []string{"host.example"}}, "network_invalid"},
		{map[string]any{"allowed_networks": []string{"fe80::1%eth0"}}, "network_invalid"},
		{map[string]any{"severities": []string{"fatal"}}, "severity_invalid"},
		{map[string]any{"methods": []string{"golden"}}, "method_invalid"},
		{map[string]any{"sort": "random"}, "sort_invalid"},
		{map[string]any{"refresh_seconds": 4}, "refresh_invalid"},
		{map[string]any{"refresh_seconds": 601}, "refresh_invalid"},
		{map[string]any{"theme": "neon"}, "theme_invalid"},
		{map[string]any{"locale": "de"}, "locale_invalid"},
		{map[string]any{"resolved_minutes": 1441}, "resolved_invalid"},
		{map[string]any{"resolved_minutes": -1}, "resolved_invalid"},
		{map[string]any{"ci_ids": []string{"CI-9"}}, "ci_not_found"},
		{map[string]any{"service_ids": []string{"S-9"}}, "service_not_found"},
		{map[string]any{"team_ids": []string{"T-9"}}, "team_not_found"},
	}
	many := make([]string, 201)
	for i := range many {
		many[i] = "10.1." + itoa(i/250) + "." + itoa(i%250)
	}
	bad = append(bad, struct {
		over map[string]any
		code string
	}{map[string]any{"allowed_networks": many}, "too_many_networks"})
	ids := make([]string, 501)
	for i := range ids {
		ids[i] = "CI-" + itoa(i)
	}
	bad = append(bad, struct {
		over map[string]any
		code string
	}{map[string]any{"ci_ids": ids}, "too_many_targets"})
	for _, b := range bad {
		var p problem
		if code := f.admin.call(http.MethodPost, "/api/wallboards", wallboardBody(b.over), &p); code != http.StatusBadRequest || p.Error != b.code {
			t.Errorf("%v: %d %s, want 400 %s", b.over, code, p.Error, b.code)
		}
	}
	var p problem
	if code := f.admin.call(http.MethodPost, "/api/wallboards", map[string]any{"slug": "x1", "unknown": 1}, &p); code != http.StatusBadRequest || p.Error != "bad_request" {
		t.Errorf("unknown fields: %d %+v", code, p)
	}

	var tv app.WallboardView
	f.expect(f.admin, http.MethodPost, "/api/wallboards", wallboardBody(map[string]any{"slug": " NOC-Main ", "title": "  Main NOC ",
		"ci_ids": []string{"CI-1", "CI-1", " "}, "service_ids": []string{"S-1"}, "team_ids": []string{"T-1"}, "severities": []string{"critical", "critical"},
		"allowed_networks": []string{"10.20.0.7/24", "10.20.0.15", "2001:DB8::1/64", "10.20.0.0/24", "::ffff:10.30.0.0/112"}}), http.StatusCreated, &tv)
	if tv.ID != "TV-1" || tv.Slug != "noc-main" || tv.Title != "Main NOC" || tv.Path != "/tv/noc-main" || tv.CreatedBy != "admin" ||
		tv.Sort != "newest" || tv.Theme != "dark" || tv.RefreshSeconds != 30 || tv.Locale != "" ||
		!slices.Equal(tv.CIIDs, []string{"CI-1"}) || !slices.Equal(tv.Severities, []string{"critical"}) || len(tv.Methods) != 0 || tv.Methods == nil ||
		len(tv.CIs) != 1 || tv.CIs[0].Name != "db-01" || len(tv.Services) != 1 || tv.Services[0].Name != "Billing" || len(tv.Teams) != 1 || tv.Teams[0].Name != "DBA" {
		t.Fatalf("created = %+v", tv)
	}
	if want := []string{"10.20.0.0/24", "10.20.0.15", "2001:db8::/64", "10.30.0.0/16"}; !slices.Equal(tv.AllowedNetworks, want) {
		t.Fatalf("networks = %v, want %v", tv.AllowedNetworks, want)
	}
	if !f.h.audited("wallboard.create", tv.ID) {
		t.Error("create is audited")
	}
	if code := f.admin.call(http.MethodPost, "/api/wallboards", wallboardBody(map[string]any{"slug": "noc-MAIN"}), &p); code != http.StatusBadRequest || p.Error != "slug_taken" {
		t.Errorf("slug taken: %d %+v", code, p)
	}
	var other app.WallboardView
	f.expect(f.admin, http.MethodPost, "/api/wallboards", wallboardBody(map[string]any{"slug": "alpha", "title": "Alpha"}), http.StatusCreated, &other)

	in := wallboardBody(map[string]any{"slug": "noc-main", "title": "Main NOC 2", "theme": "light", "sort": "oldest", "locale": "en", "refresh_seconds": 10, "resolved_minutes": 15})
	f.expect(f.admin, http.MethodPut, "/api/wallboards/"+tv.ID, in, http.StatusOK, &tv)
	if tv.Title != "Main NOC 2" || tv.Theme != "light" || tv.Sort != "oldest" || tv.Locale != "en" || tv.RefreshSeconds != 10 || tv.ResolvedMinutes != 15 || len(tv.CIs) != 0 {
		t.Fatalf("updated = %+v", tv)
	}
	in["slug"] = "alpha"
	if code := f.admin.call(http.MethodPut, "/api/wallboards/"+tv.ID, in, &p); code != http.StatusBadRequest || p.Error != "slug_taken" {
		t.Errorf("update to a taken slug: %d %+v", code, p)
	}
	f.expect(f.admin, http.MethodPut, "/api/wallboards/TV-99", wallboardBody(nil), http.StatusNotFound, nil)

	var list struct {
		Wallboards []app.WallboardView `json:"wallboards"`
		ClientIP   string              `json:"client_ip"`
	}
	f.expect(f.admin, http.MethodGet, "/api/wallboards", nil, http.StatusOK, &list)
	if len(list.Wallboards) != 2 || list.Wallboards[0].Title != "Alpha" || list.ClientIP != "127.0.0.1" {
		t.Fatalf("list = %+v", list)
	}
	f.expect(f.admin, http.MethodGet, "/api/wallboards/"+tv.ID, nil, http.StatusOK, &tv)
	f.expect(f.admin, http.MethodGet, "/api/wallboards/TV-99", nil, http.StatusNotFound, nil)

	var targets map[string][]app.TargetRef
	f.expect(f.admin, http.MethodGet, "/api/wallboards/targets?q=db", nil, http.StatusOK, &targets)
	if len(targets["cis"]) != 1 || len(targets["teams"]) != 1 || len(targets["services"]) != 0 {
		t.Fatalf("targets = %+v", targets)
	}
	f.expect(f.admin, http.MethodGet, "/api/wallboards/targets?q=10.9", nil, http.StatusOK, &targets)
	if len(targets["cis"]) != 1 {
		t.Fatalf("targets by IP = %+v", targets)
	}

	// The preview works for disabled wallboards and without an address check.
	in = wallboardBody(map[string]any{"slug": "noc-main", "enabled": false, "allowed_networks": []string{"192.0.2.1"}})
	f.expect(f.admin, http.MethodPut, "/api/wallboards/"+tv.ID, in, http.StatusOK, &tv)
	var preview map[string]any
	f.expect(f.admin, http.MethodGet, "/api/wallboards/"+tv.ID+"/preview", nil, http.StatusOK, &preview)
	if preview["ready"] != false || preview["board"].(map[string]any)["slug"] != "noc-main" {
		t.Fatalf("preview = %v", preview)
	}
	f.expect(f.admin, http.MethodGet, "/api/wallboards/TV-99/preview", nil, http.StatusNotFound, nil)

	// Permissions: view only can list and preview, not change; nobody else sees anything.
	f.h.addRole("tv-viewer", "wallboards:view")
	f.h.addLocal("viewer", "Viewer-pass-2026", "tv-viewer", time.Now())
	viewer := f.h.client()
	viewer.login("viewer", "Viewer-pass-2026")
	f.expect(viewer, http.MethodGet, "/api/wallboards", nil, http.StatusOK, nil)
	f.expect(viewer, http.MethodGet, "/api/wallboards/"+tv.ID, nil, http.StatusOK, nil)
	f.expect(viewer, http.MethodGet, "/api/wallboards/"+tv.ID+"/preview", nil, http.StatusOK, nil)
	f.expect(viewer, http.MethodGet, "/api/wallboards/targets", nil, http.StatusForbidden, nil)
	f.expect(viewer, http.MethodPost, "/api/wallboards", wallboardBody(map[string]any{"slug": "x2"}), http.StatusForbidden, nil)
	f.expect(viewer, http.MethodPut, "/api/wallboards/"+tv.ID, in, http.StatusForbidden, nil)
	f.expect(viewer, http.MethodDelete, "/api/wallboards/"+tv.ID, nil, http.StatusForbidden, nil)
	f.h.addLocal("plain", "Plain-pass-2026x", model.RoleUser, time.Now())
	plain := f.h.client()
	plain.login("plain", "Plain-pass-2026x")
	f.expect(plain, http.MethodGet, "/api/wallboards", nil, http.StatusForbidden, nil)
	f.expect(f.h.client(), http.MethodGet, "/api/wallboards", nil, http.StatusUnauthorized, nil)

	f.expect(f.admin, http.MethodDelete, "/api/wallboards/"+tv.ID, nil, http.StatusNoContent, nil)
	f.expect(f.admin, http.MethodDelete, "/api/wallboards/"+tv.ID, nil, http.StatusNotFound, nil)
	if !f.h.audited("wallboard.update", tv.ID) || !f.h.audited("wallboard.delete", tv.ID) {
		t.Error("update and delete are audited")
	}
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

func TestWallboardPublicAccess(t *testing.T) {
	h := newProxyHarness(t, "127.0.0.1")
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	admin := h.client()
	admin.login("admin", "Admin-pass-2026")
	f := connFixture{h: h, admin: admin}
	f.expect(admin, http.MethodPost, "/api/wallboards", wallboardBody(map[string]any{"slug": "noc", "allowed_networks": []string{"10.20.0.0/24", "2001:db8::/64"}}), http.StatusCreated, nil)
	f.expect(admin, http.MethodPost, "/api/wallboards", wallboardBody(map[string]any{"slug": "off", "enabled": false, "allowed_networks": []string{"10.20.0.0/24"}}), http.StatusCreated, nil)

	from := func(ip string) map[string]string { return map[string]string{"X-Forwarded-For": ip} }
	allowed := fetch(t, h, "/api/public/tv/noc", from("10.20.0.15"))
	if allowed.status != http.StatusOK || allowed.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("allowed = %+v", allowed)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(allowed.body), &payload)
	if payload["ready"] != false || payload["version"] != "test" || payload["default_locale"] != "ru" || len(payload["incidents"].([]any)) != 0 {
		t.Fatalf("payload while not ready = %v", payload)
	}
	// The page takes the alert scale from the payload: five priorities, most severe first.
	var scale struct {
		Severities []model.Severity `json:"severities"`
	}
	_ = json.Unmarshal([]byte(allowed.body), &scale)
	var levels []string
	for _, s := range scale.Severities {
		levels = append(levels, s.Priority+":"+s.Name+":"+s.Tone)
		if s.Title.En == "" || s.Title.Ru == "" || s.Rank == 0 {
			t.Errorf("severity without words or rank: %+v", s)
		}
	}
	if want := []string{"P1:critical:critical", "P2:error:error", "P3:warning:warn", "P4:low:low", "P5:info:info"}; !slices.Equal(levels, want) {
		t.Errorf("TV severities = %v, want %v", levels, want)
	}
	if fetch(t, h, "/api/public/tv/NOC", from("2001:db8::5")).status != http.StatusOK {
		t.Error("IPv6 client inside the network, slug case-insensitive")
	}
	if fetch(t, h, "/api/public/tv/noc", from("::ffff:10.20.0.16")).status != http.StatusOK {
		t.Error("IPv4-mapped client")
	}

	denied := fetch(t, h, "/api/public/tv/noc", from("10.20.1.15"))
	disabled := fetch(t, h, "/api/public/tv/off", from("10.20.1.15"))
	unknown := fetch(t, h, "/api/public/tv/nope", from("10.20.1.15"))
	disabledInside := fetch(t, h, "/api/public/tv/off", from("10.20.0.15"))
	for _, r := range []fetched{denied, disabled, unknown} {
		if r.status != http.StatusForbidden || r.body != denied.body || r.header.Get("Cache-Control") != "no-store" {
			t.Errorf("denials look the same: %+v vs %+v", r, denied)
		}
	}
	if !strings.Contains(denied.body, `"error":"tv_forbidden"`) || !strings.Contains(denied.body, `"client_ip":"10.20.1.15"`) {
		t.Errorf("denied body = %s", denied.body)
	}
	if disabledInside.status != http.StatusForbidden {
		t.Errorf("a disabled board is closed from allowed networks too: %+v", disabledInside)
	}

	page := fetch(t, h, "/tv/noc", from("10.20.0.15"))
	if page.status != http.StatusOK || !strings.HasPrefix(page.header.Get("Content-Type"), "text/html") || page.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("page = %+v", page)
	}
	pd := fetch(t, h, "/tv/noc", from("10.20.1.15"))
	po := fetch(t, h, "/tv/off", from("10.20.1.15"))
	pu := fetch(t, h, "/tv/nope", from("10.20.1.15"))
	for _, r := range []fetched{pd, po, pu} {
		if r.status != http.StatusForbidden || r.body != pd.body || !strings.HasPrefix(r.header.Get("Content-Type"), "text/html") || r.header.Get("Cache-Control") != "no-store" {
			t.Errorf("page denials look the same: %+v", r)
		}
	}
	if !strings.Contains(pd.body, "10.20.1.15") || strings.Contains(pd.body, "<script") || !strings.Contains(pd.body, "Access denied") {
		t.Errorf("denied page = %s", pd.body)
	}
	if x := fetch(t, h, "/tv/nope", from("<b>")); strings.Contains(x.body, "<b>") {
		t.Error("the address is escaped")
	}

	js := fetch(t, h, "/tv/assets/tv.js", nil)
	css := fetch(t, h, "/tv/assets/tv.css", nil)
	if js.status != http.StatusOK || !strings.HasPrefix(js.header.Get("Content-Type"), "text/javascript") || js.header.Get("Cache-Control") != "no-cache" ||
		css.status != http.StatusOK || !strings.HasPrefix(css.header.Get("Content-Type"), "text/css") {
		t.Fatalf("assets = %+v %+v", js.header, css.header)
	}
	for _, p := range []string{"/tv/assets/index.html", "/tv/assets/embed.go", "/tv/assets/..%2fembed.go"} {
		if s := fetch(t, h, p, nil).status; s != http.StatusNotFound {
			t.Errorf("%s = %d", p, s)
		}
	}
}

func TestWallboardUntrustedPeer(t *testing.T) {
	h := newProxyHarness(t, "")
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	admin := h.client()
	admin.login("admin", "Admin-pass-2026")
	f := connFixture{h: h, admin: admin}
	f.expect(admin, http.MethodPost, "/api/wallboards", wallboardBody(map[string]any{"allowed_networks": []string{"10.20.0.0/24"}}), http.StatusCreated, nil)
	r := fetch(t, h, "/api/public/tv/noc", map[string]string{"X-Forwarded-For": "10.20.0.15", "X-Real-IP": "10.20.0.15"})
	if r.status != http.StatusForbidden || !strings.Contains(r.body, `"client_ip":"127.0.0.1"`) {
		t.Fatalf("a spoofed header from an untrusted peer is ignored: %+v", r)
	}
}

func TestWallboardIncidents(t *testing.T) {
	f := newConnFixture(t, true)
	seedCatalog(f.h.st)
	userID := f.h.addLocal("dba", "Dba-pass-2026-x", model.RoleUser, time.Now())
	f.h.st.Write(func(d *store.Data) {
		d.Users[userID].TeamIDs = []string{"T-1"}
		d.Users[userID].Profile.Email = "dba@example.com"
	})
	hook := f.webhookConnector()
	f.ingest("hook", `{"id":"a1","title":"Disk full","host":"db-01","signal":"disk","severity":"critical","status":"firing","labels":{"secret":"label-value"}}`, hook)
	f.ingest("hook", `{"id":"a2","title":"Slow","host":"other-host","signal":"latency","severity":"warning","status":"firing"}`, hook)
	f.expect(f.admin, http.MethodPost, "/api/wallboards", wallboardBody(map[string]any{"team_ids": []string{"T-1"}}), http.StatusCreated, nil)
	var all app.WallboardView
	f.expect(f.admin, http.MethodPost, "/api/wallboards", wallboardBody(map[string]any{"slug": "all", "title": "All"}), http.StatusCreated, &all)

	var payload struct {
		Ready     bool           `json:"ready"`
		Counts    map[string]int `json:"counts"`
		Incidents []map[string]any
		More      bool `json:"more"`
	}
	var raw string
	waitFor(t, "incidents on the wallboard", func() bool {
		r := fetch(t, f.h, "/api/public/tv/all", nil)
		raw = r.body
		_ = json.Unmarshal([]byte(r.body), &payload)
		return r.status == http.StatusOK && payload.Ready && len(payload.Incidents) == 2
	})
	if payload.Counts["total"] != 2 || payload.Counts["critical"] != 1 || payload.Counts["warning"] != 1 || payload.Counts["open"] != 2 ||
		payload.Incidents[0]["severity"] != "critical" || payload.Incidents[0]["team"] != "DBA" || payload.More {
		t.Fatalf("payload = %s", raw)
	}
	if svcs, _ := payload.Incidents[0]["services"].([]any); len(svcs) != 1 || svcs[0] != "Billing" {
		t.Fatalf("services = %v", payload.Incidents[0]["services"])
	}
	for _, leak := range []string{"dba@example.com", "label-value", "umb-", "people", "labels", "\"pd\"", "owners", "dedup_key"} {
		if strings.Contains(raw, leak) {
			t.Errorf("the public payload contains %q: %s", leak, raw)
		}
	}

	r := fetch(t, f.h, "/api/public/tv/noc", nil)
	_ = json.Unmarshal([]byte(r.body), &payload)
	if !payload.Ready || len(payload.Incidents) != 1 || payload.Incidents[0]["ci_name"] != "db-01" || payload.Counts["total"] != 1 {
		t.Fatalf("team scope = %s", r.body)
	}
	var preview map[string]any
	f.expect(f.admin, http.MethodGet, "/api/wallboards/"+all.ID+"/preview", nil, http.StatusOK, &preview)
	if preview["ready"] != true || len(preview["incidents"].([]any)) != 2 {
		t.Fatalf("preview = %v", preview)
	}
}

func TestWallboardRandomAddressAndTimezone(t *testing.T) {
	h := newProxyHarness(t, "127.0.0.1")
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	admin := h.client()
	admin.login("admin", "Admin-pass-2026")
	f := connFixture{h: h, admin: admin}
	h.st.Write(func(d *store.Data) { d.Settings.DefaultTZ = "Europe/Moscow" })
	from := map[string]string{"X-Forwarded-For": "127.0.0.1"}

	var p struct {
		Error string `json:"error"`
	}
	if code := admin.call(http.MethodPost, "/api/wallboards", wallboardBody(map[string]any{"slug": "", "timezone": "Mars/Base"}), &p); code != http.StatusBadRequest || p.Error != "timezone_invalid" {
		t.Errorf("bad time zone: %d %+v", code, p)
	}
	// No address given: a random one nobody can guess.
	var a, b app.WallboardView
	f.expect(admin, http.MethodPost, "/api/wallboards", wallboardBody(map[string]any{"slug": ""}), http.StatusCreated, &a)
	f.expect(admin, http.MethodPost, "/api/wallboards", wallboardBody(map[string]any{"slug": "", "timezone": "Asia/Yekaterinburg"}), http.StatusCreated, &b)
	if len(a.Slug) != 32 || a.Slug == b.Slug || strings.ToLower(a.Slug) != a.Slug {
		t.Fatalf("random addresses = %q %q", a.Slug, b.Slug)
	}
	// Saving without an address keeps the one the wallboard has.
	f.expect(admin, http.MethodPut, "/api/wallboards/"+a.ID, wallboardBody(map[string]any{"slug": "", "title": "Hall"}), http.StatusOK, &a)
	if len(a.Slug) != 32 || a.Title != "Hall" {
		t.Errorf("after update = %+v", a)
	}

	// The screen gets the wallboard's time zone, or the installation's.
	zone := func(slug string) string {
		var out struct {
			Board struct {
				Timezone string `json:"timezone"`
			} `json:"board"`
		}
		r := fetch(t, h, "/api/public/tv/"+slug, from)
		_ = json.Unmarshal([]byte(r.body), &out)
		return out.Board.Timezone
	}
	if za, zb := zone(a.Slug), zone(b.Slug); za != "Europe/Moscow" || zb != "Asia/Yekaterinburg" {
		t.Errorf("time zones = %q %q", za, zb)
	}

	// A new address cuts off screens on the old one.
	old := a.Slug
	f.expect(admin, http.MethodPost, "/api/wallboards/"+a.ID+"/rotate", nil, http.StatusOK, &a)
	if a.Slug == old || len(a.Slug) != 32 {
		t.Fatalf("rotated = %q", a.Slug)
	}
	if fetch(t, h, "/api/public/tv/"+old, from).status != http.StatusForbidden || fetch(t, h, "/api/public/tv/"+a.Slug, from).status != http.StatusOK {
		t.Error("the old address must stop working and the new one work")
	}
	f.expect(admin, http.MethodPost, "/api/wallboards/TV-404/rotate", nil, http.StatusNotFound, nil)
}

func TestWallboardProxiesFromInterface(t *testing.T) {
	h := newProxyHarness(t, "192.0.2.10")
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	admin := h.client()
	admin.login("admin", "Admin-pass-2026")
	f := connFixture{h: h, admin: admin}
	f.expect(admin, http.MethodPost, "/api/wallboards", wallboardBody(map[string]any{"allowed_networks": []string{"10.20.0.0/24"}}), http.StatusCreated, nil)
	via := map[string]string{"X-Forwarded-For": "10.20.0.15"}
	if fetch(t, h, "/api/public/tv/noc", via).status != http.StatusForbidden {
		t.Fatal("the test client is not a trusted proxy yet")
	}

	var p struct {
		Error string `json:"error"`
	}
	if code := admin.call(http.MethodPut, "/api/wallboards/proxies", map[string]any{"trusted_proxies": []string{"10.0.0.0/33"}}, &p); code != http.StatusBadRequest || p.Error != "proxy_invalid" {
		t.Errorf("bad proxy: %d %+v", code, p)
	}
	var view struct {
		Set      []string `json:"trusted_proxies"`
		Env      []string `json:"env"`
		ClientIP string   `json:"client_ip"`
	}
	f.expect(admin, http.MethodPut, "/api/wallboards/proxies", map[string]any{"trusted_proxies": []string{" 127.0.0.1 ", "10.1.2.3/16", "127.0.0.1"}}, http.StatusOK, &view)
	if !slices.Equal(view.Set, []string{"127.0.0.1", "10.1.0.0/16"}) || !slices.Equal(view.Env, []string{"192.0.2.10/32"}) {
		t.Errorf("proxies = %+v", view)
	}
	if fetch(t, h, "/api/public/tv/noc", via).status != http.StatusOK {
		t.Error("behind a proxy set in the interface the forwarded address is checked")
	}
	if fetch(t, h, "/api/public/tv/noc", map[string]string{"X-Forwarded-For": "10.30.0.1"}).status != http.StatusForbidden {
		t.Error("a forwarded address outside the networks is refused")
	}

	// Changing proxies needs its own permission: wallboard editors cannot.
	h.st.Write(func(d *store.Data) {
		d.Roles["R-tv"] = &model.Role{ID: "R-tv", Name: "TV", Permissions: []string{"wallboards:view", "wallboards:edit"}}
	})
	h.addLocal("tv", "Tv-editor-pass-2026", "R-tv", time.Now())
	editor := h.client()
	editor.login("tv", "Tv-editor-pass-2026")
	f.expect(editor, http.MethodGet, "/api/wallboards/proxies", nil, http.StatusOK, nil)
	f.expect(editor, http.MethodPut, "/api/wallboards/proxies", map[string]any{"trusted_proxies": []string{}}, http.StatusForbidden, nil)
}
