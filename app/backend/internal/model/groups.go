package model

import "time"

// GroupMapping gives the members of a directory group a role, a team, or both. Rows are applied in
// table order: for the role and for the team separately, the first matching row wins.
type GroupMapping struct {
	ID     string `json:"id"`
	Source string `json:"source"` // SourceLDAP or SourceEntra
	// Group is a DN for LDAP / AD (stored normalized) and an object ID for Entra ID.
	Group  string `json:"group"`
	Label  string `json:"label"`
	RoleID string `json:"role_id"`
	TeamID string `json:"team_id"`
	// Scope is the scope mode members get (ScopeAll or ScopeTeams); empty leaves it alone.
	Scope string `json:"scope,omitempty"`
}

const (
	DefaultGroupSyncMinutes = 60
	MinGroupSyncMinutes     = 5
	MaxGroupSyncMinutes     = 7 * 24 * 60
	MaxGroupMappings        = 200
)

// GroupMappings is the mapping table and how often it is re-applied to known users.
type GroupMappings struct {
	Mappings    []GroupMapping `json:"mappings"`
	SyncOff     bool           `json:"sync_off"`
	SyncMinutes int            `json:"sync_minutes"`
}

// Interval is the synchronization period, zero when it is off.
func (g GroupMappings) Interval() time.Duration {
	if g.SyncOff {
		return 0
	}
	m := g.SyncMinutes
	if m <= 0 {
		m = DefaultGroupSyncMinutes
	}
	return time.Duration(m) * time.Minute
}

// GroupSyncSource is the outcome of a group synchronization for one directory.
type GroupSyncSource struct {
	Checked bool   `json:"checked"`
	Users   int    `json:"users"`
	Changed int    `json:"changed"`
	Missing int    `json:"missing"`
	Error   string `json:"error,omitempty"`
}

type GroupSyncState struct {
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at"`
	Actor      string          `json:"actor"`
	OK         bool            `json:"ok"`
	LDAP       GroupSyncSource `json:"ldap"`
	Entra      GroupSyncSource `json:"entra"`
}
