package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/entra/entratest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets/secretstest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store/storetest"
)

// loginFrom signs in through the trusted proxy (127.0.0.1) on behalf of the client ip.
func loginFrom(t *testing.T, h *harness, ip, username, password string) int {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/api/auth/login", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", ip)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestLoginLockoutBehindProxy(t *testing.T) {
	h := newProxyHarness(t, "127.0.0.1")
	h.addLocal("alice", "Alice-pass-2026", model.RoleUser, time.Now())
	h.addLocal("bob", "Bob-pass-2026-x", model.RoleUser, time.Now())

	// One attacker guesses alice's password from one address: locked as before.
	for i := 0; i < 10; i++ {
		if code := loginFrom(t, h, "203.0.113.1", "alice", "wrong"); code != http.StatusUnauthorized {
			t.Fatalf("wrong password %d = %d", i, code)
		}
	}
	if code := loginFrom(t, h, "203.0.113.1", "alice", "Alice-pass-2026"); code != http.StatusTooManyRequests {
		t.Fatalf("alice from the attacking address after 10 failures = %d, want 429", code)
	}
	// The rest of the organisation behind the same proxy is not locked out.
	if code := loginFrom(t, h, "203.0.113.2", "bob", "Bob-pass-2026-x"); code != http.StatusOK {
		t.Fatalf("bob from another address = %d", code)
	}
	if code := loginFrom(t, h, "203.0.113.2", "alice", "Alice-pass-2026"); code != http.StatusOK {
		t.Fatalf("alice from her own address = %d", code)
	}
	if code := loginFrom(t, h, "203.0.113.1", "bob", "Bob-pass-2026-x"); code != http.StatusOK {
		t.Fatalf("bob from the address below the loose limit = %d", code)
	}
	var detail string
	h.st.Read(func(d *store.Data) {
		for _, a := range d.Audit {
			if a.Action == "auth.login" {
				detail = a.Detail
			}
		}
	})
	if detail != "local 203.0.113.1" {
		t.Errorf("audit of the sign-in has the client address, got %q", detail)
	}

	// Password spraying from one address over many accounts is still limited.
	for i := 0; i < 50; i++ {
		loginFrom(t, h, "198.51.100.9", "user"+string(rune('a'+i%26))+string(rune('a'+i/26)), "wrong")
	}
	if code := loginFrom(t, h, "198.51.100.9", "bob", "Bob-pass-2026-x"); code != http.StatusTooManyRequests {
		t.Fatalf("the spraying address = %d, want 429", code)
	}
}

func TestLoginUntrustedForwardedFor(t *testing.T) {
	h := newProxyHarness(t, "")
	h.addLocal("alice", "Alice-pass-2026", model.RoleUser, time.Now())
	// A client that is not a trusted proxy cannot dodge the lock with a new forwarded address.
	for i := 0; i < 10; i++ {
		loginFrom(t, h, "203.0.113."+string(rune('1'+i)), "alice", "wrong")
	}
	if code := loginFrom(t, h, "203.0.113.200", "alice", "Alice-pass-2026"); code != http.StatusTooManyRequests {
		t.Fatalf("spoofed X-Forwarded-For = %d, want 429", code)
	}
}

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
	st.Write(func(d *store.Data) { d.Settings.Password = model.DefaultPasswordPolicy() })
	a := app.New(app.Options{Version: "test", TrustedProxies: app.ParseTrustedProxies("127.0.0.1")},
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
	return &harness{t: t, srv: srv, st: st, bao: bao, vault: vault, app: a}
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

func TestEntraStartRateLimit(t *testing.T) {
	h := newProxyHarness(t, "127.0.0.1")
	entratest.Start(t)
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	admin := h.client()
	admin.login("admin", "Admin-pass-2026")
	if code := admin.call(http.MethodPut, "/api/settings/entra", map[string]any{"config": entraConfig(h), "client_secret": entratest.Secret}, nil); code != 200 {
		t.Fatalf("save = %d", code)
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	start := func(ip string) string {
		req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/api/auth/entra/start", nil)
		req.Header.Set("X-Forwarded-For", ip)
		resp, err := noFollow.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.Header.Get("Location")
	}
	for i := 0; i < 60; i++ {
		if loc := start("203.0.113.1"); loc == "" || loc[0] == '/' {
			t.Fatalf("start %d = %q", i, loc)
		}
	}
	if loc := start("203.0.113.1"); loc != "/?signin_error=too_many_attempts" {
		t.Fatalf("a flood from one address = %q", loc)
	}
	if loc := start("203.0.113.2"); loc == "" || loc[0] == '/' {
		t.Fatalf("another client still starts the sign-in: %q", loc)
	}
}
