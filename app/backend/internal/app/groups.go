package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/entra"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	groupSyncActor   = "group-sync"
	groupSyncTimeout = 5 * time.Minute
	maxMappingLabel  = 100
)

var ErrGroupSyncRunning = errors.New("a group synchronization is already running")

func init() {
	statuses = append(statuses, orgStatus{ErrGroupSyncRunning, http.StatusConflict, "group_sync_running"})
}

// GroupsService keeps the table that maps directory groups to roles and teams and re-applies it
// to known directory users on a schedule. Sign-ins apply it through assignFromGroups.
type GroupsService struct {
	st      *store.Store
	secrets Secrets
	dir     Directory
	entra   func(ctx context.Context, c entra.Config, secret string, ids []string) (map[string]directory.Membership, error)
	now     func() time.Time
	running atomic.Bool
}

func NewGroupsService(st *store.Store, secrets Secrets, dir Directory) *GroupsService {
	return &GroupsService{st: st, secrets: secrets, dir: dir, entra: entra.Memberships, now: func() time.Time { return time.Now().UTC() }}
}

type GroupMappedUsers struct {
	Roles  int `json:"roles"`
	Teams  int `json:"teams"`
	Scopes int `json:"scopes"`
}

type GroupsView struct {
	Config       model.GroupMappings  `json:"config"`
	Sync         model.GroupSyncState `json:"sync"`
	LDAPEnabled  bool                 `json:"ldap_enabled"`
	LDAPKind     string               `json:"ldap_kind"`
	EntraEnabled bool                 `json:"entra_enabled"`
	Mapped       GroupMappedUsers     `json:"mapped"`
}

func (s *GroupsService) View() GroupsView {
	var out GroupsView
	s.st.Read(func(d *store.Data) { out = groupsView(d) })
	return out
}

func groupsView(d *store.Data) GroupsView {
	cfg := d.Settings.Groups
	cfg.Mappings = slices.Clone(cfg.Mappings)
	if cfg.Mappings == nil {
		cfg.Mappings = []model.GroupMapping{}
	}
	if cfg.SyncMinutes <= 0 {
		cfg.SyncMinutes = model.DefaultGroupSyncMinutes
	}
	out := GroupsView{Config: cfg, Sync: d.GroupSync, LDAPEnabled: d.Settings.LDAP.Enabled, LDAPKind: d.Settings.LDAP.Kind,
		EntraEnabled: d.Settings.Entra.Enabled}
	for _, u := range d.Users {
		if u.MappedRole != "" && u.Role == u.MappedRole {
			out.Mapped.Roles++
		}
		if slices.ContainsFunc(u.MappedTeams, u.InTeam) {
			out.Mapped.Teams++
		}
		if u.MappedScope != "" && u.ScopeMode == u.MappedScope {
			out.Mapped.Scopes++
		}
	}
	return out
}

func (s *GroupsService) Save(actor string, in model.GroupMappings) (GroupsView, error) {
	if in.SyncMinutes == 0 {
		in.SyncMinutes = model.DefaultGroupSyncMinutes
	}
	if in.SyncMinutes < model.MinGroupSyncMinutes || in.SyncMinutes > model.MaxGroupSyncMinutes {
		return GroupsView{}, invalid("group_mapping_invalid", fmt.Errorf("the synchronization period must be %d to %d minutes",
			model.MinGroupSyncMinutes, model.MaxGroupSyncMinutes))
	}
	if len(in.Mappings) > model.MaxGroupMappings {
		return GroupsView{}, invalid("group_mapping_invalid", fmt.Errorf("at most %d rows", model.MaxGroupMappings))
	}
	var out GroupsView
	var err error
	s.st.Write(func(d *store.Data) {
		rows := make([]model.GroupMapping, 0, len(in.Mappings))
		seen := map[string]bool{}
		for i, m := range in.Mappings {
			if m, err = normalMapping(d, m); err != nil {
				err = invalid("group_mapping_invalid", fmt.Errorf("row %d: %w", i+1, err))
				return
			}
			key := m.Source + "|" + m.Group
			if seen[key] {
				err = invalid("group_mapping_invalid", fmt.Errorf("row %d: the group is already in the table", i+1))
				return
			}
			seen[key] = true
			if m.ID == "" || !slices.ContainsFunc(d.Settings.Groups.Mappings, func(x model.GroupMapping) bool { return x.ID == m.ID }) {
				m.ID = d.NextID("GRP")
			}
			rows = append(rows, m)
		}
		next := model.GroupMappings{Mappings: rows, SyncOff: in.SyncOff, SyncMinutes: in.SyncMinutes}
		if !groupMappingsEqual(d.Settings.Groups, next) {
			d.Settings.Groups = next
			sync := fmt.Sprintf("every %d min", next.SyncMinutes)
			if next.SyncOff {
				sync = "off"
			}
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.groups", Detail: fmt.Sprintf("%d mapping(s), sync %s", len(rows), sync)})
		}
		out = groupsView(d)
	})
	return out, err
}

func groupMappingsEqual(a, b model.GroupMappings) bool {
	return a.SyncOff == b.SyncOff && a.SyncMinutes == b.SyncMinutes && slices.Equal(a.Mappings, b.Mappings)
}

func normalMapping(d *store.Data, m model.GroupMapping) (model.GroupMapping, error) {
	m.Group = strings.TrimSpace(m.Group)
	m.Label = strings.Join(strings.Fields(m.Label), " ")
	m.RoleID, m.TeamID = strings.TrimSpace(m.RoleID), strings.TrimSpace(m.TeamID)
	switch m.Source {
	case model.SourceLDAP:
		if !directory.ValidDN(m.Group) {
			return m, errors.New("the group must be a DN such as cn=ops,ou=groups,dc=example,dc=org")
		}
		m.Group = directory.NormalizeDN(m.Group)
	case model.SourceEntra:
		if !entra.ValidGroupID(m.Group) {
			return m, errors.New("the group must be an Entra ID group object ID (GUID)")
		}
		m.Group = strings.ToLower(m.Group)
	default:
		return m, errors.New("the source must be ldap or entra")
	}
	if len([]rune(m.Label)) > maxMappingLabel || strings.ContainsFunc(m.Label, unicode.IsControl) {
		return m, errors.New("the label is too long")
	}
	if m.Scope != "" && m.Scope != model.ScopeAll && m.Scope != model.ScopeTeams {
		return m, errors.New("the scope must be all or teams")
	}
	if m.RoleID == "" && m.TeamID == "" && m.Scope == "" {
		return m, errors.New("choose a role, a team, a scope or several")
	}
	if m.RoleID != "" && d.Roles[m.RoleID] == nil {
		return m, fmt.Errorf("unknown role %q", m.RoleID)
	}
	if m.TeamID != "" && d.Teams[m.TeamID] == nil {
		return m, fmt.Errorf("unknown team %q", m.TeamID)
	}
	return m, nil
}

// dropMappingRefs removes a deleted role or team from the table; rows left with nothing to give go away.
func dropMappingRefs(d *store.Data, roleID, teamID string) {
	rows := d.Settings.Groups.Mappings[:0]
	for _, m := range d.Settings.Groups.Mappings {
		if roleID != "" && m.RoleID == roleID {
			m.RoleID = ""
		}
		if teamID != "" && m.TeamID == teamID {
			m.TeamID = ""
		}
		if m.RoleID != "" || m.TeamID != "" || m.Scope != "" {
			rows = append(rows, m)
		}
	}
	d.Settings.Groups.Mappings = rows
}

// matchMappings finds the role and the teams the table gives to members of groups. For the role
// the first matching row in table order wins; every matching row adds its team. Rows naming a
// role or team that no longer exists are skipped.
func matchMappings(d *store.Data, source string, groups []string) (role string, teams []string, scope string) {
	in := make(map[string]bool, len(groups))
	for _, g := range groups {
		if source == model.SourceLDAP {
			g = directory.NormalizeDN(g)
		}
		in[strings.ToLower(g)] = true
	}
	for _, m := range d.Settings.Groups.Mappings {
		if m.Source != source || !in[m.Group] {
			continue
		}
		if role == "" && m.RoleID != "" && d.Roles[m.RoleID] != nil {
			role = m.RoleID
		}
		if m.TeamID != "" && d.Teams[m.TeamID] != nil && !slices.Contains(teams, m.TeamID) {
			teams = append(teams, m.TeamID)
		}
		if scope == "" && m.Scope != "" {
			scope = m.Scope
		}
	}
	slices.Sort(teams)
	return role, teams, scope
}

// assignFromGroups sets the role and the teams of a directory user from the administrators group
// and the mapping table and describes what changed. The administrators group wins over the table.
// A mapped role replaces whatever was set by hand; when no row gives it any more, it is withdrawn
// unless it was changed by hand since. Mapped teams are added to the teams set by hand; a team
// the table gave and gives no more is withdrawn. When the groups are unknown (the directory
// could not list them) only the administrators group is applied.
func assignFromGroups(d *store.Data, u *model.User, source string, admin bool, groups []string, known bool) []string {
	var mRole, mScope string
	var mTeams []string
	if known {
		mRole, mTeams, mScope = matchMappings(d, source, groups)
	}
	scope := u.ScopeMode
	switch {
	case mScope != "":
		scope = mScope
	case known && u.MappedScope != "" && scope == u.MappedScope:
		scope = model.ScopeAll
	}
	role := u.Role
	switch {
	case admin:
		role = model.RoleAdmin
	case mRole != "":
		role = mRole
	case role == "" || role == model.RoleAdmin || d.Roles[role] == nil:
		role = d.NewUserRole()
	case known && u.MappedRole != "" && role == u.MappedRole:
		role = d.NewUserRole()
	}
	var teams []string
	for _, t := range u.TeamIDs {
		if !known || slices.Contains(mTeams, t) || !slices.Contains(u.MappedTeams, t) {
			teams = append(teams, t)
		}
	}
	for _, t := range mTeams {
		if !slices.Contains(teams, t) {
			teams = append(teams, t)
		}
	}
	slices.Sort(teams)
	if known {
		u.MappedRole, u.MappedTeams, u.MappedScope = "", mTeams, mScope
		if !admin {
			u.MappedRole = mRole
		}
	}
	var changes []string
	if role != u.Role {
		changes = append(changes, "role "+userOr(u.Role, "none")+" → "+role)
		u.Role = role
	}
	if scope != u.ScopeMode && !(scope == model.ScopeAll && u.ScopeMode == "") {
		changes = append(changes, "scope "+userOr(u.ScopeMode, model.ScopeAll)+" → "+scope)
		u.ScopeMode = scope
	}
	if !slices.Equal(teams, u.TeamIDs) && (len(teams) > 0 || len(u.TeamIDs) > 0) {
		changes = append(changes, "teams "+userOr(strings.Join(u.TeamIDs, " "), "none")+" → "+userOr(strings.Join(teams, " "), "none"))
		u.TeamIDs = teams
	}
	return changes
}

// Run synchronizes on the configured period until ctx ends.
func (s *GroupsService) Run(ctx context.Context) {
	tk := time.NewTicker(time.Minute)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			if s.due() {
				if _, err := s.Sync(ctx, groupSyncActor); err != nil && !errors.Is(err, ErrGroupSyncRunning) {
					slog.Error("groups: synchronization failed", "err", err)
				}
			}
		}
	}
}

func (s *GroupsService) due() bool {
	var cfg model.GroupMappings
	var last time.Time
	var on bool
	s.st.Read(func(d *store.Data) {
		cfg, last, on = d.Settings.Groups, d.GroupSync.StartedAt, d.Settings.LDAP.Enabled || d.Settings.Entra.Enabled
	})
	every := cfg.Interval()
	return on && every > 0 && !s.now().Before(last.Add(every))
}

// Sync reads the groups of every known directory user and re-applies the mapping table. Users the
// directory no longer has are left as they are. A directory that cannot be read is reported in the
// state; the error is only for a synchronization that cannot start.
func (s *GroupsService) Sync(ctx context.Context, actor string) (GroupsView, error) {
	if !s.running.CompareAndSwap(false, true) {
		return GroupsView{}, ErrGroupSyncRunning
	}
	defer s.running.Store(false)
	ctx, cancel := context.WithTimeout(ctx, groupSyncTimeout)
	defer cancel()
	state := model.GroupSyncState{StartedAt: s.now(), Actor: actor, OK: true}
	var ldapCfg directory.Config
	var entraCfg entra.Config
	var ldapUsers, entraUsers []string
	s.st.Read(func(d *store.Data) {
		ldapCfg, entraCfg = d.Settings.LDAP, d.Settings.Entra
		for _, u := range d.Users {
			switch {
			case u.Disabled:
			case u.Source == model.SourceLDAP:
				ldapUsers = append(ldapUsers, u.Username)
			case u.Source == model.SourceEntra && u.ExternalID != "":
				entraUsers = append(entraUsers, u.ExternalID)
			}
		}
	})
	var changed []string
	if ldapCfg.Enabled {
		state.LDAP.Checked = true
		found, err := s.ldapMemberships(ldapCfg, ldapUsers)
		if err == nil {
			changed = append(changed, s.apply(model.SourceLDAP, found, &state.LDAP, func(u *model.User) string { return u.Username })...)
		} else {
			state.LDAP.Error, state.OK = err.Error(), false
		}
	}
	if entraCfg.Enabled {
		state.Entra.Checked = true
		found, err := s.entraMemberships(ctx, entraCfg, entraUsers)
		if err == nil {
			changed = append(changed, s.apply(model.SourceEntra, found, &state.Entra, func(u *model.User) string { return u.ExternalID })...)
		} else {
			state.Entra.Error, state.OK = err.Error(), false
		}
	}
	state.FinishedAt = s.now()
	var out GroupsView
	s.st.Write(func(d *store.Data) {
		d.GroupSync = state
		if actor != groupSyncActor || len(changed) > 0 || !state.OK {
			detail := fmt.Sprintf("%d user(s) changed", len(changed))
			if len(changed) > 0 {
				detail += ": " + strings.Join(changed, "; ")
			}
			for _, e := range []string{state.LDAP.Error, state.Entra.Error} {
				if e != "" {
					detail += "; failed: " + e
				}
			}
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "groups.sync", Detail: truncate(detail, 2000)})
		}
		out = groupsView(d)
	})
	return out, nil
}

func (s *GroupsService) ldapMemberships(cfg directory.Config, users []string) (map[string]directory.Membership, error) {
	if len(users) == 0 {
		return nil, nil
	}
	if s.secrets == nil {
		return nil, errors.New("OpenBao is not available")
	}
	bind, err := s.secrets.Resolve(cfg.BindPasswordRef)
	if err != nil {
		return nil, fmt.Errorf("service account password: %w", err)
	}
	return s.dir.Memberships(cfg, bind, users)
}

func (s *GroupsService) entraMemberships(ctx context.Context, cfg entra.Config, users []string) (map[string]directory.Membership, error) {
	if len(users) == 0 {
		return nil, nil
	}
	if s.secrets == nil {
		return nil, errors.New("OpenBao is not available")
	}
	secret, err := s.secrets.Resolve(cfg.ClientSecretRef)
	if err != nil {
		return nil, fmt.Errorf("client secret: %w", err)
	}
	return s.entra(ctx, cfg, secret, users)
}

// apply writes what the directory said about users of one source; key picks the map key of a user.
func (s *GroupsService) apply(source string, found map[string]directory.Membership, stats *model.GroupSyncSource, key func(*model.User) string) []string {
	var changed []string
	s.st.Write(func(d *store.Data) {
		for _, u := range d.Users {
			if u.Source != source || u.Disabled {
				continue
			}
			m, ok := found[key(u)]
			if !ok {
				continue
			}
			stats.Users++
			if !m.Found {
				stats.Missing++
				continue
			}
			if c := assignFromGroups(d, u, source, m.Admin, m.Groups, true); len(c) > 0 {
				stats.Changed++
				changed = append(changed, u.Username+" "+strings.Join(c, ", "))
			}
		}
	})
	slices.Sort(changed)
	return changed
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
