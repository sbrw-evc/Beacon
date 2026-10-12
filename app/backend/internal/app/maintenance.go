package app

import (
	"cmp"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	maxMaintenanceLength = 90 * 24 * time.Hour
	// Finished windows are kept this long for the record.
	maintenanceRetention = 180 * 24 * time.Hour
	maxMaintenanceTitle  = 200
	maxMaintenanceText   = 4000
	maxMaintenanceItems  = 500
	maxTargetResults     = 50
)

// MaintenanceService keeps maintenance windows: while one lasts, incidents of its items and
// services are kept but nothing goes to PagerDuty or backup notification.
type MaintenanceService struct {
	st  *store.Store
	now func() time.Time
}

func NewMaintenanceService(st *store.Store) *MaintenanceService {
	return &MaintenanceService{st: st, now: func() time.Time { return time.Now().UTC() }}
}

type MaintenanceView struct {
	model.Maintenance
	State    string      `json:"state"`
	CIs      []TargetRef `json:"cis"`
	Services []TargetRef `json:"services"`
	// InScope: the viewer may change the window (its targets are within their scope).
	InScope bool `json:"in_scope"`
}

type TargetRef struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Missing bool   `json:"missing,omitempty"`
}

func (s *MaintenanceService) view(d *store.Data, m *model.Maintenance, now time.Time) MaintenanceView {
	v := MaintenanceView{Maintenance: *m, State: m.State(now), CIs: []TargetRef{}, Services: []TargetRef{}}
	// gob drops empty lists, so a window read back from the snapshot may have nil here.
	v.CIIDs, v.ServiceIDs = nonNil(m.CIIDs), nonNil(m.ServiceIDs)
	for _, id := range m.CIIDs {
		if ci := d.ConfigItems[id]; ci != nil {
			v.CIs = append(v.CIs, TargetRef{ID: id, Name: ci.Name})
		} else {
			v.CIs = append(v.CIs, TargetRef{ID: id, Name: id, Missing: true})
		}
	}
	for _, id := range m.ServiceIDs {
		if svc := d.Services[id]; svc != nil {
			v.Services = append(v.Services, TargetRef{ID: id, Name: svc.Name})
		} else {
			v.Services = append(v.Services, TargetRef{ID: id, Name: id, Missing: true})
		}
	}
	return v
}

var stateOrder = map[string]int{model.MaintenanceActive: 0, model.MaintenancePlanned: 1, model.MaintenanceFinished: 2}

// List: active windows first, then planned ones by start, then finished ones, newest first.
func (s *MaintenanceService) List(sc viewScope) []MaintenanceView {
	now := s.now()
	out := []MaintenanceView{}
	s.st.Read(func(d *store.Data) {
		for _, m := range d.Maintenance {
			v := s.view(d, m, now)
			v.InScope = sc.checkTargets(d, m.CIIDs, m.ServiceIDs, nil) == nil
			out = append(out, v)
		}
	})
	slices.SortFunc(out, func(a, b MaintenanceView) int {
		if c := cmp.Compare(stateOrder[a.State], stateOrder[b.State]); c != 0 {
			return c
		}
		if a.State == model.MaintenanceFinished {
			return b.End.Compare(a.End)
		}
		return a.Start.Compare(b.Start)
	})
	return out
}

type MaintenanceInput struct {
	Title      string    `json:"title"`
	Comment    string    `json:"comment"`
	CIIDs      []string  `json:"ci_ids"`
	ServiceIDs []string  `json:"service_ids"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
}

func uniq(ids []string) []string {
	out := []string{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func (s *MaintenanceService) check(d *store.Data, in *MaintenanceInput) error {
	in.Title = strings.TrimSpace(in.Title)
	in.Comment = strings.TrimSpace(in.Comment)
	in.CIIDs, in.ServiceIDs = uniq(in.CIIDs), uniq(in.ServiceIDs)
	switch {
	case in.Title == "" || len(in.Title) > maxMaintenanceTitle:
		return invalid("title_invalid", nil)
	case len(in.Comment) > maxMaintenanceText:
		return invalid("comment_too_long", nil)
	case in.Start.IsZero() || in.End.IsZero() || !in.End.After(in.Start):
		return invalid("period_invalid", nil)
	case in.End.Sub(in.Start) > maxMaintenanceLength:
		return invalid("period_too_long", nil)
	case len(in.CIIDs)+len(in.ServiceIDs) == 0:
		return invalid("targets_required", nil)
	case len(in.CIIDs)+len(in.ServiceIDs) > maxMaintenanceItems:
		return invalid("too_many_targets", nil)
	}
	for _, id := range in.CIIDs {
		if d.ConfigItems[id] == nil {
			return invalid("ci_not_found", fmt.Errorf("configuration item %s does not exist", id))
		}
	}
	for _, id := range in.ServiceIDs {
		if d.Services[id] == nil {
			return invalid("service_not_found", fmt.Errorf("service %s does not exist", id))
		}
	}
	in.Start, in.End = in.Start.UTC().Truncate(time.Second), in.End.UTC().Truncate(time.Second)
	return nil
}

func (s *MaintenanceService) purge(d *store.Data, now time.Time) {
	for id, m := range d.Maintenance {
		if now.Sub(m.End) > maintenanceRetention {
			delete(d.Maintenance, id)
		}
	}
}

func (s *MaintenanceService) Create(actor string, sc viewScope, in MaintenanceInput) (MaintenanceView, error) {
	now := s.now()
	var out MaintenanceView
	var err error
	s.st.Write(func(d *store.Data) {
		if err = s.check(d, &in); err != nil {
			return
		}
		if err = sc.checkTargets(d, in.CIIDs, in.ServiceIDs, nil); err != nil {
			return
		}
		s.purge(d, now)
		m := &model.Maintenance{ID: d.NextID("MW"), Title: in.Title, Comment: in.Comment, CIIDs: in.CIIDs, ServiceIDs: in.ServiceIDs,
			Start: in.Start, End: in.End, CreatedBy: actor, CreatedAt: now, UpdatedBy: actor, UpdatedAt: now}
		d.Maintenance[m.ID] = m
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "maintenance.create", Object: m.ID, Detail: m.Title})
		out = s.view(d, m, now)
		out.InScope = true
	})
	return out, err
}

func (s *MaintenanceService) Update(actor string, sc viewScope, id string, in MaintenanceInput) (MaintenanceView, error) {
	now := s.now()
	var out MaintenanceView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		m := d.Maintenance[id]
		if m == nil {
			return
		}
		if err = s.check(d, &in); err != nil {
			return
		}
		if err = sc.covers(d, m.CIIDs, m.ServiceIDs, nil); err != nil {
			return
		}
		if err = sc.checkTargets(d, in.CIIDs, in.ServiceIDs, nil); err != nil {
			return
		}
		m.Title, m.Comment, m.CIIDs, m.ServiceIDs, m.Start, m.End = in.Title, in.Comment, in.CIIDs, in.ServiceIDs, in.Start, in.End
		m.UpdatedBy, m.UpdatedAt = actor, now
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "maintenance.update", Object: m.ID, Detail: m.Title})
		out = s.view(d, m, now)
		out.InScope = true
	})
	return out, err
}

// Finish ends an active window now; a planned one is cancelled by deleting it.
func (s *MaintenanceService) Finish(actor string, sc viewScope, id string) (MaintenanceView, error) {
	now := s.now().Truncate(time.Second)
	var out MaintenanceView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		m := d.Maintenance[id]
		if m == nil {
			return
		}
		if err = sc.covers(d, m.CIIDs, m.ServiceIDs, nil); err != nil {
			return
		}
		if m.State(now) != model.MaintenanceActive {
			err = invalid("not_active", nil)
			return
		}
		m.End, m.UpdatedBy, m.UpdatedAt = now, actor, now
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "maintenance.finish", Object: m.ID, Detail: m.Title})
		out = s.view(d, m, now)
		out.InScope = true
	})
	return out, err
}

func (s *MaintenanceService) Delete(actor string, sc viewScope, id string) error {
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		if m := d.Maintenance[id]; m != nil {
			if err = sc.covers(d, m.CIIDs, m.ServiceIDs, nil); err != nil {
				return
			}
			delete(d.Maintenance, id)
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "maintenance.delete", Object: id, Detail: m.Title})
			err = nil
		}
	})
	return err
}

// Targets finds configuration items and services by name for the window editor; a limited
// user gets only those within their scope.
func (s *MaintenanceService) Targets(q string, sc viewScope) map[string][]TargetRef {
	q = strings.ToLower(strings.TrimSpace(q))
	cis, svcs := []TargetRef{}, []TargetRef{}
	s.st.Read(func(d *store.Data) {
		for _, ci := range d.ConfigItems {
			if (q == "" || strings.Contains(strings.ToLower(ci.Name), q) || slices.ContainsFunc(ci.IPs, func(ip string) bool { return strings.HasPrefix(ip, q) })) &&
				sc.hasCI(d, ci.ID) {
				cis = append(cis, TargetRef{ID: ci.ID, Name: ci.Name})
			}
		}
		for _, svc := range d.Services {
			if svc.Status != model.ServiceRetired && (q == "" || strings.Contains(strings.ToLower(svc.Name), q)) && sc.hasService(svc.ID) {
				svcs = append(svcs, TargetRef{ID: svc.ID, Name: svc.Name})
			}
		}
	})
	sortRefs := func(r []TargetRef) []TargetRef {
		slices.SortFunc(r, func(a, b TargetRef) int { return byName(a.Name, b.Name) })
		if len(r) > maxTargetResults {
			r = r[:maxTargetResults]
		}
		return r
	}
	return map[string][]TargetRef{"cis": sortRefs(cis), "services": sortRefs(svcs)}
}

func (a *App) registerMaintenance(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/maintenance", a.authed(a.can("maintenance:view", a.listMaintenance)))
	mux.HandleFunc("GET /api/maintenance/targets", a.authed(a.can("maintenance:edit", a.maintenanceTargets)))
	mux.HandleFunc("POST /api/maintenance", a.authed(a.can("maintenance:edit", a.createMaintenance)))
	mux.HandleFunc("PUT /api/maintenance/{id}", a.authed(a.can("maintenance:edit", a.updateMaintenance)))
	mux.HandleFunc("POST /api/maintenance/{id}/finish", a.authed(a.can("maintenance:edit", a.finishMaintenance)))
	mux.HandleFunc("DELETE /api/maintenance/{id}", a.authed(a.can("maintenance:edit", a.deleteMaintenance)))
}

func (a *App) listMaintenance(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, a.maintenance.List(a.userScope(current(r).user)))
}

func (a *App) maintenanceTargets(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, a.maintenance.Targets(r.URL.Query().Get("q"), a.userScope(current(r).user)))
}

func (a *App) createMaintenance(w http.ResponseWriter, r *http.Request) {
	var in MaintenanceInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.maintenance.Create(current(r).user.Username, a.userScope(current(r).user), in)
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (a *App) updateMaintenance(w http.ResponseWriter, r *http.Request) {
	var in MaintenanceInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.maintenance.Update(current(r).user.Username, a.userScope(current(r).user), r.PathValue("id"), in)
	settingsRespond(w, out, err)
}

func (a *App) finishMaintenance(w http.ResponseWriter, r *http.Request) {
	out, err := a.maintenance.Finish(current(r).user.Username, a.userScope(current(r).user), r.PathValue("id"))
	settingsRespond(w, out, err)
}

func (a *App) deleteMaintenance(w http.ResponseWriter, r *http.Request) {
	if err := a.maintenance.Delete(current(r).user.Username, a.userScope(current(r).user), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
