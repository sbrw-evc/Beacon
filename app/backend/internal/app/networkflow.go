package app

import (
	"cmp"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sbrw-evc/beacon/app/backend/internal/alert"
	"github.com/sbrw-evc/beacon/app/backend/internal/httpx"
	"github.com/sbrw-evc/beacon/app/backend/internal/model"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
)

// The traffic window of a link to Current: from flowBefore before the incident opened to
// now while it lasts, or to flowAfter after it was resolved; the traffic of an item is the last
// ciFlowWindow.
const (
	flowBefore   = 30 * time.Minute
	flowAfter    = 10 * time.Minute
	ciFlowWindow = time.Hour
)

// flowConsoleURL is the incident console of Current at base: the item name, its IP
// address and Ledger reference (empty ones are left out) and the time window in RFC 3339.
func flowConsoleURL(base, ci, ip, ref string, from, to time.Time) string {
	q := url.Values{}
	if ci != "" {
		q.Set("ci", ci)
	}
	if ip != "" {
		q.Set("ip", ip)
	}
	if ref != "" {
		q.Set("ref", ref)
	}
	q.Set("from", from.UTC().Truncate(time.Second).Format(time.RFC3339))
	q.Set("to", to.UTC().Truncate(time.Second).Format(time.RFC3339))
	return strings.TrimRight(base, "/") + "/go/incident?" + q.Encode()
}

// ciRef is the primary IP address and the Ledger reference of an item.
func ciRef(ci *model.ConfigItem) (ip, ref string) {
	if ci == nil {
		return "", ""
	}
	if len(ci.IPs) > 0 {
		ip = ci.IPs[0]
	}
	if ci.InventoryDB != nil {
		ref = ci.InventoryDB.Ref
	}
	return ip, ref
}

// FlowLink is the traffic of an incident in Current at base: the item (from the catalog
// when the incident has one, otherwise the name, ip and source_ref of its events) and the time
// from 30 minutes before it opened to now, or to 10 minutes after it was resolved. Empty while
// the Current address is not set.
func FlowLink(base string, al alert.Alert, ci *model.ConfigItem, now time.Time) string {
	if base == "" {
		return ""
	}
	start := al.OpenedAt
	if start.IsZero() {
		start = al.FirstSeen
	}
	to := now
	if al.ResolvedAt != nil {
		if end := al.ResolvedAt.Add(flowAfter); end.Before(now) {
			to = end
		}
	}
	name := cmp.Or(al.CIName, al.EventCI)
	ip, ref := ciRef(ci)
	if ci != nil {
		name = ci.Name
	}
	ip = cmp.Or(ip, strings.TrimSpace(al.Labels[alert.LabelIP]))
	ref = cmp.Or(ref, strings.TrimSpace(al.Labels[alert.LabelSourceRef]))
	return flowConsoleURL(base, name, ip, ref, start.Add(-flowBefore), to)
}

// CIFlowLink is the traffic of an item in Current over the last hour.
func CIFlowLink(base string, ci *model.ConfigItem, now time.Time) string {
	if base == "" || ci == nil {
		return ""
	}
	ip, ref := ciRef(ci)
	return flowConsoleURL(base, ci.Name, ip, ref, now.Add(-ciFlowWindow), now)
}

func (a *App) flowSettings() model.NetworkFlow {
	var f model.NetworkFlow
	a.deps.Store.Read(func(d *store.Data) { f = d.Settings.Alerting.Flow })
	return f
}

// flowLink is the address of the traffic of an incident in Current; empty until Current is set up.
func (a *App) flowLink(al alert.Alert) string {
	var base string
	var ci *model.ConfigItem
	a.deps.Store.Read(func(d *store.Data) {
		base = d.Settings.Alerting.Flow.URL
		if c := d.ConfigItems[al.CIID]; al.CIID != "" && c != nil {
			cp := *c
			ci = &cp
		}
	})
	return FlowLink(base, al, ci, time.Now().UTC())
}

type FlowInput struct {
	URL string `json:"url"`
}

func (a *App) registerFlow(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/flow", a.authed(a.can("settings.alerting:view", a.flowView)))
	mux.HandleFunc("PUT /api/flow", a.authed(a.can("settings.alerting:edit", a.flowSave)))
	mux.HandleFunc("GET /go/incidents/{id}/flow", a.flowRedirect)
	mux.HandleFunc("GET /go/cis/{id}/flow", a.ciFlowRedirect)
}

func (a *App) flowView(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, a.flowSettings())
}

func (a *App) flowSave(w http.ResponseWriter, r *http.Request) {
	var in FlowInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	u, err := model.NormalizePublicURL(in.URL)
	if err != nil {
		writeError(w, invalid("url_invalid", err))
		return
	}
	now := time.Now().UTC()
	actor := current(r).user.Username
	f := model.NetworkFlow{URL: u, UpdatedAt: &now, UpdatedBy: actor}
	a.deps.Store.Write(func(d *store.Data) {
		d.Settings.Alerting.Flow = f
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.flow", Detail: fmt.Sprintf("address set: %v", u != "")})
	})
	httpx.JSON(w, http.StatusOK, f)
}

// flowRedirect sends a signed-in user who may see the incident to its traffic in Beacon
// Current, exactly as grafanaRedirect does for Grafana; anybody else goes to the incident.
func (a *App) flowRedirect(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	fallback := model.IncidentURL("", id)
	if a.alerts == nil || !a.ingestReady() {
		http.Redirect(w, r, fallback, http.StatusFound)
		return
	}
	u, ok := a.hopUser(w, r, model.FlowHopURL("", id), fallback)
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
	link := a.flowLink(al)
	if link == "" || !strings.HasPrefix(link, "http") {
		http.Redirect(w, r, fallback, http.StatusFound)
		return
	}
	http.Redirect(w, r, link, http.StatusFound)
}

// ciFlowRedirect sends a signed-in user who may see the catalog to the recent traffic of an
// item in Current; anybody else goes to the item card.
func (a *App) ciFlowRedirect(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	fallback := "/cis?id=" + url.QueryEscape(id)
	u, ok := a.hopUser(w, r, model.CIFlowHopURL("", id), fallback)
	if !ok {
		return
	}
	if !a.access.Permissions(*u).Has("cis:view") {
		http.Redirect(w, r, fallback, http.StatusFound)
		return
	}
	var link string
	a.deps.Store.Read(func(d *store.Data) {
		if ci := d.ConfigItems[id]; ci != nil {
			link = CIFlowLink(d.Settings.Alerting.Flow.URL, ci, time.Now().UTC())
		}
	})
	if link == "" || !strings.HasPrefix(link, "http") {
		http.Redirect(w, r, fallback, http.StatusFound)
		return
	}
	http.Redirect(w, r, link, http.StatusFound)
}
