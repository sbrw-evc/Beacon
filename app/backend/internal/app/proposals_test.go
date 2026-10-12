package app_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type proposalView struct {
	Key      string                 `json:"key"`
	Status   string                 `json:"status"`
	New      bool                   `json:"new"`
	Evidence model.ProposalEvidence `json:"evidence"`
	FromCI   *struct {
		ID string `json:"id"`
	} `json:"from_ci"`
	ToCI *struct {
		ID string `json:"id"`
	} `json:"to_ci"`
	Dependencies []struct {
		ServiceID   string `json:"service_id"`
		DependsOnID string `json:"depends_on_id"`
		Exists      bool   `json:"exists"`
	} `json:"dependencies"`
}

type proposalResult struct {
	Accepted int `json:"accepted"`
	Updated  int `json:"updated"`
	Ignored  int `json:"ignored"`
}

func proposal(key string, from, to map[string]any, bytes int64) map[string]any {
	return map[string]any{"key": key, "from": from, "to": to, "type": "depends_on",
		"evidence": map[string]any{"protocol": "tcp", "port": 5432, "app": "postgres", "bytes_7d": bytes, "flows_7d": 4200, "days_seen": 7,
			"first_seen": "2026-10-05T10:00:00Z", "last_seen": "2026-10-12T09:00:00Z", "flow_url": "https://flow.corp/visualize?q=x", "future_field": 1},
		"future": true}
}

func TestDependencyProposals(t *testing.T) {
	h := newHarness(t)
	admin, _, token := flowAccount(t, h)
	h.st.Write(func(d *store.Data) {
		d.Teams["T-1"] = &model.Team{ID: "T-1", Name: "Ops"}
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "app-01", IPs: []string{"10.0.0.5"}, InventoryDB: &model.InventoryRef{Ref: "inventory-db:dcim.device:100"}}
		d.ConfigItems["CI-2"] = &model.ConfigItem{ID: "CI-2", Name: "db-01", IPs: []string{"10.0.1.9"}}
		d.ConfigItems["CI-3"] = &model.ConfigItem{ID: "CI-3", Name: "cache-01", IPs: []string{"10.0.2.1"}}
		d.Services["SVC-1"] = &model.Service{ID: "SVC-1", Name: "Billing", OwnerTeamID: "T-1", Status: model.ServiceActive, Criticality: model.CriticalityHigh, CIIDs: []string{"CI-1"}}
		d.Services["SVC-2"] = &model.Service{ID: "SVC-2", Name: "Database", OwnerTeamID: "T-1", Status: model.ServiceActive, Criticality: model.CriticalityHigh, CIIDs: []string{"CI-2"}}
		d.Services["SVC-3"] = &model.Service{ID: "SVC-3", Name: "Cache", OwnerTeamID: "T-1", Status: model.ServiceActive, Criticality: model.CriticalityLow, CIIDs: []string{"CI-3"}}
	})
	post := func(ps ...map[string]any) proposalResult {
		t.Helper()
		var out proposalResult
		body := map[string]any{"source": "umbrella-flow", "generated_at": "2026-10-12T10:00:00Z", "proposals": ps}
		if code := h.bearer(token, http.MethodPost, "/api/cmdb/proposals", body, &out); code != http.StatusOK {
			t.Fatalf("post = %d", code)
		}
		return out
	}
	list := func(all bool) []proposalView {
		t.Helper()
		var out []proposalView
		path := "/api/cmdb/proposals"
		if all {
			path += "?status=all"
		}
		if code := admin.call(http.MethodGet, path, nil, &out); code != http.StatusOK {
			t.Fatalf("list = %d", code)
		}
		return out
	}
	const k1 = "flow:inventory-db:dcim.device:100->db-01:tcp/5432"
	const k2 = "flow:10.0.0.5->10.0.2.1:tcp/6379"
	// Ends resolve by source_ref, by name and by IP; unknown ends, one item on both ends and
	// malformed entries are ignored.
	got := post(
		proposal(k1, map[string]any{"source_ref": "inventory-db:dcim.device:100", "name": "renamed"}, map[string]any{"name": "db-01"}, 1000),
		proposal(k2, map[string]any{"ip": "10.0.0.5"}, map[string]any{"ip": "10.0.2.1"}, 500),
		proposal("flow:x", map[string]any{"name": "app-01"}, map[string]any{"name": "nowhere"}, 1),
		proposal("flow:self", map[string]any{"name": "app-01"}, map[string]any{"ip": "10.0.0.5"}, 1),
		proposal("", map[string]any{"name": "app-01"}, map[string]any{"name": "db-01"}, 1),
		map[string]any{"key": "flow:t", "type": "calls", "from": map[string]any{"name": "app-01"}, "to": map[string]any{"name": "db-01"}},
	)
	if got != (proposalResult{Accepted: 2, Ignored: 4}) {
		t.Fatalf("first post = %+v", got)
	}
	ps := list(false)
	if len(ps) != 2 || ps[0].Key != k1 || ps[0].FromCI.ID != "CI-1" || ps[0].ToCI.ID != "CI-2" || !ps[0].New ||
		len(ps[0].Dependencies) != 1 || ps[0].Dependencies[0].ServiceID != "SVC-1" || ps[0].Dependencies[0].DependsOnID != "SVC-2" ||
		ps[0].Evidence.Port != 5432 || ps[0].Evidence.FlowURL == "" {
		t.Fatalf("list = %+v", ps)
	}
	// Upsert by key: the evidence is refreshed.
	if got = post(proposal(k1, map[string]any{"source_ref": "inventory-db:dcim.device:100"}, map[string]any{"name": "db-01"}, 2000)); got != (proposalResult{Updated: 1}) {
		t.Fatalf("second post = %+v", got)
	}
	if ps = list(false); ps[0].Evidence.Bytes7d != 2000 {
		t.Fatalf("refreshed = %+v", ps[0])
	}

	// Accept (services:edit) creates the service dependency.
	if code := h.bearer(token, http.MethodPost, "/api/cmdb/proposals/accept", map[string]string{"key": k1}, nil); code != http.StatusForbidden {
		t.Fatalf("accept without services:edit = %d", code)
	}
	var accepted proposalView
	if code := admin.call(http.MethodPost, "/api/cmdb/proposals/accept", map[string]string{"key": k1}, &accepted); code != http.StatusOK || accepted.Status != "accepted" {
		t.Fatalf("accept = %d %+v", code, accepted)
	}
	h.st.Read(func(d *store.Data) {
		if !slices.Contains(d.Services["SVC-1"].DependsOn, "SVC-2") {
			t.Fatalf("no dependency: %v", d.Services["SVC-1"].DependsOn)
		}
	})
	if ps = list(false); len(ps) != 1 || ps[0].Key != k2 {
		t.Fatalf("pending after accept = %+v", ps)
	}
	// A refresh keeps an accepted proposal accepted.
	post(proposal(k1, map[string]any{"source_ref": "inventory-db:dcim.device:100"}, map[string]any{"name": "db-01"}, 3000))
	if ps = list(false); len(ps) != 1 {
		t.Fatalf("accepted came back: %+v", ps)
	}

	// Reject hides the proposal until the evidence grows ten times.
	if code := admin.call(http.MethodPost, "/api/cmdb/proposals/reject", map[string]string{"key": k2}, nil); code != http.StatusOK {
		t.Fatalf("reject = %d", code)
	}
	post(proposal(k2, map[string]any{"ip": "10.0.0.5"}, map[string]any{"ip": "10.0.2.1"}, 4000))
	if ps = list(false); len(ps) != 0 {
		t.Fatalf("rejected came back at 8x: %+v", ps)
	}
	if all := list(true); len(all) != 2 {
		t.Fatalf("status=all = %+v", all)
	}
	post(proposal(k2, map[string]any{"ip": "10.0.0.5"}, map[string]any{"ip": "10.0.2.1"}, 5000))
	if ps = list(false); len(ps) != 1 || ps[0].Key != k2 || ps[0].Status != "pending" {
		t.Fatalf("rejected did not come back at 10x: %+v", ps)
	}

	// A dependency that would close a cycle is refused; one without services is unresolved.
	h.st.Write(func(d *store.Data) { d.Services["SVC-3"].DependsOn = []string{"SVC-1"} })
	if code := admin.call(http.MethodPost, "/api/cmdb/proposals/accept", map[string]string{"key": k2}, nil); code != http.StatusBadRequest {
		t.Fatalf("cycle = %d", code)
	}
	h.st.Write(func(d *store.Data) { d.Services["SVC-3"].CIIDs = nil })
	var e map[string]any
	if code := admin.call(http.MethodPost, "/api/cmdb/proposals/accept", map[string]string{"key": k2}, &e); code != http.StatusConflict || e["error"] != "proposal_unresolved" {
		t.Fatalf("unresolved = %d %v", code, e)
	}
	if code := admin.call(http.MethodPost, "/api/cmdb/proposals/accept", map[string]string{"key": "nope"}, nil); code != http.StatusNotFound {
		t.Fatalf("unknown key = %d", code)
	}

	// Proposals not refreshed for 14 days expire.
	h.st.Write(func(d *store.Data) { d.Proposals[k2].RefreshedAt = time.Now().Add(-15 * 24 * time.Hour) })
	if all := list(true); len(all) != 1 || all[0].Key != k1 {
		t.Fatalf("after expiry = %+v", all)
	}

	// cmdb:propose is needed to post.
	if code := admin.call(http.MethodPost, "/api/cmdb/proposals", map[string]any{"proposals": []any{}}, nil); code != http.StatusOK {
		t.Fatalf("admin post = %d", code)
	}
	h.addLocal("viewer1", "ViewerPassw0rd!", "viewer", time.Now())
	h.st.Write(func(d *store.Data) { d.Roles["viewer"] = &model.Role{ID: "viewer", Name: "Viewer", Permissions: []string{"cmdb:view"}} })
	v := h.client()
	v.login("viewer1", "ViewerPassw0rd!")
	if code := v.call(http.MethodPost, "/api/cmdb/proposals", map[string]any{"proposals": []any{}}, nil); code != http.StatusForbidden {
		t.Fatalf("viewer post = %d", code)
	}
}
