package app

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// Health levels, from best to worst; unknown means nothing tells the state.
const (
	HealthUnknown  = "unknown"
	HealthOK       = "ok"
	HealthWarning  = "warning"
	HealthCritical = "critical"

	maxRecentEvents = 5
	// maxActive caps the active incidents read for one map.
	maxActive = 5000
)

var healthRank = map[string]int{HealthUnknown: 0, HealthOK: 1, HealthWarning: 2, HealthCritical: 3}

func worse(a, b string) string {
	if healthRank[b] > healthRank[a] {
		return b
	}
	return a
}

// HealthReason explains a health level: a code the interface translates, the level it leads
// to and, where it applies, a count or the item or service behind it.
type HealthReason struct {
	Code   string `json:"code"`
	Level  string `json:"level"`
	Count  int    `json:"count,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type Health struct {
	Level   string         `json:"level"`
	Reasons []HealthReason `json:"reasons"`
}

func (h *Health) add(r HealthReason) {
	h.Reasons = append(h.Reasons, r)
	h.Level = worse(h.Level, r.Level)
}

// MapEvent is an active incident of a configuration item.
type MapEvent struct {
	IncidentID string    `json:"incident_id"`
	Title      string    `json:"title"`
	Severity   string    `json:"severity"`
	Status     string    `json:"status"`
	Suppressed bool      `json:"suppressed,omitempty"`
	LastSeen   time.Time `json:"last_seen"`
}

// MapEvents counts the active incidents of an item by severity. Incidents under a maintenance
// window are counted apart and do not make the item worse.
type MapEvents struct {
	model.SeverityCounts
	Maintenance int        `json:"maintenance"`
	Recent      []MapEvent `json:"recent"`
}

type MapCI struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	Source    string    `json:"source"`
	IPs       []string  `json:"ips"`
	NetBoxURL string    `json:"netbox_url,omitempty"`
	Directory string    `json:"directory,omitempty"`
	Services  []string  `json:"services"`
	Events    MapEvents `json:"events"`
	Health    Health    `json:"health"`
}

type MapService struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Criticality string   `json:"criticality"`
	Status      string   `json:"status"`
	Owner       string   `json:"owner"`
	CIIDs       []string `json:"ci_ids"`
	DependsOn   []string `json:"depends_on"`
	NetBoxURL   string   `json:"netbox_url,omitempty"`
	Health      Health   `json:"health"`
}

// MapEventsInfo says whether active incidents were taken into account.
type MapEventsInfo struct {
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
	// Scoped: the viewer sees incidents of some business services only, so events count only
	// for the items of those services.
	Scoped bool `json:"scoped,omitempty"`
}

type CMDBMap struct {
	Services    []MapService  `json:"services"`
	CIs         []MapCI       `json:"cis"`
	Events      MapEventsInfo `json:"events"`
	GeneratedAt time.Time     `json:"generated_at"`
}

var errEventsNotReady = errors.New("the event tables are not ready yet")

// activeSource lists the active incidents bound to configuration items. Health comes from
// incidents, not raw connector events, so an incident resolved by hand or in PagerDuty, the
// incidents of RED/USE rules and maintenance windows count here as on the incident board and
// wallboards.
type activeSource func(ctx context.Context) ([]alert.Alert, error)

// CMDBService builds the map of business services, their configuration items and dependencies
// with the health of each.
type CMDBService struct {
	st     *store.Store
	active activeSource
	now    func() time.Time
}

func NewCMDBService(st *store.Store, active activeSource) *CMDBService {
	return &CMDBService{st: st, active: active, now: func() time.Time { return time.Now().UTC() }}
}

func (a *App) registerCMDB(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cmdb", a.authed(a.can("cmdb:view", a.cmdbMap)))
}

func (a *App) cmdbMap(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, a.cmdb.Map(r.Context(), a.incidentScope(current(r).user)...))
}

// Map builds the map. With scope (business service ids) given, incidents are taken into
// account only for the items of those services, as the viewer sees only their incidents.
func (s *CMDBService) Map(ctx context.Context, scope ...string) CMDBMap {
	now := s.now()
	out := CMDBMap{Services: []MapService{}, CIs: []MapCI{}, GeneratedAt: now,
		Events: MapEventsInfo{Scoped: len(scope) > 0}}
	var events []alert.Alert
	if s.active != nil {
		var err error
		if events, err = s.active(ctx); err != nil {
			out.Events.Error = err.Error()
		} else {
			out.Events.Available = true
		}
	}
	s.st.Read(func(d *store.Data) {
		g := newServiceGraph(d)
		bound := map[string][]string{}
		for _, svc := range d.Services {
			for _, id := range svc.CIIDs {
				if d.ConfigItems[id] != nil {
					bound[id] = append(bound[id], svc.ID)
				}
			}
		}
		cis := map[string]*MapCI{}
		for id, svcs := range bound {
			ci := d.ConfigItems[id]
			slices.Sort(svcs)
			m := &MapCI{ID: ci.ID, Name: ci.Name, Kind: ci.Kind, Status: ci.Status, Source: ci.Source, IPs: serviceStrings(ci.IPs), Services: svcs,
				Events: MapEvents{Recent: []MapEvent{}}}
			if ci.NetBox != nil {
				m.NetBoxURL = ci.NetBox.URL
			}
			if ci.Directory != nil {
				m.Directory = ci.Directory.Status
				if ci.Directory.Status == model.DirectoryMatched && ci.Directory.Disabled {
					m.Directory = "disabled"
				}
			}
			cis[id] = m
		}
		attachEvents(cis, events, scope)
		for _, m := range cis {
			m.Health = ciHealth(m)
			out.CIs = append(out.CIs, *m)
		}
		health := serviceHealth(d, cis)
		for _, svc := range d.Services {
			m := MapService{ID: svc.ID, Name: svc.Name, Criticality: svc.Criticality, Status: svc.Status, Owner: strings.Join(g.team(svc.OwnerTeamID).Path, " / "),
				CIIDs: []string{}, DependsOn: []string{}, Health: health[svc.ID]}
			for _, id := range svc.CIIDs {
				if cis[id] != nil {
					m.CIIDs = append(m.CIIDs, id)
				}
			}
			for _, id := range svc.DependsOn {
				if d.Services[id] != nil {
					m.DependsOn = append(m.DependsOn, id)
				}
			}
			if svc.NetBox != nil {
				m.NetBoxURL = svc.NetBox.URL
			}
			out.Services = append(out.Services, m)
		}
	})
	slices.SortFunc(out.Services, func(a, b MapService) int { return byName(a.Name, b.Name) })
	slices.SortFunc(out.CIs, func(a, b MapCI) int { return byName(a.Name, b.Name) })
	return out
}

// attachEvents counts the active incidents of each item; the alert engine has already bound
// them to the item. With a scope, only items of those services get incidents. Test alerts do
// not count.
func attachEvents(cis map[string]*MapCI, alerts []alert.Alert, scope []string) {
	for _, a := range alerts {
		m := cis[a.CIID]
		if m == nil || !alert.Active(a.Status) || a.IsTest() {
			continue
		}
		if len(scope) > 0 && !slices.ContainsFunc(m.Services, func(s string) bool { return slices.Contains(scope, s) }) {
			continue
		}
		sev := a.Severity
		if !model.ValidSeverity(sev) {
			sev = model.SeverityInfo
		}
		if a.Suppressed {
			m.Events.Maintenance++
		} else {
			m.Events.Add(sev, 1)
		}
		if len(m.Events.Recent) < maxRecentEvents {
			m.Events.Recent = append(m.Events.Recent, MapEvent{IncidentID: a.ID, Title: a.Title, Severity: a.Severity, Status: a.Status,
				Suppressed: a.Suppressed, LastSeen: a.LastSeen})
		}
	}
}

// ciHealth: the status of the item, the computer object of the domain controller and the
// active incidents of the item.
func ciHealth(m *MapCI) Health {
	h := Health{Level: HealthUnknown, Reasons: []HealthReason{}}
	switch m.Status {
	case model.CIStatusActive:
		h.Level = HealthOK
	case model.CIStatusFailed, model.CIStatusOffline:
		h.add(HealthReason{Code: "status_" + m.Status, Level: HealthCritical})
	case model.CIStatusDecommissioning:
		h.add(HealthReason{Code: "status_" + m.Status, Level: HealthWarning})
	default:
		h.add(HealthReason{Code: "status_" + m.Status, Level: HealthUnknown})
	}
	switch m.Directory {
	case model.DirectoryMissing:
		h.add(HealthReason{Code: "directory_missing", Level: HealthWarning})
	case "disabled":
		h.add(HealthReason{Code: "directory_disabled", Level: HealthWarning})
	}
	if n := m.Events.Critical + m.Events.Error; n > 0 {
		h.add(HealthReason{Code: "events_critical", Level: HealthCritical, Count: n})
	}
	if m.Events.Warning > 0 {
		h.add(HealthReason{Code: "events_warning", Level: HealthWarning, Count: m.Events.Warning})
	}
	if m.Events.Maintenance > 0 {
		h.add(HealthReason{Code: "events_maintenance", Level: HealthOK, Count: m.Events.Maintenance})
	}
	return h
}

// serviceHealth: a service is as healthy as the worst of its items; a service it depends on
// that is in trouble makes it a warning at most. Planned and retired services are not judged.
func serviceHealth(d *store.Data, cis map[string]*MapCI) map[string]Health {
	out := map[string]Health{}
	visiting := map[string]bool{}
	var eval func(id string) Health
	eval = func(id string) Health {
		if h, ok := out[id]; ok {
			return h
		}
		svc := d.Services[id]
		h := Health{Level: HealthUnknown, Reasons: []HealthReason{}}
		if svc == nil || visiting[id] {
			return h
		}
		visiting[id] = true
		defer delete(visiting, id)
		if svc.Status != model.ServiceActive {
			h.add(HealthReason{Code: "service_" + svc.Status, Level: HealthUnknown})
			out[id] = h
			return h
		}
		judged := false
		for _, ciID := range svc.CIIDs {
			m := cis[ciID]
			if m == nil {
				continue
			}
			if m.Health.Level != HealthUnknown {
				judged = true
				h.Level = worse(h.Level, HealthOK)
			}
			if m.Health.Level == HealthWarning || m.Health.Level == HealthCritical {
				h.add(HealthReason{Code: "ci_" + m.Health.Level, Level: m.Health.Level, Ref: m.ID, Detail: m.Name})
			}
		}
		for _, dep := range svc.DependsOn {
			dh := eval(dep)
			if dh.Level == HealthUnknown {
				continue
			}
			judged = true
			h.Level = worse(h.Level, HealthOK)
			if dh.Level == HealthWarning || dh.Level == HealthCritical {
				h.add(HealthReason{Code: "dependency_" + dh.Level, Level: HealthWarning, Ref: dep, Detail: d.Services[dep].Name})
			}
		}
		if !judged && len(h.Reasons) == 0 {
			code := "no_cis"
			if len(svc.CIIDs) > 0 {
				code = "cis_unknown"
			}
			h.add(HealthReason{Code: code, Level: HealthUnknown})
		}
		out[id] = h
		return h
	}
	for id := range d.Services {
		eval(id)
	}
	return out
}
