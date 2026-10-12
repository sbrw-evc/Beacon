package app

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	maxServiceName        = 120
	maxServiceDescription = 4000
	maxServiceTags        = 20
	maxServiceTag         = 40
	maxServiceLinks       = 20
	maxLinkTitle          = 120
	maxLinkURL            = 2000
	maxSupportingTeams    = 50
	maxDependencies       = 100
)

var (
	ErrServiceNameTaken = errors.New("a service with this name already exists")
)

type ServiceInput struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	OwnerTeamID string       `json:"owner_team_id"`
	TeamIDs     []string     `json:"team_ids"`
	Criticality string       `json:"criticality"`
	Status      string       `json:"status"`
	Tags        []string     `json:"tags"`
	Links       []model.Link `json:"links"`
	DependsOn   []string     `json:"depends_on"`
}

type ServiceFilter struct {
	Query       string
	Team        string
	Owner       string
	Descendants bool
	Criticality string
	Status      string
	Tag         string
}

type ServiceTeam struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Path    []string `json:"path"`
	Deleted bool     `json:"deleted"`
}

type ServiceRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ServiceCI is a configuration item a service runs on.
type ServiceCI struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Source  string `json:"source"`
	NetBox  bool   `json:"netbox"`
	Deleted bool   `json:"deleted"`
}

type ServiceView struct {
	model.Service
	CIs        []ServiceCI   `json:"cis"`
	Owner      ServiceTeam   `json:"owner"`
	Teams      []ServiceTeam `json:"teams"`
	Depends    []ServiceRef  `json:"dependencies"`
	Dependents []ServiceRef  `json:"dependents"`
}

type ServiceList struct {
	Services []ServiceView `json:"services"`
	Tags     []string      `json:"tags"`
	Total    int           `json:"total"`
}

type ServicesOfTeam struct {
	Team     ServiceTeam   `json:"team"`
	Services []ServiceView `json:"services"`
}

type ServicesService struct {
	st     *store.Store
	netbox *NetBoxService
	now    func() time.Time
}

func NewServicesService(st *store.Store, nb *NetBoxService) *ServicesService {
	return &ServicesService{st: st, netbox: nb, now: func() time.Time { return time.Now().UTC() }}
}

func (s *ServicesService) List(f ServiceFilter) ServiceList {
	out := ServiceList{Services: []ServiceView{}, Tags: []string{}}
	s.st.Read(func(d *store.Data) {
		g := newServiceGraph(d)
		match := f.matcher(d)
		tags := map[string]bool{}
		for _, svc := range d.Services {
			for _, tag := range svc.Tags {
				tags[tag] = true
			}
			if match(svc) {
				out.Services = append(out.Services, g.view(svc))
			}
		}
		out.Total = len(d.Services)
		for tag := range tags {
			out.Tags = append(out.Tags, tag)
		}
	})
	slices.Sort(out.Tags)
	sortServiceViews(out.Services)
	return out
}

func (s *ServicesService) Get(id string) (ServiceView, error) {
	var out ServiceView
	found := false
	s.st.Read(func(d *store.Data) {
		if svc := d.Services[id]; svc != nil {
			out, found = newServiceGraph(d).view(svc), true
		}
	})
	if !found {
		return out, ErrNotFound
	}
	return out, nil
}

func (s *ServicesService) ForTeam(id string, descendants bool) (ServicesOfTeam, error) {
	out := ServicesOfTeam{Services: []ServiceView{}}
	found := false
	s.st.Read(func(d *store.Data) {
		if d.Teams[id] == nil {
			return
		}
		found = true
		g := newServiceGraph(d)
		out.Team = g.team(id)
		scope := map[string]bool{id: true}
		if descendants {
			scope = serviceTeamScope(d, id)
		}
		for _, svc := range d.Services {
			if svc.Involves(scope) {
				out.Services = append(out.Services, g.view(svc))
			}
		}
	})
	if !found {
		return out, ErrNotFound
	}
	sortServiceViews(out.Services)
	return out, nil
}

func (s *ServicesService) Create(actor string, in ServiceInput) (ServiceView, error) {
	spec, err := in.normalize()
	if err != nil {
		return ServiceView{}, err
	}
	var out ServiceView
	s.st.Write(func(d *store.Data) {
		if err = spec.check(d, ""); err != nil {
			return
		}
		now := s.now()
		svc := &model.Service{ID: d.NextID("SVC"), CreatedAt: now, UpdatedAt: now}
		spec.apply(svc)
		d.Services[svc.ID] = svc
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "service.create", Object: svc.ID, Detail: svc.Name})
		out = newServiceGraph(d).view(svc)
	})
	return out, err
}

func (s *ServicesService) Update(ctx context.Context, actor, id string, in ServiceInput) (ServiceView, error) {
	spec, err := in.normalize()
	if err != nil {
		return ServiceView{}, err
	}
	var link *model.ServiceNetBox
	err = ErrNotFound
	s.st.Read(func(d *store.Data) {
		if svc := d.Services[id]; svc != nil {
			link, err = svc.NetBox, spec.check(d, id)
		}
	})
	if err != nil {
		return ServiceView{}, err
	}
	if link != nil {
		next := model.Service{ID: id}
		spec.apply(&next)
		if link, err = s.pushTag(ctx, &next, link.TagID); err != nil {
			return ServiceView{}, err
		}
	}
	var out ServiceView
	s.st.Write(func(d *store.Data) {
		svc := d.Services[id]
		if svc == nil {
			err = ErrNotFound
			return
		}
		if err = spec.check(d, id); err != nil {
			return
		}
		before := *svc
		spec.apply(svc)
		if link != nil && svc.NetBox != nil {
			svc.NetBox = link
		}
		if changed := serviceChanges(before, *svc); len(changed) > 0 {
			svc.UpdatedAt = s.now()
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "service.update", Object: id, Detail: svc.Name + ": " + strings.Join(changed, ", ")})
		}
		out = newServiceGraph(d).view(svc)
	})
	return out, err
}

// Delete removes the service and, when it is linked to NetBox, its tag there.
func (s *ServicesService) Delete(ctx context.Context, actor, id string) error {
	var link *model.ServiceNetBox
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		if svc := d.Services[id]; svc != nil {
			link, err = svc.NetBox, nil
		}
	})
	if err != nil {
		return err
	}
	if link != nil {
		if err := s.deleteTag(ctx, link.TagID); err != nil {
			return err
		}
	}
	err = ErrNotFound
	s.st.Write(func(d *store.Data) {
		svc := d.Services[id]
		if svc == nil {
			return
		}
		err = nil
		delete(d.Services, id)
		now := s.now()
		for _, other := range d.Services {
			if slices.Contains(other.DependsOn, id) {
				other.DependsOn = slices.DeleteFunc(other.DependsOn, func(x string) bool { return x == id })
				other.UpdatedAt = now
			}
		}
		detail := svc.Name
		if routes := dropPDRoutes(d, "", id); len(routes) > 0 {
			detail += "; PagerDuty routes removed: " + strings.Join(routes, ", ")
		}
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "service.delete", Object: id, Detail: detail})
	})
	return err
}

func (f ServiceFilter) matcher(d *store.Data) func(*model.Service) bool {
	q := strings.ToLower(strings.TrimSpace(f.Query))
	tag := strings.ToLower(strings.TrimSpace(f.Tag))
	scope := func(id string) map[string]bool {
		if id == "" {
			return nil
		}
		if f.Descendants {
			return serviceTeamScope(d, id)
		}
		return map[string]bool{id: true}
	}
	team, owner := scope(f.Team), scope(f.Owner)
	return func(s *model.Service) bool {
		switch {
		case f.Criticality != "" && s.Criticality != f.Criticality,
			f.Status != "" && s.Status != f.Status,
			tag != "" && !slices.Contains(s.Tags, tag),
			team != nil && !s.Involves(team),
			owner != nil && !owner[s.OwnerTeamID]:
			return false
		}
		return q == "" || serviceMatches(s, q)
	}
}

func serviceMatches(s *model.Service, q string) bool {
	if strings.Contains(strings.ToLower(s.Name), q) || strings.Contains(strings.ToLower(s.Description), q) || strings.EqualFold(s.ID, q) {
		return true
	}
	return slices.ContainsFunc(s.Tags, func(t string) bool { return strings.Contains(t, q) })
}

func serviceTeamScope(d *store.Data, root string) map[string]bool {
	children := map[string][]string{}
	for _, t := range d.Teams {
		if t.ParentID != "" {
			children[t.ParentID] = append(children[t.ParentID], t.ID)
		}
	}
	out := map[string]bool{}
	queue := []string{root}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if out[id] {
			continue
		}
		out[id] = true
		queue = append(queue, children[id]...)
	}
	return out
}

func sortServiceViews(v []ServiceView) {
	slices.SortFunc(v, func(x, y ServiceView) int {
		if c := byName(x.Name, y.Name); c != 0 {
			return c
		}
		return strings.Compare(x.ID, y.ID)
	})
}

type serviceGraph struct {
	d          *store.Data
	dependents map[string][]string
}

func newServiceGraph(d *store.Data) serviceGraph {
	g := serviceGraph{d: d, dependents: map[string][]string{}}
	for _, s := range d.Services {
		for _, dep := range s.DependsOn {
			g.dependents[dep] = append(g.dependents[dep], s.ID)
		}
	}
	return g
}

func (g serviceGraph) team(id string) ServiceTeam {
	t := g.d.Teams[id]
	if t == nil {
		return ServiceTeam{ID: id, Path: []string{}, Deleted: true}
	}
	path := []string{}
	seen := map[string]bool{}
	for cur := t; cur != nil && !seen[cur.ID]; cur = g.d.Teams[cur.ParentID] {
		seen[cur.ID] = true
		path = append([]string{cur.Name}, path...)
	}
	return ServiceTeam{ID: id, Name: t.Name, Path: path}
}

func (g serviceGraph) refs(ids []string) []ServiceRef {
	out := []ServiceRef{}
	for _, id := range ids {
		if s := g.d.Services[id]; s != nil {
			out = append(out, ServiceRef{ID: s.ID, Name: s.Name})
		}
	}
	slices.SortFunc(out, func(x, y ServiceRef) int { return byName(x.Name, y.Name) })
	return out
}

func (g serviceGraph) cis(ids []string) []ServiceCI {
	out := []ServiceCI{}
	for _, id := range ids {
		ci := g.d.ConfigItems[id]
		if ci == nil {
			out = append(out, ServiceCI{ID: id, Deleted: true})
			continue
		}
		out = append(out, ServiceCI{ID: ci.ID, Name: ci.Name, Kind: ci.Kind, Status: ci.Status, Source: ci.Source, NetBox: ci.NetBox != nil})
	}
	slices.SortFunc(out, func(x, y ServiceCI) int { return byName(x.Name, y.Name) })
	return out
}

func (g serviceGraph) view(s *model.Service) ServiceView {
	v := ServiceView{Service: *s, CIs: g.cis(s.CIIDs), Owner: g.team(s.OwnerTeamID), Teams: []ServiceTeam{}, Depends: g.refs(s.DependsOn), Dependents: g.refs(g.dependents[s.ID])}
	v.TeamIDs = serviceStrings(s.TeamIDs)
	v.Tags = serviceStrings(s.Tags)
	v.DependsOn = serviceStrings(s.DependsOn)
	v.CIIDs = serviceStrings(s.CIIDs)
	if v.Links == nil {
		v.Links = []model.Link{}
	}
	for _, id := range s.TeamIDs {
		v.Teams = append(v.Teams, g.team(id))
	}
	return v
}

func serviceStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return slices.Clone(v)
}

func reachesService(from, target string, edges map[string][]string) bool {
	seen := map[string]bool{}
	stack := []string{from}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == target {
			return true
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		stack = append(stack, edges[id]...)
	}
	return false
}

func (in ServiceInput) normalize() (ServiceInput, error) {
	out := ServiceInput{
		Name:        strings.TrimSpace(in.Name),
		Description: strings.TrimSpace(in.Description),
		OwnerTeamID: strings.TrimSpace(in.OwnerTeamID),
		Criticality: strings.TrimSpace(in.Criticality),
		Status:      strings.TrimSpace(in.Status),
	}
	if n := utf8.RuneCountInString(out.Name); n == 0 || n > maxServiceName || strings.ContainsFunc(out.Name, unicode.IsControl) {
		return out, invalid("invalid_service_name", nil)
	}
	if utf8.RuneCountInString(out.Description) > maxServiceDescription {
		return out, invalid("invalid_service_description", nil)
	}
	if out.OwnerTeamID == "" {
		return out, invalid("owner_team_required", nil)
	}
	if out.Criticality == "" {
		out.Criticality = model.CriticalityMedium
	}
	if !model.ValidCriticality(out.Criticality) {
		return out, invalid("invalid_criticality", nil)
	}
	if out.Status == "" {
		out.Status = model.ServiceActive
	}
	if !model.ValidServiceStatus(out.Status) {
		return out, invalid("invalid_service_status", nil)
	}
	var err error
	if out.TeamIDs, err = serviceIDs(in.TeamIDs, maxSupportingTeams, "too_many_teams"); err != nil {
		return out, err
	}
	if slices.Contains(out.TeamIDs, out.OwnerTeamID) {
		return out, invalid("duplicate_team", nil)
	}
	if out.DependsOn, err = serviceIDs(in.DependsOn, maxDependencies, "too_many_dependencies"); err != nil {
		return out, err
	}
	if out.Tags, err = serviceTags(in.Tags); err != nil {
		return out, err
	}
	if out.Links, err = serviceLinks(in.Links); err != nil {
		return out, err
	}
	return out, nil
}

func serviceIDs(in []string, limit int, code string) ([]string, error) {
	out := []string{}
	for _, id := range in {
		id = strings.TrimSpace(id)
		if id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	if len(out) > limit {
		return nil, invalid(code, nil)
	}
	return out, nil
}

func serviceTags(in []string) ([]string, error) {
	out := []string{}
	for _, tag := range in {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}
		if utf8.RuneCountInString(tag) > maxServiceTag || strings.ContainsFunc(tag, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == ',' }) {
			return nil, invalid("invalid_tag", nil)
		}
		if !slices.Contains(out, tag) {
			out = append(out, tag)
		}
	}
	if len(out) > maxServiceTags {
		return nil, invalid("too_many_tags", nil)
	}
	return out, nil
}

func serviceLinks(in []model.Link) ([]model.Link, error) {
	if len(in) > maxServiceLinks {
		return nil, invalid("too_many_links", nil)
	}
	out := []model.Link{}
	for _, l := range in {
		l = model.Link{Title: strings.TrimSpace(l.Title), URL: strings.TrimSpace(l.URL)}
		if l.Title == "" && l.URL == "" {
			continue
		}
		if utf8.RuneCountInString(l.Title) > maxLinkTitle || !serviceLinkURL(l.URL) {
			return nil, invalid("invalid_link", nil)
		}
		out = append(out, l)
	}
	return out, nil
}

func serviceLinkURL(raw string) bool {
	if raw == "" || len(raw) > maxLinkURL || strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

func (in ServiceInput) check(d *store.Data, self string) error {
	for _, s := range d.Services {
		if s.ID != self && strings.EqualFold(s.Name, in.Name) {
			return ErrServiceNameTaken
		}
	}
	for _, id := range append([]string{in.OwnerTeamID}, in.TeamIDs...) {
		if d.Teams[id] == nil {
			return invalid("unknown_team", nil)
		}
	}
	for _, id := range in.DependsOn {
		if self != "" && id == self {
			return invalid("dependency_self", nil)
		}
		if d.Services[id] == nil {
			return invalid("unknown_service", nil)
		}
	}
	if self == "" {
		return nil
	}
	edges := map[string][]string{}
	for _, s := range d.Services {
		edges[s.ID] = s.DependsOn
	}
	edges[self] = in.DependsOn
	for _, dep := range in.DependsOn {
		if reachesService(dep, self, edges) {
			return invalid("dependency_cycle", nil)
		}
	}
	return nil
}

func (in ServiceInput) apply(s *model.Service) {
	s.Name, s.Description, s.OwnerTeamID = in.Name, in.Description, in.OwnerTeamID
	s.TeamIDs, s.Criticality, s.Status = in.TeamIDs, in.Criticality, in.Status
	s.Tags, s.Links, s.DependsOn = in.Tags, in.Links, in.DependsOn
}

func serviceChanges(a, b model.Service) []string {
	var out []string
	add := func(name string, changed bool) {
		if changed {
			out = append(out, name)
		}
	}
	add("name", a.Name != b.Name)
	add("description", a.Description != b.Description)
	add("owner team", a.OwnerTeamID != b.OwnerTeamID)
	add("teams", !slices.Equal(a.TeamIDs, b.TeamIDs))
	add("criticality", a.Criticality != b.Criticality)
	add("status", a.Status != b.Status)
	add("tags", !slices.Equal(a.Tags, b.Tags))
	add("links", !slices.Equal(a.Links, b.Links))
	add("dependencies", !slices.Equal(a.DependsOn, b.DependsOn))
	return out
}
