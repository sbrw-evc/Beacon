package app_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// bearer calls the API with an API token instead of a session, without a CSRF header.
func (h *harness) bearer(token, method, path string, body, out any) int {
	h.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

type saView struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Role     string           `json:"role"`
	RoleName string           `json:"role_name"`
	Tokens   []model.APIToken `json:"tokens"`
}

type issued struct {
	Token  model.APIToken `json:"token"`
	Secret string         `json:"secret"`
}

// flowAccount makes a role (by default the one Umbrella Flow needs), a service account with it
// and a token; it returns the admin client, the account ID and the token.
func flowAccount(t *testing.T, h *harness, perms ...string) (*client, string, string) {
	t.Helper()
	if len(perms) == 0 {
		perms = []string{"integrations.flow:view", "integrations.flow:read", "cmdb:view", "cmdb:propose"}
	}
	h.addLocal("root", "Adm1nPassw0rd!", model.RoleAdmin, time.Now())
	h.st.Write(func(d *store.Data) {
		d.Roles["flow"] = &model.Role{ID: "flow", Name: "Umbrella Flow", Permissions: perms}
	})
	admin := h.client()
	admin.login("root", "Adm1nPassw0rd!")
	var sa saView
	if code := admin.call(http.MethodPost, "/api/service-accounts", map[string]any{"name": "umbrella-flow", "role_id": "flow", "description": "Flow"}, &sa); code != http.StatusCreated {
		t.Fatalf("create = %d", code)
	}
	var tok issued
	if code := admin.call(http.MethodPost, "/api/service-accounts/"+sa.ID+"/tokens", map[string]any{"name": "prod"}, &tok); code != http.StatusCreated {
		t.Fatalf("issue = %d", code)
	}
	return admin, sa.ID, tok.Secret
}

func TestServiceAccountTokens(t *testing.T) {
	h := newHarness(t)
	admin, id, secret := flowAccount(t, h, "cmdb:view", "serviceaccounts:view", "serviceaccounts:edit")
	if !strings.HasPrefix(secret, "umb_") || len(secret) != len("umb_")+43 {
		t.Fatalf("token format %q", secret)
	}
	// Only a keyed hash is kept; the key is in OpenBao.
	snap, err := h.st.Export()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(snap, []byte(secret)) || bytes.Contains(snap, []byte(secret[4:])) {
		t.Fatal("the token is in the snapshot")
	}
	var keyRef string
	h.st.Read(func(d *store.Data) { keyRef = d.Settings.APITokenKeyRef })
	if !strings.HasPrefix(keyRef, "openbao://") || h.bao.Get("umbrella/system/api-tokens") == nil {
		t.Fatalf("key ref %q, paths %v", keyRef, h.bao.Paths())
	}

	// The token works without a session and without CSRF, within its role.
	if code := h.bearer(secret, http.MethodGet, "/api/cmdb", nil, nil); code != http.StatusOK {
		t.Fatalf("map = %d", code)
	}
	if code := h.bearer(secret, http.MethodPost, "/api/service-accounts", map[string]any{"name": "zz-script", "role_id": "flow"}, nil); code != http.StatusCreated {
		t.Fatalf("POST without CSRF = %d", code)
	}
	if code := h.bearer(secret, http.MethodGet, "/api/users", nil, nil); code != http.StatusForbidden {
		t.Fatalf("outside the role = %d", code)
	}
	var me map[string]any
	if code := h.bearer(secret, http.MethodGet, "/api/auth/me", nil, &me); code != http.StatusOK || me["username"] != "service:umbrella-flow" || me["source"] != "service" {
		t.Fatalf("me = %d %v", code, me)
	}
	if code := h.bearer(secret, http.MethodPost, "/api/auth/logout", nil, nil); code != http.StatusForbidden {
		t.Fatalf("logout with a token = %d", code)
	}
	for _, bad := range []string{"umb_nope", "nope", secret + "x"} {
		if code := h.bearer(bad, http.MethodGet, "/api/cmdb", nil, nil); code != http.StatusUnauthorized {
			t.Fatalf("bad token %q = %d", bad, code)
		}
	}
	// Sessions are unchanged: a modifying request still needs CSRF.
	noCSRF := *admin
	noCSRF.csrf = ""
	if code := noCSRF.call(http.MethodPost, "/api/service-accounts", map[string]any{"name": "x2", "role_id": "flow"}, nil); code != http.StatusForbidden {
		t.Fatalf("session without CSRF = %d", code)
	}

	// The list shows when and where the token was used, never the hash.
	var list []saView
	admin.call(http.MethodGet, "/api/service-accounts", nil, &list)
	if len(list) != 2 || len(list[0].Tokens) != 1 || list[0].Tokens[0].LastUsedAt == nil || list[0].Tokens[0].LastUsedIP != "127.0.0.1" ||
		list[0].RoleName != "Umbrella Flow" || !strings.HasPrefix(list[0].Tokens[0].Hint, "umb_") {
		t.Fatalf("list = %+v", list)
	}
	raw := rawGet(t, admin, "/api/service-accounts")
	if strings.Contains(raw, "hash") || strings.Contains(raw, secret) {
		t.Fatalf("the list leaks the token: %s", raw)
	}

	// Revoked and expired tokens stop working.
	tokID := list[0].Tokens[0].ID
	if code := admin.call(http.MethodPost, "/api/service-accounts/"+id+"/tokens/"+tokID+"/revoke", nil, nil); code != http.StatusOK {
		t.Fatalf("revoke = %d", code)
	}
	if code := h.bearer(secret, http.MethodGet, "/api/cmdb", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("revoked = %d", code)
	}
	var second issued
	soon := time.Now().Add(time.Hour)
	admin.call(http.MethodPost, "/api/service-accounts/"+id+"/tokens", map[string]any{"name": "short", "expires_at": soon}, &second)
	if code := h.bearer(second.Secret, http.MethodGet, "/api/cmdb", nil, nil); code != http.StatusOK {
		t.Fatalf("second token = %d", code)
	}
	h.st.Write(func(d *store.Data) {
		past := time.Now().Add(-time.Minute)
		d.APITokens[second.Token.ID].ExpiresAt = &past
	})
	if code := h.bearer(second.Secret, http.MethodGet, "/api/cmdb", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("expired = %d", code)
	}
	if code := admin.call(http.MethodPost, "/api/service-accounts/"+id+"/tokens", map[string]any{"name": "old", "expires_at": time.Now().Add(-time.Hour)}, nil); code != http.StatusBadRequest {
		t.Fatalf("expiry in the past = %d", code)
	}

	// A disabled account's tokens stop working.
	var third issued
	admin.call(http.MethodPost, "/api/service-accounts/"+id+"/tokens", map[string]any{"name": "third"}, &third)
	if code := admin.call(http.MethodPut, "/api/service-accounts/"+id, map[string]any{"name": "umbrella-flow", "role_id": "flow", "disabled": true}, nil); code != http.StatusOK {
		t.Fatalf("disable = %d", code)
	}
	if code := h.bearer(third.Secret, http.MethodGet, "/api/cmdb", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("disabled account = %d", code)
	}

	// Audit: creation, use from a new address, revocation.
	var actions []string
	h.st.Read(func(d *store.Data) {
		for _, a := range d.Audit {
			actions = append(actions, a.Action)
		}
	})
	all := strings.Join(actions, " ")
	for _, want := range []string{"serviceaccount.create", "serviceaccount.token.create", "serviceaccount.token.new_address", "serviceaccount.token.revoke", "serviceaccount.update"} {
		if !strings.Contains(all, want) {
			t.Fatalf("audit lacks %s: %v", want, actions)
		}
	}

	// Deleting the account deletes its tokens.
	if code := admin.call(http.MethodDelete, "/api/service-accounts/"+id, nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete = %d", code)
	}
	h.st.Read(func(d *store.Data) {
		if len(d.APITokens) != 0 || len(d.ServiceAccounts) != 1 {
			t.Fatalf("left: %d tokens, %d accounts", len(d.APITokens), len(d.ServiceAccounts))
		}
	})
}

func TestServiceAccountValidation(t *testing.T) {
	h := newHarness(t)
	admin, _, _ := flowAccount(t, h)
	h.addLocal("lead", "LeadPassw0rd!x", "lead", time.Now())
	h.st.Write(func(d *store.Data) {
		d.Roles["lead"] = &model.Role{ID: "lead", Name: "Lead", Permissions: []string{"serviceaccounts:view", "serviceaccounts:edit"}}
	})
	cases := []struct {
		body map[string]any
		code int
	}{
		{map[string]any{"name": "umbrella-flow", "role_id": "flow"}, http.StatusConflict},
		{map[string]any{"name": "", "role_id": "flow"}, http.StatusBadRequest},
		{map[string]any{"name": "x y", "role_id": "flow"}, http.StatusBadRequest},
		{map[string]any{"name": "ok", "role_id": "missing"}, http.StatusBadRequest},
		{map[string]any{"name": "ok", "role_id": "flow", "scope_mode": "services"}, http.StatusBadRequest},
		{map[string]any{"name": "ok", "role_id": "flow", "team_ids": []string{"T-404"}}, http.StatusBadRequest},
	}
	for _, c := range cases {
		if code := admin.call(http.MethodPost, "/api/service-accounts", c.body, nil); code != c.code {
			t.Fatalf("%v = %d, want %d", c.body, code, c.code)
		}
	}
	// Only administrators give the administrator role.
	lead := h.client()
	lead.login("lead", "LeadPassw0rd!x")
	if code := lead.call(http.MethodPost, "/api/service-accounts", map[string]any{"name": "root2", "role_id": model.RoleAdmin}, nil); code != http.StatusForbidden {
		t.Fatalf("lead gives admin = %d", code)
	}
	if code := lead.call(http.MethodPost, "/api/service-accounts", map[string]any{"name": "script", "role_id": "flow"}, nil); code != http.StatusCreated {
		t.Fatalf("lead creates = %d", code)
	}
}

// Service accounts and token hashes are part of the state snapshot.
func TestServiceAccountsSurviveSnapshot(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	st := restarted(t, func(d *store.Data) {
		d.ServiceAccounts["SA-1"] = &model.ServiceAccount{ID: "SA-1", Name: "flow", Role: "viewer", ScopeMode: model.ScopeAll, CreatedAt: now}
		d.APITokens["TOK-1"] = &model.APIToken{ID: "TOK-1", AccountID: "SA-1", Name: "prod", Hash: "abc", IPs: []string{"10.0.0.1"}, CreatedAt: now}
		d.Settings.APITokenKeyRef = "openbao://umbrella/system/api-tokens#hmac_key"
	})
	st.Read(func(d *store.Data) {
		if d.ServiceAccounts["SA-1"].Name != "flow" || d.APITokens["TOK-1"].Hash != "abc" || d.APITokens["TOK-1"].IPs[0] != "10.0.0.1" ||
			d.Settings.APITokenKeyRef == "" {
			t.Fatalf("lost: %+v %+v", d.ServiceAccounts, d.APITokens)
		}
	})
	h := newHarnessWith(t, st)
	h.addLocal("root", "Adm1nPassw0rd!", model.RoleAdmin, time.Now())
	c := h.client()
	c.login("root", "Adm1nPassw0rd!")
	raw := rawGet(t, c, "/api/service-accounts")
	if !strings.Contains(raw, `"team_ids":[]`) || !strings.Contains(raw, `"service_ids":[]`) {
		t.Fatalf("lists after a restart: %s", raw)
	}
}
