// Package entra signs users in with Microsoft Entra ID (Azure AD) over OpenID Connect:
// authorization code flow with PKCE, a confidential client secret, ID token verification
// against the tenant keys, and profile and group data from Microsoft Graph.
package entra

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	SecretPath   = "entra"
	SecretKey    = "client_secret"
	CallbackPath = "/api/auth/entra/callback"

	CloudGlobal = "global"
	CloudUSGov  = "usgov"
	CloudChina  = "china"
)

var Timeout = 10 * time.Second

// Cloud is a national cloud: where users sign in and where Microsoft Graph answers.
type Cloud struct {
	Login string
	Graph string
}

// Clouds maps a cloud name to its endpoints. Tests add their own entry.
var Clouds = map[string]Cloud{
	CloudGlobal: {Login: "https://login.microsoftonline.com", Graph: "https://graph.microsoft.com"},
	CloudUSGov:  {Login: "https://login.microsoftonline.us", Graph: "https://graph.microsoft.us"},
	CloudChina:  {Login: "https://login.chinacloudapi.cn", Graph: "https://microsoftgraph.chinacloudapi.cn"},
}

var (
	ErrNotAllowed  = errors.New("the account is not a member of the group allowed to sign in")
	ErrInvalidUser = errors.New("the sign-in response has no usable account")

	guidRe   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	tenantRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`)
)

type Config struct {
	Enabled         bool   `json:"enabled"`
	Cloud           string `json:"cloud"`
	TenantID        string `json:"tenant_id"`
	ClientID        string `json:"client_id"`
	ClientSecretRef string `json:"client_secret_ref,omitempty"`
	RedirectURL     string `json:"redirect_url"`
	AdminGroupID    string `json:"admin_group_id,omitempty"`
	UserGroupID     string `json:"user_group_id,omitempty"`
	// ReadGroups asks the sign-in to read the group list even when no group is configured here,
	// for the group mapping table. It is set on a copy at sign-in and never saved.
	ReadGroups bool `json:"-"`
}

func (c Config) Normalize() (Config, error) {
	c.Cloud = strings.ToLower(strings.TrimSpace(c.Cloud))
	if c.Cloud == "" {
		c.Cloud = CloudGlobal
	}
	if _, ok := Clouds[c.Cloud]; !ok {
		return c, errors.New("unknown Microsoft cloud")
	}
	c.TenantID = strings.ToLower(strings.TrimSpace(c.TenantID))
	c.ClientID = strings.ToLower(strings.TrimSpace(c.ClientID))
	c.RedirectURL = strings.TrimSpace(c.RedirectURL)
	c.AdminGroupID = strings.ToLower(strings.TrimSpace(c.AdminGroupID))
	c.UserGroupID = strings.ToLower(strings.TrimSpace(c.UserGroupID))
	switch c.TenantID {
	case "":
		return c, errors.New("tenant ID is required")
	case "common", "organizations", "consumers":
		return c, errors.New("use your own tenant ID or domain: multi-tenant sign-in would let any Microsoft account in")
	}
	if !tenantRe.MatchString(c.TenantID) {
		return c, errors.New("tenant must be a directory (tenant) ID or a domain such as contoso.onmicrosoft.com")
	}
	if !guidRe.MatchString(c.ClientID) {
		return c, errors.New("application (client) ID must be a GUID")
	}
	if c.AdminGroupID != "" && !guidRe.MatchString(c.AdminGroupID) {
		return c, errors.New("administrators group must be a group object ID (GUID)")
	}
	if c.UserGroupID != "" && !guidRe.MatchString(c.UserGroupID) {
		return c, errors.New("allowed users group must be a group object ID (GUID)")
	}
	u, err := url.Parse(c.RedirectURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != CallbackPath || u.RawQuery != "" || u.Fragment != "" {
		return c, fmt.Errorf("redirect URI must look like https://umbrella.example%s", CallbackPath)
	}
	return c, nil
}

func (c Config) Public() Config {
	c.ClientSecretRef = ""
	return c
}

func (c Config) cloud() Cloud { return Clouds[c.Cloud] }

func (c Config) discoveryURL() string {
	return c.cloud().Login + "/" + url.PathEscape(c.TenantID) + "/v2.0/.well-known/openid-configuration"
}

type TestRequest struct {
	Config       Config `json:"config"`
	ClientSecret string `json:"client_secret"`
}

// Probe is what a settings check found out about the tenant and the application.
type Probe struct {
	Issuer      string `json:"issuer,omitempty"`
	TenantID    string `json:"tenant_id,omitempty"`
	Credentials bool   `json:"credentials"`
}

type TestReport struct {
	OK    bool   `json:"ok"`
	Probe Probe  `json:"probe"`
	Error string `json:"error,omitempty"`
}

func Report(p Probe, err error) TestReport {
	r := TestReport{OK: err == nil, Probe: p}
	if err != nil {
		r.Error = err.Error()
	}
	return r
}
