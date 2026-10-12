package app_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func TestPasswordPolicySettings(t *testing.T) {
	h := newHarness(t)
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	h.addLocal("viewer", "Viewer-pass-2026", model.RoleUser, time.Now())

	user := h.client()
	user.login("viewer", "Viewer-pass-2026")
	if code := user.call(http.MethodGet, "/api/settings/password-policy", nil, nil); code != http.StatusForbidden {
		t.Fatalf("user reading the policy = %d", code)
	}

	admin := h.client()
	admin.login("admin", "Admin-pass-2026")
	var got app.PolicySummary
	if code := admin.call(http.MethodGet, "/api/settings/password-policy", nil, &got); code != 200 || got.Policy != model.DefaultPasswordPolicy() || got.LocalUsers != 2 {
		t.Fatalf("policy = %d %+v", code, got)
	}
	var problem map[string]any
	bad := model.DefaultPasswordPolicy()
	bad.MaxAgeDays, bad.WarnDays = 10, 10
	if code := admin.call(http.MethodPut, "/api/settings/password-policy", bad, &problem); code != 400 || problem["error"] != "invalid_password_policy" {
		t.Fatalf("inconsistent policy = %d %v", code, problem)
	}
	next := model.DefaultPasswordPolicy()
	next.MinLength, next.MaxAgeDays, next.WarnDays = 14, 30, 7
	if code := admin.call(http.MethodPut, "/api/settings/password-policy", next, &got); code != 200 || got.Policy != next {
		t.Fatalf("update = %d %+v", code, got)
	}
	var meta map[string]any
	admin.call(http.MethodGet, "/api/meta", nil, &meta)
	if p := meta["password_policy"].(map[string]any); p["max_age_days"] != float64(30) || p["warn_days"] != float64(7) || p["min_length"] != float64(14) {
		t.Fatalf("meta policy = %v", p)
	}
	h.st.Read(func(d *store.Data) {
		if d.Audit[len(d.Audit)-1].Action != "settings.password_policy" {
			t.Fatalf("policy change not audited: %+v", d.Audit[len(d.Audit)-1])
		}
	})
}

func TestPasswordExpiry(t *testing.T) {
	h := newHarness(t)
	h.st.Write(func(d *store.Data) { d.Settings.Password.MaxAgeDays, d.Settings.Password.WarnDays = 30, 7 })
	opsID := h.addLocal("ops", "Ops-pass-2026!", model.RoleAdmin, time.Now().AddDate(0, 0, -40))
	h.addLocal("soon", "Soon-pass-2026!", model.RoleUser, time.Now().AddDate(0, 0, -25))
	h.addLocal("fresh", "Fresh-pass-2026!", model.RoleUser, time.Now())

	ops := h.client()
	if me := ops.login("ops", "Ops-pass-2026!"); me["password_expired"] != true || me["password_expires_at"] == nil {
		t.Fatalf("login of an expired account = %v", me)
	}
	var me map[string]any
	if code := ops.call(http.MethodGet, "/api/auth/me", nil, &me); code != 200 || me["password_expired"] != true {
		t.Fatalf("me = %d %v", code, me)
	}
	var problem map[string]any
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/settings/password-policy"},
		{http.MethodPut, "/api/auth/me/profile"},
		{http.MethodPut, "/api/settings"},
		{http.MethodGet, "/api/settings/ldap"},
	} {
		if code := ops.call(c.method, c.path, map[string]string{}, &problem); code != http.StatusForbidden || problem["error"] != "password_expired" {
			t.Fatalf("%s %s with an expired password = %d %v", c.method, c.path, code, problem)
		}
	}
	if code := ops.call(http.MethodGet, "/api/users/"+opsID+"/avatar", nil, nil); code != http.StatusNotFound {
		t.Fatalf("avatar with an expired password = %d", code)
	}
	if code := ops.call(http.MethodPut, "/api/auth/me/password", map[string]string{"current_password": "Ops-pass-2026!", "new_password": "Ops-new-pass-2026!"}, nil); code != 204 {
		t.Fatalf("change of an expired password = %d", code)
	}
	if code := ops.call(http.MethodGet, "/api/auth/me", nil, &me); code != 200 || me["password_expired"] != false || me["password_expiry_warning"] != false {
		t.Fatalf("me after change = %d %v", code, me)
	}
	if code := ops.call(http.MethodGet, "/api/settings/password-policy", nil, nil); code != 200 {
		t.Fatalf("admin page after the change = %d", code)
	}

	soon := h.client()
	if me := soon.login("soon", "Soon-pass-2026!"); me["password_expired"] != false || me["password_expiry_warning"] != true {
		t.Fatalf("expiring account = %v", me)
	}
	if code := soon.call(http.MethodPut, "/api/auth/me/preferences", map[string]string{"timezone": "UTC"}, nil); code != 200 {
		t.Fatalf("expiring account is still allowed to work, got %d", code)
	}
	fresh := h.client()
	if me := fresh.login("fresh", "Fresh-pass-2026!"); me["password_expiry_warning"] != false || me["password_expired"] != false {
		t.Fatalf("fresh account = %v", me)
	}

	var sum app.PolicySummary
	ops.call(http.MethodGet, "/api/settings/password-policy", nil, &sum)
	if sum.LocalUsers != 3 || sum.Expired != 0 || sum.Expiring != 1 {
		t.Fatalf("summary = %+v", sum)
	}

	policies := app.NewPolicyService(h.st)
	if age := policies.Age(model.User{Source: model.SourceLDAP, PasswordChangedAt: time.Now().AddDate(-5, 0, 0)}); age.Expired || age.Warning {
		t.Fatalf("directory accounts are exempt, got %+v", age)
	}
	h.st.Write(func(d *store.Data) { d.Settings.Password.MaxAgeDays, d.Settings.Password.WarnDays = 0, 0 })
	if age := policies.Age(model.User{Source: model.SourceLocal, PasswordChangedAt: time.Now().AddDate(-5, 0, 0)}); age.Expired {
		t.Fatal("no max age means passwords never expire")
	}
}
