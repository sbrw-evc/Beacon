package app_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory/directorytest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	ldapBase  = "dc=example,dc=org"
	ldapBind  = "cn=umbrella,dc=example,dc=org"
	ldapPass  = "ldap-bind-secret"
	ldapAdmin = "cn=admins,dc=example,dc=org"
)

func ldapServer(t *testing.T) *directorytest.Server {
	return directorytest.Start(t,
		directorytest.Entry{DN: ldapBase},
		directorytest.Entry{DN: ldapBind, Password: ldapPass},
		directorytest.Entry{DN: "uid=anna,dc=example,dc=org", Password: "anna-pass-1", Attrs: map[string][]string{
			"objectClass": {"inetOrgPerson"}, "uid": {"anna"}, "cn": {"Anna Ivanova"}, "title": {"Lead engineer"}}},
		directorytest.Entry{DN: ldapAdmin, Attrs: map[string][]string{"member": {"uid=anna,dc=example,dc=org"}}},
	)
}

func ldapConfig(url string) map[string]any {
	return map[string]any{"enabled": true, "kind": "openldap", "url": url, "bind_dn": ldapBind, "base_dn": ldapBase,
		"admin_group_dn": ldapAdmin, "title_attr": "title"}
}

type directoryReply struct {
	Config          map[string]any `json:"config"`
	BindPasswordSet bool           `json:"bind_password_set"`
}

func TestDirectorySettings(t *testing.T) {
	h := newHarness(t)
	srv := ldapServer(t)
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	admin := h.client()
	admin.login("admin", "Admin-pass-2026")

	var view directoryReply
	if code := admin.call(http.MethodGet, "/api/settings/ldap", nil, &view); code != 200 || view.Config["enabled"] != false || view.BindPasswordSet {
		t.Fatalf("initial view = %d %+v", code, view)
	}

	var problem map[string]any
	if code := admin.call(http.MethodPost, "/api/settings/ldap/test", map[string]any{"config": ldapConfig(srv.URL)}, &problem); code != 400 || problem["error"] != "ldap_bind_password_required" {
		t.Fatalf("test without any password = %d %v", code, problem)
	}
	var rep map[string]any
	admin.call(http.MethodPost, "/api/settings/ldap/test", map[string]any{"config": ldapConfig(srv.URL), "bind_password": ldapPass,
		"test_username": "anna", "test_password": "anna-pass-1"}, &rep)
	if rep["ok"] != true {
		t.Fatalf("test = %v", rep)
	}
	if u := rep["probe"].(map[string]any)["user"].(map[string]any); u["admin"] != true || u["title"] != "Lead engineer" || rep["probe"].(map[string]any)["user_authenticated"] != true {
		t.Fatalf("probe user = %v", u)
	}
	admin.call(http.MethodPost, "/api/settings/ldap/test", map[string]any{"config": ldapConfig(srv.URL), "bind_password": "wrong"}, &rep)
	if rep["ok"] != false || rep["error"] == "" {
		t.Fatalf("test with a wrong password = %v", rep)
	}
	admin.call(http.MethodPost, "/api/settings/ldap/test", map[string]any{"config": ldapConfig("http://nope"), "bind_password": ldapPass}, &rep)
	if rep["ok"] != false {
		t.Fatalf("test with a bad address = %v", rep)
	}

	if code := admin.call(http.MethodPut, "/api/settings/ldap", map[string]any{"config": ldapConfig("http://nope"), "bind_password": ldapPass}, &problem); code != 400 || problem["error"] != "ldap_invalid" {
		t.Fatalf("save invalid = %d %v", code, problem)
	}
	if code := admin.call(http.MethodPut, "/api/settings/ldap", map[string]any{"config": ldapConfig(srv.URL), "bind_password": "wrong"}, &problem); code != 400 || problem["error"] != "ldap_unavailable" {
		t.Fatalf("save with a wrong password = %d %v", code, problem)
	}
	if code := admin.call(http.MethodPut, "/api/settings/ldap", map[string]any{"config": ldapConfig(srv.URL), "bind_password": ldapPass}, &view); code != 200 ||
		view.Config["enabled"] != true || !view.BindPasswordSet || view.Config["bind_password_ref"] != nil {
		t.Fatalf("save = %d %+v", code, view)
	}
	if v := h.bao.Get("umbrella/ldap"); v["bind_password"] != ldapPass {
		t.Fatalf("bind password in OpenBao = %v", v)
	}
	h.st.Read(func(d *store.Data) {
		if !strings.HasPrefix(d.Settings.LDAP.BindPasswordRef, "openbao://") || d.Settings.LDAP.URL != srv.URL {
			t.Fatalf("stored settings = %+v", d.Settings.LDAP)
		}
	})

	if admin.call(http.MethodPost, "/api/settings/ldap/test", map[string]any{"config": ldapConfig(srv.URL)}, &rep); rep["ok"] != true {
		t.Fatalf("test with the stored password = %v", rep)
	}
	moved := ldapConfig("ldap://127.0.0.1:1")
	if code := admin.call(http.MethodPost, "/api/settings/ldap/test", map[string]any{"config": moved}, &problem); code != 400 || problem["error"] != "ldap_bind_password_required" {
		t.Fatalf("stored password must not be sent to another server: %d %v", code, problem)
	}
	if code := admin.call(http.MethodPut, "/api/settings/ldap", map[string]any{"config": moved}, &problem); code != 400 || problem["error"] != "ldap_bind_password_required" {
		t.Fatalf("saving another server without a password = %d %v", code, problem)
	}
	edited := ldapConfig(srv.URL)
	edited["admin_group_dn"] = ""
	var kept directoryReply
	if code := admin.call(http.MethodPut, "/api/settings/ldap", map[string]any{"config": edited}, &kept); code != 200 || kept.Config["admin_group_dn"] != nil || !kept.BindPasswordSet {
		t.Fatalf("save keeping the stored password = %d %+v", code, kept)
	}
	admin.call(http.MethodPut, "/api/settings/ldap", map[string]any{"config": ldapConfig(srv.URL)}, &view)

	// Turning the directory off keeps the connection for later; nobody signs in with it.
	if code := admin.call(http.MethodPut, "/api/settings/ldap", map[string]any{"config": map[string]any{"enabled": false}}, &view); code != 200 || view.Config["enabled"] != false ||
		view.Config["url"] != srv.URL || !view.BindPasswordSet {
		t.Fatalf("disable = %d %+v", code, view)
	}
	if code := admin.call(http.MethodGet, "/api/settings/ldap", nil, nil); code != 200 {
		t.Fatalf("session after disable = %d", code)
	}
}
