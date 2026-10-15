package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// newPGProxyHarness runs the app with PostgreSQL behind a trusted proxy at 127.0.0.1.
func newPGProxyHarness(t *testing.T) *harness {
	cfg := storetest.TempDatabase(t)
	backend, err := store.OpenPostgres(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(backend.Close)
	bao, vault := secretstest.New(t)
	st := store.New()
	fake := testSPA(t, st)
	a := app.New(app.Options{Version: "test", TrustedProxies: app.ParseTrustedProxies("127.0.0.1"), SPAKey: fake.ConnectionKey()},
		app.Deps{Vault: vault, Store: st, Sessions: auth.NewSessions(), Backend: backend})
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
	deadline := time.Now().Add(15 * time.Second)
	for !a.IngestReady() {
		if time.Now().After(deadline) {
			t.Fatal("ingest did not become ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return &harness{t: t, srv: srv, st: st, bao: bao, vault: vault, app: a, spa: fake}
}

func TestIngestNetworksBehindProxy(t *testing.T) {
	h := newPGProxyHarness(t)
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	admin := h.client()
	admin.login("admin", "Admin-pass-2026")
	f := connFixture{h: h, admin: admin}
	cred := f.credential(flow.CredBearer, nil, map[string]string{"token": "tkn-1"})
	var c app.ConnectorView
	f.expect(admin, http.MethodPost, "/api/connectors", map[string]any{"name": "Webhook", "slug": "hook", "preset": "webhook",
		"credentials": map[string]string{"webhook.credential": cred.ID}}, http.StatusCreated, &c)
	var g flow.Graph
	_ = json.Unmarshal(c.Draft.Graph, &g)
	g.Nodes[0].Params["allowed_networks"] = []any{"198.51.100.0/24"}
	f.expect(admin, http.MethodPut, "/api/connectors/"+c.ID+"/draft", map[string]any{"graph": g, "revision": c.Draft.Revision}, http.StatusOK, nil)
	f.expect(admin, http.MethodPost, "/api/connectors/"+c.ID+"/publish", map[string]any{}, http.StatusOK, nil)

	body := `{"title":"T","host":"h"}`
	headers := func(ip string) map[string]string {
		return map[string]string{"Authorization": "Bearer tkn-1", "Content-Type": "application/json", "X-Forwarded-For": ip}
	}
	if p := f.ingest("hook", body, headers("203.0.113.5")); p.status != http.StatusForbidden {
		t.Fatalf("a client outside the networks through the proxy = %d %v", p.status, p.body)
	}
	if p := f.ingest("hook", body, headers("198.51.100.7")); p.status != http.StatusAccepted {
		t.Fatalf("a client inside the networks through the proxy = %d %v", p.status, p.body)
	}
	var reqs []map[string]any
	f.expect(admin, http.MethodGet, "/api/connectors/"+c.ID+"/requests", nil, http.StatusOK, &reqs)
	if len(reqs) == 0 || reqs[0]["remote_ip"] != "198.51.100.7" {
		t.Errorf("the archived request has the client address: %v", reqs)
	}
}
