package app_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/oidcx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/passkey/passkeytest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const appURL = "http://127.0.0.1:9999"

type connKey struct {
	Hub          string `json:"hub"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Token        string `json:"token"`
}

func decodeKey(t *testing.T, key string) connKey {
	t.Helper()
	if !strings.HasPrefix(key, "umbid1.") {
		t.Fatalf("connection key %q", key)
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(key, "umbid1."))
	if err != nil {
		t.Fatal(err)
	}
	var k connKey
	if err := json.Unmarshal(b, &k); err != nil {
		t.Fatal(err)
	}
	return k
}

func identityHarness(t *testing.T) (*harness, *client, connKey, string) {
	h := newHarness(t)
	h.addLocal("admin", "Correct-Horse-9", model.RoleAdmin, time.Now())
	h.st.Write(func(d *store.Data) { d.Settings.Alerting.PublicURL = h.srv.URL })
	c := h.client()
	c.login("admin", "Correct-Horse-9")
	var conn struct {
		Application struct {
			ID          string `json:"id"`
			RedirectURI string `json:"redirect_uri"`
			People      int    `json:"people"`
		} `json:"application"`
		Key string `json:"connection_key"`
	}
	if code := c.call(http.MethodPost, "/api/identity/applications", map[string]any{"kind": "network-control", "url": appURL, "access": "all", "sync_passkeys": true}, &conn); code != http.StatusCreated {
		t.Fatalf("connect = %d", code)
	}
	if conn.Application.RedirectURI != appURL+"/api/auth/umbrella/callback" || conn.Application.People != 1 {
		t.Fatalf("application %+v", conn.Application)
	}
	k := decodeKey(t, conn.Key)
	if k.Hub != h.srv.URL || k.ClientID == "" || k.ClientSecret == "" || !strings.HasPrefix(k.Token, "umb_") {
		t.Fatalf("key %+v", k)
	}
	return h, c, k, conn.Application.ID
}

func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// authorize starts a sign-in and returns the pending request ID.
func authorize(t *testing.T, h *harness, k connKey, verifier string, extra url.Values) string {
	t.Helper()
	q := url.Values{"client_id": {k.ClientID}, "redirect_uri": {appURL + "/api/auth/umbrella/callback"}, "response_type": {"code"},
		"scope": {"openid profile email groups"}, "state": {"st-1"}, "nonce": {"nonce-1"},
		"code_challenge": {oidcx.Challenge(verifier)}, "code_challenge_method": {"S256"}}
	for key, v := range extra {
		q[key] = v
	}
	res, err := noRedirect().Get(h.srv.URL + "/oauth2/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	loc := res.Header.Get("Location")
	if res.StatusCode != http.StatusFound || !strings.HasPrefix(loc, "/sso?request=") {
		t.Fatalf("authorize = %d %s", res.StatusCode, loc)
	}
	return strings.TrimPrefix(loc, "/sso?request=")
}

func exchange(t *testing.T, h *harness, k connKey, code, verifier string) (int, map[string]any) {
	t.Helper()
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {appURL + "/api/auth/umbrella/callback"}, "code_verifier": {verifier}}
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/oauth2/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(k.ClientID), url.QueryEscape(k.ClientSecret))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestIdentitySingleSignIn(t *testing.T) {
	h, c, k, _ := identityHarness(t)
	verifier := oidcx.Random()
	id := authorize(t, h, k, verifier, url.Values{"idp_hint": {"passkey"}})

	var req map[string]any
	if code := h.client().call(http.MethodGet, "/api/oauth2/requests/"+id, nil, &req); code != http.StatusOK || req["application"] != "Network Control" || req["idp_hint"] != "passkey" {
		t.Fatalf("request = %d %v", code, req)
	}
	// A stranger cannot approve the request.
	if code := h.client().call(http.MethodPost, "/api/oauth2/requests/"+id+"/approve", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous approve = %d", code)
	}
	var approved map[string]string
	if code := c.call(http.MethodPost, "/api/oauth2/requests/"+id+"/approve", nil, &approved); code != http.StatusOK {
		t.Fatalf("approve = %d", code)
	}
	u, _ := url.Parse(approved["redirect"])
	if !strings.HasPrefix(approved["redirect"], appURL+"/api/auth/umbrella/callback?") || u.Query().Get("state") != "st-1" || u.Query().Get("iss") != h.srv.URL {
		t.Fatalf("redirect %s", approved["redirect"])
	}
	code := u.Query().Get("code")
	if st, out := exchange(t, h, k, code, "wrong-verifier"); st != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Fatalf("wrong verifier = %d %v", st, out)
	}
	// The code was spent by the failed attempt: start again.
	id = authorize(t, h, k, verifier, nil)
	c.call(http.MethodPost, "/api/oauth2/requests/"+id+"/approve", nil, &approved)
	u, _ = url.Parse(approved["redirect"])
	code = u.Query().Get("code")
	st, tokens := exchange(t, h, k, code, verifier)
	if st != http.StatusOK {
		t.Fatalf("token = %d %v", st, tokens)
	}
	if st, _ := exchange(t, h, k, code, verifier); st != http.StatusBadRequest {
		t.Fatalf("code reuse = %d", st)
	}
	m := oidcx.Metadata{Issuer: h.srv.URL, JWKS: h.srv.URL + "/oauth2/jwks"}
	cl, err := oidcx.Verify(context.Background(), m, k.ClientID, tokens["id_token"].(string), "nonce-1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if cl.String("preferred_username") != "admin" || !cl.Bool("umbrella_admin") || !slices.Equal(cl.Strings("amr"), []string{"pwd"}) {
		t.Fatalf("claims %v", cl)
	}
	var info map[string]any
	req2, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/oauth2/userinfo", nil)
	req2.Header.Set("Authorization", "Bearer "+tokens["access_token"].(string))
	res, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.NewDecoder(res.Body).Decode(&info)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || info["preferred_username"] != "admin" {
		t.Fatalf("userinfo = %d %v", res.StatusCode, info)
	}
	var disc map[string]any
	if code := h.client().call(http.MethodGet, "/.well-known/openid-configuration", nil, &disc); code != http.StatusOK || disc["issuer"] != h.srv.URL {
		t.Fatalf("discovery = %d %v", code, disc)
	}
}

func TestIdentityAuthorizeRejects(t *testing.T) {
	h, _, k, _ := identityHarness(t)
	q := url.Values{"client_id": {k.ClientID}, "redirect_uri": {"https://evil.example/cb"}, "response_type": {"code"}, "scope": {"openid"},
		"code_challenge": {"x"}, "code_challenge_method": {"S256"}}
	res, _ := noRedirect().Get(h.srv.URL + "/oauth2/authorize?" + q.Encode())
	res.Body.Close()
	if loc := res.Header.Get("Location"); !strings.HasPrefix(loc, "/sso?error=") {
		t.Fatalf("foreign redirect went to %q", loc)
	}
	q.Set("redirect_uri", appURL+"/api/auth/umbrella/callback")
	q.Del("code_challenge")
	res, _ = noRedirect().Get(h.srv.URL + "/oauth2/authorize?" + q.Encode())
	res.Body.Close()
	if loc := res.Header.Get("Location"); !strings.HasPrefix(loc, appURL) || !strings.Contains(loc, "error=invalid_request") {
		t.Fatalf("missing PKCE went to %q", loc)
	}
}

func federation(t *testing.T, h *harness, token, path, etag string) (int, string, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header.Get("ETag"), b
}

func TestIdentityFederation(t *testing.T) {
	h, c, k, appID := identityHarness(t)
	h.addLocal("ivanov", "Correct-Horse-9", model.RoleUser, time.Now())
	h.st.Write(func(d *store.Data) {
		d.Settings.Passkeys = model.PasskeySettings{Enabled: true, RPID: "127.0.0.1"}
		u := d.UserByName("ivanov")
		u.Passkeys = []model.Passkey{{ID: "PK-1", Name: "Windows Hello", CredentialID: []byte{1, 2, 3}, PublicKey: []byte{4, 5}, UserHandle: []byte(u.ID), CreatedAt: time.Now()}}
	})
	st, etag, body := federation(t, h, k.Token, "/api/identity/v1/config", "")
	if st != http.StatusOK || etag == "" {
		t.Fatalf("config = %d", st)
	}
	var cfg map[string]any
	_ = json.Unmarshal(body, &cfg)
	if cfg["issuer"] != h.srv.URL || cfg["password_policy"].(map[string]any)["history_count"] != float64(5) {
		t.Fatalf("config %s", body)
	}
	if st, _, _ := federation(t, h, k.Token, "/api/identity/v1/config", etag); st != http.StatusNotModified {
		t.Fatalf("config with ETag = %d", st)
	}
	st, _, body = federation(t, h, k.Token, "/api/identity/v1/users", "")
	var users struct {
		Users []struct {
			Username string `json:"username"`
			Disabled bool   `json:"disabled"`
			Admin    bool   `json:"admin"`
			Passkeys []struct {
				CredentialID string `json:"credential_id"`
			} `json:"passkeys"`
		} `json:"users"`
	}
	_ = json.Unmarshal(body, &users)
	if st != http.StatusOK || len(users.Users) != 2 {
		t.Fatalf("users = %d %s", st, body)
	}
	for _, u := range users.Users {
		if u.Username == "ivanov" && (u.Disabled || len(u.Passkeys) != 1 || u.Passkeys[0].CredentialID != "AQID") {
			t.Fatalf("ivanov %+v", u)
		}
	}
	// Only administrators may use the application now: ivanov is disabled in it.
	if code := c.call(http.MethodPut, "/api/identity/applications/"+appID, map[string]any{"url": appURL, "access": "roles", "roles": []string{model.RoleAdmin}, "sync_passkeys": true}, nil); code != http.StatusOK {
		t.Fatalf("update = %d", code)
	}
	_, _, body = federation(t, h, k.Token, "/api/identity/v1/users", "")
	_ = json.Unmarshal(body, &users)
	for _, u := range users.Users {
		if u.Username == "ivanov" && (!u.Disabled || len(u.Passkeys) != 0) {
			t.Fatalf("ivanov after access change %+v", u)
		}
	}
	if st, _, _ := federation(t, h, "umb_wrong", "/api/identity/v1/users", ""); st != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d", st)
	}
	// A new key revokes the old token and secret.
	var conn struct {
		Key string `json:"connection_key"`
	}
	if code := c.call(http.MethodPost, "/api/identity/applications/"+appID+"/key", nil, &conn); code != http.StatusOK {
		t.Fatalf("reissue = %d", code)
	}
	if st, _, _ := federation(t, h, k.Token, "/api/identity/v1/users", ""); st != http.StatusUnauthorized {
		t.Fatalf("old token = %d", st)
	}
	if st, _, _ := federation(t, h, decodeKey(t, conn.Key).Token, "/api/identity/v1/users", ""); st != http.StatusOK {
		t.Fatalf("new token = %d", st)
	}
}

func TestIdentityAccessDenied(t *testing.T) {
	h, _, k, appID := identityHarness(t)
	h.addLocal("ivanov", "Correct-Horse-9", model.RoleUser, time.Now())
	h.st.Write(func(d *store.Data) {
		d.Applications[appID].Access, d.Applications[appID].Roles = model.AccessRoles, []string{model.RoleAdmin}
	})
	c := h.client()
	c.login("ivanov", "Correct-Horse-9")
	id := authorize(t, h, k, oidcx.Random(), nil)
	var out map[string]any
	if code := c.call(http.MethodPost, "/api/oauth2/requests/"+id+"/approve", nil, &out); code != http.StatusForbidden || out["error"] != "app_access_denied" {
		t.Fatalf("approve = %d %v", code, out)
	}
}

func TestPasskeyOriginChecks(t *testing.T) {
	h := newHarness(t)
	h.st.Write(func(d *store.Data) { d.Settings.Passkeys = model.PasskeySettings{Enabled: true, RPID: "example.com"} })
	post := func(origin string) int {
		req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/api/auth/passkey/begin", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if st := post("https://umbrella.example.com"); st != http.StatusOK {
		t.Fatalf("subdomain = %d", st)
	}
	for _, o := range []string{"", "https://example.org", "http://umbrella.example.com", "https://evilexample.com"} {
		if st := post(o); st != http.StatusBadRequest {
			t.Fatalf("origin %q = %d", o, st)
		}
	}
	h.st.Write(func(d *store.Data) { d.Settings.Passkeys.Enabled = false })
	if st := post("https://umbrella.example.com"); st != http.StatusConflict {
		t.Fatalf("disabled = %d", st)
	}
}

func TestPasswordHistoryAndCommon(t *testing.T) {
	h := newHarness(t)
	h.addLocal("petrov", "Correct-Horse-9", model.RoleUser, time.Now())
	c := h.client()
	c.login("petrov", "Correct-Horse-9")
	var out struct {
		Error      string   `json:"error"`
		Detail     string   `json:"detail"`
		Violations []string `json:"violations"`
	}
	if code := c.call(http.MethodPut, "/api/auth/me/password", map[string]string{"current_password": "Correct-Horse-9", "new_password": "Qwerty123456!"}, &out); code != http.StatusBadRequest || !slices.Contains(out.Violations, "common") {
		t.Fatalf("common password = %d %v", code, out)
	}
	if code := c.call(http.MethodPut, "/api/auth/me/password", map[string]string{"current_password": "Correct-Horse-9", "new_password": "Battery-Staple-42"}, &out); code != http.StatusNoContent && code != http.StatusOK {
		t.Fatalf("new password = %d %v", code, out)
	}
	// Going back to the previous password is refused by the history.
	if code := c.call(http.MethodPut, "/api/auth/me/password", map[string]string{"current_password": "Battery-Staple-42", "new_password": "Correct-Horse-9"}, &out); code != http.StatusBadRequest || !slices.Contains(out.Violations, "history") {
		t.Fatalf("previous password = %d %v", code, out)
	}
}

func TestPasskeyRegisterAndSignIn(t *testing.T) {
	h := newHarness(t)
	h.addLocal("sidorov", "Correct-Horse-9", model.RoleUser, time.Now())
	h.st.Write(func(d *store.Data) { d.Settings.Passkeys = model.PasskeySettings{Enabled: true, RPID: "example.com"} })
	const origin = "https://umbrella.example.com"
	c := h.client()
	c.login("sidorov", "Correct-Horse-9")
	withOrigin := func(c *client, path string, body, out any) int {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, h.srv.URL+path, strings.NewReader(string(b)))
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		if c.csrf != "" {
			req.Header.Set("X-CSRF-Token", c.csrf)
		}
		res, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		_ = json.NewDecoder(res.Body).Decode(out)
		return res.StatusCode
	}
	var started struct {
		Ceremony string `json:"ceremony"`
		Options  any    `json:"options"`
	}
	if st := withOrigin(c, "/api/auth/me/passkeys/begin", nil, &started); st != http.StatusOK {
		t.Fatalf("begin registration = %d", st)
	}
	auth := passkeytest.New(origin, "example.com")
	var pk map[string]any
	if st := withOrigin(c, "/api/auth/me/passkeys/finish", map[string]any{"ceremony": started.Ceremony, "credential": auth.Create(started.Options), "name": "Ноутбук"}, &pk); st != http.StatusCreated || pk["name"] != "Ноутбук" {
		t.Fatalf("finish registration = %d %v", st, pk)
	}
	// Sign in with the passkey from a new browser.
	b := h.client()
	if st := withOrigin(b, "/api/auth/passkey/begin", nil, &started); st != http.StatusOK {
		t.Fatalf("begin sign-in = %d", st)
	}
	var me map[string]any
	if st := withOrigin(b, "/api/auth/passkey/finish", map[string]any{"ceremony": started.Ceremony, "credential": auth.Get(started.Options)}, &me); st != http.StatusOK || me["username"] != "sidorov" {
		t.Fatalf("passkey sign-in = %d %v", st, me)
	}
	// A replayed ceremony is refused.
	if st := withOrigin(b, "/api/auth/passkey/finish", map[string]any{"ceremony": started.Ceremony, "credential": auth.Get(started.Options)}, &me); st == http.StatusOK {
		t.Fatal("replayed ceremony accepted")
	}
	// An authenticator without user verification is refused.
	withOrigin(b, "/api/auth/passkey/begin", nil, &started)
	auth.SkipUV = true
	if st := withOrigin(b, "/api/auth/passkey/finish", map[string]any{"ceremony": started.Ceremony, "credential": auth.Get(started.Options)}, &me); st != http.StatusUnauthorized {
		t.Fatalf("no user verification = %d", st)
	}
}
