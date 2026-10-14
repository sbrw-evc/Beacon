package model

import (
	"errors"
	"net/mail"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/entra"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/inventorydb"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/netbox"
)

const (
	SourceLocal = "local"
	SourceLDAP  = "ldap"
	SourceEntra = "entra"

	ScopeAll      = "all"
	ScopeTeams    = "teams"
	ScopeServices = "services"

	RoleAdmin = "admin"
	RoleUser  = "user"

	ThemeLight = "light"
	ThemeDark  = "dark"

	LocaleEN = "en"
	LocaleRU = "ru"
)

func ValidTheme(v string) bool  { return v == ThemeLight || v == ThemeDark }
func ValidLocale(v string) bool { return v == LocaleEN || v == LocaleRU }

func ValidTimezone(v string) bool {
	if v == "" || v == "Local" || len(v) > 64 || strings.ContainsAny(v, " .\\") {
		return false
	}
	_, err := time.LoadLocation(v)
	return err == nil
}

const (
	AvatarUpload = "upload"
	AvatarLDAP   = "ldap"
)

type Profile struct {
	LastName   string `json:"last_name"`
	FirstName  string `json:"first_name"`
	MiddleName string `json:"middle_name"`
	Title      string `json:"title"`
	Department string `json:"department"`
	Manager    string `json:"manager"`
	Email      string `json:"email"`
}

const MaxProfileField = 200

func (p Profile) Normalize() (Profile, error) {
	fields := []*string{&p.LastName, &p.FirstName, &p.MiddleName, &p.Title, &p.Department, &p.Manager, &p.Email}
	for _, f := range fields {
		*f = strings.Join(strings.Fields(*f), " ")
		if len([]rune(*f)) > MaxProfileField || strings.ContainsFunc(*f, unicode.IsControl) {
			return p, errors.New("profile field is too long or contains control characters")
		}
	}
	return p, nil
}

func ValidEmail(v string) bool {
	if v == "" {
		return true
	}
	a, err := mail.ParseAddress(v)
	return err == nil && a.Address == v
}

func (p Profile) DisplayName(fallback string) string {
	parts := []string{}
	for _, v := range []string{p.LastName, p.FirstName, p.MiddleName} {
		if v != "" {
			parts = append(parts, v)
		}
	}
	if len(parts) == 0 {
		return fallback
	}
	return strings.Join(parts, " ")
}

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Profile
	Avatar       []byte     `json:"-"`
	AvatarSource string     `json:"avatar_source,omitempty"`
	AvatarAt     *time.Time `json:"avatar_at,omitempty"`
	Source       string     `json:"source"`
	Timezone     string     `json:"timezone"`
	Role         string     `json:"role"`
	// TeamIDs are the teams the user is a member of; a person may be in several teams.
	TeamIDs []string `json:"team_ids"`
	// TeamID and MappedTeam are the single team of older versions, kept only so that their
	// snapshots decode; the store moves them into TeamIDs and MappedTeams on load.
	TeamID     string `json:"-"`
	MappedTeam string `json:"-"`
	// MappedRole and MappedTeams are what a group mapping last gave the user. When the mapping
	// stops giving them, a role still equal to MappedRole and the teams of MappedTeams are
	// withdrawn; a role changed by hand since then is kept.
	MappedRole  string   `json:"mapped_role,omitempty"`
	MappedTeams []string `json:"mapped_teams,omitempty"`
	// ScopeMode is what the user sees and may act on: every service (ScopeAll), the services
	// their teams own or support (ScopeTeams), or the chosen ServiceIDs (ScopeServices).
	// Administrators are never limited.
	ScopeMode  string   `json:"scope_mode"`
	ServiceIDs []string `json:"service_ids,omitempty"`
	// MappedScope is the scope mode a group mapping last gave the user.
	MappedScope string `json:"mapped_scope,omitempty"`
	// Telegram is the chat ID backup notification sends to.
	Telegram string `json:"telegram"`
	// Locale is the interface language the user chose (en, ru); empty is the default of the
	// installation. Voice calls speak it.
	Locale             string    `json:"locale,omitempty"`
	MustChangePassword bool      `json:"must_change_password"`
	PasswordRef        string    `json:"-"`
	ExternalID         string    `json:"-"`
	PasswordHash       string    `json:"-"`
	PasswordChangedAt  time.Time `json:"password_changed_at,omitzero"`
	// Passkeys are the WebAuthn credentials of the user (Windows Hello, Touch ID, security keys).
	Passkeys []Passkey `json:"-"`
	// Groups are the directory groups the user was last seen in (LDAP DNs, Entra object IDs,
	// OpenID Connect group names): Umbrella Identity gives them to connected applications.
	Groups      []string   `json:"-"`
	Disabled    bool       `json:"disabled"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

type Settings struct {
	DefaultTheme  string           `json:"default_theme"`
	DefaultLocale string           `json:"default_locale"`
	DefaultTZ     string           `json:"default_timezone"`
	Password      PasswordPolicy   `json:"password_policy"`
	LDAP          directory.Config `json:"ldap"`
	Entra         entra.Config     `json:"entra"`
	NetBox        netbox.Config    `json:"netbox"`
	// InventoryDB is the optional Inventory DB connection: devices in, alert states out.
	InventoryDB inventorydb.Config `json:"inventory_db"`
	Groups      GroupMappings      `json:"groups"`
	Alerting    Alerting           `json:"alerting"`
	// Response is incident response: impact, priority policies, escalation, war rooms, Jira.
	Response Response `json:"response"`
	// HostContext is how the incident card shows the machine of the incident.
	HostContext HostContext `json:"host_context"`
	// NewUserRole is the role of accounts created by a directory sign-in or synchronization or by
	// NetBox when no group mapping gives one. Empty or unknown: the system role "user".
	NewUserRole string `json:"new_user_role"`
	// TrustedProxies are reverse proxies set in the interface, in addition to
	// UMBRELLA_TRUSTED_PROXIES: their X-Forwarded-For is believed for the client address.
	TrustedProxies []string `json:"trusted_proxies"`
	// APITokenKeyRef is the OpenBao reference of the HMAC key API tokens are hashed with.
	APITokenKeyRef string `json:"-"`
	// PresetRolesSeeded: the preset roles were created once and are not created again.
	PresetRolesSeeded bool `json:"-"`
	// NetBoxUsersDecided: NetBox settings saved before «create accounts for contacts» existed were
	// given it on (their behaviour), later ones choose it.
	NetBoxUsersDecided bool `json:"-"`
	// PolicyExtended: the password policy got history, common-password blocking and lockout.
	PolicyExtended bool `json:"-"`
	// Passkeys are the passkey settings; Providers the external sign-in providers.
	Passkeys  PasskeySettings `json:"passkeys"`
	Providers Providers       `json:"providers"`
	// IdentityKeyRef is the OpenBao reference of the key ID tokens of Umbrella Identity are
	// signed with.
	IdentityKeyRef string    `json:"-"`
	SetupAt        time.Time `json:"setup_at"`
	SetupBy        string    `json:"setup_by"`
}

// InTeam reports whether the user is a member of the team.
func (u *User) InTeam(id string) bool { return id != "" && slices.Contains(u.TeamIDs, id) }

// MigrateTeams moves the single team of older versions into the team list.
func (u *User) MigrateTeams() bool {
	changed := false
	if u.TeamID != "" {
		if !slices.Contains(u.TeamIDs, u.TeamID) {
			u.TeamIDs = append(u.TeamIDs, u.TeamID)
			slices.Sort(u.TeamIDs)
		}
		u.TeamID, changed = "", true
	}
	if u.MappedTeam != "" {
		if !slices.Contains(u.MappedTeams, u.MappedTeam) {
			u.MappedTeams = append(u.MappedTeams, u.MappedTeam)
		}
		u.MappedTeam, changed = "", true
	}
	return changed
}

func ValidScope(v string) bool { return v == ScopeAll || v == ScopeTeams || v == ScopeServices }

// MigrateScope sets the scope mode of users from versions where a service list was the only scope.
func (u *User) MigrateScope() bool {
	if u.ScopeMode != "" {
		return false
	}
	u.ScopeMode = ScopeAll
	if len(u.ServiceIDs) > 0 {
		u.ScopeMode = ScopeServices
	}
	return true
}
