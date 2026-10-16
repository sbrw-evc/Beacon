package alert

import (
	"testing"

	"github.com/sbrw-evc/beacon/app/backend/internal/model"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
)

// An event names its item by the Ledger source reference first, then by its ci, and
// falls back to the ip label when the name matches no item: what Current sends.
func TestResolveCISourceRefAndIP(t *testing.T) {
	st := store.New()
	st.Write(func(d *store.Data) {
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "edge-01", Source: model.SourceInventoryDB, IPs: []string{"10.0.0.1"},
			InventoryDB: &model.InventoryRef{ID: 100, Ref: "inventory-db:dcim.device:100"}}
		d.ConfigItems["CI-2"] = &model.ConfigItem{ID: "CI-2", Name: "web-01", IPs: []string{"203.0.113.11"}}
		d.ConfigItems["CI-3"] = &model.ConfigItem{ID: "CI-3", Name: "dup"}
		d.ConfigItems["CI-4"] = &model.ConfigItem{ID: "CI-4", Name: "Dup"}
	})
	w := snapshot(st)
	for _, c := range []struct {
		name   string
		labels map[string]string
		want   string
	}{
		// The reference wins over a name that means another item.
		{"web-01", map[string]string{LabelSourceRef: "Inventory-DB:dcim.device:100"}, "CI-1"},
		// An unknown reference: the name decides.
		{"web-01", map[string]string{LabelSourceRef: "inventory-db:dcim.device:7"}, "CI-2"},
		{"", map[string]string{LabelSourceRef: "inventory-db:dcim.device:100"}, "CI-1"},
		// A name the catalog does not know, or an ambiguous one: the ip label.
		{"edge-01-re0", map[string]string{LabelIP: "203.0.113.11"}, "CI-2"},
		{"dup", map[string]string{LabelIP: "10.0.0.1"}, "CI-1"},
		{"dup", nil, ""},
		{"nowhere", map[string]string{LabelIP: "not-an-ip"}, ""},
		// The name matches: the ip label does not move the event.
		{"web-01", map[string]string{LabelIP: "10.0.0.1"}, "CI-2"},
	} {
		got := ""
		if ci := w.resolve(c.name, c.labels); ci != nil {
			got = ci.ID
		}
		if got != c.want {
			t.Errorf("resolve(%q, %v) = %q, want %q", c.name, c.labels, got, c.want)
		}
	}
}
