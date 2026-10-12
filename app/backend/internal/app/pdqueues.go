package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/pagerduty"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// QueuesView is the queues of PagerDuty (its services) as last read.
type QueuesView struct {
	Queues []pagerduty.Queue `json:"queues"`
	At     *time.Time        `json:"at,omitempty"`
	Error  string            `json:"error,omitempty"`
	// Gone are the routes whose PagerDuty service is no longer in PagerDuty.
	Gone []string `json:"gone"`
}

func (s *PagerDutyService) Queues() QueuesView {
	st := s.gw.Status()
	v := QueuesView{Queues: s.gw.Queues(), At: st.QueuesAt, Error: st.QueuesError, Gone: []string{}}
	if v.At == nil {
		return v
	}
	known := map[string]bool{}
	for _, q := range v.Queues {
		known[q.ID] = true
	}
	pd, _ := s.settings()
	for _, r := range pd.Routes {
		if r.PDServiceID != "" && !known[r.PDServiceID] {
			v.Gone = append(v.Gone, r.ID)
		}
	}
	return v
}

func (s *PagerDutyService) settings() (model.PagerDuty, map[string]model.Team) {
	var pd model.PagerDuty
	teams := map[string]model.Team{}
	s.st.Read(func(d *store.Data) {
		pd = d.Settings.Alerting.PagerDuty
		pd.Routes = append([]model.PDRoute(nil), d.Settings.Alerting.PagerDuty.Routes...)
		for id, t := range d.Teams {
			teams[id] = *t
		}
	})
	return pd, teams
}

// QueueLinks is what linking the queues did.
type QueueLinks struct {
	// Created are the names of the routes created.
	Created []string `json:"created"`
	// Renamed counts the routes whose PagerDuty service name was brought up to date.
	Renamed int `json:"renamed"`
	// Failed are the queues that could not be linked, with the reason.
	Failed map[string]string `json:"failed,omitempty"`
}

// LinkQueues links the queues of PagerDuty to the teams of Umbrella: a queue no route sends to
// yet, owned by exactly one PagerDuty team whose name is the name of a team of Umbrella, gets a
// route of that team (its Events API v2 key is read or the integration created). The PagerDuty
// names of linked queues are kept current. Routes are never removed here: one whose queue is
// gone from PagerDuty is shown as such (QueuesView.Gone) for a person to decide.
func (s *PagerDutyService) LinkQueues(ctx context.Context, actor string, queues []pagerduty.Queue) (QueueLinks, error) {
	out := QueueLinks{Created: []string{}}
	pd, teams := s.settings()
	byName := map[string][]string{}
	for id, t := range teams {
		n := strings.ToLower(strings.TrimSpace(t.Name))
		byName[n] = append(byName[n], id)
	}
	linked := map[string]bool{pd.ServiceID: pd.ServiceID != ""}
	for _, r := range pd.Routes {
		if r.PDServiceID != "" {
			linked[r.PDServiceID] = true
		}
	}
	names := map[string]string{}
	var routes []model.PDRoute
	for _, q := range queues {
		names[q.ID] = q.Name
		if linked[q.ID] || q.Status == "disabled" {
			continue
		}
		team := ""
		for _, t := range q.Teams {
			ids := byName[strings.ToLower(strings.TrimSpace(t.Name))]
			if len(ids) == 1 && (team == "" || team == ids[0]) {
				team = ids[0]
			} else if len(ids) > 0 {
				team = "-" // several teams match: a person decides
			}
		}
		if team == "" || team == "-" {
			continue
		}
		if len(pd.Routes)+len(routes) >= maxPDRoutes {
			break
		}
		key, _, err := s.gw.ServiceKey(ctx, pd, "", q.ID, true)
		if err != nil {
			if out.Failed == nil {
				out.Failed = map[string]string{}
			}
			out.Failed[q.Name] = err.Error()
			continue
		}
		r := model.PDRoute{ID: "PDR-" + strings.ToLower(auth.RandomToken("", 4)), Name: q.Name, TeamID: team, PDServiceID: q.ID, PDServiceName: q.Name}
		if r.RoutingKeyRef, err = putSecret(ctx, s.secrets, pdRoutesSecretDir, r.ID, key); err != nil {
			return out, err
		}
		routes = append(routes, r)
	}
	s.st.Write(func(d *store.Data) {
		cur := &d.Settings.Alerting.PagerDuty
		have := map[string]bool{}
		for i, r := range cur.Routes {
			have[r.PDServiceID] = true
			if n, ok := names[r.PDServiceID]; ok && r.PDServiceID != "" && n != r.PDServiceName {
				cur.Routes[i].PDServiceName = n
				out.Renamed++
			}
		}
		if n, ok := names[cur.ServiceID]; ok && cur.ServiceID != "" && n != cur.ServiceName {
			cur.ServiceName = n
			out.Renamed++
		}
		for _, r := range routes {
			// Saved settings may have linked the queue meanwhile.
			if _, team := d.Teams[r.TeamID]; have[r.PDServiceID] || !team {
				continue
			}
			cur.Routes = append(cur.Routes, r)
			out.Created = append(out.Created, r.Name)
		}
		if len(out.Created) > 0 || out.Renamed > 0 {
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.pagerduty",
				Detail: fmt.Sprintf("queues linked: %d routes created, %d renamed", len(out.Created), out.Renamed)})
		}
	})
	return out, nil
}

// autoLinkQueues is the queue hook: it links the queues when the settings ask for it.
func (s *PagerDutyService) autoLinkQueues(ctx context.Context, queues []pagerduty.Queue) {
	if pd, _ := s.settings(); !pd.Sync.Queues {
		return
	}
	res, err := s.LinkQueues(ctx, "pagerduty-sync", queues)
	if err != nil || len(res.Failed) > 0 {
		slog.Warn("pagerduty queues not linked", "err", err, "failed", res.Failed)
	}
}

func (a *App) pdQueues(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, a.pagerduty.Queues())
}

// pdLinkQueues reads the queues now and links them to the teams of Umbrella.
func (a *App) pdLinkQueues(w http.ResponseWriter, r *http.Request) {
	if err := a.pdGateway.RefreshQueues(r.Context()); err != nil {
		httpx.Error(w, http.StatusBadGateway, "pagerduty_failed", err)
		return
	}
	res, err := a.pagerduty.LinkQueues(r.Context(), current(r).user.Username, a.pdGateway.Queues())
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"links": res, "queues": a.pagerduty.Queues()})
}
