package app_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/entra"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/entra/entratest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	entraAdmins = "00000000-0000-0000-0000-0000000000a1"
	entraUsers  = "00000000-0000-0000-0000-0000000000b2"
	entraOther  = "00000000-0000-0000-0000-0000000000c3"
)

func entraConfig(h *harness) map[string]any {
	return map[string]any{"enabled": true, "cloud": entratest.Cloud, "tenant_id": entratest.TenantID, "client_id": entratest.ClientID,
		"redirect_url": h.srv.URL + entra.CallbackPath, "admin_group_id": entraAdmins, "user_group_id": entraUsers}
}

type entraReply struct {
	Config          map[string]any `json:"config"`
	ClientSecretSet bool           `json:"client_secret_set"`
	Users           struct {
		Total  int `json:"total"`
		Admins int `json:"admins"`
	} `json:"users"`
}

// signInEntra runs the browser side of the sign-in and returns where the browser ended up.
func (c *client) signInEntra() string {
	c.h.t.Helper()
	resp, err := c.http.Get(c.h.srv.URL + "/api/auth/entra/start")
	if err != nil {
		c.h.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.Request.URL.RequestURI()
}

func (c *client) me() (int, map[string]any) {
	var out map[string]any
	code := c.call(http.MethodGet, "/api/auth/me", nil, &out)
	c.csrf, _ = out["csrf"].(string)
	return code, out
}

func TestEntraSignIn(t *testing.T) {
	h := newHarness(t)
	idp := entratest.Start(t)
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	admin := h.client()
	admin.login("admin", "Admin-pass-2026")

	var view entraReply
	if code := admin.call(http.MethodGet, "/api/settings/entra", nil, &view); code != 200 || view.Config["enabled"] != false || view.ClientSecretSet {
		t.Fatalf("initial view = %d %+v", code, view)
	}
	var problem map[string]any
	if code := admin.call(http.MethodPost, "/api/settings/entra/test", map[string]any{"config": entraConfig(h)}, &problem); code != 400 || problem["error"] != "entra_secret_required" {
		t.Fatalf("test without a secret = %d %v", code, problem)
	}
	var rep map[string]any
	admin.call(http.MethodPost, "/api/settings/entra/test", map[string]any{"config": entraConfig(h), "client_secret": "wrong"}, &rep)
	if rep["ok"] != false || !strings.Contains(rep["error"].(string), "AADSTS7000215") {
		t.Fatalf("test with a wrong secret = %v", rep)
	}
	common := entraConfig(h)
	common["tenant_id"] = "common"
	admin.call(http.MethodPost, "/api/settings/entra/test", map[string]any{"config": common, "client_secret": entratest.Secret}, &rep)
	if rep["ok"] != false {
		t.Fatalf("multi-tenant authority accepted: %v", rep)
	}
	admin.call(http.MethodPost, "/api/settings/entra/test", map[string]any{"config": entraConfig(h), "client_secret": entratest.Secret}, &rep)
	if p := rep["probe"].(map[string]any); rep["ok"] != true || p["credentials"] != true || p["tenant_id"] != entratest.TenantID {
		t.Fatalf("test = %v", rep)
	}
	if code := admin.call(http.MethodPut, "/api/settings/entra", map[string]any{"config": entraConfig(h), "client_secret": "wrong"}, &problem); code != 400 || problem["error"] != "entra_unavailable" {
		t.Fatalf("save with a wrong secret = %d %v", code, problem)
	}
	if code := admin.call(http.MethodPut, "/api/settings/entra", map[string]any{"config": entraConfig(h), "client_secret": entratest.Secret}, &view); code != 200 || !view.ClientSecretSet || view.Config["enabled"] != true || view.Config["client_secret_ref"] != nil {
		t.Fatalf("save = %d %+v", code, view)
	}
	var meta map[string]any
	h.client().call(http.MethodGet, "/api/meta", nil, &meta)
	if meta["entra_enabled"] != true {
		t.Fatalf("meta = %v", meta)
	}
	// Saving again without the secret keeps the stored one.
	if code := admin.call(http.MethodPut, "/api/settings/entra", map[string]any{"config": entraConfig(h)}, &view); code != 200 {
		t.Fatalf("save keeping the secret = %d", code)
	}

	// An administrator through a nested group, found with Microsoft Graph paging.
	idp.User = entratest.User{ObjectID: "0b1d0000-0000-0000-0000-000000000001", Username: "Anna@Contoso.com", Name: "Anna Ivanova",
		Title: "Lead engineer", Department: "Ops", Manager: "Boris Petrov", Groups: []string{entraOther, entraUsers, entraAdmins}}
	anna := h.client()
	if where := anna.signInEntra(); where != "/" {
		t.Fatalf("sign-in ended at %s", where)
	}
	code, me := anna.me()
	if code != 200 || me["username"] != "anna@contoso.com" || me["source"] != "entra" || me["role"] != model.RoleAdmin ||
		me["title"] != "Lead engineer" || me["manager"] != "Boris Petrov" || me["name"] != "Anna Ivanova" {
		t.Fatalf("me = %d %v", code, me)
	}
	if code := anna.call(http.MethodPost, "/api/auth/login", map[string]string{"username": "anna@contoso.com", "password": "x"}, nil); code != 401 {
		t.Fatalf("password sign-in of an Entra account = %d", code)
	}

	// A regular user with the groups in the ID token.
	idp.User = entratest.User{ObjectID: "0b1d0000-0000-0000-0000-000000000002", Username: "boris@contoso.com", Name: "Boris Petrov",
		Groups: []string{entraUsers}, GroupsInToken: true}
	boris := h.client()
	if where := boris.signInEntra(); where != "/" {
		t.Fatalf("sign-in ended at %s", where)
	}
	if _, me := boris.me(); me["role"] != model.RoleUser {
		t.Fatalf("boris = %v", me)
	}

	// Outside the allowed group.
	idp.User = entratest.User{ObjectID: "0b1d0000-0000-0000-0000-000000000003", Username: "eve@contoso.com", Groups: []string{entraOther}}
	if where := h.client().signInEntra(); where != "/?signin_error=entra_not_allowed" {
		t.Fatalf("outsider ended at %s", where)
	}
	// The same sign-in name with another object ID does not take over Anna's account.
	idp.User = entratest.User{ObjectID: "0b1d0000-0000-0000-0000-000000000004", Username: "anna@contoso.com", Groups: []string{entraAdmins}}
	if where := h.client().signInEntra(); where != "/?signin_error=entra_account" {
		t.Fatalf("reused name ended at %s", where)
	}
	// A replayed or forged ID token nonce.
	idp.User.ObjectID = "0b1d0000-0000-0000-0000-000000000001"
	idp.Nonce = "forged"
	if where := h.client().signInEntra(); where != "/?signin_error=entra_unavailable" {
		t.Fatalf("wrong nonce ended at %s", where)
	}
	// A callback in a browser that did not start the sign-in.
	stranger := h.client()
	resp, err := stranger.http.Get(h.srv.URL + entra.CallbackPath + "?code=x&state=y")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if where := resp.Request.URL.RequestURI(); where != "/?signin_error=entra_state" {
		t.Fatalf("foreign callback ended at %s", where)
	}

	admin.call(http.MethodGet, "/api/settings/entra", nil, &view)
	if view.Users.Total != 2 || view.Users.Admins != 1 {
		t.Fatalf("users = %+v", view.Users)
	}

	// Turning sign-in off signs Entra users out.
	if code := admin.call(http.MethodPut, "/api/settings/entra", map[string]any{"config": map[string]any{"enabled": false}}, &view); code != 200 {
		t.Fatalf("disable = %d", code)
	}
	if code, _ := anna.me(); code != 401 {
		t.Fatalf("anna after disable = %d", code)
	}
	if where := h.client().signInEntra(); where != "/?signin_error=entra_off" {
		t.Fatalf("sign-in when off ended at %s", where)
	}
	h.st.Read(func(d *store.Data) {
		if d.Settings.Entra.ClientSecretRef == "" {
			t.Fatal("client secret reference lost")
		}
	})
}
