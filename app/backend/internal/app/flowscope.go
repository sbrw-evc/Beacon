package app

import (
	"net/http"
	"slices"
	"strings"

	"github.com/sbrw-evc/beacon/app/backend/internal/httpx"
	"github.com/sbrw-evc/beacon/app/backend/internal/model"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
)

// Scope modes as Current names them (docs/integrations.md §3.2).
const (
	flowScopeAll      = "all"
	flowScopeTeams    = "teams"
	flowScopeSelected = "selected"
)

// FlowScopeCI is a configuration item a user of Current may see the traffic of.
type FlowScopeCI struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	SourceRefs []string `json:"source_refs"`
	IPs        []string `json:"ips"`
}

type FlowScopeService struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// FlowScope is the visibility scope of a user for Current: everything, or the business
// services of the scope and the configuration items bound to them.
type FlowScope struct {
	Username string             `json:"username"`
	Known    bool               `json:"known"`
	Mode     string             `json:"mode,omitempty"`
	CIs      []FlowScopeCI      `json:"cis"`
	Services []FlowScopeService `json:"services"`
}

func (a *App) registerFlowScope(mux *http.ServeMux) {
	a.route(mux, "GET /api/integrations/flow/scope", "integrations.flow:read", func(w http.ResponseWriter, r *http.Request) {
		username := strings.TrimSpace(r.URL.Query().Get("username"))
		if username == "" {
			writeError(w, invalid("username_required", nil))
			return
		}
		var out FlowScope
		a.deps.Store.Read(func(d *store.Data) { out = flowScopeOf(d, username) })
		httpx.JSON(w, http.StatusOK, out)
	})
}

// flowScopeOf: users are matched by login, ignoring case. An unknown login is known: false. A
// locked user sees nothing (mode selected, no items). Administrators and the "all services"
// mode see everything: empty lists that mean all. Otherwise the services of the scope (the
// user's teams own or support them, or they were chosen) and every item bound to one of them;
// retired services and their items are left out.
func flowScopeOf(d *store.Data, username string) FlowScope {
	out := FlowScope{Username: username, CIs: []FlowScopeCI{}, Services: []FlowScopeService{}}
	u := d.UserByName(username)
	if u == nil {
		return out
	}
	out.Username, out.Known = u.Username, true
	if u.Disabled {
		out.Mode = flowScopeSelected
		return out
	}
	scope := scopeOf(d, u)
	if scope.all {
		out.Mode = flowScopeAll
		return out
	}
	out.Mode = flowScopeSelected
	if u.ScopeMode == model.ScopeTeams {
		out.Mode = flowScopeTeams
	}
	cis := map[string]bool{}
	for id := range scope.services {
		svc := d.Services[id]
		if svc == nil || svc.Status == model.ServiceRetired {
			continue
		}
		out.Services = append(out.Services, FlowScopeService{ID: svc.ID, Name: svc.Name})
		for _, ci := range svc.CIIDs {
			cis[ci] = true
		}
	}
	for id := range cis {
		ci := d.ConfigItems[id]
		if ci == nil {
			continue
		}
		v := FlowScopeCI{ID: ci.ID, Name: ci.Name, SourceRefs: []string{}, IPs: append([]string{}, ci.IPs...)}
		if ci.InventoryDB != nil && ci.InventoryDB.Ref != "" {
			v.SourceRefs = append(v.SourceRefs, ci.InventoryDB.Ref)
		}
		out.CIs = append(out.CIs, v)
	}
	slices.SortFunc(out.Services, func(a, b FlowScopeService) int { return byName(a.Name, b.Name) })
	slices.SortFunc(out.CIs, func(a, b FlowScopeCI) int { return byName(a.Name, b.Name) })
	return out
}
