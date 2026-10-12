package pagerduty

import (
	"context"
	"slices"
	"time"
)

// Queues of PagerDuty. An incident in PagerDuty always sits in a service: the service is the
// queue, its escalation policy says who works the queue and its teams own it. Umbrella reads
// the queues (status, policy, teams, open incidents), shows which route of Umbrella sends to
// each, links queues to the teams of Umbrella (see Gateway.SetQueueHook) and follows an
// incident moved to another queue in PagerDuty (PD.Queue of the alert).

// Queue is a PagerDuty service seen as a queue of incidents.
type Queue struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	HTMLURL string `json:"html_url"`
	// Status: active, warning, critical, maintenance or disabled.
	Status   string `json:"status"`
	PolicyID string `json:"policy_id,omitempty"`
	Policy   string `json:"policy,omitempty"`
	Teams    []Team `json:"teams"`
	// EventsKey: the service has an Events API v2 integration.
	EventsKey bool `json:"events_key"`
	// Triggered and Acknowledged count the open incidents of the queue.
	Triggered    int `json:"triggered"`
	Acknowledged int `json:"acknowledged"`
	// Routes are the Umbrella routes that send to the queue (DefaultRoute: the default
	// integration), as the settings are now.
	Routes []string `json:"routes"`
}

// Team is a PagerDuty team.
type Team struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// QueueHook is called with the queues each time they are read.
type QueueHook func(ctx context.Context, queues []Queue)

// SetQueueHook sets what runs after the queues are read (linking them to teams).
func (g *Gateway) SetQueueHook(h QueueHook) { g.queueHook = h }

// RefreshQueues reads the queues and their open incidents, then runs the queue hook.
func (g *Gateway) RefreshQueues(ctx context.Context) error {
	set, _ := g.settings()
	if !set.Enabled || set.APITokenRef == "" {
		g.mu.Lock()
		g.queues, g.stat.QueuesAt = nil, nil
		g.mu.Unlock()
		return nil
	}
	list, err := g.listServices(ctx, "integrations", "teams", "escalation_policies")
	if err == nil {
		var open map[string]*incident
		if _, open, err = g.openIncidents(ctx); err == nil {
			queues := queuesOf(list, open)
			now := time.Now().UTC()
			g.mu.Lock()
			g.queues, g.stat.QueuesAt, g.stat.QueuesError = queues, &now, ""
			g.mu.Unlock()
			if g.queueHook != nil {
				g.queueHook(ctx, g.Queues())
			}
			return nil
		}
	}
	g.mu.Lock()
	g.stat.QueuesError = err.Error()
	g.mu.Unlock()
	return err
}

// queuesOf builds the queues from the services and the open incidents (by ID).
func queuesOf(list []serviceObj, open map[string]*incident) []Queue {
	out := make([]Queue, 0, len(list))
	at := map[string]int{}
	for _, s := range list {
		q := Queue{ID: s.ID, Name: s.name(), HTMLURL: s.HTMLURL, Status: s.Status, PolicyID: s.EscalationPolicy.ID,
			Policy: s.EscalationPolicy.Summary, Teams: []Team{}, EventsKey: s.eventsKey()}
		for _, t := range s.Teams {
			q.Teams = append(q.Teams, Team{ID: t.ID, Name: t.Summary})
		}
		at[s.ID] = len(out)
		out = append(out, q)
	}
	for _, in := range open {
		i, ok := at[in.Service.ID]
		if !ok {
			continue
		}
		switch in.Status {
		case "triggered":
			out[i].Triggered++
		case "acknowledged":
			out[i].Acknowledged++
		}
	}
	return out
}

// Queues are the queues as last read, each with the routes that send to it now.
func (g *Gateway) Queues() []Queue {
	set, _ := g.settings()
	routes := map[string][]string{}
	if set.ServiceID != "" {
		routes[set.ServiceID] = append(routes[set.ServiceID], DefaultRoute)
	}
	for _, r := range set.Routes {
		if r.PDServiceID != "" {
			routes[r.PDServiceID] = append(routes[r.PDServiceID], r.ID)
		}
	}
	g.mu.Lock()
	out := slices.Clone(g.queues)
	g.mu.Unlock()
	for i := range out {
		out[i].Teams = slices.Clone(out[i].Teams)
		out[i].Routes = append([]string{}, routes[out[i].ID]...)
	}
	return out
}
