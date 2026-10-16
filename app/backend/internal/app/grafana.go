package app

import (
	"cmp"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sbrw-evc/beacon/app/backend/internal/alert"
	"github.com/sbrw-evc/beacon/app/backend/internal/httpx"
	"github.com/sbrw-evc/beacon/app/backend/internal/model"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
)

const (
	defaultGrafanaWindow = 10
	maxGrafanaWindow     = 24 * 60
)

// GrafanaLink is the dashboard of an incident: the time from the window before it opened to the
// window after it ended (now while it lasts) and the variables ci, ci_id, service, team, signal
// and incident, and ip and ref for the Current "CI traffic" dashboard: the first address
// and the Ledger reference of the item (ci, may be nil), or the ip and source_ref labels
// of the events.
func GrafanaLink(set model.Grafana, al alert.Alert, ci *model.ConfigItem, now time.Time) string {
	if set.DashboardURL == "" {
		return ""
	}
	u, err := url.Parse(set.DashboardURL)
	if err != nil {
		return ""
	}
	win := time.Duration(set.WindowMinute) * time.Minute
	if set.WindowMinute <= 0 {
		win = defaultGrafanaWindow * time.Minute
	}
	start := al.OpenedAt
	if start.IsZero() {
		start = al.FirstSeen
	}
	q := u.Query()
	q.Set("from", strconv.FormatInt(start.Add(-win).UnixMilli(), 10))
	if al.ResolvedAt != nil {
		end := al.ResolvedAt.Add(win)
		if end.After(now) {
			q.Set("to", "now")
		} else {
			q.Set("to", strconv.FormatInt(end.UnixMilli(), 10))
		}
	} else {
		q.Set("to", "now")
	}
	q.Set("var-incident", al.ID)
	if al.CIName != "" {
		q.Set("var-ci", al.CIName)
	}
	if al.CIID != "" {
		q.Set("var-ci_id", al.CIID)
	}
	if al.Signal != "" {
		q.Set("var-signal", al.Signal)
	}
	if len(al.Route.Services) > 0 {
		q.Set("var-service", al.Route.Services[0].Name)
	}
	if al.Route.Team != nil {
		q.Set("var-team", al.Route.Team.Name)
	}
	ip, ref := ciRef(ci)
	if ip = cmp.Or(ip, strings.TrimSpace(al.Labels[alert.LabelIP])); ip != "" {
		q.Set("var-ip", ip)
	}
	if ref = cmp.Or(ref, strings.TrimSpace(al.Labels[alert.LabelSourceRef])); ref != "" {
		q.Set("var-ref", ref)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (a *App) grafanaSettings() model.Grafana {
	var g model.Grafana
	a.deps.Store.Read(func(d *store.Data) { g = d.Settings.Alerting.Grafana })
	return g
}

// grafanaLink is the address of the incident context in Grafana; empty until Grafana is set up.
func (a *App) grafanaLink(al alert.Alert) string {
	var g model.Grafana
	var ci *model.ConfigItem
	a.deps.Store.Read(func(d *store.Data) {
		g = d.Settings.Alerting.Grafana
		if c := d.ConfigItems[al.CIID]; al.CIID != "" && c != nil {
			cp := *c
			ci = &cp
		}
	})
	return GrafanaLink(g, al, ci, time.Now().UTC())
}

type GrafanaInput struct {
	DashboardURL  string `json:"dashboard_url"`
	WindowMinutes int    `json:"window_minutes"`
}

func (a *App) registerGrafana(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/grafana", a.authed(a.can("settings.alerting:view", a.grafanaView)))
	mux.HandleFunc("PUT /api/grafana", a.authed(a.can("settings.alerting:edit", a.grafanaSave)))
	mux.HandleFunc("GET /go/incidents/{id}/grafana", a.grafanaRedirect)
}

func (a *App) grafanaView(w http.ResponseWriter, r *http.Request) {
	g := a.grafanaSettings()
	if g.WindowMinute == 0 {
		g.WindowMinute = defaultGrafanaWindow
	}
	httpx.JSON(w, http.StatusOK, g)
}

func (a *App) grafanaSave(w http.ResponseWriter, r *http.Request) {
	var in GrafanaInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	dash, err := optionalURL(in.DashboardURL)
	if err != nil {
		writeError(w, invalid("url_invalid", err))
		return
	}
	if in.WindowMinutes < 0 || in.WindowMinutes > maxGrafanaWindow {
		writeError(w, invalid("window_invalid", nil))
		return
	}
	if in.WindowMinutes == 0 {
		in.WindowMinutes = defaultGrafanaWindow
	}
	now := time.Now().UTC()
	actor := current(r).user.Username
	g := model.Grafana{DashboardURL: dash, WindowMinute: in.WindowMinutes, UpdatedAt: &now, UpdatedBy: actor}
	a.deps.Store.Write(func(d *store.Data) {
		d.Settings.Alerting.Grafana = g
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.grafana", Detail: fmt.Sprintf("dashboard set: %v", dash != "")})
	})
	httpx.JSON(w, http.StatusOK, g)
}

// grafanaRedirect sends a signed-in user to the Grafana context of an incident; PagerDuty links
// point here, so the link stays right when Grafana settings change. Anybody else goes to the
// incident in Beacon, which asks them to sign in.
//
// The session cookie is SameSite=Strict, so a click in PagerDuty, a mail or a messenger (a
// cross-site navigation) arrives without it. A request without the cookie gets a tiny page of
// this origin that navigates here again: that navigation starts on Beacon itself, so the
// browser sends the cookie. The second request carries a marker, so somebody who is really not
// signed in goes on to the incident page instead of looping.
func (a *App) grafanaRedirect(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	fallback := model.IncidentURL("", id)
	if a.alerts == nil || !a.ingestReady() {
		http.Redirect(w, r, fallback, http.StatusFound)
		return
	}
	u, ok := a.hopUser(w, r, model.GrafanaHopURL("", id), fallback)
	if !ok {
		return
	}
	if !a.access.Permissions(*u).Has("incidents:view") {
		http.Redirect(w, r, fallback, http.StatusFound)
		return
	}
	al, _, err := a.alerts.Get(r.Context(), id)
	if err != nil || !al.InScope(a.incidentScope(*u)) {
		http.Redirect(w, r, fallback, http.StatusFound)
		return
	}
	link := a.grafanaLink(al)
	if link == "" || !strings.HasPrefix(link, "http") {
		http.Redirect(w, r, fallback, http.StatusFound)
		return
	}
	http.Redirect(w, r, link, http.StatusFound)
}

// hopUser is the signed-in user of a request to a /go/ link (hop is its path). Without the
// session cookie the first request gets the hop page and the second goes to the fallback; a
// session that is gone or a disabled user goes to the fallback too. ok is false when the
// response is written.
func (a *App) hopUser(w http.ResponseWriter, r *http.Request, hop, fallback string) (*model.User, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		if r.URL.Query().Get(sameSiteHop) == "" {
			hopPage(w, hop+"?"+sameSiteHop+"=1")
			return nil, false
		}
		http.Redirect(w, r, fallback, http.StatusFound)
		return nil, false
	}
	ss := a.deps.Sessions.Get(c.Value)
	var u *model.User
	if ss != nil {
		a.deps.Store.Read(func(d *store.Data) {
			if x := d.Users[ss.UserID]; x != nil && !x.Disabled {
				cp := *x
				u = &cp
			}
		})
	}
	if u == nil {
		http.Redirect(w, r, fallback, http.StatusFound)
		return nil, false
	}
	return u, true
}

// sameSiteHop marks the request made again from Beacon's own page.
const sameSiteHop = "same"

var hopTemplate = template.Must(template.New("hop").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta name="robots" content="noindex"><meta name="referrer" content="no-referrer">
<meta http-equiv="refresh" content="0;url={{.}}"><title>Beacon</title></head>
<body><p><a href="{{.}}">Beacon</a></p></body></html>`))

// hopPage navigates to a path of this origin from a page of this origin, so that the request
// is same-site and carries the SameSite=Strict session cookie.
func hopPage(w http.ResponseWriter, path string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = hopTemplate.Execute(w, path)
}
