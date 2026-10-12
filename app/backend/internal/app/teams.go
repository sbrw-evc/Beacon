package app

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/notify"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// MaxTeamDepth is model.MaxTeamDepth, kept for callers of this package.
const MaxTeamDepth = model.MaxTeamDepth

var (
	ErrTeamNameTaken     = errors.New("a sibling team with this name already exists")
	ErrTeamChildConflict = errors.New("a child team has the same name as a team under the new parent")

	errTeamUnknownParent = errors.New("unknown parent team")
	errTeamCycle         = errors.New("a team cannot be placed under itself or its descendant")
	errTeamTooDeep       = fmt.Errorf("the hierarchy cannot be deeper than %d levels", MaxTeamDepth)
	errTeamUnknownLead   = errors.New("unknown lead")
)

func init() {
	statuses = append(statuses,
		orgStatus{ErrTeamNameTaken, http.StatusConflict, "team_name_taken"},
		orgStatus{ErrTeamChildConflict, http.StatusConflict, "team_child_conflict"},
	)
}

type TeamInput struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	ParentID    *string `json:"parent_id"`
	LeadID      *string `json:"lead_id"`
	Email       *string `json:"email"`
	Telegram    *string `json:"telegram"`
	// Teams and Zoom: incoming webhook URLs (https) of a Teams channel and a Zoom chat.
	Teams *string `json:"teams"`
	Zoom  *string `json:"zoom"`
}

type TeamView struct {
	model.Team
	Depth       int         `json:"depth"`
	ChildCount  int         `json:"child_count"`
	MemberCount int         `json:"member_count"`
	Members     []OrgMember `json:"members"`
	Lead        *OrgMember  `json:"lead"`
}

type TeamsService struct {
	st  *store.Store
	now func() time.Time
}

func NewTeamsService(st *store.Store) *TeamsService {
	return &TeamsService{st: st, now: func() time.Time { return time.Now().UTC() }}
}

func (s *TeamsService) List() []TeamView {
	var out []TeamView
	s.st.Read(func(d *store.Data) {
		h := newHierarchy(d)
		members := teamMembers(d)
		out = make([]TeamView, 0, len(d.Teams))
		for _, t := range d.Teams {
			out = append(out, teamView(d, h, t, members[t.ID]))
		}
	})
	slices.SortFunc(out, func(x, y TeamView) int { return byName(x.Name, y.Name) })
	return out
}

func (s *TeamsService) Get(id string) (TeamView, error) {
	var out TeamView
	err := ErrNotFound
	s.st.Read(func(d *store.Data) { out, err = s.view(d, id) })
	return out, err
}

func (s *TeamsService) view(d *store.Data, id string) (TeamView, error) {
	t := d.Teams[id]
	if t == nil {
		return TeamView{}, ErrNotFound
	}
	return teamView(d, newHierarchy(d), t, teamMembers(d)[id]), nil
}

func (s *TeamsService) Create(actor string, in TeamInput) (TeamView, error) {
	if in.Name == nil {
		return TeamView{}, invalid("invalid_team_name", errOrgNameEmpty)
	}
	t := &model.Team{}
	if err := applyTeamText(t, in); err != nil {
		return TeamView{}, err
	}
	var out TeamView
	var err error
	s.st.Write(func(d *store.Data) {
		if err = applyTeamLinks(d, t, in); err != nil {
			return
		}
		if err = newHierarchy(d).place(t.ID, t.Name, t.ParentID); err != nil {
			return
		}
		t.ID = d.NextID("TEAM")
		t.CreatedAt, t.UpdatedAt = s.now(), s.now()
		d.Teams[t.ID] = t
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "team.create", Object: t.ID, Detail: teamPath(d, t.ID)})
		out, err = s.view(d, t.ID)
	})
	return out, err
}

func (s *TeamsService) Update(actor, id string, in TeamInput) (TeamView, error) {
	var out TeamView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		cur := d.Teams[id]
		if cur == nil {
			return
		}
		next := *cur
		if err = applyTeamText(&next, in); err != nil {
			return
		}
		if err = applyTeamLinks(d, &next, in); err != nil {
			return
		}
		if err = newHierarchy(d).place(id, next.Name, next.ParentID); err != nil {
			return
		}
		if detail := diffTeam(d, cur, &next); detail != "" {
			next.UpdatedAt = s.now()
			*cur = next
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "team.update", Object: id, Detail: detail})
		}
		out, err = s.view(d, id)
	})
	return out, err
}

func (s *TeamsService) Delete(actor, id string) error {
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		t := d.Teams[id]
		if t == nil {
			return
		}
		h := newHierarchy(d)
		children := h.children[id]
		for _, c := range children {
			if h.siblingTaken(t.ParentID, c.Name, id) {
				err = ErrTeamChildConflict
				return
			}
		}
		path := teamPath(d, id)
		for _, c := range children {
			c.ParentID, c.UpdatedAt = t.ParentID, s.now()
		}
		cleared := 0
		for _, u := range d.Users {
			if u.InTeam(id) {
				u.TeamIDs = withoutID(u.TeamIDs, id)
				u.MappedTeams = withoutID(u.MappedTeams, id)
				cleared++
			}
		}
		for _, svc := range d.Services {
			if svc.OwnerTeamID == id {
				svc.OwnerTeamID = ""
			}
			if slices.Contains(svc.TeamIDs, id) {
				svc.TeamIDs = slices.DeleteFunc(slices.Clone(svc.TeamIDs), func(x string) bool { return x == id })
			}
		}
		delete(d.Teams, id)
		dropMappingRefs(d, "", id)
		detail := fmt.Sprintf("%s; %d child team(s) moved up, %d member(s) removed from it", path, len(children), cleared)
		if routes := dropPDRoutes(d, id, ""); len(routes) > 0 {
			detail += "; PagerDuty routes removed: " + strings.Join(routes, ", ")
		}
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "team.delete", Object: id, Detail: detail})
		err = nil
	})
	return err
}

func (s *TeamsService) SetMembers(actor, id string, userIDs []string) (TeamView, error) {
	var out TeamView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		t := d.Teams[id]
		if t == nil {
			return
		}
		var users []*model.User
		if users, err = orgUsers(d, userIDs); err != nil {
			return
		}
		keep := map[string]bool{}
		var added, removed []*model.User
		for _, u := range users {
			keep[u.ID] = true
			if !u.InTeam(id) {
				u.TeamIDs = append(slices.Clone(u.TeamIDs), id)
				slices.Sort(u.TeamIDs)
				added = append(added, u)
			}
		}
		for _, u := range d.Users {
			if u.InTeam(id) && !keep[u.ID] {
				u.TeamIDs = withoutID(u.TeamIDs, id)
				removed = append(removed, u)
			}
		}
		if len(added)+len(removed) > 0 {
			var parts []string
			if len(added) > 0 {
				parts = append(parts, "added "+usernames(added))
			}
			if len(removed) > 0 {
				parts = append(parts, "removed "+usernames(removed))
			}
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "team.members", Object: id, Detail: t.Name + ": " + strings.Join(parts, "; ")})
		}
		out, err = s.view(d, id)
	})
	return out, err
}

func applyTeamText(t *model.Team, in TeamInput) error {
	if in.Name != nil {
		name, err := orgName(*in.Name)
		if err != nil {
			return invalid("invalid_team_name", err)
		}
		t.Name = name
	}
	if in.Description != nil {
		desc, err := orgDescription(*in.Description)
		if err != nil {
			return invalid("invalid_description", err)
		}
		t.Description = desc
	}
	if in.Email != nil {
		e := strings.TrimSpace(*in.Email)
		if e != "" && !notify.ValidEmail(e) {
			return invalid("invalid_email", nil)
		}
		t.Email = e
	}
	if in.Telegram != nil {
		c := strings.TrimSpace(*in.Telegram)
		if c != "" && !notify.ValidChat(c) {
			return invalid("invalid_telegram", nil)
		}
		t.Telegram = c
	}
	if in.Teams != nil {
		v := strings.TrimSpace(*in.Teams)
		if v != "" && !validWebhook(v) {
			return invalid("invalid_teams", nil)
		}
		t.Teams = v
	}
	if in.Zoom != nil {
		v := strings.TrimSpace(*in.Zoom)
		if v != "" && !validWebhook(v) {
			return invalid("invalid_zoom", nil)
		}
		t.Zoom = v
	}
	return nil
}

func applyTeamLinks(d *store.Data, t *model.Team, in TeamInput) error {
	if in.ParentID != nil {
		p := strings.TrimSpace(*in.ParentID)
		if p != "" && d.Teams[p] == nil {
			return invalid("unknown_parent", errTeamUnknownParent)
		}
		t.ParentID = p
	}
	if in.LeadID != nil {
		l := strings.TrimSpace(*in.LeadID)
		if l != "" && d.Users[l] == nil {
			return invalid("unknown_lead", errTeamUnknownLead)
		}
		t.LeadID = l
	}
	return nil
}

type hierarchy struct {
	d        *store.Data
	children map[string][]*model.Team
}

func newHierarchy(d *store.Data) hierarchy {
	h := hierarchy{d: d, children: map[string][]*model.Team{}}
	for _, t := range d.Teams {
		h.children[t.ParentID] = append(h.children[t.ParentID], t)
	}
	return h
}

func (h hierarchy) depth(id string) int {
	n := 0
	for cur := id; cur != "" && n <= len(h.d.Teams); n++ {
		t := h.d.Teams[cur]
		if t == nil {
			break
		}
		cur = t.ParentID
	}
	return n
}

func (h hierarchy) height(id string) int {
	best := 0
	for _, c := range h.children[id] {
		best = max(best, h.height(c.ID))
	}
	return best + 1
}

func (h hierarchy) isSelfOrDescendant(id, candidate string) bool {
	for cur, n := candidate, 0; cur != "" && n <= len(h.d.Teams); n++ {
		if cur == id {
			return true
		}
		t := h.d.Teams[cur]
		if t == nil {
			return false
		}
		cur = t.ParentID
	}
	return false
}

func (h hierarchy) siblingTaken(parentID, name, except string) bool {
	for _, t := range h.children[parentID] {
		if t.ID != except && strings.EqualFold(t.Name, name) {
			return true
		}
	}
	return false
}

func (h hierarchy) place(id, name, parentID string) error {
	if id != "" && h.isSelfOrDescendant(id, parentID) {
		return invalid("team_cycle", errTeamCycle)
	}
	height := 1
	if id != "" {
		height = h.height(id)
	}
	if h.depth(parentID)+height > MaxTeamDepth {
		return invalid("team_too_deep", errTeamTooDeep)
	}
	if h.siblingTaken(parentID, name, id) {
		return ErrTeamNameTaken
	}
	return nil
}

func teamMembers(d *store.Data) map[string][]OrgMember {
	out := map[string][]OrgMember{}
	for _, u := range d.Users {
		for _, t := range u.TeamIDs {
			out[t] = append(out[t], orgMemberOf(d, u))
		}
	}
	return out
}

func teamView(d *store.Data, h hierarchy, t *model.Team, members []OrgMember) TeamView {
	v := TeamView{Team: *t, Depth: h.depth(t.ID), ChildCount: len(h.children[t.ID]), Members: slices.Clone(members), MemberCount: len(members)}
	if v.Members == nil {
		v.Members = []OrgMember{}
	}
	sortMembers(v.Members)
	if u := d.Users[t.LeadID]; u != nil {
		lead := orgMemberOf(d, u)
		v.Lead = &lead
	}
	return v
}

func teamPath(d *store.Data, id string) string {
	var parts []string
	for cur, n := id, 0; cur != "" && n <= len(d.Teams); n++ {
		t := d.Teams[cur]
		if t == nil {
			break
		}
		parts = append([]string{t.Name}, parts...)
		cur = t.ParentID
	}
	return strings.Join(parts, " / ")
}

func diffTeam(d *store.Data, cur, next *model.Team) string {
	var parts []string
	if cur.Name != next.Name {
		parts = append(parts, fmt.Sprintf("name %q -> %q", cur.Name, next.Name))
	}
	if cur.Description != next.Description {
		parts = append(parts, "description changed")
	}
	if cur.ParentID != next.ParentID {
		parts = append(parts, fmt.Sprintf("parent %q -> %q", teamPath(d, cur.ParentID), teamPath(d, next.ParentID)))
	}
	if cur.Email != next.Email || cur.Telegram != next.Telegram {
		parts = append(parts, fmt.Sprintf("channel %q %q -> %q %q", cur.Email, cur.Telegram, next.Email, next.Telegram))
	}
	// Webhook URLs carry a secret: the record shows them redacted.
	if cur.Teams != next.Teams {
		parts = append(parts, fmt.Sprintf("teams webhook %q -> %q", redactedOrEmpty(cur.Teams), redactedOrEmpty(next.Teams)))
	}
	if cur.Zoom != next.Zoom {
		parts = append(parts, fmt.Sprintf("zoom webhook %q -> %q", redactedOrEmpty(cur.Zoom), redactedOrEmpty(next.Zoom)))
	}
	if cur.LeadID != next.LeadID {
		parts = append(parts, fmt.Sprintf("lead %q -> %q", leadName(d, cur.LeadID), leadName(d, next.LeadID)))
	}
	return strings.Join(parts, "; ")
}

// redacted is the team as people who cannot edit it see it: the webhook URLs of its channel carry
// a secret and are redacted.
func (v TeamView) redacted() TeamView {
	v.Teams, v.Zoom = redactedOrEmpty(v.Teams), redactedOrEmpty(v.Zoom)
	return v
}

func redactedOrEmpty(v string) string {
	if v == "" {
		return ""
	}
	return model.RedactURL(v)
}

func leadName(d *store.Data, id string) string {
	if u := d.Users[id]; u != nil {
		return u.Username
	}
	return ""
}

// withoutID is a copy of ids without id; nil when nothing is left.
func withoutID(ids []string, id string) []string {
	var out []string
	for _, x := range ids {
		if x != id {
			out = append(out, x)
		}
	}
	return out
}
