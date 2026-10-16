package app_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/beacon/app/backend/internal/app"
	"github.com/sbrw-evc/beacon/app/backend/internal/auth"
	"github.com/sbrw-evc/beacon/app/backend/internal/keystone"
	"github.com/sbrw-evc/beacon/app/backend/internal/keystone/keystonetest"
	"github.com/sbrw-evc/beacon/app/backend/internal/model"
	"github.com/sbrw-evc/beacon/app/backend/internal/secrets"
	"github.com/sbrw-evc/beacon/app/backend/internal/secrets/secretstest"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

// harness serves the app connected to a fake Keystone: people sign in there.
type harness struct {
	t        *testing.T
	srv      *httptest.Server
	st       *store.Store
	bao      *secretstest.Fake
	vault    *secrets.Client
	app      *app.App
	keystone *keystonetest.Keystone
}

func newHarness(t *testing.T) *harness {
	return newHarnessWith(t, store.New())
}

// newHarnessWith serves the app over a given store, e.g. one loaded from a snapshot.
func newHarnessWith(t *testing.T, st *store.Store) *harness {
	return newHarnessOpts(t, st, app.Options{Version: "test"})
}

func newHarnessOpts(t *testing.T, st *store.Store, opt app.Options) *harness {
	bao, vault := secretstest.New(t)
	fake := testSPA(t, st)
	if opt.KeystoneKey == "" {
		opt.KeystoneKey = fake.ConnectionKey()
	}
	a := app.New(opt, app.Deps{Vault: vault, Store: st, Sessions: auth.NewSessions()})
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	return &harness{t: t, srv: srv, st: st, bao: bao, vault: vault, app: a, keystone: fake}
}

// testSPA starts a fake Keystone for the app over st, whose system roles it creates.
func testSPA(t *testing.T, st *store.Store) *keystonetest.Keystone {
	st.Write(func(d *store.Data) { d.EnsureSystemRoles(time.Now().UTC()) })
	return keystonetest.New(t)
}

// spaID is the Keystone user ID of a test person.
func spaID(username string) string { return "usr_" + username }

// addLocal adds a person with a role in Beacon to the fake Keystone and their synchronized record
// to Beacon; the password is not used: people sign in at Keystone.
func (h *harness) addLocal(username, _ string, role string, changed time.Time) string {
	h.t.Helper()
	h.addSPA(keystone.User{ID: spaID(username), Username: username, Name: username, Roles: []string{strings.ToLower(role)}})
	var id string
	h.st.Write(func(d *store.Data) {
		id = d.NextID("USR")
		d.Users[id] = &model.User{ID: id, Username: username, Name: username, Source: model.SourceSPA, ExternalID: spaID(username),
			Role: role, ScopeMode: model.ScopeAll, CreatedAt: changed}
	})
	return id
}

// addSPA adds (or replaces) a person in the fake Keystone only.
func (h *harness) addSPA(u keystone.User) {
	for i := range h.keystone.Users {
		if h.keystone.Users[i].ID == u.ID {
			h.keystone.Users[i] = u
			return
		}
	}
	h.keystone.Users = append(h.keystone.Users, u)
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

// login signs the person in through the fake Keystone: start, Keystone's authorization, the callback.
func (c *client) login(username, _ string) map[string]any {
	c.h.t.Helper()
	if final := c.signIn(username); final != "/" {
		c.h.t.Fatalf("login %s ended at %s", username, final)
	}
	var out map[string]any
	if code := c.call(http.MethodGet, "/api/auth/me", nil, &out); code != http.StatusOK {
		c.h.t.Fatalf("login %s: me = %d %v", username, code, out)
	}
	c.csrf, _ = out["csrf"].(string)
	return out
}

// signIn runs the sign-in through Keystone for the Keystone person with this username and returns the
// path and query Beacon sent the browser to at the end.
func (c *client) signIn(username string) string {
	c.h.t.Helper()
	c.h.keystone.SignedIn = spaID(username)
	for _, u := range c.h.keystone.Users {
		if u.Username == username {
			c.h.keystone.SignedIn = u.ID
		}
	}
	var last *http.Request
	hc := *c.http
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		last = req
		if len(via) > 10 {
			return http.ErrUseLastResponse
		}
		return nil
	}
	resp, err := hc.Get(c.h.srv.URL + "/api/auth/spa/start?return=/")
	if err != nil {
		c.h.t.Fatal(err)
	}
	resp.Body.Close()
	if last == nil {
		return ""
	}
	return last.URL.RequestURI()
}

func (h *harness) named(name string) model.User {
	var out model.User
	h.st.Read(func(d *store.Data) {
		if u := d.UserByName(name); u != nil {
			out = *u
		}
	})
	return out
}

func (c *client) me() (int, map[string]any) {
	var out map[string]any
	code := c.call(http.MethodGet, "/api/auth/me", nil, &out)
	c.csrf, _ = out["csrf"].(string)
	return code, out
}
