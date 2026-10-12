package app_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func addCI(h *harness, name, kind string) string {
	var id string
	h.st.Write(func(d *store.Data) {
		id = d.NextID("CI")
		d.ConfigItems[id] = &model.ConfigItem{ID: id, Name: name, Kind: kind, Status: model.CIStatusActive, Source: model.SourceLocal}
	})
	return id
}

func ciIDs(v app.ServiceView) []string {
	out := []string{}
	for _, ci := range v.CIs {
		out = append(out, ci.ID)
	}
	return out
}

func TestServiceBindings(t *testing.T) {
	f := newServiceFixture(t)
	db, web := addCI(f.h, "srv-db-01", model.CIKindDevice), addCI(f.h, "vm-web-01", model.CIKindVM)
	billing := f.create(app.ServiceInput{Name: "Billing", OwnerTeamID: "TEAM-3"})
	shop := f.create(app.ServiceInput{Name: "Shop", OwnerTeamID: "TEAM-3"})

	var v app.ServiceView
	if code := f.admin.call(http.MethodPost, "/api/services/"+billing.ID+"/cis", map[string]any{"ids": []string{db, web, db}}, &v); code != 200 {
		t.Fatalf("bind = %d", code)
	}
	if got := ciIDs(v); !slices.Equal(got, []string{db, web}) || v.CIs[0].Name != "srv-db-01" {
		t.Fatalf("bound = %+v", v.CIs)
	}
	// Binding again changes nothing; an unknown item is refused.
	f.admin.call(http.MethodPost, "/api/services/"+billing.ID+"/cis", map[string]any{"ids": []string{web}}, &v)
	if len(v.CIIDs) != 2 {
		t.Fatalf("rebind = %v", v.CIIDs)
	}
	f.expect(http.MethodPost, "/api/services/"+billing.ID+"/cis", map[string]any{"ids": []string{"CI-404"}}, http.StatusBadRequest, "unknown_ci")
	f.expect(http.MethodPost, "/api/services/SVC-404/cis", map[string]any{"ids": []string{db}}, http.StatusNotFound, "")

	// The item lists the services it belongs to.
	var ci app.CIView
	f.admin.call(http.MethodGet, "/api/cis/"+db, nil, &ci)
	if len(ci.Services) != 1 || ci.Services[0].Name != "Billing" {
		t.Fatalf("ci services = %+v", ci.Services)
	}

	// An edit keeps the bindings.
	f.admin.call(http.MethodPut, "/api/services/"+billing.ID, app.ServiceInput{Name: "Billing 2", OwnerTeamID: "TEAM-3"}, &v)
	if len(v.CIIDs) != 2 {
		t.Fatalf("after edit = %v", v.CIIDs)
	}

	if code := f.admin.call(http.MethodDelete, "/api/services/"+billing.ID+"/cis/"+web, nil, &v); code != 200 || !slices.Equal(v.CIIDs, []string{db}) {
		t.Fatalf("unbind = %d %v", code, v.CIIDs)
	}

	// Dependencies are bound and unbound one by one, with the cycle check of an edit.
	if code := f.admin.call(http.MethodPost, "/api/services/"+shop.ID+"/dependencies", map[string]any{"ids": []string{billing.ID}}, &v); code != 200 ||
		len(v.Depends) != 1 {
		t.Fatalf("depend = %d %+v", code, v.Depends)
	}
	f.expect(http.MethodPost, "/api/services/"+billing.ID+"/dependencies", map[string]any{"ids": []string{shop.ID}}, http.StatusBadRequest, "dependency_cycle")
	f.expect(http.MethodPost, "/api/services/"+shop.ID+"/dependencies", map[string]any{"ids": []string{shop.ID}}, http.StatusBadRequest, "dependency_self")
	if code := f.admin.call(http.MethodDelete, "/api/services/"+shop.ID+"/dependencies/"+billing.ID, nil, &v); code != 200 || len(v.DependsOn) != 0 {
		t.Fatalf("undepend = %d %v", code, v.DependsOn)
	}

	// Deleting an item removes it from the services.
	f.expect(http.MethodDelete, "/api/cis/"+db, nil, http.StatusNoContent, "")
	f.admin.call(http.MethodGet, "/api/services/"+billing.ID, nil, &v)
	if len(v.CIIDs) != 0 {
		t.Fatalf("after ci delete = %v", v.CIIDs)
	}

	// Linking to NetBox needs a NetBox connection.
	f.expect(http.MethodPost, "/api/services/"+billing.ID+"/netbox", nil, http.StatusConflict, "netbox_off")
	f.cleanup()
}

func TestServiceBindingAccess(t *testing.T) {
	f := newServiceFixture(t)
	defer f.cleanup()
	db := addCI(f.h, "srv-db-01", model.CIKindDevice)
	svc := f.create(app.ServiceInput{Name: "Billing", OwnerTeamID: "TEAM-3"})
	f.h.st.Write(func(d *store.Data) {
		d.Roles["viewer"] = &model.Role{ID: "viewer", Name: "Viewer", Permissions: []string{"services:view", "cmdb:view"}}
	})
	f.h.addLocal("viewer", "Viewer-pass-2026", "viewer", time.Now())
	viewer := f.h.client()
	viewer.login("viewer", "Viewer-pass-2026")
	if code := viewer.call(http.MethodPost, "/api/services/"+svc.ID+"/cis", map[string]any{"ids": []string{db}}, nil); code != http.StatusForbidden {
		t.Fatalf("viewer bind = %d", code)
	}
	if code := viewer.call(http.MethodPost, "/api/services/"+svc.ID+"/netbox", nil, nil); code != http.StatusForbidden {
		t.Fatalf("viewer link = %d", code)
	}
	var m app.CMDBMap
	if code := viewer.call(http.MethodGet, "/api/cmdb", nil, &m); code != 200 || len(m.Services) != 1 || m.Events.Available {
		t.Fatalf("viewer map = %d %+v", code, m)
	}
}

func TestCMDBMap(t *testing.T) {
	f := newServiceFixture(t)
	defer f.cleanup()
	db, web := addCI(f.h, "srv-db-01", model.CIKindDevice), addCI(f.h, "vm-web-01", model.CIKindVM)
	billing := f.create(app.ServiceInput{Name: "Billing", OwnerTeamID: "TEAM-3"})
	shop := f.create(app.ServiceInput{Name: "Shop", OwnerTeamID: "TEAM-3", DependsOn: []string{billing.ID}})
	f.create(app.ServiceInput{Name: "Empty", OwnerTeamID: "TEAM-3"})
	f.admin.call(http.MethodPost, "/api/services/"+billing.ID+"/cis", map[string]any{"ids": []string{db}}, nil)
	f.admin.call(http.MethodPost, "/api/services/"+shop.ID+"/cis", map[string]any{"ids": []string{web}}, nil)
	f.h.st.Write(func(d *store.Data) { d.ConfigItems[db].Status = model.CIStatusFailed })

	var m app.CMDBMap
	if code := f.admin.call(http.MethodGet, "/api/cmdb", nil, &m); code != 200 {
		t.Fatalf("map = %d", code)
	}
	if len(m.Services) != 3 || len(m.CIs) != 2 {
		t.Fatalf("map = %+v", m)
	}
	level := map[string]string{}
	for _, s := range m.Services {
		level[s.Name] = s.Health.Level
	}
	for _, c := range m.CIs {
		level[c.Name] = c.Health.Level
	}
	want := map[string]string{"Billing": "critical", "Shop": "warning", "Empty": "unknown", "srv-db-01": "critical", "vm-web-01": "ok"}
	for k, v := range want {
		if level[k] != v {
			t.Errorf("%s = %s, want %s", k, level[k], v)
		}
	}
}
