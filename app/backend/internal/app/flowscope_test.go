package app_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type flowScope struct {
	Username string `json:"username"`
	Known    bool   `json:"known"`
	Mode     string `json:"mode"`
	CIs      []struct {
		ID         string   `json:"id"`
		Name       string   `json:"name"`
		SourceRefs []string `json:"source_refs"`
		IPs        []string `json:"ips"`
	} `json:"cis"`
	Services []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"services"`
}

func TestFlowScope(t *testing.T) {
	h := newHarness(t)
	admin, _, token := flowAccount(t, h)
	now := time.Now().UTC()
	h.st.Write(func(d *store.Data) {
		d.Teams["T-1"] = &model.Team{ID: "T-1", Name: "Billing team"}
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "app-01", IPs: []string{"10.0.0.5"},
			InventoryDB: &model.InventoryRef{ID: 100, Ref: "inventory-db:dcim.device:100"}}
		d.ConfigItems["CI-2"] = &model.ConfigItem{ID: "CI-2", Name: "db-01", IPs: []string{"10.0.1.9"}}
		d.ConfigItems["CI-3"] = &model.ConfigItem{ID: "CI-3", Name: "hr-01"}
		d.Services["SVC-1"] = &model.Service{ID: "SVC-1", Name: "Billing", OwnerTeamID: "T-1", Status: model.ServiceActive, CIIDs: []string{"CI-1", "CI-2"}}
		d.Services["SVC-2"] = &model.Service{ID: "SVC-2", Name: "HR", Status: model.ServiceActive, CIIDs: []string{"CI-3"}}
		d.Services["SVC-3"] = &model.Service{ID: "SVC-3", Name: "Old", OwnerTeamID: "T-1", Status: model.ServiceRetired, CIIDs: []string{"CI-3"}}
		add := func(id, name, mode string, teams, services []string, disabled bool) {
			d.Users[id] = &model.User{ID: id, Username: name, Source: model.SourceLDAP, Role: "viewer", ScopeMode: mode, TeamIDs: teams, ServiceIDs: services, Disabled: disabled, CreatedAt: now}
		}
		add("U-1", "Ivanov", model.ScopeTeams, []string{"T-1"}, nil, false)
		add("U-2", "petrov", model.ScopeServices, nil, []string{"SVC-2"}, false)
		add("U-3", "sidorov", model.ScopeAll, nil, nil, false)
		add("U-4", "locked", model.ScopeAll, nil, nil, true)
		d.Users["U-5"] = &model.User{ID: "U-5", Username: "boss", Source: model.SourceLocal, Role: model.RoleAdmin, ScopeMode: model.ScopeServices, ServiceIDs: []string{"SVC-2"}}
	})
	get := func(name string) flowScope {
		t.Helper()
		var out flowScope
		if code := h.bearer(token, http.MethodGet, "/api/integrations/flow/scope?username="+name, nil, &out); code != http.StatusOK {
			t.Fatalf("scope %s = %d", name, code)
		}
		return out
	}

	// Teams: the services the user's teams own (not retired) and their items; login ignores case.
	s := get("ivanov")
	if !s.Known || s.Mode != "teams" || s.Username != "Ivanov" || len(s.Services) != 1 || s.Services[0].ID != "SVC-1" || len(s.CIs) != 2 {
		t.Fatalf("teams = %+v", s)
	}
	if s.CIs[0].Name != "app-01" || s.CIs[0].SourceRefs[0] != "inventory-db:dcim.device:100" || s.CIs[0].IPs[0] != "10.0.0.5" ||
		s.CIs[1].Name != "db-01" || len(s.CIs[1].SourceRefs) != 0 {
		t.Fatalf("teams items = %+v", s.CIs)
	}
	// Chosen services are "selected".
	if s = get("petrov"); s.Mode != "selected" || len(s.Services) != 1 || s.Services[0].Name != "HR" || len(s.CIs) != 1 || s.CIs[0].ID != "CI-3" {
		t.Fatalf("selected = %+v", s)
	}
	// All services and administrators: empty lists that mean everything.
	for _, name := range []string{"sidorov", "boss"} {
		if s = get(name); s.Mode != "all" || len(s.CIs) != 0 || len(s.Services) != 0 {
			t.Fatalf("%s = %+v", name, s)
		}
	}
	// A locked user sees nothing; an unknown login is not known.
	if s = get("locked"); !s.Known || s.Mode != "selected" || len(s.CIs) != 0 {
		t.Fatalf("locked = %+v", s)
	}
	if s = get("ghost"); s.Known {
		t.Fatalf("unknown = %+v", s)
	}
	if code := h.bearer(token, http.MethodGet, "/api/integrations/flow/scope", nil, nil); code != http.StatusBadRequest {
		t.Fatalf("no username = %d", code)
	}
	// Without integrations.flow:read: forbidden.
	h.addLocal("viewer1", "ViewerPassw0rd!", "viewer", now)
	h.st.Write(func(d *store.Data) {
		d.Roles["viewer"] = &model.Role{ID: "viewer", Name: "Viewer", Permissions: []string{"cmdb:view"}}
	})
	v := h.client()
	v.login("viewer1", "ViewerPassw0rd!")
	if code := v.call(http.MethodGet, "/api/integrations/flow/scope?username=ivanov", nil, nil); code != http.StatusForbidden {
		t.Fatalf("viewer = %d", code)
	}
	// An administrator session may read it too.
	var out flowScope
	if code := admin.call(http.MethodGet, "/api/integrations/flow/scope?username=ivanov", nil, &out); code != http.StatusOK || out.Mode != "teams" {
		t.Fatalf("admin = %d %+v", code, out)
	}
}
