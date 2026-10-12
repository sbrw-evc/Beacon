package app

import (
	"net/http"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/notify"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/pagerduty"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// RoutePreview is where an incident would go now: the services, the primary service and its
// team, the people (or the owners of the item when the team has nobody) and the PagerDuty
// route. It is computed by the alert engine's own rules.
type RoutePreview struct {
	Services []alert.Ref    `json:"services"`
	Service  *alert.Ref     `json:"service,omitempty"`
	Team     *alert.Ref     `json:"team,omitempty"`
	People   []alert.Person `json:"people"`
	Channel  *alert.Channel `json:"channel,omitempty"`
	Owners   []alert.Person `json:"owners"`
	Via      string         `json:"via"`
	// PagerDuty is null when PagerDuty is off.
	PagerDuty *PreviewPD `json:"pagerduty_route"`
	// Elsewhere (service preview only): items of the service routed by a more critical service.
	Elsewhere []alert.Elsewhere `json:"elsewhere,omitempty"`
	// Backup is null when every backup notification channel is off.
	Backup *PreviewBackup `json:"backup"`
}

// PreviewBackup is where backup notification would go: the addresses, after how long an
// incident nobody took waits, and from which severity.
type PreviewBackup struct {
	Targets      []notify.Target `json:"targets"`
	DelaySeconds int             `json:"delay_seconds"`
	MinSeverity  string          `json:"min_severity"`
}

type PreviewPD struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MinSeverity string `json:"min_severity"`
	// NoKey: the chosen route (or the default integration) has no integration key.
	NoKey bool `json:"no_key,omitempty"`
}

func (a *App) previewOf(r alert.Route) RoutePreview {
	out := RoutePreview{Services: r.Services, Service: r.Service, Team: r.Team, People: r.People, Owners: r.Owners, Via: r.Via}
	if r.Channel != nil {
		// Webhook URLs carry a secret: the preview shows them redacted.
		ch := r.Channel.Redacted()
		out.Channel = &ch
	}
	var p *PreviewPD
	var n model.Notify
	a.deps.Store.Read(func(d *store.Data) {
		n = d.Settings.Alerting.Notify
		set := d.Settings.Alerting.PagerDuty
		if !set.Enabled {
			return
		}
		id, name := pagerduty.RouteOf(set, alert.Alert{Route: r})
		p = &PreviewPD{ID: id, Name: name, MinSeverity: set.MinSeverity, NoKey: id == pagerduty.DefaultRoute && set.RoutingKeyRef == ""}
	})
	out.PagerDuty = p
	if notify.On(n) {
		// The delay and the severity as the alert engine takes them.
		b := &PreviewBackup{Targets: a.notifier.PreviewTargets(r), MinSeverity: n.MinSeverity}
		switch {
		case n.DelaySeconds != nil:
			b.DelaySeconds = max(0, *n.DelaySeconds)
		case p != nil:
			b.DelaySeconds = int(alert.DefaultFallbackAfter.Seconds())
		}
		if alert.SeverityRank(b.MinSeverity) == 0 {
			b.MinSeverity = alert.DefaultFallbackSeverity
		}
		out.Backup = b
	}
	return out
}

func (a *App) registerRoutePreview(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cis/{id}/route", a.authed(a.can("cis:view", a.ciRoute)))
	mux.HandleFunc("GET /api/services/{id}/route", a.authed(a.can("services:view", a.serviceRoute)))
}

func (a *App) ciRoute(w http.ResponseWriter, r *http.Request) {
	rt, ok := alert.PreviewCI(a.deps.Store, r.PathValue("id"), time.Now().UTC())
	if !ok {
		writeError(w, ErrNotFound)
		return
	}
	httpx.JSON(w, http.StatusOK, a.previewOf(rt))
}

func (a *App) serviceRoute(w http.ResponseWriter, r *http.Request) {
	rt, elsewhere, ok := alert.PreviewService(a.deps.Store, r.PathValue("id"), time.Now().UTC())
	if !ok {
		writeError(w, ErrNotFound)
		return
	}
	out := a.previewOf(rt)
	out.Elsewhere = elsewhere
	httpx.JSON(w, http.StatusOK, out)
}
