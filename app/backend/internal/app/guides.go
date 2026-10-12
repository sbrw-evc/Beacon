package app

import (
	"context"
	"net/http"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/access"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// Guide actions: what a step without a path does on its own page. The web app maps each one to
// the button of the page (a dialog to open, a tab to show).
const (
	ActionCreate      = "create"
	ActionConnect     = "connect"
	ActionSource      = "source"
	ActionIntegration = "integrations"
)

// guideFacts is what the page setup guides are computed from, read in one pass over the state.
type guideFacts struct {
	onboardingFacts
	event          bool
	connectors     int
	customConn     bool
	monitoring     int
	pollAlerts     bool
	hostLinks      bool
	cis            int
	ciOwners       bool
	ciInService    bool
	netbox         bool
	netboxSynced   bool
	teams          int
	teamLead       bool
	teamChannel    bool
	services       int
	dependencies   bool
	metricSources  bool
	rules          int
	ruleMethods    map[string]bool
	maintenance    int
	wallboards     int
	wallNetworks   bool
	users          int
	directory      bool
	customRoles    bool
	responseMode   string
	responseIntegr bool
	backup         bool
	grafana        bool
}

func readGuideFacts(d *store.Data) guideFacts {
	f := guideFacts{onboardingFacts: readOnboardingFacts(d), ruleMethods: map[string]bool{}}
	f.connectors = len(d.Connectors)
	for _, c := range d.Connectors {
		if c.Preset == "" {
			f.customConn = true
		}
	}
	f.monitoring = len(d.MonitoringSources)
	for _, m := range d.MonitoringSources {
		f.pollAlerts = f.pollAlerts || m.PollAlerts
		f.hostLinks = f.hostLinks || len(m.Links) > 0
		f.metricSources = f.metricSources || m.Kind == model.MonitoringPrometheus
	}
	f.metricSources = f.metricSources || len(d.MetricSources) > 0
	f.cis = len(d.ConfigItems)
	for _, c := range d.ConfigItems {
		f.ciOwners = f.ciOwners || len(c.Owners) > 0
	}
	f.services = len(d.Services)
	for _, s := range d.Services {
		f.dependencies = f.dependencies || len(s.DependsOn) > 0
		for _, id := range s.CIIDs {
			if d.ConfigItems[id] != nil {
				f.ciInService = true
			}
		}
	}
	nb := d.Settings.NetBox
	f.netbox = nb.Enabled && nb.URL != ""
	f.netboxSynced = d.NetBoxSync.OK && !d.NetBoxSync.FinishedAt.IsZero()
	f.teams = len(d.Teams)
	for _, t := range d.Teams {
		f.teamLead = f.teamLead || (t.LeadID != "" && d.Users[t.LeadID] != nil)
		f.teamChannel = f.teamChannel || t.Email != "" || t.Telegram != "" || t.Teams != "" || t.Zoom != ""
	}
	f.rules = len(d.Rules)
	for _, r := range d.Rules {
		f.ruleMethods[r.Method] = true
	}
	f.maintenance = len(d.Maintenance)
	f.wallboards = len(d.Wallboards)
	for _, w := range d.Wallboards {
		f.wallNetworks = f.wallNetworks || len(w.AllowedNetworks) > 0
	}
	f.users = len(d.Users)
	for _, u := range d.Users {
		f.customRoles = f.customRoles || u.Role != model.RoleAdmin
	}
	f.directory = d.Settings.LDAP.Enabled || d.Settings.Entra.Enabled
	rs := d.Settings.Response
	f.responseMode = rs.Mode
	f.responseIntegr = on(rs.Graph.Mode) || on(rs.Jira.Mode) || on(rs.ZoomAPI.Mode)
	al := d.Settings.Alerting
	var people []*model.User
	for _, ms := range teamMembers(d) {
		for _, m := range ms {
			if u := d.Users[m.ID]; u != nil && !u.Disabled {
				people = append(people, u)
			}
		}
	}
	var teams []*model.Team
	for _, t := range d.Teams {
		teams = append(teams, t)
	}
	f.backup = backupDelivers(al.Notify, people, teams)
	f.grafana = al.Grafana.DashboardURL != ""
	return f
}

func on(mode string) bool { return mode != "" && mode != model.ModeOff }

// guideStep builds a step. A step with an action is done on the page of the guide; a step with a
// path is done on that page; a step with neither is an instruction for the page itself.
type guideStep struct {
	id       string
	done     bool
	optional bool
	path     string
	action   string
	perms    []string
}

// guides are the setup guides of the key pages: page ID to its steps, in the order to do them.
// The incidents page and the system status show the «first steps» checklist instead.
var guides = map[string]func(f guideFacts) []guideStep{
	"monitoring": func(f guideFacts) []guideStep {
		return []guideStep{
			{id: "source", done: f.monitoring > 0, action: ActionCreate, perms: []string{"monitoring:edit"}},
			{id: "alerts", done: f.pollAlerts || len(f.published) > 0, optional: true, perms: []string{"monitoring:edit"}},
			{id: "hosts", done: f.hostLinks, optional: true, perms: []string{"monitoring:link"}},
			{id: "rules", done: f.rules > 0, optional: true, path: "/rules", perms: []string{"rules:edit"}},
		}
	},
	"connectors": func(f guideFacts) []guideStep {
		return []guideStep{
			{id: "connect", done: len(f.published) > 0, action: ActionConnect, perms: []string{"connectors:edit", "connectors:publish", "credentials:edit"}},
			{id: "event", done: f.event, perms: []string{"connectors:view"}},
			{id: "custom", done: f.customConn, optional: true, action: ActionCreate, perms: []string{"connectors:edit"}},
		}
	},
	"cis": func(f guideFacts) []guideStep {
		return []guideStep{
			{id: "ci", done: f.cis > 0, action: ActionCreate, perms: []string{"cis:edit"}},
			{id: "owners", done: f.ciOwners, perms: []string{"cis:edit"}},
			{id: "service", done: f.ciInService, path: "/services", perms: []string{"services:edit"}},
			{id: "netbox", done: f.netbox, optional: true, path: "/netbox", perms: []string{"netbox:edit"}},
		}
	},
	"services": func(f guideFacts) []guideStep {
		return []guideStep{
			{id: "team", done: f.teams > 0, path: "/teams", perms: []string{"teams:edit"}},
			{id: "service", done: f.services > 0, action: ActionCreate, perms: []string{"services:edit"}},
			{id: "catalog", done: f.catalog, perms: []string{"services:edit"}},
			{id: "depends", done: f.dependencies, optional: true, perms: []string{"services:edit"}},
		}
	},
	"cmdb": func(f guideFacts) []guideStep {
		return []guideStep{
			{id: "ci", done: f.cis > 0, path: "/cis", perms: []string{"cis:edit"}},
			{id: "service", done: f.services > 0, path: "/services", perms: []string{"services:edit"}},
			{id: "catalog", done: f.ciInService, path: "/services", perms: []string{"services:edit"}},
			{id: "depends", done: f.dependencies, optional: true, path: "/services", perms: []string{"services:edit"}},
		}
	},
	"teams": func(f guideFacts) []guideStep {
		return []guideStep{
			{id: "team", done: f.teams > 0, action: ActionCreate, perms: []string{"teams:edit"}},
			{id: "members", done: f.team, perms: []string{"teams:edit"}},
			{id: "lead", done: f.teamLead, optional: true, perms: []string{"teams:edit"}},
			{id: "channel", done: f.teamChannel, optional: true, perms: []string{"teams:edit"}},
		}
	},
	"users": func(f guideFacts) []guideStep {
		return []guideStep{
			{id: "user", done: f.users > 1, action: ActionCreate, perms: []string{"users:create"}},
			{id: "roles", done: f.customRoles, perms: []string{"users:edit"}},
			{id: "teams", done: f.team, path: "/teams", perms: []string{"teams:edit"}},
			{id: "directory", done: f.directory, optional: true, path: "/settings/ldap", perms: []string{"settings.ldap:edit"}},
		}
	},
	"rules": func(f guideFacts) []guideStep {
		return []guideStep{
			{id: "source", done: f.metricSources, action: ActionSource, perms: []string{"rules:edit"}},
			{id: "rule", done: f.rules > 0, action: ActionCreate, perms: []string{"rules:edit"}},
			{id: "both", done: f.ruleMethods[model.MethodRED] && f.ruleMethods[model.MethodUSE], optional: true, action: ActionCreate, perms: []string{"rules:edit"}},
		}
	},
	"maintenance": func(f guideFacts) []guideStep {
		return []guideStep{
			{id: "target", done: f.cis > 0 || f.services > 0, path: "/cis", perms: []string{"cis:edit"}},
			{id: "window", done: f.maintenance > 0, action: ActionCreate, perms: []string{"maintenance:edit"}},
		}
	},
	"wallboards": func(f guideFacts) []guideStep {
		return []guideStep{
			{id: "wallboard", done: f.wallboards > 0, action: ActionCreate, perms: []string{"wallboards:edit"}},
			{id: "networks", done: f.wallNetworks, perms: []string{"wallboards:edit"}},
		}
	},
	"response": func(f guideFacts) []guideStep {
		return []guideStep{
			{id: "catalog", done: f.catalog, path: "/services", perms: []string{"services:edit"}},
			{id: "integrations", done: f.responseIntegr, optional: true, action: ActionIntegration, perms: []string{"response:edit"}},
			{id: "dry_run", done: on(f.responseMode), perms: []string{"response:edit"}},
			{id: "live", done: f.responseMode == model.ModeLive, perms: []string{"response:edit"}},
		}
	},
	"settings.alerting": func(f guideFacts) []guideStep {
		return []guideStep{
			{id: "public_url", done: f.publicURL, perms: []string{"settings.alerting:edit"}},
			{id: "delivery", done: f.delivery, perms: []string{"settings.alerting:edit"}},
			{id: "backup", done: f.backup, optional: true, perms: []string{"settings.alerting:edit"}},
			{id: "grafana", done: f.grafana, optional: true, perms: []string{"settings.alerting:edit"}},
		}
	},
	"netbox": func(f guideFacts) []guideStep {
		return []guideStep{
			{id: "connect", done: f.netbox, perms: []string{"netbox:edit"}},
			{id: "sync", done: f.netboxSynced, perms: []string{"netbox:sync"}},
		}
	},
}

// PageGuide computes the setup guide of a page for a user: CanFix follows their permissions.
// ok is false when the page has no guide.
func (a *App) PageGuide(ctx context.Context, page string, perms access.Set) (Onboarding, bool) {
	build, ok := guides[page]
	if !ok {
		return Onboarding{}, false
	}
	var f guideFacts
	a.deps.Store.Read(func(d *store.Data) { f = readGuideFacts(d) })
	// The first event is the only fact outside the state: read it only for the page that asks.
	if page == "connectors" {
		f.event = a.anyEvent(ctx)
	}
	raw := build(f)
	out := Onboarding{Done: true, Steps: make([]OnboardingStep, 0, len(raw))}
	for _, s := range raw {
		can := true
		for _, p := range s.perms {
			can = can && perms.Has(p)
		}
		step := OnboardingStep{ID: s.id, Done: s.done, Optional: s.optional, Path: s.path, Action: s.action, CanFix: can}
		if s.id == "event" {
			step.Path = "/connectors"
			if len(f.published) == 1 {
				step.Path = "/connectors/" + f.published[0]
			}
		}
		out.Steps = append(out.Steps, step)
		if !s.optional && !s.done {
			out.Done = false
		}
	}
	return out, true
}

func (a *App) registerGuides(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/guides/{page}", a.authed(a.pageGuide))
}

func (a *App) pageGuide(w http.ResponseWriter, r *http.Request) {
	page := r.PathValue("page")
	perms := a.access.Permissions(current(r).user)
	if !perms.Has(access.Perm(page, access.View)) {
		writeError(w, forbidden)
		return
	}
	g, ok := a.PageGuide(r.Context(), page, perms)
	if !ok {
		writeError(w, ErrNotFound)
		return
	}
	httpx.JSON(w, http.StatusOK, g)
}
