package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/access"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/notify"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// Onboarding steps: what has to exist before an event reaches a person.
const (
	StepSource    = "source"
	StepEvent     = "event"
	StepTeam      = "team"
	StepCatalog   = "catalog"
	StepDelivery  = "delivery"
	StepPublicURL = "public_url"
)

// OnboardingStep is one item of the «first steps» checklist or of a page setup guide. Path is
// the page that fixes it; Action, in a page guide, is the button of the page itself that does;
// CanFix tells whether the signed-in user has the permissions to do that.
type OnboardingStep struct {
	ID       string `json:"id"`
	Done     bool   `json:"done"`
	Optional bool   `json:"optional"`
	Path     string `json:"path"`
	Action   string `json:"action,omitempty"`
	CanFix   bool   `json:"can_fix"`
}

// Onboarding is the progress of a new installation: Done when every required step is done.
type Onboarding struct {
	Done  bool             `json:"done"`
	Steps []OnboardingStep `json:"steps"`
}

// onboardingFacts is what the checklist is computed from, read in one pass over the state.
type onboardingFacts struct {
	published []string // published connectors, by ID
	team      bool
	catalog   bool
	delivery  bool
	publicURL bool
}

func readOnboardingFacts(d *store.Data) onboardingFacts {
	var f onboardingFacts
	for _, c := range d.Connectors {
		if c.Published > 0 {
			f.published = append(f.published, c.ID)
		}
	}
	// People who can be reached: active members of existing teams.
	members := teamMembers(d)
	var people []*model.User
	staffed := map[string]bool{}
	for teamID, ms := range members {
		if d.Teams[teamID] == nil {
			continue
		}
		for _, m := range ms {
			if u := d.Users[m.ID]; u != nil && !u.Disabled {
				staffed[teamID] = true
				people = append(people, u)
			}
		}
	}
	f.team = len(staffed) > 0
	for _, s := range d.Services {
		if s.OwnerTeamID != "" && d.Teams[s.OwnerTeamID] != nil && len(s.CIIDs) > 0 {
			for _, id := range s.CIIDs {
				if d.ConfigItems[id] != nil {
					f.catalog = true
					break
				}
			}
		}
	}
	al := d.Settings.Alerting
	f.publicURL = al.PublicURL != ""
	var teams []*model.Team
	for _, t := range d.Teams {
		teams = append(teams, t)
	}
	f.delivery = pagerDutyDelivers(al.PagerDuty) || backupDelivers(al.Notify, people, teams)
	return f
}

// pagerDutyDelivers: PagerDuty is on and has a key to send with (the default one or a route's).
func pagerDutyDelivers(pd model.PagerDuty) bool {
	if !pd.Enabled {
		return false
	}
	if pd.RoutingKeyRef != "" {
		return true
	}
	for _, r := range pd.Routes {
		if r.RoutingKeyRef != "" {
			return true
		}
	}
	return false
}

// backupDelivers: a backup notification channel is set up and somebody would get the message:
// an "always notify" recipient, a team member with an address or a chat, or a team's own
// channel (a mailbox, a chat, a Teams or Zoom webhook).
func backupDelivers(n model.Notify, people []*model.User, teams []*model.Team) bool {
	return notify.Reaches(n, people, teams)
}

// anyEvent reports whether an event was ever received (by a connector or made by a rule).
func (a *App) anyEvent(ctx context.Context) bool {
	if !a.ingestReady() {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var seen bool
	err := a.deps.Backend.Pool().QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM connector_events) OR EXISTS (SELECT 1 FROM alerts)`).Scan(&seen)
	if err != nil {
		slog.Warn("onboarding: events not checked", "err", err)
		return false
	}
	return seen
}

// Onboarding computes the checklist for a user: paths and CanFix follow their permissions.
func (a *App) Onboarding(ctx context.Context, perms access.Set) Onboarding {
	var f onboardingFacts
	a.deps.Store.Read(func(d *store.Data) { f = readOnboardingFacts(d) })
	all := func(ps ...string) bool {
		for _, p := range ps {
			if !perms.Has(p) {
				return false
			}
		}
		return true
	}
	// Quick connect lives on «Monitoring systems» and on «Connectors».
	sourcePath := "/connectors?connect=1"
	if perms.Has("monitoring:view") {
		sourcePath = "/monitoring?connect=1"
	}
	eventPath := "/connectors"
	if len(f.published) == 1 {
		eventPath = "/connectors/" + f.published[0]
	}
	steps := []OnboardingStep{
		{ID: StepSource, Done: len(f.published) > 0, Path: sourcePath, CanFix: all("connectors:edit", "connectors:publish", "credentials:edit")},
		{ID: StepEvent, Done: a.anyEvent(ctx), Path: eventPath, CanFix: all("connectors:view")},
		{ID: StepTeam, Done: f.team, Path: "/teams", CanFix: all("teams:edit")},
		{ID: StepCatalog, Optional: true, Done: f.catalog, Path: "/services", CanFix: all("services:edit")},
		{ID: StepDelivery, Done: f.delivery, Path: "/settings/alerting", CanFix: all("settings.alerting:edit")},
		{ID: StepPublicURL, Done: f.publicURL, Path: "/settings/alerting", CanFix: all("settings.alerting:edit")},
	}
	out := Onboarding{Done: true, Steps: steps}
	for _, s := range steps {
		if !s.Optional && !s.Done {
			out.Done = false
		}
	}
	return out
}

type publicURLView struct {
	PublicURL string `json:"public_url"`
}

// SetPublicURL saves the address people and PagerDuty reach Umbrella at.
func (s *SettingsService) SetPublicURL(actor, v string) (string, error) {
	pub, err := model.NormalizePublicURL(v)
	if err != nil {
		return "", invalid("public_url_invalid", err)
	}
	s.st.Write(func(d *store.Data) {
		if d.Settings.Alerting.PublicURL == pub {
			return
		}
		d.Settings.Alerting.PublicURL = pub
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.public_url", Detail: fmt.Sprintf("public address %q", pub)})
	})
	return pub, nil
}

func (a *App) registerOnboarding(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/onboarding", a.authed(a.onboarding))
	mux.HandleFunc("GET /api/settings/public-url", a.authed(a.can("settings.alerting:view", a.getPublicURL)))
	mux.HandleFunc("PUT /api/settings/public-url", a.authed(a.can("settings.alerting:edit", a.putPublicURL)))
	mux.HandleFunc("POST /api/connectors/credentials/token", a.authed(a.can("connectors:edit", a.can("credentials:edit", a.createConnectorToken))))
}

func (a *App) onboarding(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, a.Onboarding(r.Context(), a.access.Permissions(current(r).user)))
}

func (a *App) getPublicURL(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, publicURLView{PublicURL: a.settings.Get().Alerting.PublicURL})
}

func (a *App) putPublicURL(w http.ResponseWriter, r *http.Request) {
	var in publicURLView
	if !httpx.Decode(w, r, &in) {
		return
	}
	pub, err := a.settings.SetPublicURL(current(r).user.Username, in.PublicURL)
	settingsRespond(w, publicURLView{PublicURL: pub}, err)
}
