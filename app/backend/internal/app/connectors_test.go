package app_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets/secretstest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store/storetest"
)

type connFixture struct {
	h     *harness
	admin *client
}

func newConnFixture(t *testing.T, pg bool) connFixture {
	t.Helper()
	var h *harness
	if pg {
		h = newPGHarness(t)
	} else {
		h = newHarness(t)
	}
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	admin := h.client()
	admin.login("admin", "Admin-pass-2026")
	return connFixture{h: h, admin: admin}
}

// newPGHarness runs the app with PostgreSQL, so the intake and the workers are live.
func newPGHarness(t *testing.T) *harness {
	cfg := storetest.TempDatabase(t)
	backend, err := store.OpenPostgres(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(backend.Close)
	bao, vault := secretstest.New(t)
	st := store.New()
	st.Write(func(d *store.Data) { d.Settings.Password = model.DefaultPasswordPolicy() })
	a := app.New(app.Options{Version: "test"}, app.Deps{Vault: vault, Store: st, Sessions: auth.NewSessions(), Backend: backend})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		a.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	h := &harness{t: t, srv: srv, st: st, bao: bao, vault: vault, app: a}
	deadline := time.Now().Add(15 * time.Second)
	for !a.IngestReady() {
		if time.Now().After(deadline) {
			t.Fatal("ingest did not become ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return h
}

func (f connFixture) expect(c *client, method, path string, body any, status int, out any) {
	f.h.t.Helper()
	var raw json.RawMessage
	got := c.call(method, path, body, &raw)
	if got != status {
		f.h.t.Fatalf("%s %s = %d %s, want %d", method, path, got, raw, status)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			f.h.t.Fatalf("%s %s: %v", method, path, err)
		}
	}
}

func (f connFixture) credential(typ string, fields, secretValues map[string]string) app.CredentialView {
	var out app.CredentialView
	f.expect(f.admin, http.MethodPost, "/api/credentials", app.CredentialInput{Name: "Cred " + typ, Type: typ, Fields: fields, Secrets: secretValues}, http.StatusCreated, &out)
	return out
}

type post struct {
	status int
	body   map[string]any
	header http.Header
}

func (f connFixture) ingest(slug, body string, headers map[string]string) post {
	f.h.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, f.h.srv.URL+"/api/ingest/"+slug, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.h.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := post{status: resp.StatusCode, header: resp.Header}
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &out.body)
	return out
}

func TestCredentialsWriteOnly(t *testing.T) {
	f := newConnFixture(t, false)
	c := f.credential(flow.CredBasic, map[string]string{"username": "alertmanager"}, map[string]string{"password": "s3cret-value"})
	if c.Fields["username"] != "alertmanager" || len(c.SecretsSet) != 1 || c.Version != 1 {
		t.Fatalf("credential = %+v", c)
	}
	raw, _ := json.Marshal(c)
	if bytes.Contains(raw, []byte("s3cret-value")) || bytes.Contains(raw, []byte("openbao://")) {
		t.Fatalf("the secret or its reference leaks: %s", raw)
	}
	if got := f.h.bao.Get("umbrella/credentials/" + c.ID); got == nil {
		t.Errorf("the secret is in OpenBao, paths = %v", f.h.bao.Paths())
	}
	var kinds []app.CredentialKind
	f.expect(f.admin, http.MethodGet, "/api/credentials/kinds", nil, http.StatusOK, &kinds)
	if len(kinds) != 4 || kinds[0].Type != flow.CredBearer || kinds[1].Fields[0] != "username" || kinds[1].Secrets[0] != "password" {
		t.Errorf("credential kinds = %+v", kinds)
	}
	var problem map[string]any
	if code := f.admin.call(http.MethodPost, "/api/credentials", app.CredentialInput{Name: "x", Type: flow.CredBasic, Fields: map[string]string{"username": "u"}}, &problem); code != http.StatusBadRequest || problem["error"] != "invalid_credential_secret" {
		t.Errorf("a new credential needs its secret: %d %v", code, problem)
	}
	var updated app.CredentialView
	f.expect(f.admin, http.MethodPut, "/api/credentials/"+c.ID, app.CredentialInput{Name: "Renamed", Fields: map[string]string{"username": "am"}}, http.StatusOK, &updated)
	if updated.Version != 1 || updated.Name != "Renamed" {
		t.Errorf("renaming keeps the secret version: %+v", updated)
	}
	f.expect(f.admin, http.MethodPut, "/api/credentials/"+c.ID, app.CredentialInput{Name: "Renamed", Fields: map[string]string{"username": "am"}, Secrets: map[string]string{"password": "new"}}, http.StatusOK, &updated)
	if updated.Version != 2 {
		t.Errorf("replacing the secret bumps the version: %+v", updated)
	}
	if code := f.admin.call(http.MethodPut, "/api/credentials/"+c.ID, app.CredentialInput{Name: "x", Type: flow.CredBearer}, &problem); code != http.StatusBadRequest {
		t.Errorf("the type is fixed: %d", code)
	}
}

func TestConnectorLifecycle(t *testing.T) {
	f := newConnFixture(t, false)
	cred := f.credential(flow.CredBearer, nil, map[string]string{"token": "tkn"})

	var presets []map[string]any
	f.expect(f.admin, http.MethodGet, "/api/connectors/presets", nil, http.StatusOK, &presets)
	if len(presets) != 6 {
		t.Fatalf("presets = %v", presets)
	}
	for _, p := range presets {
		if q, _ := p["quick"].(map[string]any); q == nil || q["steps"] == nil || q["name"] == nil {
			t.Errorf("the web app builds quick connect from the presets: %v", p)
		}
	}
	var c app.ConnectorView
	f.expect(f.admin, http.MethodPost, "/api/connectors", map[string]any{"name": "Prod Alertmanager", "slug": "am-prod", "preset": "alertmanager",
		"credentials": map[string]string{"webhook.credential": cred.ID}}, http.StatusCreated, &c)
	if c.Status != app.StatusDraft || c.Samples != 2 || c.IngestPath != "/api/ingest/am-prod" || flow.HasErrors(c.Issues) {
		t.Fatalf("created = %+v", c)
	}
	var problem map[string]any
	if code := f.admin.call(http.MethodPost, "/api/connectors", map[string]any{"name": "Other", "slug": "am-prod"}, &problem); code != http.StatusConflict || problem["error"] != "connector_slug_taken" {
		t.Errorf("slugs are unique: %d %v", code, problem)
	}
	if code := f.admin.call(http.MethodPost, "/api/connectors", map[string]any{"name": "Bad", "slug": "Bad Slug"}, &problem); code != http.StatusBadRequest {
		t.Errorf("slug format: %d", code)
	}

	var samples []app.SampleView
	f.expect(f.admin, http.MethodGet, "/api/connectors/"+c.ID+"/samples", nil, http.StatusOK, &samples)
	var run app.TestRunResult
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/test-run", map[string]any{"sample_id": samples[len(samples)-1].ID}, http.StatusOK, &run)
	if len(run.Events) != 2 || run.Events[0].Severity != "critical" || run.Events[0].Labels["cluster"] != "prod-1" || run.Result.Trace["parse"] == nil {
		t.Fatalf("test run = %+v", run)
	}
	run = app.TestRunResult{}
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/test-run", map[string]any{"body": `{"alerts":[{"labels":{"alertname":"X","instance":"h"}}]}`, "stop_at": "severity"}, http.StatusOK, &run)
	if len(run.Events) != 0 || run.Result.Trace["map"] != nil || run.Result.Trace["severity"].Out["main"] != 1 {
		t.Errorf("run to a node = %+v", run.Result)
	}
	var all app.TestAllResult
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/test-all", map[string]any{}, http.StatusOK, &all)
	if len(all.Samples) != 2 || len(all.Samples[0].Events) == 0 || len(all.Samples[1].Events) == 0 {
		t.Errorf("test all = %+v", all)
	}

	var published app.ConnectorView
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/publish", map[string]any{"name": "first"}, http.StatusOK, &published)
	if published.Status != app.StatusPublished || published.Published != 1 || len(published.Versions) != 1 {
		t.Fatalf("published = %+v", published)
	}

	// Moving a node is not a change; changing a parameter is.
	var g flow.Graph
	_ = json.Unmarshal(published.Draft.Graph, &g)
	g.Nodes[0].Position.X += 50
	var saved app.DraftSaved
	f.expect(f.admin, http.MethodPut, "/api/connectors/"+c.ID+"/draft", map[string]any{"graph": g, "revision": published.Draft.Revision}, http.StatusOK, &saved)
	if saved.Status != app.StatusPublished {
		t.Errorf("moving a node: status = %s", saved.Status)
	}
	for i := range g.Nodes {
		if g.Nodes[i].ID == "map" {
			g.Nodes[i].Params["title"] = "[AM] ${labels.alertname}"
		}
	}
	f.expect(f.admin, http.MethodPut, "/api/connectors/"+c.ID+"/draft", map[string]any{"graph": g, "revision": saved.Revision}, http.StatusOK, &saved)
	if saved.Status != app.StatusChanged {
		t.Errorf("changing a parameter: status = %s", saved.Status)
	}
	if code := f.admin.call(http.MethodPut, "/api/connectors/"+c.ID+"/draft", map[string]any{"graph": g, "revision": saved.Revision - 1}, &problem); code != http.StatusConflict || problem["error"] != "draft_conflict" {
		t.Errorf("a stale revision is refused: %d %v", code, problem)
	}

	// A broken draft cannot be published, and the reason is kept.
	g.Edges = g.Edges[:2]
	f.expect(f.admin, http.MethodPut, "/api/connectors/"+c.ID+"/draft", map[string]any{"graph": g, "revision": saved.Revision}, http.StatusOK, &saved)
	if code := f.admin.call(http.MethodPost, "/api/connectors/"+c.ID+"/publish", map[string]any{}, &problem); code != http.StatusBadRequest || problem["error"] != "publish_failed" || problem["issues"] == nil {
		t.Fatalf("publish broken = %d %v", code, problem)
	}
	var got app.ConnectorView
	f.expect(f.admin, http.MethodGet, "/api/connectors/"+c.ID, nil, http.StatusOK, &got)
	if got.Status != app.StatusPublishFailed || got.Published != 1 || got.PublishError == "" {
		t.Errorf("after failed publish = %s %d %q", got.Status, got.Published, got.PublishError)
	}

	// Restoring version 1 and publishing it is a rollback.
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/versions/1/restore", nil, http.StatusOK, &got)
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/publish", map[string]any{"comment": "rollback"}, http.StatusOK, &got)
	if got.Published != 2 || got.Status != app.StatusPublished {
		t.Errorf("rollback = %d %s", got.Published, got.Status)
	}
	var v1 app.VersionView
	f.expect(f.admin, http.MethodGet, "/api/connectors/"+c.ID+"/versions/1", nil, http.StatusOK, &v1)
	if v1.Name != "first" || len(v1.Graph) == 0 {
		t.Errorf("version 1 = %+v", v1)
	}

	// The credential is in use, so it cannot be deleted.
	if code := f.admin.call(http.MethodDelete, "/api/credentials/"+cred.ID, nil, &problem); code != http.StatusConflict || problem["error"] != "credential_in_use" {
		t.Errorf("delete used credential = %d %v", code, problem)
	}

	// Without PostgreSQL the intake refuses published connectors with 503.
	if p := f.ingest("am-prod", "{}", map[string]string{"Authorization": "Bearer tkn"}); p.status != http.StatusServiceUnavailable {
		t.Errorf("ingest without the queue = %d", p.status)
	}

	// Export never carries credentials; import maps the slot again.
	var doc flow.Document
	f.expect(f.admin, http.MethodGet, "/api/connectors/"+c.ID+"/export?samples=true", nil, http.StatusOK, &doc)
	raw, _ := json.Marshal(doc)
	if bytes.Contains(raw, []byte(cred.ID)) || len(doc.Credentials) != 1 || doc.Credentials[0].Name != cred.Name || len(doc.Samples) != 2 {
		t.Fatalf("export = %s", raw)
	}
	var check map[string]any
	f.expect(f.admin, http.MethodPost, "/api/connectors/import/check", map[string]any{"document": doc}, http.StatusOK, &check)
	if check["credentials"] == nil || check["nodes"].(float64) != 6 {
		t.Errorf("import check = %v", check)
	}
	var imported app.ConnectorView
	f.expect(f.admin, http.MethodPost, "/api/connectors/import", map[string]any{"document": doc, "slug": "am-copy",
		"credentials": map[string]string{"webhook.credential": cred.ID}}, http.StatusCreated, &imported)
	if flow.HasErrors(imported.Issues) || imported.Samples != 2 || imported.Name != "Prod Alertmanager" {
		t.Errorf("imported = %+v", imported)
	}
	if code := f.admin.call(http.MethodPost, "/api/connectors/import", map[string]any{"document": doc, "slug": "am-x",
		"credentials": map[string]string{"webhook.credential": "CRD-404"}}, &problem); code != http.StatusBadRequest {
		t.Errorf("unknown credential in mapping = %d", code)
	}
	doc.Graph.Nodes[0].TypeVersion = 99
	if code := f.admin.call(http.MethodPost, "/api/connectors/import", map[string]any{"document": doc, "slug": "am-y"}, &problem); code != http.StatusBadRequest || problem["error"] != "import_invalid" {
		t.Errorf("unknown node version = %d %v", code, problem)
	}

	var preview map[string]any
	f.expect(f.admin, http.MethodPost, "/api/connectors/preview", map[string]any{"kind": "template", "expr": "${a|upper}", "data": map[string]any{"a": "x"}}, http.StatusOK, &preview)
	if preview["value"] != "X" {
		t.Errorf("template preview = %v", preview)
	}
	f.expect(f.admin, http.MethodPost, "/api/connectors/preview", map[string]any{"kind": "cel", "expr": "event.n > 1", "data": map[string]any{"n": 2}}, http.StatusOK, &preview)
	if preview["value"] != true {
		t.Errorf("cel preview = %v", preview)
	}
	f.expect(f.admin, http.MethodPost, "/api/connectors/preview", map[string]any{"kind": "cel", "expr": "event.n +", "data": map[string]any{}}, http.StatusOK, &preview)
	if preview["error"] == nil {
		t.Errorf("cel preview error = %v", preview)
	}

	f.expect(f.admin, http.MethodDelete, "/api/connectors/"+c.ID, nil, http.StatusNoContent, nil)
	f.expect(f.admin, http.MethodDelete, "/api/connectors/"+imported.ID, nil, http.StatusNoContent, nil)
	f.expect(f.admin, http.MethodDelete, "/api/credentials/"+cred.ID, nil, http.StatusNoContent, nil)
}

func TestConnectorLockAndPermissions(t *testing.T) {
	f := newConnFixture(t, false)
	f.h.st.Write(func(d *store.Data) {
		d.Roles["editor"] = &model.Role{ID: "editor", Name: "Editor", Permissions: []string{"connectors:view", "connectors:edit"}}
		d.Roles["viewer"] = &model.Role{ID: "viewer", Name: "Viewer", Permissions: []string{"connectors:view"}}
	})
	f.h.addLocal("ed", "Editor-pass-2026", "editor", time.Now())
	f.h.addLocal("vi", "Viewer-pass-2026", "viewer", time.Now())
	ed, vi := f.h.client(), f.h.client()
	ed.login("ed", "Editor-pass-2026")
	vi.login("vi", "Viewer-pass-2026")

	var c app.ConnectorView
	f.expect(f.admin, http.MethodPost, "/api/connectors", map[string]any{"name": "Mine", "slug": "mine"}, http.StatusCreated, &c)
	var lock app.LockView
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/lock", nil, http.StatusOK, &lock)
	if !lock.Mine || lock.Username != "admin" {
		t.Errorf("lock = %+v", lock)
	}
	var problem map[string]any
	if code := ed.call(http.MethodPost, "/api/connectors/"+c.ID+"/lock", nil, &problem); code != http.StatusConflict || problem["error"] != "locked" {
		t.Errorf("a second editor is refused: %d %v", code, problem)
	}
	if code := ed.call(http.MethodPut, "/api/connectors/"+c.ID+"/draft", map[string]any{"graph": app.StarterGraph(), "revision": c.Draft.Revision}, &problem); code != http.StatusConflict {
		t.Errorf("saving under someone else's lock = %d", code)
	}
	f.expect(f.admin, http.MethodDelete, "/api/connectors/"+c.ID+"/lock", nil, http.StatusNoContent, nil)
	f.expect(ed, http.MethodPut, "/api/connectors/"+c.ID+"/draft", map[string]any{"graph": app.StarterGraph(), "revision": c.Draft.Revision}, http.StatusOK, nil)

	if code := ed.call(http.MethodPost, "/api/connectors/"+c.ID+"/publish", map[string]any{}, nil); code != http.StatusForbidden {
		t.Errorf("publishing needs connectors:publish, got %d", code)
	}
	if code := ed.call(http.MethodGet, "/api/connectors/"+c.ID+"/samples", nil, nil); code != http.StatusForbidden {
		t.Errorf("samples need connectors:payload, got %d", code)
	}
	if code := vi.call(http.MethodPut, "/api/connectors/"+c.ID, map[string]any{"name": "x", "slug": "x1"}, nil); code != http.StatusForbidden {
		t.Errorf("viewers cannot edit, got %d", code)
	}
	f.expect(vi, http.MethodGet, "/api/connectors/"+c.ID, nil, http.StatusOK, nil)
	var choices []map[string]any
	f.expect(vi, http.MethodGet, "/api/connectors/credentials", nil, http.StatusOK, &choices)
	if code := vi.call(http.MethodGet, "/api/credentials", nil, nil); code != http.StatusForbidden {
		t.Errorf("credentials page needs credentials:view, got %d", code)
	}
}

// Capture works before the connector is published, even without PostgreSQL.
func TestCaptureUnpublished(t *testing.T) {
	f := newConnFixture(t, false)
	var c app.ConnectorView
	f.expect(f.admin, http.MethodPost, "/api/connectors", map[string]any{"name": "Probe", "slug": "probe"}, http.StatusCreated, &c)
	if p := f.ingest("probe", "{}", nil); p.status != http.StatusNotFound {
		t.Errorf("an unpublished connector without capture is unknown: %d", p.status)
	}
	var g flow.Graph
	_ = json.Unmarshal(c.Draft.Graph, &g)
	g.Nodes[0].Params["anonymous"] = true
	f.expect(f.admin, http.MethodPut, "/api/connectors/"+c.ID+"/draft", map[string]any{"graph": g, "revision": c.Draft.Revision}, http.StatusOK, nil)
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/capture", map[string]any{"count": 1}, http.StatusOK, nil)
	p := f.ingest("probe", `{"title":"t","host":"h"}`, map[string]string{"X-Source": "probe", "Authorization": "Bearer secret", "Content-Type": "application/json"})
	if p.status != http.StatusAccepted || p.body["status"] != "captured" {
		t.Fatalf("capture = %d %v", p.status, p.body)
	}
	if p := f.ingest("probe", "{}", nil); p.status != http.StatusNotFound {
		t.Errorf("capture stops after the requested count: %d", p.status)
	}
	var samples []app.SampleView
	f.expect(f.admin, http.MethodGet, "/api/connectors/"+c.ID+"/samples", nil, http.StatusOK, &samples)
	if len(samples) != 1 || samples[0].Source != "capture" {
		t.Fatalf("samples = %+v", samples)
	}
	var full app.SampleView
	f.expect(f.admin, http.MethodGet, "/api/connectors/"+c.ID+"/samples/"+samples[0].ID, nil, http.StatusOK, &full)
	if *full.Body != `{"title":"t","host":"h"}` || full.Headers["x-source"] != "probe" || full.Headers["authorization"] != "" {
		t.Errorf("captured sample = %+v", full)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestIngestEndToEnd(t *testing.T) {
	f := newConnFixture(t, true)
	cred := f.credential(flow.CredBearer, nil, map[string]string{"token": "tkn-1"})
	var c app.ConnectorView
	f.expect(f.admin, http.MethodPost, "/api/connectors", map[string]any{"name": "Webhook", "slug": "hook", "preset": "webhook",
		"credentials": map[string]string{"webhook.credential": cred.ID}}, http.StatusCreated, &c)
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/publish", map[string]any{}, http.StatusOK, nil)

	auth := map[string]string{"Authorization": "Bearer tkn-1", "Content-Type": "application/json"}
	if p := f.ingest("hook", `{}`, map[string]string{"Authorization": "Bearer wrong"}); p.status != http.StatusUnauthorized {
		t.Errorf("wrong token = %d", p.status)
	}
	if p := f.ingest("hook", `{}`, nil); p.status != http.StatusUnauthorized {
		t.Errorf("no token = %d", p.status)
	}
	body := `[{"id":"a1","title":"Disk full","host":"db-01","severity":"critical"},{"id":"a2","title":"Bad","host":"db-02","severity":"nonsense"}]`
	p := f.ingest("hook", body, auth)
	if p.status != http.StatusAccepted || p.body["status"] != "queued" {
		t.Fatalf("ingest = %d %v", p.status, p.body)
	}
	keyed := map[string]string{"Authorization": "Bearer tkn-1", "Idempotency-Key": "d-1"}
	if p := f.ingest("hook", `{"id":"a3","title":"T","host":"h"}`, keyed); p.body["status"] != "queued" {
		t.Errorf("first keyed = %v", p.body)
	}
	if p := f.ingest("hook", `{"id":"a3","title":"T","host":"h"}`, keyed); p.body["status"] != "duplicate" {
		t.Errorf("repeated keyed = %v", p.body)
	}

	var events []map[string]any
	waitFor(t, "events", func() bool {
		f.admin.call(http.MethodGet, "/api/connectors/"+c.ID+"/events", nil, &events)
		return len(events) == 2
	})
	var failures []map[string]any
	f.expect(f.admin, http.MethodGet, "/api/connectors/"+c.ID+"/failures", nil, http.StatusOK, &failures)
	if len(failures) != 1 || failures[0]["node"] != "map" || !strings.Contains(failures[0]["error"].(string), "nonsense") {
		t.Fatalf("failures = %v", failures)
	}
	var reqs []map[string]any
	f.expect(f.admin, http.MethodGet, "/api/connectors/"+c.ID+"/requests", nil, http.StatusOK, &reqs)
	if len(reqs) != 2 {
		t.Fatalf("requests = %v", reqs)
	}
	var req map[string]any
	f.expect(f.admin, http.MethodGet, fmt.Sprintf("/api/connectors/%s/requests/%v", c.ID, failures[0]["request_id"]), nil, http.StatusOK, &req)
	if req["body"] != body || req["headers"].(map[string]any)["authorization"] != nil {
		t.Errorf("stored request = %v", req)
	}

	// Fix the mapping, publish, reprocess the failure on the new version.
	var got app.ConnectorView
	f.expect(f.admin, http.MethodGet, "/api/connectors/"+c.ID, nil, http.StatusOK, &got)
	var g flow.Graph
	_ = json.Unmarshal(got.Draft.Graph, &g)
	for i := range g.Nodes {
		if g.Nodes[i].ID == "map" {
			g.Nodes[i].Params["severity"] = "${severity|warning}"
			g.Nodes[i].OnError = ""
		}
	}
	sevIdx := len(g.Nodes)
	g.Nodes = append(g.Nodes, flow.Node{ID: "sev", Type: "map.severity", TypeVersion: 1, Params: map[string]any{"source": "${severity}", "default": "warning"}})
	for i, e := range g.Edges {
		if e.Target == "map" {
			g.Edges[i].Target = "sev"
		}
	}
	g.Edges = append(g.Edges, flow.Edge{ID: "s1", Source: g.Nodes[sevIdx].ID, SourceOutput: "main", Target: "map"})
	f.expect(f.admin, http.MethodPut, "/api/connectors/"+c.ID+"/draft", map[string]any{"graph": g, "revision": got.Draft.Revision}, http.StatusOK, nil)
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/publish", map[string]any{}, http.StatusOK, nil)
	var rep map[string]int
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/failures/reprocess", map[string]any{}, http.StatusOK, &rep)
	if rep["requeued"] != 1 {
		t.Fatalf("reprocess = %v", rep)
	}
	waitFor(t, "the reprocessed event", func() bool {
		f.admin.call(http.MethodGet, "/api/connectors/"+c.ID+"/events", nil, &events)
		return len(events) == 3
	})
	f.expect(f.admin, http.MethodGet, "/api/connectors/"+c.ID+"/failures", nil, http.StatusOK, &failures)
	if len(failures) != 0 {
		t.Errorf("open failures after the fix = %v", failures)
	}
	var stats map[string]any
	f.expect(f.admin, http.MethodGet, "/api/connectors/"+c.ID+"/stats?hours=1", nil, http.StatusOK, &stats)
	if b := stats["buckets"].([]any); len(b) == 0 {
		t.Errorf("stats = %v", stats)
	}
	var list map[string]any
	f.expect(f.admin, http.MethodGet, "/api/connectors", nil, http.StatusOK, &list)
	if list["ingest"] != true || list["connectors"].([]any)[0].(map[string]any)["stats"] == nil {
		t.Errorf("list = %v", list)
	}
}

func TestIngestChecks(t *testing.T) {
	f := newConnFixture(t, true)
	cred := f.credential(flow.CredHMAC, nil, map[string]string{"secret": "shh"})
	var c app.ConnectorView
	f.expect(f.admin, http.MethodPost, "/api/connectors", map[string]any{"name": "Signed", "slug": "signed", "preset": "webhook",
		"credentials": map[string]string{"webhook.credential": cred.ID}}, http.StatusCreated, &c)
	var g flow.Graph
	_ = json.Unmarshal(c.Draft.Graph, &g)
	g.Nodes[0].Params["rate_limit"] = 2.0
	g.Nodes[0].Params["max_body_kb"] = 1.0
	g.Nodes[0].Params["accept_if"] = `request.query.?env.orValue("") != "test"`
	g.Nodes = append(g.Nodes, flow.Node{ID: "ack", Type: "ack.response", TypeVersion: 1, Params: map[string]any{"status": 200.0, "body": `{"ok":true}`}})
	f.expect(f.admin, http.MethodPut, "/api/connectors/"+c.ID+"/draft", map[string]any{"graph": g, "revision": c.Draft.Revision}, http.StatusOK, nil)
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/publish", map[string]any{}, http.StatusOK, nil)

	body := `{"title":"T","host":"h"}`
	mac := hmac.New(sha256.New, []byte("shh"))
	mac.Write([]byte(body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	p := f.ingest("signed", body, map[string]string{"X-Signature": sig})
	if p.status != http.StatusOK || p.body["ok"] != true {
		t.Fatalf("signed request with ack = %d %v", p.status, p.body)
	}
	time.Sleep(600 * time.Millisecond)
	if p := f.ingest("signed", body+" ", map[string]string{"X-Signature": sig}); p.status != http.StatusUnauthorized {
		t.Errorf("a changed body breaks the signature: %d", p.status)
	}
	time.Sleep(600 * time.Millisecond)
	big := `{"pad":"` + strings.Repeat("x", 2000) + `"}`
	if p := f.ingest("signed", big, nil); p.status != http.StatusRequestEntityTooLarge {
		t.Errorf("body limit = %d", p.status)
	}
	time.Sleep(600 * time.Millisecond)
	req, _ := http.NewRequest(http.MethodPost, f.h.srv.URL+"/api/ingest/signed?env=test", strings.NewReader(body))
	req.Header.Set("X-Signature", sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || !strings.Contains(string(b), "ignored") {
		t.Errorf("accept_if = %d %s", resp.StatusCode, b)
	}
	limited := false
	for i := 0; i < 5; i++ {
		if p := f.ingest("signed", body, map[string]string{"X-Signature": sig}); p.status == http.StatusTooManyRequests {
			limited = p.header.Get("Retry-After") != ""
			break
		}
	}
	if !limited {
		t.Error("the rate limit answers 429 with Retry-After")
	}
	var reqs []map[string]any
	f.expect(f.admin, http.MethodGet, "/api/connectors/"+c.ID+"/requests", nil, http.StatusOK, &reqs)
	for _, r := range reqs {
		if r["headers"].(map[string]any)["x-signature"] != nil {
			t.Errorf("the signature header is not archived: %v", r["headers"])
		}
	}

	g.Nodes[0].Params["allowed_networks"] = []any{"10.0.0.0/8"}
	var got app.ConnectorView
	f.expect(f.admin, http.MethodGet, "/api/connectors/"+c.ID, nil, http.StatusOK, &got)
	f.expect(f.admin, http.MethodPut, "/api/connectors/"+c.ID+"/draft", map[string]any{"graph": g, "revision": got.Draft.Revision}, http.StatusOK, nil)
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/publish", map[string]any{}, http.StatusOK, nil)
	if p := f.ingest("signed", body, map[string]string{"X-Signature": sig}); p.status != http.StatusForbidden {
		t.Errorf("networks = %d", p.status)
	}
}
