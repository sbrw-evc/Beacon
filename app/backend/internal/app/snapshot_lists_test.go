package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// memBackend gives back one saved snapshot, as the state store does after a restart.
type memBackend struct{ snap []byte }

func (b *memBackend) Kind() string                              { return "memory" }
func (b *memBackend) Where() string                             { return "test" }
func (b *memBackend) Load(context.Context) ([][]byte, error)    { return [][]byte{b.snap}, nil }
func (b *memBackend) Save(_ context.Context, data []byte) error { b.snap = data; return nil }
func (b *memBackend) Close()                                    {}

// restarted is a store loaded from a snapshot of before: what Umbrella has after a restart.
func restarted(t *testing.T, before func(d *store.Data)) *store.Store {
	t.Helper()
	old := store.New()
	old.Write(before)
	snap, err := old.Export()
	if err != nil {
		t.Fatal(err)
	}
	st := store.New()
	if ok, err := st.Attach(context.Background(), &memBackend{snap: snap}); err != nil || !ok {
		t.Fatalf("attach: %v %v", ok, err)
	}
	return st
}

// rawGet returns the body of a GET as text.
func rawGet(t *testing.T, c *client, path string) string {
	t.Helper()
	var raw json.RawMessage
	if code := c.call(http.MethodGet, path, nil, &raw); code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, code, raw)
	}
	return string(raw)
}

// The snapshot (gob) does not keep empty lists. A Prometheus target named by DNS has no IP
// addresses and a Zabbix host may have no groups; after a restart those came back as null and
// the monitoring page failed on [...h.ips].
func TestListsSurviveSnapshotAsEmptyArrays(t *testing.T) {
	now := time.Now().UTC()
	st := restarted(t, func(d *store.Data) {
		d.MonitoringSources["MON-1"] = &model.MonitoringSource{ID: "MON-1", Name: "Prometheus", Kind: model.MonitoringPrometheus, URL: "http://prom", Enabled: true,
			Hosts: []model.MonitoringHost{
				{Key: "web.corp.local", Host: "web.corp.local", IPs: []string{}, DNS: []string{"web.corp.local"}, Groups: []string{"node"}, State: model.HostUp},
				{Key: "10.0.0.9", Host: "10.0.0.9", IPs: []string{"10.0.0.9"}, DNS: []string{}, Groups: []string{}, State: model.HostDown},
				{Key: "lab", Host: "lab", IPs: []string{}, DNS: []string{}, Groups: []string{}, State: model.HostUnknown},
			},
			Links: map[string]string{"lab": model.HostNoCI},
			Sync:  model.MonitoringSync{StartedAt: now, FinishedAt: now, OK: true, Hosts: 3}}
		// A source that was never read.
		d.MonitoringSources["MON-2"] = &model.MonitoringSource{ID: "MON-2", Name: "Zabbix", Kind: model.MonitoringZabbix, URL: "http://zbx", Hosts: []model.MonitoringHost{}}
		d.Maintenance["MW-1"] = &model.Maintenance{ID: "MW-1", Title: "Patch", CIIDs: []string{}, ServiceIDs: []string{"S-1"}, Start: now, End: now.Add(time.Hour)}
	})
	h := newHarnessWith(t, st)
	h.addLocal("admin", "Admin-pass-2026", model.RoleAdmin, time.Now())
	c := h.client()
	c.login("admin", "Admin-pass-2026")

	body := rawGet(t, c, "/api/monitoring/hosts")
	if strings.Contains(body, "null") {
		t.Errorf("hosts have null lists: %s", body)
	}
	var list app.HostList
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 3 || list.Summary != (app.HostSummary{Total: 3, Unmatched: 2, Excluded: 1}) {
		t.Errorf("hosts: %+v", list)
	}
	for _, v := range list.Items {
		if v.IPs == nil || v.DNS == nil || v.Groups == nil {
			t.Errorf("host %s: %+v", v.Key, v)
		}
		if v.Key != "lab" && v.Match != "" {
			t.Errorf("an unmatched host has no match kind: %+v", v)
		}
	}
	// The unmatched filter returns the hosts without a match kind.
	var un app.HostList
	c.call(http.MethodGet, "/api/monitoring/hosts?match=unmatched", nil, &un)
	if len(un.Items) != 2 || un.Summary.Unmatched != 2 {
		t.Errorf("unmatched: %+v", un)
	}

	body = rawGet(t, c, "/api/monitoring")
	if strings.Contains(body, "null") {
		t.Errorf("monitoring view has null: %s", body)
	}
	var view app.MonitoringView
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatal(err)
	}
	// A host said to be no item is not counted as unmatched, as in the host list.
	if len(view.Sources) != 2 || view.Sources[0].Hosts != 3 || view.Sources[0].Unmatched != 2 || view.Sources[0].Matched != 0 || view.Sources[1].Hosts != 0 {
		t.Errorf("sources: %+v", view.Sources)
	}

	if body = rawGet(t, c, "/api/maintenance"); strings.Contains(body, "null") {
		t.Errorf("maintenance has null lists: %s", body)
	}
}
