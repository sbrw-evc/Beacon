package app_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type refsReply struct {
	Roles    []map[string]any `json:"roles"`
	Teams    []map[string]any `json:"teams"`
	Users    []map[string]any `json:"users"`
	Services []map[string]any `json:"services"`
}

func TestRefsFollowCallerPermissions(t *testing.T) {
	h := newHarness(t)
	h.st.Write(func(d *store.Data) {
		d.EnsureSystemRoles(time.Now())
		d.Services["SVC-1"] = &model.Service{ID: "SVC-1", Name: "Billing"}
	})
	h.addTeam("TEAM-1", "")
	h.addRole("people", "users:view")
	h.addRole("oncall", "incidents:view")
	h.addRole("alerting", "settings.alerting:view")
	h.addLocal("plain", "Plain-pass-2026-x", model.RoleUser, time.Now())
	h.addLocal("hr", "Hr-pass-2026-xx", "people", time.Now())
	h.addLocal("duty", "Duty-pass-2026-x", "oncall", time.Now())
	h.addLocal("pd", "Pd-pass-2026-xxx", "alerting", time.Now())
	h.addLocal("root", adminPass, model.RoleAdmin, time.Now())

	get := func(username, password string) refsReply {
		t.Helper()
		c := h.client()
		c.login(username, password)
		var out refsReply
		if code := c.call(http.MethodGet, "/api/refs", nil, &out); code != http.StatusOK {
			t.Fatalf("refs as %s = %d", username, code)
		}
		if out.Roles == nil || out.Teams == nil || out.Users == nil || out.Services == nil {
			t.Fatalf("refs as %s has null sections: %+v", username, out)
		}
		return out
	}
	sizes := func(r refsReply) [4]int { return [4]int{len(r.Users), len(r.Roles), len(r.Teams), len(r.Services)} }

	if got := sizes(get("plain", "Plain-pass-2026-x")); got != [4]int{} {
		t.Fatalf("plain user refs users/roles/teams/services = %v, want all empty", got)
	}
	if got := get("hr", "Hr-pass-2026-xx"); len(got.Users) != 5 || len(got.Roles) == 0 || len(got.Teams) != 1 || len(got.Services) != 0 {
		t.Fatalf("users:view refs = %v", sizes(got))
	}
	if got := sizes(get("duty", "Duty-pass-2026-x")); got != [4]int{0, 0, 1, 0} {
		t.Fatalf("incidents:view refs users/roles/teams/services = %v, want only teams", got)
	}
	if got := sizes(get("pd", "Pd-pass-2026-xxx")); got != [4]int{0, 0, 1, 1} {
		t.Fatalf("settings.alerting:view refs = %v, want teams and services", got)
	}
	if got := get("root", adminPass); len(got.Users) != 5 || len(got.Roles) == 0 || len(got.Teams) != 1 || len(got.Services) != 1 {
		t.Fatalf("admin refs = %v", sizes(got))
	}
}
