package model

import (
	"net/url"
	"slices"
	"strings"
	"time"
)

// Sign-in sources of accounts created by external providers.
const (
	SourceGoogle = "google"
	SourceApple  = "apple"
	SourceOIDC   = "oidc"
)

// Passkey is a WebAuthn (FIDO2) credential of a user: Windows Hello (face, fingerprint, iris,
// PIN), Touch ID / Face ID, Android biometrics or a security key. Only the public key is kept.
type Passkey struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	CredentialID []byte `json:"-"`
	PublicKey    []byte `json:"-"`
	// UserHandle is the user ID the authenticator stored with the credential.
	UserHandle     []byte     `json:"-"`
	SignCount      uint32     `json:"-"`
	AAGUID         []byte     `json:"-"`
	Transports     []string   `json:"transports"`
	BackupEligible bool       `json:"backup_eligible"`
	BackupState    bool       `json:"backup_state"`
	CreatedAt      time.Time  `json:"created_at"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
}

// MaxPasskeys is how many passkeys one person may register.
const MaxPasskeys = 20

// PasskeySettings: passkeys on or off, and the passkey domain (WebAuthn relying party ID). A
// domain that is a parent of every application's host name (example.com for
// umbrella.example.com and flow.example.com) lets one passkey sign in to all of them.
type PasskeySettings struct {
	Enabled bool   `json:"enabled"`
	RPID    string `json:"rp_id"`
}

// Normalize checks the passkey domain: a host name without a scheme, port or path.
func (p PasskeySettings) Normalize() (PasskeySettings, bool) {
	p.RPID = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(p.RPID), "."))
	if p.RPID == "" {
		return p, true
	}
	if strings.ContainsAny(p.RPID, "/:@ ") || !strings.Contains(p.RPID, ".") && p.RPID != "localhost" {
		return p, false
	}
	return p, true
}

// HostWithin reports whether host (no port) is the passkey domain or a subdomain of it.
func HostWithin(host, rpID string) bool {
	host, rpID = strings.ToLower(host), strings.ToLower(rpID)
	return rpID != "" && (host == rpID || strings.HasSuffix(host, "."+rpID))
}

// External provider kinds.
const (
	ProviderGoogle = "google"
	ProviderApple  = "apple"
	ProviderOIDC   = "oidc"
)

// Provider is an external OpenID Connect sign-in: Google (Workspace), Sign in with Apple or
// any OpenID Connect provider (Keycloak, ADFS, Okta, Authentik…).
type Provider struct {
	Enabled bool `json:"enabled"`
	// Title is the button text of a generic provider ("Keycloak").
	Title    string `json:"title"`
	Issuer   string `json:"issuer"`
	ClientID string `json:"client_id"`
	// ClientSecretRef is the OpenBao reference of the client secret (Google, OpenID Connect).
	ClientSecretRef string `json:"-"`
	// TeamID, KeyID and PrivateKeyRef sign the client secret of Sign in with Apple.
	TeamID        string `json:"team_id,omitempty"`
	KeyID         string `json:"key_id,omitempty"`
	PrivateKeyRef string `json:"-"`
	// AllowedDomains are the e-mail domains whose people may sign in (Google hd).
	AllowedDomains []string `json:"allowed_domains"`
	// ExistingOnly: only people who already have an account (by user name or e-mail) sign in.
	ExistingOnly bool `json:"existing_only"`
	// Scopes are extra scopes of a generic provider; GroupsClaim is the claim with groups and
	// AdminGroup the group whose members become administrators.
	Scopes      string `json:"scopes,omitempty"`
	GroupsClaim string `json:"groups_claim,omitempty"`
	AdminGroup  string `json:"admin_group,omitempty"`
}

// Providers are the external sign-in providers.
type Providers struct {
	Google Provider `json:"google"`
	Apple  Provider `json:"apple"`
	OIDC   Provider `json:"oidc"`
}

// Get returns the provider of a kind.
func (p *Providers) Get(kind string) *Provider {
	switch kind {
	case ProviderGoogle:
		return &p.Google
	case ProviderApple:
		return &p.Apple
	case ProviderOIDC:
		return &p.OIDC
	}
	return nil
}

// EmailAllowed reports whether the e-mail's domain is one of the allowed domains (or no
// domains are set: every domain).
func (p Provider) EmailAllowed(email string) bool {
	if len(p.AllowedDomains) == 0 {
		return true
	}
	at := strings.LastIndexByte(email, '@')
	if at < 0 {
		return false
	}
	return slices.Contains(p.AllowedDomains, strings.ToLower(email[at+1:]))
}

// Application kinds.
const (
	AppNetworkControl = "network-control"
	AppFlow           = "umbrella-flow"
	AppInventoryDB    = "inventory-db"
	AppOIDC           = "oidc"
)

// Application access modes.
const (
	AccessAll    = "all"
	AccessRoles  = "roles"
	AccessGroups = "groups"
)

// Application is an application connected to Umbrella Identity: it signs people in through
// Umbrella (OpenID Connect) and reads users, the password policy and passkeys.
type Application struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	URL         string   `json:"url"`
	RedirectURI string   `json:"redirect_uri"`
	Access      string   `json:"access"`
	Roles       []string `json:"roles"`
	Groups      []string `json:"groups"`
	// SyncPasskeys: the application receives the passkeys of its people.
	SyncPasskeys bool `json:"sync_passkeys"`
	// ClientID is public; the client secret and the synchronization token are kept as keyed
	// hashes only (the key is the API token key in OpenBao) and shown once, in the connection key.
	ClientID     string     `json:"client_id"`
	SecretHash   string     `json:"-"`
	TokenHash    string     `json:"-"`
	KeyIssuedAt  time.Time  `json:"key_issued_at"`
	LastSyncAt   *time.Time `json:"last_sync_at,omitempty"`
	LastSyncIP   string     `json:"last_sync_ip,omitempty"`
	LastSignInAt *time.Time `json:"last_sign_in_at,omitempty"`
	SignIns      int        `json:"sign_ins"`
	Disabled     bool       `json:"disabled"`
	CreatedAt    time.Time  `json:"created_at"`
	CreatedBy    string     `json:"created_by"`
}

// CallbackPath is where every Umbrella application receives the single sign-in answer.
const CallbackPath = "/api/auth/umbrella/callback"

// NormalizeURL checks an application address: https (http only for localhost), no query.
func NormalizeURL(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", false
	}
	host := u.Hostname()
	if u.Scheme != "https" && !(u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")) {
		return "", false
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/"), true
}
