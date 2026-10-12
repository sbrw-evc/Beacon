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

func TestServiceNetBoxTags(t *testing.T) {
	f := newNetBoxFixture(t)
	f.connect(nil)
	f.sync()
	f.h.st.Write(func(d *store.Data) {
		now := time.Now()
		d.Teams["TEAM-1"] = &model.Team{ID: "TEAM-1", Name: "Payments", CreatedAt: now, UpdatedAt: now}
	})
	db, web, pg := f.byName("srv-db-01"), f.byName("vm-web-01"), f.byName("postgres")
	local := addCI(f.h, "laptop-01", model.CIKindOther)

	var svc app.ServiceView
	if code := f.admin.call(http.MethodPost, "/api/services", app.ServiceInput{Name: "Billing", OwnerTeamID: "TEAM-1", Criticality: "critical"}, &svc); code != 201 {
		t.Fatalf("create = %d", code)
	}
	f.admin.call(http.MethodPost, "/api/services/"+svc.ID+"/cis", map[string]any{"ids": []string{db.ID, local}}, &svc)

	// Linking creates the tag and puts it on the NetBox objects already bound.
	if code := f.admin.call(http.MethodPost, "/api/services/"+svc.ID+"/netbox", nil, &svc); code != 200 || svc.NetBox == nil {
		t.Fatalf("link = %d %+v", code, svc)
	}
	slug := svc.NetBox.Slug
	if slug != "bs-svc-1" || svc.NetBox.URL == "" {
		t.Fatalf("link = %+v", svc.NetBox)
	}
	tag := f.nb.Get("extras/tags", svc.NetBox.TagID)
	if tag["name"] != "Billing" || tag["color"] != "f44336" {
		t.Fatalf("tag = %v", tag)
	}
	if got := f.nb.Tagged("dcim/devices", 1); !slices.Equal(got, []string{"prod", slug}) {
		t.Fatalf("device tags = %v", got)
	}

	// Binding a NetBox item tags it there; unbinding takes the tag off and keeps the others.
	f.admin.call(http.MethodPost, "/api/services/"+svc.ID+"/cis", map[string]any{"ids": []string{pg.ID}}, &svc)
	if got := f.nb.Tagged("ipam/services", 9); !slices.Equal(got, []string{slug}) {
		t.Fatalf("service tags = %v", got)
	}
	f.admin.call(http.MethodDelete, "/api/services/"+svc.ID+"/cis/"+pg.ID, nil, &svc)
	if got := f.nb.Tagged("ipam/services", 9); len(got) != 0 {
		t.Fatalf("service tags after unbind = %v", got)
	}

	// A sync keeps what was done here and follows tags set or removed in NetBox.
	vm := f.nb.Get("virtualization/virtual-machines", 7)
	vm["tags"] = []any{map[string]any{"id": svc.NetBox.TagID, "name": "Billing", "slug": slug}}
	f.nb.Put("virtualization/virtual-machines", vm)
	dev := f.nb.Get("dcim/devices", 1)
	dev["tags"] = []any{map[string]any{"id": 50, "name": "Prod", "slug": "prod"}}
	f.nb.Put("dcim/devices", dev)
	var st model.SyncState
	f.admin.call(http.MethodPost, "/api/netbox/sync", nil, &st)
	if st.Stats.ServiceBound != 1 || st.Stats.ServiceUnbound != 1 {
		t.Fatalf("sync stats = %+v", st.Stats)
	}
	f.admin.call(http.MethodGet, "/api/services/"+svc.ID, nil, &svc)
	if got := ciIDs(svc); !slices.Contains(got, web.ID) || slices.Contains(got, db.ID) || !slices.Contains(got, local) {
		t.Fatalf("after sync = %v", got)
	}

	// Renaming the service renames the tag.
	f.admin.call(http.MethodPut, "/api/services/"+svc.ID, app.ServiceInput{Name: "Billing v2", OwnerTeamID: "TEAM-1", Criticality: "low"}, &svc)
	if tag := f.nb.Get("extras/tags", svc.NetBox.TagID); tag["name"] != "Billing v2" || tag["color"] != "9e9e9e" {
		t.Fatalf("renamed tag = %v", tag)
	}

	// A tag deleted in NetBox unlinks the service; linking again takes over a tag with the same slug.
	f.nb.Remove("extras/tags", svc.NetBox.TagID)
	f.admin.call(http.MethodPost, "/api/netbox/sync", nil, &st)
	id := svc.ID
	svc = app.ServiceView{}
	f.admin.call(http.MethodGet, "/api/services/"+id, nil, &svc)
	if svc.NetBox != nil || st.Stats.ServiceUnlinked != 1 || len(svc.CIIDs) != 2 {
		t.Fatalf("after tag delete = %+v %+v", svc.NetBox, st.Stats)
	}
	f.nb.Put("extras/tags", map[string]any{"id": 500, "name": "Old billing", "slug": slug})
	f.admin.call(http.MethodPost, "/api/services/"+svc.ID+"/netbox", nil, &svc)
	if svc.NetBox == nil || svc.NetBox.TagID != 500 {
		t.Fatalf("relink = %+v", svc.NetBox)
	}

	// Unlinking deletes the tag and keeps the bindings.
	svc = app.ServiceView{}
	f.admin.call(http.MethodDelete, "/api/services/"+id+"/netbox", nil, &svc)
	if svc.NetBox != nil || f.nb.Get("extras/tags", 500) != nil || len(svc.CIIDs) != 2 {
		t.Fatalf("unlink = %+v", svc)
	}

	// A tag name taken by another tag gets the slug added; deleting the service deletes its tag.
	f.nb.Put("extras/tags", map[string]any{"id": 501, "name": "Billing v2", "slug": "billing"})
	f.admin.call(http.MethodPost, "/api/services/"+svc.ID+"/netbox", nil, &svc)
	if svc.NetBox == nil || svc.NetBox.Name != "Billing v2 ("+slug+")" {
		t.Fatalf("name clash = %+v", svc.NetBox)
	}
	tagID := svc.NetBox.TagID
	if code := f.admin.call(http.MethodDelete, "/api/services/"+svc.ID, nil, nil); code != http.StatusNoContent || f.nb.Get("extras/tags", tagID) != nil {
		t.Fatalf("delete = %d", code)
	}
}
