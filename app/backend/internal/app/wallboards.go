package app

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"html"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app/tvweb"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	maxWallboardTitle       = 200
	maxWallboardDescription = 2000
	maxWallboardNetworks    = 200
	maxWallboardTargets     = 500
	maxResolvedMinutes      = 1440
	defaultRefreshSeconds   = 30
	minRefreshSeconds       = 5
	maxRefreshSeconds       = 600
)

var wallboardSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

// randomSlug is an address nobody can guess: 160 random bits in lower-case base32.
func randomSlug() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

// WallboardService keeps the TV wallboards: pages for the operations room opened without
// signing in from the allowed networks.
type WallboardService struct {
	st  *store.Store
	now func() time.Time
}

func NewWallboardService(st *store.Store) *WallboardService {
	return &WallboardService{st: st, now: func() time.Time { return time.Now().UTC() }}
}

type WallboardView struct {
	model.Wallboard
	CIs      []TargetRef `json:"cis"`
	Services []TargetRef `json:"services"`
	Teams    []TargetRef `json:"teams"`
	Path     string      `json:"path"`
	// InScope: the viewer may change the wallboard (its targets are within their scope).
	InScope bool `json:"in_scope"`
}

type WallboardInput struct {
	Slug             string   `json:"slug"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	Enabled          bool     `json:"enabled"`
	CIIDs            []string `json:"ci_ids"`
	ServiceIDs       []string `json:"service_ids"`
	TeamIDs          []string `json:"team_ids"`
	Severities       []string `json:"severities"`
	Methods          []string `json:"methods"`
	ShowAcknowledged bool     `json:"show_acknowledged"`
	ShowSuppressed   bool     `json:"show_suppressed"`
	ResolvedMinutes  int      `json:"resolved_minutes"`
	Sort             string   `json:"sort"`
	RefreshSeconds   int      `json:"refresh_seconds"`
	Theme            string   `json:"theme"`
	Locale           string   `json:"locale"`
	Timezone         string   `json:"timezone"`
	AllowedNetworks  []string `json:"allowed_networks"`
}

func refsOf(ids []string, name func(string) (string, bool)) []TargetRef {
	out := []TargetRef{}
	for _, id := range ids {
		if n, ok := name(id); ok {
			out = append(out, TargetRef{ID: id, Name: n})
		} else {
			out = append(out, TargetRef{ID: id, Name: id, Missing: true})
		}
	}
	return out
}

func (s *WallboardService) view(d *store.Data, w *model.Wallboard) WallboardView {
	v := WallboardView{Wallboard: *w, Path: "/tv/" + w.Slug}
	v.CIIDs, v.ServiceIDs, v.TeamIDs = nonNil(w.CIIDs), nonNil(w.ServiceIDs), nonNil(w.TeamIDs)
	v.Severities, v.Methods, v.AllowedNetworks = nonNil(w.Severities), nonNil(w.Methods), nonNil(w.AllowedNetworks)
	v.CIs = refsOf(w.CIIDs, func(id string) (string, bool) {
		if ci := d.ConfigItems[id]; ci != nil {
			return ci.Name, true
		}
		return "", false
	})
	v.Services = refsOf(w.ServiceIDs, func(id string) (string, bool) {
		if svc := d.Services[id]; svc != nil {
			return svc.Name, true
		}
		return "", false
	})
	v.Teams = refsOf(w.TeamIDs, func(id string) (string, bool) {
		if t := d.Teams[id]; t != nil {
			return t.Name, true
		}
		return "", false
	})
	return v
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (s *WallboardService) List(sc viewScope) []WallboardView {
	out := []WallboardView{}
	s.st.Read(func(d *store.Data) {
		for _, w := range d.Wallboards {
			v := s.view(d, w)
			v.InScope = sc.checkTargets(d, w.CIIDs, w.ServiceIDs, w.TeamIDs) == nil
			out = append(out, v)
		}
	})
	slices.SortFunc(out, func(a, b WallboardView) int {
		return cmp.Or(byName(a.Title, b.Title), cmp.Compare(a.ID, b.ID))
	})
	return out
}

func (s *WallboardService) Get(id string) (WallboardView, error) {
	var out WallboardView
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		if w := d.Wallboards[id]; w != nil {
			out, err = s.view(d, w), nil
		}
	})
	return out, err
}

// normalizeNetworks validates the allowed networks: CIDRs are masked, single addresses are
// kept as written.
func normalizeNetworks(in []string) ([]string, error) {
	out := []string{}
	for _, n := range in {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if strings.Contains(n, "%") {
			return nil, invalid("network_invalid", fmt.Errorf("%q: zones are not allowed", n))
		}
		p, err := parseNetwork(n)
		if err != nil {
			return nil, invalid("network_invalid", fmt.Errorf("%q is not an address or a network", n))
		}
		if strings.Contains(n, "/") {
			n = p.String()
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	switch {
	case len(out) == 0:
		return nil, invalid("networks_required", nil)
	case len(out) > maxWallboardNetworks:
		return nil, invalid("too_many_networks", nil)
	}
	return out, nil
}

func (s *WallboardService) check(d *store.Data, selfID string, in *WallboardInput) error {
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	in.CIIDs, in.ServiceIDs, in.TeamIDs = uniq(in.CIIDs), uniq(in.ServiceIDs), uniq(in.TeamIDs)
	in.Severities, in.Methods = uniq(in.Severities), uniq(in.Methods)
	in.Sort, in.Theme, in.Locale = strings.TrimSpace(in.Sort), strings.TrimSpace(in.Theme), strings.TrimSpace(in.Locale)
	in.Timezone = strings.TrimSpace(in.Timezone)
	if in.Sort == "" {
		in.Sort = model.WallboardSortNewest
	}
	if in.Theme == "" {
		in.Theme = model.WallboardThemeDark
	}
	if in.RefreshSeconds == 0 {
		in.RefreshSeconds = defaultRefreshSeconds
	}
	switch {
	case in.Title == "" || utf8.RuneCountInString(in.Title) > maxWallboardTitle:
		return invalid("title_invalid", nil)
	case utf8.RuneCountInString(in.Description) > maxWallboardDescription:
		return invalid("description_too_long", nil)
	case !wallboardSlug.MatchString(in.Slug):
		return invalid("slug_invalid", nil)
	case in.Sort != model.WallboardSortNewest && in.Sort != model.WallboardSortOldest:
		return invalid("sort_invalid", nil)
	case in.RefreshSeconds < minRefreshSeconds || in.RefreshSeconds > maxRefreshSeconds:
		return invalid("refresh_invalid", nil)
	case in.Theme != model.WallboardThemeDark && in.Theme != model.WallboardThemeLight:
		return invalid("theme_invalid", nil)
	case in.Locale != "" && !model.ValidLocale(in.Locale):
		return invalid("locale_invalid", nil)
	case in.Timezone != "" && !model.ValidTimezone(in.Timezone):
		return invalid("timezone_invalid", nil)
	case in.ResolvedMinutes < 0 || in.ResolvedMinutes > maxResolvedMinutes:
		return invalid("resolved_invalid", nil)
	case len(in.CIIDs)+len(in.ServiceIDs)+len(in.TeamIDs) > maxWallboardTargets:
		return invalid("too_many_targets", nil)
	}
	for _, sev := range in.Severities {
		if !model.ValidSeverity(sev) {
			return invalid("severity_invalid", fmt.Errorf("unknown severity %q", sev))
		}
	}
	for _, m := range in.Methods {
		if !model.ValidMethod(m) {
			return invalid("method_invalid", fmt.Errorf("unknown method %q", m))
		}
	}
	nets, err := normalizeNetworks(in.AllowedNetworks)
	if err != nil {
		return err
	}
	in.AllowedNetworks = nets
	for _, w := range d.Wallboards {
		if w.ID != selfID && strings.EqualFold(w.Slug, in.Slug) {
			return invalid("slug_taken", nil)
		}
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
	for _, id := range in.TeamIDs {
		if d.Teams[id] == nil {
			return invalid("team_not_found", fmt.Errorf("team %s does not exist", id))
		}
	}
	return nil
}

func (in WallboardInput) apply(w *model.Wallboard) {
	w.Slug, w.Title, w.Description, w.Enabled = in.Slug, in.Title, in.Description, in.Enabled
	w.CIIDs, w.ServiceIDs, w.TeamIDs = in.CIIDs, in.ServiceIDs, in.TeamIDs
	w.Severities, w.Methods = in.Severities, in.Methods
	w.ShowAcknowledged, w.ShowSuppressed, w.ResolvedMinutes = in.ShowAcknowledged, in.ShowSuppressed, in.ResolvedMinutes
	w.Sort, w.RefreshSeconds, w.Theme, w.Locale, w.Timezone = in.Sort, in.RefreshSeconds, in.Theme, in.Locale, in.Timezone
	w.AllowedNetworks = in.AllowedNetworks
}

func (s *WallboardService) Create(actor string, sc viewScope, in WallboardInput) (WallboardView, error) {
	now := s.now()
	var out WallboardView
	var err error
	// Without an address of its own choosing the wallboard gets one nobody can guess.
	if strings.TrimSpace(in.Slug) == "" {
		in.Slug = randomSlug()
	}
	s.st.Write(func(d *store.Data) {
		if err = s.check(d, "", &in); err != nil {
			return
		}
		if err = sc.checkTargets(d, in.CIIDs, in.ServiceIDs, in.TeamIDs); err != nil {
			return
		}
		w := &model.Wallboard{ID: d.NextID("TV"), CreatedBy: actor, CreatedAt: now, UpdatedBy: actor, UpdatedAt: now}
		in.apply(w)
		d.Wallboards[w.ID] = w
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "wallboard.create", Object: w.ID, Detail: w.Title})
		out = s.view(d, w)
		out.InScope = true
	})
	return out, err
}

func (s *WallboardService) Update(actor string, sc viewScope, id string, in WallboardInput) (WallboardView, error) {
	now := s.now()
	var out WallboardView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		w := d.Wallboards[id]
		if w == nil {
			return
		}
		if strings.TrimSpace(in.Slug) == "" {
			in.Slug = w.Slug
		}
		if err = s.check(d, id, &in); err != nil {
			return
		}
		if err = sc.covers(d, w.CIIDs, w.ServiceIDs, w.TeamIDs); err != nil {
			return
		}
		if err = sc.checkTargets(d, in.CIIDs, in.ServiceIDs, in.TeamIDs); err != nil {
			return
		}
		in.apply(w)
		w.UpdatedBy, w.UpdatedAt = actor, now
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "wallboard.update", Object: w.ID, Detail: w.Title})
		out = s.view(d, w)
		out.InScope = true
	})
	return out, err
}

// Rotate gives the wallboard a new random address; screens on the old one stop getting data.
func (s *WallboardService) Rotate(actor string, sc viewScope, id string) (WallboardView, error) {
	now := s.now()
	var out WallboardView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		w := d.Wallboards[id]
		if w == nil {
			return
		}
		if err = sc.covers(d, w.CIIDs, w.ServiceIDs, w.TeamIDs); err != nil {
			return
		}
		w.Slug, w.UpdatedBy, w.UpdatedAt = randomSlug(), actor, now
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "wallboard.rotate", Object: w.ID, Detail: w.Title})
		out = s.view(d, w)
		out.InScope = true
	})
	return out, err
}

func (s *WallboardService) Delete(actor string, sc viewScope, id string) error {
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		if w := d.Wallboards[id]; w != nil {
			if err = sc.covers(d, w.CIIDs, w.ServiceIDs, w.TeamIDs); err != nil {
				return
			}
			delete(d.Wallboards, id)
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "wallboard.delete", Object: id, Detail: w.Title})
			err = nil
		}
	})
	return err
}

// Targets finds configuration items, services and teams by name for the wallboard editor.
func (s *WallboardService) Targets(q string, sc viewScope) map[string][]TargetRef {
	out := (&MaintenanceService{st: s.st}).Targets(q, sc)
	q = strings.ToLower(strings.TrimSpace(q))
	teams := []TargetRef{}
	s.st.Read(func(d *store.Data) {
		for _, t := range d.Teams {
			if (q == "" || strings.Contains(strings.ToLower(t.Name), q)) && sc.hasTeam(d, t.ID) {
				teams = append(teams, TargetRef{ID: t.ID, Name: t.Name})
			}
		}
	})
	slices.SortFunc(teams, func(a, b TargetRef) int { return byName(a.Name, b.Name) })
	if len(teams) > maxTargetResults {
		teams = teams[:maxTargetResults]
	}
	out["teams"] = teams
	return out
}

// BySlug returns a copy of the wallboard with the slug.
func (s *WallboardService) BySlug(slug string) *model.Wallboard {
	slug = strings.ToLower(slug)
	var out *model.Wallboard
	s.st.Read(func(d *store.Data) {
		for _, w := range d.Wallboards {
			if w.Slug == slug {
				c := *w
				out = &c
				return
			}
		}
	})
	return out
}

// Allows reports whether a client address is inside one of the allowed networks.
func wallboardAllows(w *model.Wallboard, ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	for _, n := range w.AllowedNetworks {
		if p, err := parseNetwork(n); err == nil && p.Contains(ip) {
			return true
		}
	}
	return false
}

// Public payload of a wallboard: no people, contacts, PagerDuty keys or labels.

type tvBoard struct {
	Slug           string `json:"slug"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	RefreshSeconds int    `json:"refresh_seconds"`
	Theme          string `json:"theme"`
	Locale         string `json:"locale"`
	// Timezone is the zone of the clock and times: the wallboard's own or the installation's.
	Timezone         string `json:"timezone"`
	Sort             string `json:"sort"`
	ShowAcknowledged bool   `json:"show_acknowledged"`
	ResolvedMinutes  int    `json:"resolved_minutes"`
}

type tvCounts struct {
	Total int `json:"total"`
	model.SeverityCounts
	Acknowledged int `json:"acknowledged"`
	Open         int `json:"open"`
}

type tvIncident struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	CIName     string     `json:"ci_name"`
	CIKind     string     `json:"ci_kind"`
	Signal     string     `json:"signal"`
	Method     string     `json:"method"`
	Severity   string     `json:"severity"`
	Status     string     `json:"status"`
	OpenedAt   time.Time  `json:"opened_at"`
	FirstSeen  time.Time  `json:"first_seen"`
	LastSeen   time.Time  `json:"last_seen"`
	ResolvedAt *time.Time `json:"resolved_at"`
	AckedBy    string     `json:"acked_by"`
	Count      int        `json:"count"`
	Suppressed bool       `json:"suppressed"`
	Fallback   bool       `json:"fallback"`
	Services   []string   `json:"services"`
	Team       string     `json:"team"`
}

type TVPayload struct {
	Board         tvBoard      `json:"board"`
	DefaultLocale string       `json:"default_locale"`
	Version       string       `json:"version"`
	GeneratedAt   time.Time    `json:"generated_at"`
	Ready         bool         `json:"ready"`
	Counts        tvCounts     `json:"counts"`
	Incidents     []tvIncident `json:"incidents"`
	More          bool         `json:"more"`
	// Severities is the alert scale (model.Severities), most severe first: the page takes
	// its order, counters, colours and words from it.
	Severities []model.Severity `json:"severities"`
}

// boardFilter: a team target matches the incidents routed to the team and the incidents of every
// service the team owns or supports.
func boardFilter(d *store.Data, w *model.Wallboard) alert.BoardFilter {
	services := slices.Clone(w.ServiceIDs)
	if len(w.TeamIDs) > 0 {
		for _, id := range teamServiceIDs(d, w.TeamIDs) {
			if !slices.Contains(services, id) {
				services = append(services, id)
			}
		}
	}
	return alert.BoardFilter{CIIDs: w.CIIDs, ServiceIDs: services, TeamIDs: w.TeamIDs, Severities: w.Severities, Methods: w.Methods,
		ShowAcknowledged: w.ShowAcknowledged, ShowSuppressed: w.ShowSuppressed, ResolvedMinutes: w.ResolvedMinutes,
		Oldest: w.Sort == model.WallboardSortOldest}
}

func tvIncidentOf(a alert.Alert) tvIncident {
	inc := tvIncident{ID: a.ID, Title: a.Title, CIName: a.CIName, CIKind: a.CIKind, Signal: a.Signal, Method: a.Method, Severity: a.Severity,
		Status: a.Status, OpenedAt: a.OpenedAt, FirstSeen: a.FirstSeen, LastSeen: a.LastSeen, ResolvedAt: a.ResolvedAt, AckedBy: a.AckedBy,
		Count: a.Count, Suppressed: a.Suppressed, Fallback: a.Fallback, Services: []string{}}
	for _, s := range a.Route.Services {
		inc.Services = append(inc.Services, s.Name)
	}
	if a.Route.Team != nil {
		inc.Team = a.Route.Team.Name
	}
	return inc
}

// wallboardPayload builds what the TV page shows; while the alert engine is not ready the
// lists are empty and Ready is false.
func (a *App) wallboardPayload(ctx context.Context, w *model.Wallboard) (TVPayload, error) {
	set := a.settings.Get()
	locale := set.DefaultLocale
	if !model.ValidLocale(locale) {
		locale = model.LocaleRU
	}
	out := TVPayload{
		Board: tvBoard{Slug: w.Slug, Title: w.Title, Description: w.Description, RefreshSeconds: w.RefreshSeconds, Theme: w.Theme,
			Locale: w.Locale, Timezone: cmp.Or(w.Timezone, DefaultTimezone(set)), Sort: w.Sort, ShowAcknowledged: w.ShowAcknowledged, ResolvedMinutes: w.ResolvedMinutes},
		DefaultLocale: locale, Version: a.opt.Version, GeneratedAt: time.Now().UTC().Truncate(time.Second), Incidents: []tvIncident{},
		Severities: model.Severities,
	}
	if a.alerts == nil || !a.ingestReady() {
		return out, nil
	}
	var f alert.BoardFilter
	a.deps.Store.Read(func(d *store.Data) { f = boardFilter(d, w) })
	page, err := a.alerts.Board(ctx, f)
	if err != nil {
		return out, err
	}
	out.Ready, out.More = true, page.More
	c := page.Counts
	out.Counts = tvCounts{Total: c.Total, SeverityCounts: c.SeverityCounts, Acknowledged: c.Acknowledged, Open: c.Open}
	for _, al := range page.Alerts {
		out.Incidents = append(out.Incidents, tvIncidentOf(al))
	}
	return out, nil
}

func (a *App) registerWallboards(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/wallboards", a.authed(a.can("wallboards:view", a.listWallboards)))
	mux.HandleFunc("GET /api/wallboards/targets", a.authed(a.can("wallboards:edit", a.wallboardTargets)))
	mux.HandleFunc("GET /api/wallboards/{id}", a.authed(a.can("wallboards:view", a.getWallboard)))
	mux.HandleFunc("GET /api/wallboards/{id}/preview", a.authed(a.can("wallboards:view", a.previewWallboard)))
	mux.HandleFunc("POST /api/wallboards", a.authed(a.can("wallboards:edit", a.createWallboard)))
	mux.HandleFunc("PUT /api/wallboards/{id}", a.authed(a.can("wallboards:edit", a.updateWallboard)))
	mux.HandleFunc("POST /api/wallboards/{id}/rotate", a.authed(a.can("wallboards:edit", a.rotateWallboard)))
	mux.HandleFunc("DELETE /api/wallboards/{id}", a.authed(a.can("wallboards:edit", a.deleteWallboard)))
	mux.HandleFunc("GET /api/wallboards/proxies", a.authed(a.can("wallboards:view", a.proxySettings)))
	mux.HandleFunc("PUT /api/wallboards/proxies", a.authed(a.can("wallboards:proxies", a.saveProxySettings)))

	// Public: no sign-in; the client address is checked against the allowed networks.
	mux.HandleFunc("GET /tv/{slug}", a.tvPage)
	mux.HandleFunc("GET /tv/assets/{file}", a.tvAsset)
	mux.HandleFunc("GET /api/public/tv/{slug}", a.tvData)
}

type wallboardList struct {
	Wallboards []WallboardView `json:"wallboards"`
	ClientIP   string          `json:"client_ip"`
}

func addrString(ip netip.Addr) string {
	if !ip.IsValid() {
		return ""
	}
	return ip.String()
}

func (a *App) listWallboards(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, wallboardList{Wallboards: a.wallboards.List(a.userScope(current(r).user)), ClientIP: addrString(a.trustedProxies().ClientIP(r))})
}

func (a *App) getWallboard(w http.ResponseWriter, r *http.Request) {
	out, err := a.wallboards.Get(r.PathValue("id"))
	settingsRespond(w, out, err)
}

func (a *App) wallboardTargets(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, a.wallboards.Targets(r.URL.Query().Get("q"), a.userScope(current(r).user)))
}

func (a *App) createWallboard(w http.ResponseWriter, r *http.Request) {
	var in WallboardInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.wallboards.Create(current(r).user.Username, a.userScope(current(r).user), in)
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (a *App) updateWallboard(w http.ResponseWriter, r *http.Request) {
	var in WallboardInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.wallboards.Update(current(r).user.Username, a.userScope(current(r).user), r.PathValue("id"), in)
	settingsRespond(w, out, err)
}

func (a *App) rotateWallboard(w http.ResponseWriter, r *http.Request) {
	out, err := a.wallboards.Rotate(current(r).user.Username, a.userScope(current(r).user), r.PathValue("id"))
	settingsRespond(w, out, err)
}

func (a *App) deleteWallboard(w http.ResponseWriter, r *http.Request) {
	if err := a.wallboards.Delete(current(r).user.Username, a.userScope(current(r).user), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) previewWallboard(w http.ResponseWriter, r *http.Request) {
	var board *model.Wallboard
	a.deps.Store.Read(func(d *store.Data) {
		if x := d.Wallboards[r.PathValue("id")]; x != nil {
			c := *x
			board = &c
		}
	})
	if board == nil {
		writeError(w, ErrNotFound)
		return
	}
	out, err := a.wallboardPayload(r.Context(), board)
	settingsRespond(w, out, err)
}

// tvAccess finds an enabled wallboard the client may open. Unknown, disabled and denied
// wallboards look the same to the client.
func (a *App) tvAccess(r *http.Request) (*model.Wallboard, netip.Addr) {
	ip := a.trustedProxies().ClientIP(r)
	board := a.wallboards.BySlug(r.PathValue("slug"))
	if board == nil || !board.Enabled || !wallboardAllows(board, ip) {
		return nil, ip
	}
	return board, ip
}

type tvForbidden struct {
	Error    string `json:"error"`
	ClientIP string `json:"client_ip"`
}

func (a *App) tvData(w http.ResponseWriter, r *http.Request) {
	board, ip := a.tvAccess(r)
	if board == nil {
		httpx.JSON(w, http.StatusForbidden, tvForbidden{Error: "tv_forbidden", ClientIP: addrString(ip)})
		return
	}
	out, err := a.wallboardPayload(r.Context(), board)
	settingsRespond(w, out, err)
}

const tvDeniedPage = `<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Доступ запрещён · Access denied</title>
<style>
html,body{height:100%%;margin:0}
body{display:flex;align-items:center;justify-content:center;background:#111418;color:#e8eaed;font:clamp(16px,1.6vw,40px)/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;text-align:center}
main{max-width:70ch;padding:2em}
h1{font-size:1.6em;margin:0 0 .3em;color:#ff6b6b}
p{margin:.3em 0;color:#b8bec6}
code{display:inline-block;margin:.8em 0;padding:.3em .8em;border-radius:.3em;background:#1f242b;color:#fff;font-size:1.4em}
hr{border:0;border-top:1px solid #2c333b;margin:1.5em 0}
</style></head>
<body><main>
<h1>Доступ запрещён</h1>
<p>Эта ТВ-панель не существует, отключена или недоступна с вашего адреса.</p>
<p>Сообщите администратору ваш IP-адрес:</p>
<code>%[1]s</code>
<hr>
<h1 lang="en">Access denied</h1>
<p lang="en">This TV wallboard does not exist, is disabled or is not allowed from your address.</p>
<p lang="en">Give your IP address to an administrator:</p>
<code>%[1]s</code>
</main></body></html>
`

func (a *App) tvPage(w http.ResponseWriter, r *http.Request) {
	board, ip := a.tvAccess(r)
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", "text/html; charset=utf-8")
	if board == nil {
		w.WriteHeader(http.StatusForbidden)
		shown := addrString(ip)
		if shown == "" {
			shown = "?"
		}
		fmt.Fprintf(w, tvDeniedPage, html.EscapeString(shown))
		return
	}
	body, err := tvweb.FS.ReadFile("index.html")
	if err != nil {
		writeError(w, err)
		return
	}
	_, _ = w.Write(body)
}

var tvAssetTypes = map[string]string{"tv.js": "text/javascript; charset=utf-8", "tv.css": "text/css; charset=utf-8"}

func (a *App) tvAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	ctype, ok := tvAssetTypes[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	body, err := tvweb.FS.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(body)
}

// teamServiceIDs are the services the teams own or support, sorted.
func teamServiceIDs(d *store.Data, teamIDs []string) []string {
	in := map[string]bool{}
	for _, id := range teamIDs {
		in[id] = true
	}
	var out []string
	for _, s := range d.Services {
		if s.Involves(in) {
			out = append(out, s.ID)
		}
	}
	slices.Sort(out)
	return out
}
