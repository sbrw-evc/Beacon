package app_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func TestAlertPolicySettings(t *testing.T) {
	h := newHarness(t)
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	admin := h.client()
	admin.login("admin", "Admin-pass-2026")

	var v app.AlertPolicyView
	if code := admin.call(http.MethodGet, "/api/alerting/policy", nil, &v); code != http.StatusOK {
		t.Fatalf("get = %d", code)
	}
	if v.Policy.ReopenWindowSeconds != 0 || v.Defaults.ReopenWindowSeconds != 600 || v.Defaults.FallbackDelaySeconds != 120 ||
		v.Defaults.FallbackRetrySeconds != 300 || v.Defaults.RetentionDays != 90 || v.Defaults.TestLifetimeSeconds != 300 {
		t.Fatalf("view = %+v", v)
	}
	if code := admin.call(http.MethodPut, "/api/alerting/policy", map[string]any{"reopen_window_seconds": -1}, nil); code != http.StatusBadRequest {
		t.Fatalf("negative window = %d", code)
	}
	if code := admin.call(http.MethodPut, "/api/alerting/policy", map[string]any{"retention_days": 100000}, nil); code != http.StatusBadRequest {
		t.Fatalf("too long retention = %d", code)
	}
	if code := admin.call(http.MethodPut, "/api/alerting/policy", map[string]any{"reopen_window_seconds": 1800, "retention_days": 30}, &v); code != http.StatusOK {
		t.Fatalf("put = %d", code)
	}
	if v.Policy.ReopenWindowSeconds != 1800 || v.Policy.RetentionDays != 30 || v.Policy.FallbackDelaySeconds != 0 || v.Policy.UpdatedBy != "admin" {
		t.Fatalf("saved = %+v", v.Policy)
	}
	var stored *model.AlertPolicy
	h.st.Read(func(d *store.Data) { stored = d.Settings.Alerting.Policy })
	if stored == nil || stored.ReopenWindowSeconds != 1800 {
		t.Fatalf("stored = %+v", stored)
	}
}
