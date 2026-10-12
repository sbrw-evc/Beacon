package alert

import (
	"cmp"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// EventKeys are the forms of the ci field of an event a configuration item may be known by:
// the value itself, without a port (Prometheus instances look like host:9100) and the short
// host name.
func EventKeys(v string) []string {
	v = strings.ToLower(strings.TrimSpace(v))
	keys := []string{v}
	if host, port, err := net.SplitHostPort(v); err == nil {
		if _, err := strconv.Atoi(port); err == nil {
			v = host
			keys = append(keys, v)
		}
	}
	if net.ParseIP(v) == nil {
		if short, _, ok := strings.Cut(v, "."); ok && short != "" {
			keys = append(keys, short)
		}
	}
	return keys
}

// CIKeys are the names a configuration item is known by: its name, the short host name, its
// IP addresses, its aliases and the DNS name the domain controller has for it. Events and the hosts of
// monitoring systems are matched against them with EventKeys.
func CIKeys(ci *model.ConfigItem) []string {
	keys := []string{ci.Name}
	if net.ParseIP(ci.Name) == nil {
		if short, _, ok := strings.Cut(ci.Name, "."); ok {
			keys = append(keys, short)
		}
	}
	keys = append(keys, ci.IPs...)
	keys = append(keys, ci.Aliases...)
	if ci.Directory != nil && ci.Directory.DNSName != "" {
		keys = append(keys, ci.Directory.DNSName)
	}
	return keys
}

// world is what the engine needs from the catalog for one batch of events: configuration
// items and how events name them, services, teams, people and maintenance windows. It is a
// copy, so the engine does not hold the store lock while it talks to the database.
type world struct {
	cis   map[string]model.ConfigItem
	index map[string][]string
	// refs are the items by the source reference Inventory DB gives them (lower case).
	refs        map[string][]string
	hostLinks   map[string]string
	services    []model.Service
	teams       map[string]model.Team
	users       map[string]model.User
	members     map[string][]string
	maintenance []model.Maintenance
}

func snapshot(st *store.Store) *world {
	w := &world{cis: map[string]model.ConfigItem{}, index: map[string][]string{}, refs: map[string][]string{}, teams: map[string]model.Team{},
		users: map[string]model.User{}, members: map[string][]string{}}
	add := func(key, id string) {
		if key = strings.ToLower(strings.TrimSpace(key)); key != "" && !slices.Contains(w.index[key], id) {
			w.index[key] = append(w.index[key], id)
		}
	}
	st.Read(func(d *store.Data) {
		w.indexHostLinks(d)
		for id, ci := range d.ConfigItems {
			c := *ci
			c.Owners = slices.Clone(ci.Owners)
			w.cis[id] = c
			add(ci.ID, id)
			for _, k := range CIKeys(ci) {
				add(k, id)
			}
			if ci.InventoryDB != nil {
				if ref := strings.ToLower(strings.TrimSpace(ci.InventoryDB.Ref)); ref != "" && !slices.Contains(w.refs[ref], id) {
					w.refs[ref] = append(w.refs[ref], id)
				}
			}
		}
		for _, s := range d.Services {
			c := *s
			c.CIIDs = slices.Clone(s.CIIDs)
			w.services = append(w.services, c)
		}
		for id, t := range d.Teams {
			w.teams[id] = *t
		}
		for id, u := range d.Users {
			c := *u
			c.Avatar = nil
			w.users[id] = c
			if !u.Disabled {
				for _, t := range u.TeamIDs {
					w.members[t] = append(w.members[t], id)
				}
			}
		}
		for _, m := range d.Maintenance {
			w.maintenance = append(w.maintenance, *m)
		}
	})
	for _, ids := range w.index {
		slices.Sort(ids)
	}
	for _, ids := range w.refs {
		slices.Sort(ids)
	}
	for _, ids := range w.members {
		slices.Sort(ids)
	}
	slices.SortFunc(w.services, func(a, b model.Service) int { return cmp.Compare(a.ID, b.ID) })
	return w
}

// resolve finds the configuration item an event is about (see resolveCI); a host said to
// belong to no item resolves to nothing.
func (w *world) resolve(name string, labels map[string]string) *model.ConfigItem {
	ci, _ := w.resolveCI(name, labels)
	return ci
}

// Labels an event may carry besides the ci field to name its item more precisely.
const (
	// LabelSourceRef is the source reference of the item in Inventory DB
	// (inventory-db:dcim.device:100), as Umbrella Flow sends it.
	LabelSourceRef = "source_ref"
	// LabelIP is an IP address of the item, tried when the ci field matches no item.
	LabelIP = "ip"
)

// resolveCI finds the configuration item an event is about. A source_ref label that names the
// Inventory DB reference of exactly one item wins. Otherwise a ci label wins over the ci field.
// The hosts linked by hand on the monitoring systems page come first: a linked host resolves
// to its item, a host marked as no item is excluded. Then the item is matched by ID, name,
// short name, IP address or the DNS name of its computer object. An ambiguous name matches
// nothing. A name that matches no item (or several) falls back to the ip label, when it is an
// IP address of exactly one item.
func (w *world) resolveCI(name string, labels map[string]string) (ci *model.ConfigItem, excluded bool) {
	if ref := strings.ToLower(strings.TrimSpace(labels[LabelSourceRef])); ref != "" {
		if ids := w.refs[ref]; len(ids) == 1 {
			c := w.cis[ids[0]]
			return &c, false
		}
	}
	name = eventCIName(name, labels)
	if name == "" {
		return nil, false
	}
	keys := EventKeys(name)
	for _, k := range keys {
		if id, ok := w.hostLinks[k]; ok {
			if id == model.HostNoCI {
				return nil, true
			}
			c := w.cis[id]
			return &c, false
		}
	}
	for _, k := range keys {
		ids := w.index[k]
		if len(ids) == 1 {
			c := w.cis[ids[0]]
			return &c, false
		}
		if len(ids) > 1 {
			break
		}
	}
	if ip := strings.TrimSpace(labels[LabelIP]); net.ParseIP(ip) != nil {
		if ids := w.index[strings.ToLower(ip)]; len(ids) == 1 {
			c := w.cis[ids[0]]
			return &c, false
		}
	}
	return nil, false
}

// route: the item belongs to business services; the owning team of the most critical active
// one gets the alert, with its lead and members. A team with nobody hands it to its parent.
// The people responsible for the item are kept as owners.
func (w *world) route(ci *model.ConfigItem, now time.Time) Route {
	if ci == nil {
		return Route{Services: []Ref{}, People: []Person{}, Owners: []Person{}, Via: ViaNone, At: now}
	}
	var owners []Person
	for _, o := range ci.Owners {
		if u, ok := w.users[o.UserID]; ok && !u.Disabled {
			owners = append(owners, w.person(u, o.Role))
		}
	}
	return w.routeServices(w.servicesOf(ci.ID), owners, now)
}

// servicesOf lists the active and planned services of an item, the one whose team gets its
// alerts first: by criticality, active before planned, then by name.
func (w *world) servicesOf(ciID string) []model.Service {
	var svcs []model.Service
	for _, s := range w.services {
		if s.Status != model.ServiceRetired && slices.Contains(s.CIIDs, ciID) {
			svcs = append(svcs, s)
		}
	}
	slices.SortStableFunc(svcs, func(a, b model.Service) int {
		if c := cmp.Compare(model.CriticalityRank(b.Criticality), model.CriticalityRank(a.Criticality)); c != 0 {
			return c
		}
		if a.Status != b.Status {
			if a.Status == model.ServiceActive {
				return -1
			}
			if b.Status == model.ServiceActive {
				return 1
			}
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return svcs
}

// routeServices is the rule chain after the item: the first of the ordered services with an
// owning team is the primary service and its team gets the alert; the people are the lead and
// members of that team, or of its nearest ancestor that has anybody. Without people the
// owners of the item get it.
func (w *world) routeServices(svcs []model.Service, owners []Person, now time.Time) Route {
	r := Route{Services: []Ref{}, People: []Person{}, Owners: []Person{}, Via: ViaNone, At: now}
	r.Owners = append(r.Owners, owners...)
	for _, s := range svcs {
		r.Services = append(r.Services, Ref{ID: s.ID, Name: s.Name})
	}
	if len(svcs) > 0 {
		r.Service = &Ref{ID: svcs[0].ID, Name: svcs[0].Name}
	}
	for _, s := range svcs {
		t, ok := w.teams[s.OwnerTeamID]
		if !ok {
			continue
		}
		r.Service = &Ref{ID: s.ID, Name: s.Name}
		r.Team = &Ref{ID: t.ID, Name: t.Name}
		seen := map[string]bool{}
		for cur, depth := t, 0; depth < model.MaxTeamDepth && len(r.People) == 0 && r.Channel == nil; depth++ {
			if seen[cur.ID] {
				break
			}
			seen[cur.ID] = true
			r.People = w.teamPeople(cur)
			if ch := TeamChannel(cur); !ch.Empty() {
				// A team with its own channel: the channel and the lead, not every member.
				r.Channel = &ch
				r.People = slices.DeleteFunc(r.People, func(p Person) bool { return p.Role != "lead" })
			}
			p, ok := w.teams[cur.ParentID]
			if !ok {
				break
			}
			cur = p
		}
		break
	}
	switch {
	case len(r.People) > 0 || r.Channel != nil:
		r.Via = ViaService
	case len(r.Owners) > 0:
		r.Via = ViaCIOwners
	}
	return r
}

// PreviewCI is the route an incident of the item would take now, computed by the same rules
// as the engine. ok is false when the item does not exist.
func PreviewCI(st *store.Store, ciID string, now time.Time) (r Route, ok bool) {
	w := snapshot(st)
	ci, ok := w.cis[ciID]
	if !ok {
		return Route{}, false
	}
	return w.route(&ci, now), true
}

// PreviewService is the route of an incident of an item that belongs to this service only.
// Elsewhere lists the items of the service whose incidents go by another, more critical
// service instead.
func PreviewService(st *store.Store, serviceID string, now time.Time) (r Route, elsewhere []Elsewhere, ok bool) {
	w := snapshot(st)
	i := slices.IndexFunc(w.services, func(s model.Service) bool { return s.ID == serviceID })
	if i < 0 {
		return Route{}, nil, false
	}
	svc := w.services[i]
	r = w.routeServices([]model.Service{svc}, nil, now)
	elsewhere = []Elsewhere{}
	for _, id := range svc.CIIDs {
		ci, found := w.cis[id]
		if !found {
			continue
		}
		cr := w.route(&ci, now)
		if cr.Service != nil && cr.Service.ID != svc.ID {
			elsewhere = append(elsewhere, Elsewhere{CI: Ref{ID: ci.ID, Name: ci.Name}, Service: *cr.Service, Team: cr.Team})
		}
	}
	slices.SortFunc(elsewhere, func(a, b Elsewhere) int {
		return strings.Compare(strings.ToLower(a.CI.Name), strings.ToLower(b.CI.Name))
	})
	return r, elsewhere, true
}

// Elsewhere is an item of a service whose incidents are routed by another service.
type Elsewhere struct {
	CI      Ref  `json:"ci"`
	Service Ref  `json:"service"`
	Team    *Ref `json:"team,omitempty"`
}

func (w *world) teamPeople(t model.Team) []Person {
	out := []Person{}
	if u, ok := w.users[t.LeadID]; ok && !u.Disabled {
		out = append(out, w.person(u, "lead"))
	}
	for _, id := range w.members[t.ID] {
		if id != t.LeadID {
			out = append(out, w.person(w.users[id], "member"))
		}
	}
	return out
}

func (w *world) person(u model.User, role string) Person {
	name := u.Name
	if name == "" {
		name = u.DisplayName(u.Username)
	}
	return Person{UserID: u.ID, Name: name, Email: u.Email, Telegram: u.Telegram, Role: role}
}

// maintenanceFor returns the active window that covers an alert.
func (w *world) maintenanceFor(a *Alert, now time.Time) *model.Maintenance {
	for i := range w.maintenance {
		m := &w.maintenance[i]
		if m.State(now) == model.MaintenanceActive && m.Covers(a.CIID, a.Route.ServiceIDs()) {
			return m
		}
	}
	return nil
}
