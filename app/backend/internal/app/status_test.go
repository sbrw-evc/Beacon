package app_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

func TestSystemStatusIsDetailed(t *testing.T) {
	h := newHarness(t)
	h.addLocal("admin", "Admin-password-1", model.RoleAdmin, time.Now())
	c := h.client()
	c.login("admin", "Admin-password-1")

	var s struct {
		Build struct {
			Version   string `json:"version"`
			GoVersion string `json:"go_version"`
			Platform  string `json:"platform"`
		} `json:"build"`
		Runtime struct {
			Uptime     int64 `json:"uptime_seconds"`
			PID        int   `json:"pid"`
			Goroutines int   `json:"goroutines"`
		} `json:"runtime"`
		OpenBao struct {
			Configured bool `json:"configured"`
		} `json:"openbao"`
		Postgres struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"postgres"`
		Intake struct {
			Ready bool `json:"ready"`
		} `json:"intake"`
		Inventory struct {
			Users    map[string]int `json:"users"`
			Sessions int            `json:"sessions"`
		} `json:"inventory"`
	}
	if code := c.call(http.MethodGet, "/api/system", nil, &s); code != http.StatusOK {
		t.Fatalf("GET /api/system = %d", code)
	}
	if s.Build.Version != "test" || s.Build.GoVersion == "" || s.Build.Platform == "" || s.Runtime.PID == 0 || s.Runtime.Goroutines == 0 {
		t.Fatalf("build and runtime: %+v %+v", s.Build, s.Runtime)
	}
	if !s.OpenBao.Configured || s.Postgres.OK || s.Postgres.Error == "" || s.Intake.Ready {
		t.Fatalf("without PostgreSQL the status says so: %+v %+v %+v", s.OpenBao, s.Postgres, s.Intake)
	}
	if s.Inventory.Users[model.SourceLocal] != 1 || s.Inventory.Sessions != 1 {
		t.Fatalf("inventory: %+v", s.Inventory)
	}
}
