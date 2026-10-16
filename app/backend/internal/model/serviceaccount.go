package model

import (
	"slices"
	"time"
)

const (
	// SourceService marks the user a service account acts as: it signs in only with API tokens.
	SourceService = "service"

	// TokenPrefix starts every Beacon API token (umb_<random 32 bytes, base64url>).
	TokenPrefix = "umb_"
)

// ServiceAccount is a machine identity (Current, scripts) that calls the API with API
// tokens instead of a session. It has a role, teams and a visibility scope like a user, and is
// kept apart from users: it never signs in, gets no notifications and is never synchronized.
type ServiceAccount struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Role        string   `json:"role"`
	TeamIDs     []string `json:"team_ids"`
	// ScopeMode and ServiceIDs are the visibility scope, as for users (ScopeAll, ScopeTeams,
	// ScopeServices).
	ScopeMode  string   `json:"scope_mode"`
	ServiceIDs []string `json:"service_ids"`
	Disabled   bool     `json:"disabled"`
	// OwnerID is the person (a user synchronized from Keystone) the account belongs to: its tokens
	// stop working while Keystone disables that person. Accounts made before owners existed get the
	// person who created them; without one the tokens keep working until an owner is set.
	OwnerID   string    `json:"owner_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by"`
}

// Username is the name requests of the account carry in the audit log.
func (a *ServiceAccount) Username() string { return "service:" + a.Name }

// User is the account as the permission and scope checks see it.
func (a *ServiceAccount) User() User {
	return User{ID: a.ID, Username: a.Username(), Name: a.Name, Source: SourceService, Role: a.Role,
		TeamIDs: slices.Clone(a.TeamIDs), ScopeMode: a.ScopeMode, ServiceIDs: slices.Clone(a.ServiceIDs),
		Disabled: a.Disabled, CreatedAt: a.CreatedAt}
}

// APIToken is a bearer token of a service account. Only a keyed hash (HMAC-SHA256, the key in
// OpenBao) of the token is kept; the token itself is shown once, when it is created.
type APIToken struct {
	ID        string `json:"id"`
	AccountID string `json:"account_id"`
	Name      string `json:"name"`
	// Hash is the hex HMAC-SHA256 of the whole token.
	Hash string `json:"-"`
	// Hint is the start and the end of the token, enough to recognize it.
	Hint       string     `json:"hint"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	LastUsedIP string     `json:"last_used_ip,omitempty"`
	// IPs are the addresses the token was used from; a new one is written to the audit log.
	IPs       []string   `json:"-"`
	CreatedAt time.Time  `json:"created_at"`
	CreatedBy string     `json:"created_by"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	RevokedBy string     `json:"revoked_by,omitempty"`
}

// Active: the token is not revoked and has not expired.
func (t *APIToken) Active(now time.Time) bool {
	return t.RevokedAt == nil && (t.ExpiresAt == nil || now.Before(*t.ExpiresAt))
}
