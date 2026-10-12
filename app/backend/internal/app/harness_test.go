package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/credentials"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets/secretstest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func TestMain(m *testing.M) {
	auth.Iterations = 1000
	os.Exit(m.Run())
}

type harness struct {
	t     *testing.T
	srv   *httptest.Server
	st    *store.Store
	bao   *secretstest.Fake
	vault *secrets.Client
	app   *app.App
}

func newHarness(t *testing.T) *harness {
	return newHarnessWith(t, store.New())
}

// newHarnessWith serves the app over a given store, e.g. one loaded from a snapshot.
func newHarnessWith(t *testing.T, st *store.Store) *harness {
	bao, vault := secretstest.New(t)
	st.Write(func(d *store.Data) { d.Settings.Password = model.DefaultPasswordPolicy() })
	a := app.New(app.Options{Version: "test"}, app.Deps{Vault: vault, Store: st, Sessions: auth.NewSessions()})
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	return &harness{t: t, srv: srv, st: st, bao: bao, vault: vault}
}

func (h *harness) addLocal(username, password, role string, changed time.Time) string {
	h.t.Helper()
	var id string
	h.st.Write(func(d *store.Data) {
		id = d.NextID("USR")
		d.Users[id] = &model.User{ID: id, Username: username, Name: username, Source: model.SourceLocal, Role: role, CreatedAt: changed}
	})
	ref, err := credentials.NewPasswords(h.vault).Set(context.Background(), id, password)
	if err != nil {
		h.t.Fatal(err)
	}
	h.st.Write(func(d *store.Data) { d.Users[id].PasswordRef, d.Users[id].PasswordChangedAt = ref, changed })
	return id
}

type client struct {
	h    *harness
	http *http.Client
	csrf string
}

func (h *harness) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{h: h, http: &http.Client{Jar: jar}}
}

func (c *client) call(method, path string, body, out any) int {
	c.h.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, c.h.srv.URL+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if c.csrf != "" {
		req.Header.Set(app.CSRFHeader, c.csrf)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.h.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func (c *client) login(username, password string) map[string]any {
	c.h.t.Helper()
	var out map[string]any
	if code := c.call(http.MethodPost, "/api/auth/login", map[string]string{"username": username, "password": password}, &out); code != http.StatusOK {
		c.h.t.Fatalf("login %s = %d %v", username, code, out)
	}
	c.csrf, _ = out["csrf"].(string)
	return out
}
