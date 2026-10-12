// Package response is incident response: it assesses the impact of an incident on business
// services and its priority, and then runs the policy of that priority: notification and
// escalation steps, the war room chat in Microsoft Teams, the bridge call, and the Jira task
// and postmortem. It works beside the alert engine: it reads incidents, keeps its own state per
// incident and writes what it did on the incident timeline.
package response

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

// ServiceImpact is a business service the incident touches.
type ServiceImpact struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Criticality string `json:"criticality"`
	// Direct: the item of the incident is one of the service's items; otherwise the service
	// depends on one that is (Via names it).
	Direct bool   `json:"direct"`
	Via    string `json:"via,omitempty"`
	// OwnerTeamID is the team that owns the service.
	OwnerTeamID string `json:"owner_team_id,omitempty"`
}

// Reason explains a step of the assessment; Code and Args are translated by the interface.
type Reason struct {
	Code string            `json:"code"`
	Args map[string]string `json:"args,omitempty"`
}

// Assessment is the impact of an incident and the priority of its response.
type Assessment struct {
	// Urgency is the severity of the events.
	Urgency string `json:"urgency"`
	Impact  string `json:"impact"`
	// Priority is the severity name of the response priority (critical is P1).
	Priority string          `json:"priority"`
	Services []ServiceImpact `json:"services"`
	// TopCriticality is the criticality of the most critical affected service.
	TopCriticality string `json:"top_criticality,omitempty"`
	// Incidents is how many active incidents hit the affected services, this one included.
	Incidents int       `json:"incidents"`
	Reasons   []Reason  `json:"reasons"`
	At        time.Time `json:"at"`
}

// Catalog is what the assessment reads of the catalog.
type Catalog struct {
	Services map[string]model.Service
	Teams    map[string]model.Team
	Users    map[string]model.User
}

// affected lists the services of the incident and, transitively, the services that depend on
// them, most critical first.
func (c Catalog) affected(direct []alert.Ref) []ServiceImpact {
	var out []ServiceImpact
	seen := map[string]bool{}
	queue := []ServiceImpact{}
	for _, r := range direct {
		s, ok := c.Services[r.ID]
		if !ok || seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		queue = append(queue, ServiceImpact{ID: s.ID, Name: s.Name, Criticality: s.Criticality, Direct: true, OwnerTeamID: s.OwnerTeamID})
	}
	for len(queue) > 0 && len(out) < 200 {
		cur := queue[0]
		queue = queue[1:]
		out = append(out, cur)
		ids := make([]string, 0)
		for id, s := range c.Services {
			if !seen[id] && s.Status != model.ServiceRetired && slices.Contains(s.DependsOn, cur.ID) {
				ids = append(ids, id)
			}
		}
		slices.Sort(ids)
		for _, id := range ids {
			s := c.Services[id]
			seen[id] = true
			queue = append(queue, ServiceImpact{ID: s.ID, Name: s.Name, Criticality: s.Criticality, Via: cur.Name, OwnerTeamID: s.OwnerTeamID})
		}
	}
	slices.SortStableFunc(out, func(a, b ServiceImpact) int {
		if a.Direct != b.Direct {
			if a.Direct {
				return -1
			}
			return 1
		}
		return cmp.Compare(model.CriticalityRank(b.Criticality), model.CriticalityRank(a.Criticality))
	})
	return out
}

// Assess computes the impact and the priority of an incident. incidents counts the active
// incidents per service ID (this one included).
func Assess(p model.ImpactPolicy, c Catalog, a alert.Alert, incidents map[string]int, now time.Time) Assessment {
	as := Assessment{Urgency: a.Severity, Services: c.affected(a.Route.Services), Reasons: []Reason{}, At: now}
	if !model.ValidSeverity(as.Urgency) {
		as.Urgency = model.SeverityWarning
	}
	if as.Services == nil {
		as.Services = []ServiceImpact{}
	}
	top := ""
	for _, s := range as.Services {
		if model.CriticalityRank(s.Criticality) > model.CriticalityRank(top) {
			top = s.Criticality
		}
	}
	as.TopCriticality = top
	if top == "" {
		as.Impact = p.NoService
		as.Reasons = append(as.Reasons, Reason{Code: "no_service", Args: map[string]string{"impact": as.Impact}})
	} else {
		as.Impact = p.Criticality[top]
		name := ""
		for _, s := range as.Services {
			if s.Criticality == top {
				name = s.Name
				break
			}
		}
		as.Reasons = append(as.Reasons, Reason{Code: "criticality", Args: map[string]string{"service": name, "criticality": top, "impact": as.Impact}})
	}
	if model.ImpactRank(as.Impact) == 0 {
		as.Impact = model.ImpactModerate
	}
	raise := func(code string, args map[string]string) {
		from := as.Impact
		as.Impact = model.RaiseImpact(as.Impact)
		if args == nil {
			args = map[string]string{}
		}
		args["from"], args["to"] = from, as.Impact
		as.Reasons = append(as.Reasons, Reason{Code: code, Args: args})
	}
	if p.RaiseRED && a.Method == model.MethodRED {
		raise("red", nil)
	}
	dependents := 0
	for _, s := range as.Services {
		if !s.Direct {
			dependents++
		}
	}
	if p.RaiseDependents > 0 && dependents >= p.RaiseDependents {
		raise("dependents", map[string]string{"count": fmt.Sprint(dependents)})
	}
	for _, s := range as.Services {
		as.Incidents = max(as.Incidents, incidents[s.ID])
	}
	if p.RaiseIncidents > 0 && as.Incidents >= p.RaiseIncidents {
		raise("mass", map[string]string{"count": fmt.Sprint(as.Incidents)})
	}
	as.Priority = p.Matrix[as.Impact][as.Urgency]
	if !model.ValidSeverity(as.Priority) {
		as.Priority = as.Urgency
	}
	as.Reasons = append(as.Reasons, Reason{Code: "matrix", Args: map[string]string{"impact": as.Impact, "urgency": as.Urgency, "priority": as.Priority}})
	if p.NeverLower && model.SeverityRank(as.Priority) < model.SeverityRank(as.Urgency) {
		as.Priority = as.Urgency
		as.Reasons = append(as.Reasons, Reason{Code: "never_lower", Args: map[string]string{"priority": as.Priority}})
	}
	return as
}

// ServiceNames lists the names of the services, direct ones first.
func (as Assessment) ServiceNames() string {
	names := make([]string, 0, len(as.Services))
	for _, s := range as.Services {
		names = append(names, s.Name)
	}
	return strings.Join(names, ", ")
}
