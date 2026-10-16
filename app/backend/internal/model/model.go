package model

import (
	"errors"
	"net/mail"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/sbrw-evc/beacon/app/backend/internal/directory"
	"github.com/sbrw-evc/beacon/app/backend/internal/ledger"
	"github.com/sbrw-evc/beacon/app/backend/internal/netbox"
)

const (
	// SourceSPA marks the people synchronized from Keystone: the
	// only accounts that sign in. SourceLocal, SourceLDAP and SourceEntra are the accounts of
	// versions before Keystone; they cannot sign in and are bound to Keystone once (app/keystone.go).
	SourceSPA   = "spa"
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
	// snapshots decode; the store moves TeamID into TeamIDs on load.
	TeamID     string `json:"-"`
	MappedTeam string `json:"-"`
	// ScopeMode is what the user sees and may act on: every service (ScopeAll), the services
	// their teams own or support (ScopeTeams), or the chosen ServiceIDs (ScopeServices).
	// Administrators are never limited.
	ScopeMode  string   `json:"scope_mode"`
	ServiceIDs []string `json:"service_ids,omitempty"`
	// Telegram is the chat ID backup notification sends to.
	Telegram string `json:"telegram"`
	// Locale is the interface language the user chose (en, ru); empty is the default of the
	// installation. Voice calls speak it.
	Locale string `json:"locale,omitempty"`
	// ExternalID is the Keystone user ID of a Keystone account (the only key it is bound by).
	ExternalID string `json:"-"`
	// Groups are the Keystone groups of the person (names), as Keystone last gave them.
	Groups      []string   `json:"groups,omitempty"`
	Disabled    bool       `json:"disabled"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

type Settings struct {
	DefaultTheme  string `json:"default_theme"`
	DefaultLocale string `json:"default_locale"`
	DefaultTZ     string `json:"default_timezone"`
	// LDAP is the Active Directory the NetBox synchronization reads computer objects from (it
	// is not used for sign-in: people sign in through Keystone).
	LDAP   directory.Config `json:"ldap"`
	NetBox netbox.Config    `json:"netbox"`
	// InventoryDB is the optional Ledger connection: devices in, alert states out.
	InventoryDB ledger.Config `json:"inventory_db"`
	Alerting    Alerting      `json:"alerting"`
	// Response is incident response: impact, priority policies, escalation, war rooms, Jira.
	Response Response `json:"response"`
	// HostContext is how the incident card shows the machine of the incident.
	HostContext HostContext `json:"host_context"`
	// NewUserRole is the role of the accounts NetBox creates for its contacts. Empty or unknown:
	// the system role "user". People synchronized from Keystone get the role Keystone gives them.
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
	// Keystone is the connection to Keystone as it was last seen (app/keystone.go).
	SPA SPAState `json:"-"`
	// IdentityCleaned: the secrets of the sign-in before Keystone (password hashes, the LDAP
	// sign-in, Entra ID, providers, Umbrella Identity) were deleted from OpenBao.
	IdentityCleaned bool      `json:"-"`
	SetupAt         time.Time `json:"setup_at"`
	SetupBy         string    `json:"setup_by"`
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

// SPAState is what Keystone said last and how the last synchronization went.
type SPAState struct {
	Issuer      string      `json:"issuer"`
	ClientID    string      `json:"client_id"`
	AppID       string      `json:"app_id"`
	AppName     string      `json:"app_name"`
	AppType     string      `json:"app_type"`
	AccountURL  string      `json:"account_url"`
	SignOutURL  string      `json:"sign_out_url"`
	Methods     []SPAMethod `json:"methods"`
	LastSync    *time.Time  `json:"last_sync,omitempty"`
	OK          bool        `json:"ok"`
	Error       string      `json:"error,omitempty"`
	Users       int         `json:"users"`
	Created     int         `json:"created"`
	Updated     int         `json:"updated"`
	Bound       int         `json:"bound"`
	Disabled    int         `json:"disabled"`
	Conflicts   []string    `json:"conflicts"`
	NoRole      []string    `json:"no_role"`
	ConfigETag  string      `json:"-"`
	UsersETag   string      `json:"-"`
	CatalogHash string      `json:"-"`
	CatalogAt   *time.Time  `json:"catalog_at,omitempty"`
	CatalogRole int         `json:"catalog_roles"`
	CatalogErr  string      `json:"catalog_error,omitempty"`
}

// SPAMethod is a sign-in method Keystone offers.
type SPAMethod struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}
