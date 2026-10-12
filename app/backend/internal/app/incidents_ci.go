package app

import (
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

// The incident card leads on to the catalog: the item and services of the incident with their
// links and the maintenance window that holds it; an incident whose item is not in the catalog
// can create the item or name an existing one.

type incidentCI struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Imported  bool   `json:"imported"`
	NetBoxURL string `json:"netbox_url,omitempty"`
	// InventoryURL is the device card in Inventory DB.
	InventoryURL string   `json:"inventory_url,omitempty"`
	Aliases      []string `json:"aliases"`
}

type incidentService struct {
	ID    string       `json:"id"`
	Name  string       `json:"name"`
	Links []model.Link `json:"links"`
}

type incidentMaintenance struct {
	ID    string    `json:"id"`
	Title string    `json:"title"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// incidentContext fills in what the catalog knows about the item, services and window of an
// incident.
func incidentContext(d *store.Data, al alert.Alert, v *incidentView) {
	if ci := d.ConfigItems[al.CIID]; al.CIID != "" && ci != nil {
		c := &incidentCI{ID: ci.ID, Name: ci.Name, Kind: ci.Kind, Imported: ci.Imported(), Aliases: slices.Clone(ci.Aliases)}
		if c.Aliases == nil {
			c.Aliases = []string{}
		}
		if ci.NetBox != nil {
			c.NetBoxURL = ci.NetBox.URL
		}
		if ci.InventoryDB != nil {
			c.InventoryURL = ci.InventoryDB.URL
		}
		v.CI = c
	}
	v.Services = []incidentService{}
	for _, ref := range al.Route.Services {
		svc := d.Services[ref.ID]
		if svc == nil {
			continue
		}
		links := slices.Clone(svc.Links)
		if links == nil {
			links = []model.Link{}
		}
		v.Services = append(v.Services, incidentService{ID: svc.ID, Name: svc.Name, Links: links})
	}
	if m := d.Maintenance[al.MaintenanceID]; al.MaintenanceID != "" && m != nil {
		v.Maintenance = &incidentMaintenance{ID: m.ID, Title: m.Title, Start: m.Start, End: m.End}
	}
}

var (
	errIncidentBound   = errors.New("the incident is already bound to a configuration item")
	errIncidentUnbound = errors.New("the incident still matches no configuration item")
	errIncidentNoName  = errors.New("the events of the incident name no configuration item")
)

// unboundIncident is the incident of the request when the user sees it and it waits for an item.
func (a *App) unboundIncident(w http.ResponseWriter, r *http.Request) (alert.Alert, bool) {
	if !a.alertsReady(w) {
		return alert.Alert{}, false
	}
	al, _, err := a.alerts.Get(r.Context(), r.PathValue("id"))
	if err == nil && !al.InScope(a.incidentScope(current(r).user)) {
		err = alert.ErrNotFound
	}
	if err != nil {
		writeError(w, err)
		return al, false
	}
	if al.CIID != "" {
		httpx.Error(w, http.StatusConflict, "incident_bound", errIncidentBound)
		return al, false
	}
	if strings.TrimSpace(al.CIName) == "" {
		httpx.Error(w, http.StatusConflict, "incident_no_ci_name", errIncidentNoName)
		return al, false
	}
	return al, true
}

type incidentCIResult struct {
	CI    CIView      `json:"ci"`
	Alert alert.Alert `json:"alert"`
	// Bound: every incident bound to an item by this change (incidents of the same name with
	// other signals too).
	Bound []string `json:"bound"`
}

// rebind binds the waiting incidents now and answers with the incident of the request.
func (a *App) rebind(w http.ResponseWriter, r *http.Request, ci CIView, status int) {
	bound, err := a.alerts.BindUnknown(r.Context(), current(r).user.Username)
	if err != nil {
		writeError(w, err)
		return
	}
	al, _, err := a.alerts.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if al.CIID == "" {
		// Another item is known by the same name, so the name matches neither.
		httpx.Error(w, http.StatusConflict, "incident_unbound", errIncidentUnbound)
		return
	}
	httpx.JSON(w, status, incidentCIResult{CI: ci, Alert: al.Redacted(), Bound: bound})
}

type incidentNewCI struct {
	CIInput
	// ServiceID binds the new item to a business service (needs services:edit).
	ServiceID string `json:"service_id"`
}

// createIncidentCI creates the item an incident waits for, named as the event names it unless
// the person chose another name (then the event name is kept as an alias).
func (a *App) createIncidentCI(w http.ResponseWriter, r *http.Request) {
	al, ok := a.unboundIncident(w, r)
	if !ok {
		return
	}
	var in incidentNewCI
	if !httpx.Decode(w, r, &in) {
		return
	}
	u := current(r).user
	if in.Name == "" {
		in.Name = al.CIName
	}
	if in.ServiceID != "" {
		if !a.access.Permissions(u).Has("services:edit") {
			writeError(w, forbidden)
			return
		}
		if scope := a.incidentScope(u); scope != nil && !slices.Contains(scope, in.ServiceID) {
			writeError(w, forbidden)
			return
		}
		exists := false
		a.deps.Store.Read(func(d *store.Data) { exists = d.Services[in.ServiceID] != nil })
		if !exists {
			writeError(w, invalid("unknown_service", nil))
			return
		}
	}
	ci, err := a.cis.Create(r.Context(), u.Username, in.CIInput)
	if err != nil {
		writeError(w, err)
		return
	}
	if !knownAs(&ci.ConfigItem, al.CIName) {
		if ci, err = a.cis.AddAlias(u.Username, ci.ID, al.CIName); err != nil {
			writeError(w, err)
			return
		}
	}
	if in.ServiceID != "" {
		if _, err := a.services.BindCIs(r.Context(), u.Username, in.ServiceID, []string{ci.ID}); err != nil {
			writeError(w, err)
			return
		}
		if ci, err = a.cis.Get(ci.ID); err != nil {
			writeError(w, err)
			return
		}
	}
	a.rebind(w, r, ci, http.StatusCreated)
}

type incidentBindCI struct {
	CIID string `json:"ci_id"`
}

// bindIncidentCI names an existing item as the one the incident is about: the event name becomes
// an alias of the item, so this incident and the later events find it.
func (a *App) bindIncidentCI(w http.ResponseWriter, r *http.Request) {
	al, ok := a.unboundIncident(w, r)
	if !ok {
		return
	}
	var in incidentBindCI
	if !httpx.Decode(w, r, &in) {
		return
	}
	ci, err := a.cis.AddAlias(current(r).user.Username, in.CIID, al.CIName)
	if err != nil {
		writeError(w, err)
		return
	}
	a.rebind(w, r, ci, http.StatusOK)
}
