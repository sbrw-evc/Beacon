package app_test

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/beacon/app/backend/internal/keystone"
	"github.com/sbrw-evc/beacon/app/backend/internal/model"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
)

func ssoError(t *testing.T, end string) string {
	t.Helper()
	u, err := url.Parse(end)
	if err != nil || u.Path != "/login" {
		t.Fatalf("sign-in ended at %q, want /login?sso_error=…", end)
	}
	return u.Query().Get("sso_error")
}

func TestSPASignIn(t *testing.T) {
	h := newHarness(t)
	h.addSPA(keystone.User{ID: "usr_anna", Username: "anna", Name: "Ivanova Anna", Email: "anna@example.org", Roles: []string{"user"}, Groups: []string{"noc"}})

	var info map[string]any
	if code := h.client().call(http.MethodGet, "/api/auth/spa", nil, &info); code != 200 || info["connected"] != true {
		t.Fatalf("spa info = %d %v", code, info)
	}

	c := h.client()
	if end := c.signIn("anna"); end != "/" {
		t.Fatalf("sign-in ended at %s", end)
	}
	q := h.keystone.Requests[len(h.keystone.Requests)-1]
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" || q.Get("state") == "" ||
		q.Get("redirect_uri") != h.srv.URL+keystone.CallbackPath || !strings.Contains(q.Get("scope"), "openid") {
		t.Fatalf("authorization request = %v", q)
	}
	code, me := c.me()
	if code != 200 || me["source"] != model.SourceSPA || me["role"] != model.RoleUser || me["email"] != "anna@example.org" {
		t.Fatalf("me = %d %v", code, me)
	}
	u := h.named("anna")
	if u.ExternalID != "usr_anna" || !slices.Equal(u.Groups, []string{"noc"}) || u.LastName != "Ivanova" || u.FirstName != "Anna" {
		t.Fatalf("record = %+v", u)
	}

	// A new role in Keystone applies at the next sign-in.
	h.addSPA(keystone.User{ID: "usr_anna", Username: "anna", Name: "Ivanova Anna", Roles: []string{"user", "admin"}})
	c2 := h.client()
	c2.login("anna", "")
	if _, me := c2.me(); me["role"] != model.RoleAdmin {
		t.Fatalf("role after Keystone change = %v", me["role"])
	}

	// Signing out ends the session and sends the browser to Keystone's sign-out.
	var out map[string]string
	if code := c2.call(http.MethodPost, "/api/auth/logout", nil, &out); code != 200 || !strings.HasPrefix(out["redirect"], h.keystone.SignOutURL()) ||
		!strings.Contains(out["redirect"], url.QueryEscape(h.srv.URL)) {
		t.Fatalf("logout = %d %v", code, out)
	}
	if code, _ := c2.me(); code != 401 {
		t.Fatalf("me after logout = %d", code)
	}
}

func TestSPASignInRefusals(t *testing.T) {
	h := newHarness(t)
	h.addSPA(keystone.User{ID: "usr_anna", Username: "anna", Name: "Anna", Roles: []string{"user"}})
	h.addSPA(keystone.User{ID: "usr_ghost", Username: "ghost", Name: "Ghost", Roles: []string{"no-such-role"}})

	for _, tc := range []struct {
		name, want string
		tamper     func()
	}{
		{"nonce", "invalid_token", func() { h.keystone.Nonce = "other" }},
		{"audience", "invalid_token", func() { h.keystone.Audience = "app_other" }},
		{"issuer", "invalid_token", func() { h.keystone.Issuer = "https://evil.example.org" }},
		{"alg none", "invalid_token", func() { h.keystone.Alg = "none" }},
	} {
		tc.tamper()
		if got := ssoError(t, h.client().signIn("anna")); got != tc.want {
			t.Errorf("%s: sso_error = %q, want %q", tc.name, got, tc.want)
		}
		h.keystone.Nonce, h.keystone.Audience, h.keystone.Issuer, h.keystone.Alg = "", "", "", ""
	}
	if got := ssoError(t, h.client().signIn("ghost")); got != "no_role" {
		t.Fatalf("unknown role: sso_error = %q", got)
	}
	if h.named("ghost").ID != "" {
		t.Fatal("a person without a role got an account")
	}

	// The callback without the flow cookie, with a forged state and with a replayed state.
	c := h.client()
	for _, path := range []string{keystone.CallbackPath + "?code=x&state=y", keystone.CallbackPath + "?code=x"} {
		resp, err := noRedirect(c).Get(h.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if loc := resp.Header.Get("Location"); resp.StatusCode != http.StatusSeeOther || ssoError(t, loc) != "state" {
			t.Fatalf("%s = %d %s", path, resp.StatusCode, loc)
		}
	}
	if code, _ := c.me(); code != 401 {
		t.Fatal("a refused callback must not sign in")
	}

	// A return address outside Beacon is ignored.
	h.keystone.SignedIn = "usr_anna"
	resp, err := c.http.Get(h.srv.URL + keystone.StartPath + "?return=https://evil.example.org/x")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Request.URL.Host != strings.TrimPrefix(h.srv.URL, "http://") || resp.Request.URL.Path != "/" {
		t.Fatalf("return address followed to %s", resp.Request.URL)
	}
}

func noRedirect(c *client) *http.Client {
	hc := *c.http
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &hc
}

func TestSPASynchronization(t *testing.T) {
	h := newHarness(t)
	h.addLocal("root", "", model.RoleAdmin, time.Now())
	admin := h.client()
	admin.login("root", "")

	// Accounts of versions before Keystone: one Keystone knows by username (bound), one it does not (locked).
	var legacy, orphan string
	h.st.Write(func(d *store.Data) {
		legacy = d.NextID("USR")
		d.Users[legacy] = &model.User{ID: legacy, Username: "boris", Source: model.SourceLocal, Role: model.RoleUser, TeamIDs: []string{}}
		orphan = d.NextID("USR")
		d.Users[orphan] = &model.User{ID: orphan, Username: "olga", Source: model.SourceLDAP, Role: model.RoleUser}
	})
	h.addSPA(keystone.User{ID: "usr_boris", Username: "boris", Name: "Petrov Boris", Roles: []string{"user"}})
	h.addSPA(keystone.User{ID: "usr_vera", Username: "vera", Name: "Vera", Roles: []string{"admin"}})
	h.addSPA(keystone.User{ID: "usr_none", Username: "none", Name: "None"})
	h.addLocal("ivan", "", model.RoleUser, time.Now())
	ivan := h.client()
	ivan.login("ivan", "")

	// An API token of a service account Ivan owns.
	var sa struct{ ID string }
	if code := admin.call(http.MethodPost, "/api/service-accounts", map[string]any{"name": "script", "role_id": model.RoleUser, "owner_id": h.named("ivan").ID}, &sa); code != http.StatusCreated {
		t.Fatalf("service account = %d", code)
	}
	var tok struct{ Secret string }
	admin.call(http.MethodPost, "/api/service-accounts/"+sa.ID+"/tokens", map[string]any{"name": "t"}, &tok)
	if code := h.bearer(tok.Secret, http.MethodGet, "/api/auth/me", nil, nil); code != 200 {
		t.Fatalf("token before = %d", code)
	}

	h.addSPA(keystone.User{ID: spaID("ivan"), Username: "ivan", Name: "ivan", Disabled: true, Roles: []string{"user"}})
	if code := admin.call(http.MethodPost, "/api/settings/spa/sync", nil, nil); code != 200 {
		t.Fatalf("sync = %d", code)
	}
	var view struct {
		model.SPAState
		Counts struct{ SPA, Disabled, Legacy int }
		Roles  []keystone.Role
	}
	if code := admin.call(http.MethodGet, "/api/settings/spa", nil, &view); code != 200 || !view.OK || view.Created != 1 || view.Bound != 1 || view.Disabled < 2 {
		t.Fatalf("sync state = %d %+v", code, view)
	}
	if !slices.Contains(view.NoRole, "none") {
		t.Fatalf("people without a role = %v", view.NoRole)
	}
	if u := h.user(legacy); u.Source != model.SourceSPA || u.ExternalID != "usr_boris" || u.Disabled {
		t.Fatalf("bound = %+v", u)
	}
	if u := h.user(orphan); !u.Disabled {
		t.Fatalf("an account Keystone does not know stays active: %+v", u)
	}
	if u := h.named("vera"); u.Role != model.RoleAdmin || u.Source != model.SourceSPA {
		t.Fatalf("created = %+v", u)
	}
	if !h.named("ivan").Disabled {
		t.Fatal("disabled in Keystone stays active")
	}
	if code, _ := ivan.me(); code != 401 {
		t.Fatalf("session of a disabled person = %d", code)
	}
	if code := h.bearer(tok.Secret, http.MethodGet, "/api/auth/me", nil, nil); code != 401 {
		t.Fatalf("token of a disabled owner = %d", code)
	}
	if code := ivan.call(http.MethodGet, "/api/auth/me", nil, nil); code != 401 {
		t.Fatal("disabled person signed in")
	}
	if got := ssoError(t, h.client().signIn("ivan")); got == "" {
		t.Fatal("a disabled person signed in again")
	}
}

func TestSPACatalogue(t *testing.T) {
	h := newHarness(t)
	h.addLocal("root", "", model.RoleAdmin, time.Now())
	admin := h.client()
	admin.login("root", "")
	h.st.Write(func(d *store.Data) {
		d.Roles["ROL-7"] = &model.Role{ID: "ROL-7", Name: "Night shift", Description: "Duty engineers"}
	})
	if code := admin.call(http.MethodPost, "/api/settings/spa/sync", nil, nil); code != 200 {
		t.Fatalf("sync = %d", code)
	}
	c := h.keystone.Catalogue
	if c == nil {
		t.Fatal("no catalogue published")
	}
	ids := []string{}
	for _, r := range c.Roles {
		if !keystone.RoleIDRe.MatchString(r.ID) {
			t.Errorf("role ID %q is not in the protocol's form", r.ID)
		}
		ids = append(ids, r.ID)
	}
	if !slices.Contains(ids, "admin") || !slices.Contains(ids, "user") || !slices.Contains(ids, "rol-7") {
		t.Fatalf("catalogue = %v", ids)
	}
	published := h.keystone.Published
	admin.call(http.MethodPost, "/api/settings/spa/sync", nil, nil)
	if h.keystone.Published != published {
		t.Fatal("an unchanged catalogue is published again")
	}

	// Keystone gives the custom role by its published ID.
	h.addSPA(keystone.User{ID: "usr_nina", Username: "nina", Name: "Nina", Roles: []string{"rol-7"}})
	c2 := h.client()
	c2.login("nina", "")
	if _, me := c2.me(); me["role"] != "ROL-7" {
		t.Fatalf("custom role = %v", me["role"])
	}
}
