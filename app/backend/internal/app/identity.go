package app

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/oidcx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/passkey"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// Umbrella Identity: Umbrella Monitoring is the OpenID Connect provider and the user directory
// of the Umbrella family (Umbrella Flow, Inventory DB, Network Control). See
// docs/identity-federation.md.

const (
	identityKeyPath = "identity/signing"
	identityKeyName = "private_key"
	// ConnectionKeyPrefix starts the connection key an application is connected with.
	ConnectionKeyPrefix = "umbid1."

	authzRequestTTL = 10 * time.Minute
	authzCodeTTL    = 60 * time.Second
	accessTokenTTL  = 10 * time.Minute
	idTokenTTL      = 5 * time.Minute
	authzMax        = 10000
	maxApplications = 100
	syncTouchEvery  = time.Minute
)

var (
	ErrAppAccessDenied = errors.New("you have no access to this application")
	ErrReauthenticate  = errors.New("the application asks you to sign in again")
	errAuthzRequest    = errors.New("the sign-in request has expired: open the application again")
)

func init() {
	statuses = append(statuses,
		orgStatus{ErrAppAccessDenied, http.StatusForbidden, "app_access_denied"},
		orgStatus{ErrReauthenticate, http.StatusUnauthorized, "identity_reauth"},
		orgStatus{errAuthzRequest, http.StatusGone, "identity_request_expired"},
	)
}

type authzRequest struct {
	ID          string
	AppID       string
	RedirectURI string
	State       string
	Nonce       string
	Challenge   string
	Scope       string
	IDPHint     string
	LoginHint   string
	Prompt      string
	At          time.Time
	Expiry      time.Time
}

type authzCode struct {
	AppID       string
	RedirectURI string
	UserID      string
	Nonce       string
	Challenge   string
	Scope       string
	Method      string
	AuthTime    time.Time
	Expiry      time.Time
}

type accessToken struct {
	AppID  string
	UserID string
	Scope  string
	Expiry time.Time
}

// IdentityService is Umbrella Identity: connected applications, the OpenID Connect provider and
// the federation API.
type IdentityService struct {
	st       *store.Store
	sec      Secrets
	key      *tokenKey
	access   *AccessService
	sessions *auth.Sessions
	now      func() time.Time

	keyMu  sync.Mutex
	signer *rsa.PrivateKey
	kid    string

	mu       sync.Mutex
	requests map[string]authzRequest
	codes    map[string]authzCode
	tokens   map[string]accessToken
}

func NewIdentityService(st *store.Store, sec Secrets, key *tokenKey, access *AccessService, sessions *auth.Sessions) *IdentityService {
	return &IdentityService{st: st, sec: sec, key: key, access: access, sessions: sessions, now: func() time.Time { return time.Now().UTC() },
		requests: map[string]authzRequest{}, codes: map[string]authzCode{}, tokens: map[string]accessToken{}}
}

// Issuer is the public address of Umbrella; empty until it is set.
func (s *IdentityService) Issuer() string {
	var out string
	s.st.Read(func(d *store.Data) { out = strings.TrimRight(d.Settings.Alerting.PublicURL, "/") })
	return out
}

// signingKey reads (or, with create, makes) the RSA key ID tokens are signed with. The key lives
// in OpenBao.
func (s *IdentityService) signingKey(ctx context.Context, create bool) (*rsa.PrivateKey, string, error) {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	if s.signer != nil {
		return s.signer, s.kid, nil
	}
	var ref string
	s.st.Read(func(d *store.Data) { ref = d.Settings.IdentityKeyRef })
	var key *rsa.PrivateKey
	if ref != "" && s.sec != nil {
		text, err := s.sec.Resolve(ref)
		if err != nil {
			return nil, "", fmt.Errorf("signing key: %w", err)
		}
		signer, err := oidcx.ParseKey(text)
		if err != nil {
			return nil, "", fmt.Errorf("signing key: %w", err)
		}
		k, ok := signer.(*rsa.PrivateKey)
		if !ok {
			return nil, "", errors.New("signing key: not an RSA key")
		}
		key = k
	} else {
		if !create {
			return nil, "", ErrNotFound
		}
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, "", err
		}
		text := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
		made, err := putSecret(ctx, s.sec, identityKeyPath, identityKeyName, text)
		if err != nil {
			return nil, "", err
		}
		s.st.Write(func(d *store.Data) {
			d.Settings.IdentityKeyRef = made
			d.AddAudit(store.AuditEntry{Actor: "system", Action: "identity.signing_key", Detail: "created"})
		})
		key = k
	}
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	h := sha256.Sum256(der)
	s.signer, s.kid = key, hex.EncodeToString(h[:8])
	return s.signer, s.kid, nil
}

// ---- Connected applications

// ApplicationInput connects an application or changes who may use it.
type ApplicationInput struct {
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	URL          string   `json:"url"`
	RedirectURI  string   `json:"redirect_uri"`
	Access       string   `json:"access"`
	Roles        []string `json:"roles"`
	Groups       []string `json:"groups"`
	SyncPasskeys bool     `json:"sync_passkeys"`
	Disabled     bool     `json:"disabled"`
}

// ApplicationView is an application with the number of people allowed to use it.
type ApplicationView struct {
	model.Application
	People int `json:"people"`
}

// Connection is a connected application with its connection key, shown once.
type Connection struct {
	Application ApplicationView `json:"application"`
	Key         string          `json:"connection_key"`
}

var appKinds = map[string]string{model.AppNetworkControl: "Network Control", model.AppFlow: "Umbrella Flow", model.AppInventoryDB: "Inventory DB", model.AppOIDC: ""}

func (in ApplicationInput) normalize(d *store.Data) (model.Application, error) {
	var a model.Application
	if _, ok := appKinds[in.Kind]; !ok {
		return a, invalid("app_kind", errors.New("choose the application type"))
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = appKinds[in.Kind]
	}
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return a, invalid("app_name", errors.New("enter a name of up to 100 characters"))
	}
	u, ok := model.NormalizeURL(in.URL)
	if !ok {
		return a, invalid("app_url", errors.New("enter the address of the application: https://…"))
	}
	redirect := u + model.CallbackPath
	if in.Kind == model.AppOIDC && strings.TrimSpace(in.RedirectURI) != "" {
		r, ok := model.NormalizeURL(in.RedirectURI)
		if !ok {
			return a, invalid("app_redirect", errors.New("the redirect address must be https://…"))
		}
		redirect = r
	}
	access := in.Access
	switch access {
	case "", model.AccessAll:
		access = model.AccessAll
	case model.AccessRoles:
		if len(in.Roles) == 0 {
			return a, invalid("app_roles", errors.New("choose at least one role"))
		}
		for _, r := range in.Roles {
			if d.Roles[r] == nil && r != model.RoleAdmin && r != model.RoleUser {
				return a, invalid("app_roles", fmt.Errorf("unknown role %s", r))
			}
		}
	case model.AccessGroups:
		if len(in.Groups) == 0 {
			return a, invalid("app_groups", errors.New("enter at least one group"))
		}
	default:
		return a, invalid("app_access", errors.New("unknown access mode"))
	}
	var groups []string
	for _, g := range in.Groups {
		if g = strings.TrimSpace(g); g != "" && !slices.Contains(groups, g) {
			groups = append(groups, g)
		}
	}
	a = model.Application{Kind: in.Kind, Name: name, URL: u, RedirectURI: redirect, Access: access, SyncPasskeys: in.SyncPasskeys, Disabled: in.Disabled}
	if access == model.AccessRoles {
		a.Roles = slices.Clone(in.Roles)
	}
	if access == model.AccessGroups {
		a.Groups = groups
	}
	return a, nil
}

// allows reports whether the person may use the application.
func appAllows(app *model.Application, u *model.User) bool {
	if app == nil || u == nil || u.Source == model.SourceService || u.Source == model.SourceNetBox {
		return false
	}
	switch app.Access {
	case model.AccessRoles:
		return slices.Contains(app.Roles, u.Role)
	case model.AccessGroups:
		for _, g := range app.Groups {
			for _, x := range u.Groups {
				if strings.EqualFold(g, x) || directory.NormalizeDN(g) == directory.NormalizeDN(x) {
					return true
				}
			}
		}
		return false
	}
	return true
}

func (s *IdentityService) viewOf(d *store.Data, a *model.Application) ApplicationView {
	v := ApplicationView{Application: *a}
	if v.Roles == nil {
		v.Roles = []string{}
	}
	if v.Groups == nil {
		v.Groups = []string{}
	}
	for _, u := range d.Users {
		if !u.Disabled && appAllows(a, u) {
			v.People++
		}
	}
	return v
}

// Applications lists the connected applications by name.
func (s *IdentityService) Applications() []ApplicationView {
	out := []ApplicationView{}
	s.st.Read(func(d *store.Data) {
		for _, a := range d.Applications {
			out = append(out, s.viewOf(d, a))
		}
	})
	slices.SortFunc(out, func(a, b ApplicationView) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}

func randomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// credentials makes a new client secret and synchronization token and their hashes.
func (s *IdentityService) credentials(ctx context.Context) (secret, token, secretHash, tokenHash_ string, err error) {
	key, err := s.key.get(ctx, true)
	if err != nil {
		return "", "", "", "", err
	}
	if token, err = newToken(); err != nil {
		return "", "", "", "", err
	}
	secret = randomID(32)
	return secret, token, tokenHash(key, secret), tokenHash(key, token), nil
}

// ConnectionKey is the one string an application is connected with.
func ConnectionKey(hub, clientID, secret, token string) string {
	b, _ := json.Marshal(map[string]string{"hub": hub, "client_id": clientID, "client_secret": secret, "token": token})
	return ConnectionKeyPrefix + base64.RawURLEncoding.EncodeToString(b)
}

// Connect connects an application and returns its connection key.
func (s *IdentityService) Connect(ctx context.Context, actor string, in ApplicationInput) (Connection, error) {
	issuer := s.Issuer()
	if issuer == "" {
		return Connection{}, invalid("identity_public_url", errNoPublicURL)
	}
	if _, _, err := s.signingKey(ctx, true); err != nil {
		return Connection{}, err
	}
	var app model.Application
	var err error
	s.st.Read(func(d *store.Data) {
		app, err = in.normalize(d)
		if err == nil && len(d.Applications) >= maxApplications {
			err = invalid("app_limit", errors.New("too many applications"))
		}
	})
	if err != nil {
		return Connection{}, err
	}
	secret, token, sh, th, err := s.credentials(ctx)
	if err != nil {
		return Connection{}, err
	}
	now := s.now()
	app.ClientID, app.SecretHash, app.TokenHash, app.KeyIssuedAt, app.CreatedAt, app.CreatedBy = "umb-"+randomID(12), sh, th, now, now, actor
	var out Connection
	s.st.Write(func(d *store.Data) {
		app.ID = d.NextID("APP")
		d.Applications[app.ID] = &app
		d.AddAudit(store.AuditEntry{At: now, Actor: actor, Action: "identity.app.connect", Object: app.ID, Detail: app.Name + " " + app.URL + " access " + app.Access})
		out = Connection{Application: s.viewOf(d, &app), Key: ConnectionKey(issuer, app.ClientID, secret, token)}
	})
	return out, nil
}

// Update changes the access of an application.
func (s *IdentityService) Update(actor, id string, in ApplicationInput) (ApplicationView, error) {
	var out ApplicationView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		a := d.Applications[id]
		if a == nil {
			return
		}
		in.Kind = a.Kind
		var n model.Application
		if n, err = in.normalize(d); err != nil {
			return
		}
		a.Name, a.URL, a.RedirectURI, a.Access, a.Roles, a.Groups, a.SyncPasskeys, a.Disabled = n.Name, n.URL, n.RedirectURI, n.Access, n.Roles, n.Groups, n.SyncPasskeys, n.Disabled
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "identity.app.update", Object: a.ID, Detail: fmt.Sprintf("%s access %s, passkeys %v, disabled %v", a.Name, a.Access, a.SyncPasskeys, a.Disabled)})
		out = s.viewOf(d, a)
	})
	return out, err
}

// Reissue makes a new connection key; the old client secret and token stop working at once.
func (s *IdentityService) Reissue(ctx context.Context, actor, id string) (Connection, error) {
	issuer := s.Issuer()
	if issuer == "" {
		return Connection{}, invalid("identity_public_url", errNoPublicURL)
	}
	secret, token, sh, th, err := s.credentials(ctx)
	if err != nil {
		return Connection{}, err
	}
	var out Connection
	err = ErrNotFound
	s.st.Write(func(d *store.Data) {
		a := d.Applications[id]
		if a == nil {
			return
		}
		a.SecretHash, a.TokenHash, a.KeyIssuedAt = sh, th, s.now()
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "identity.app.key", Object: a.ID, Detail: a.Name + ": new connection key"})
		out, err = Connection{Application: s.viewOf(d, a), Key: ConnectionKey(issuer, a.ClientID, secret, token)}, nil
	})
	s.dropTokens(id)
	return out, err
}

// Disconnect removes an application.
func (s *IdentityService) Disconnect(actor, id string) error {
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		a := d.Applications[id]
		if a == nil {
			return
		}
		delete(d.Applications, id)
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "identity.app.disconnect", Object: id, Detail: a.Name})
		err = nil
	})
	s.dropTokens(id)
	return err
}

func (s *IdentityService) dropTokens(appID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, t := range s.tokens {
		if t.AppID == appID {
			delete(s.tokens, k)
		}
	}
	for k, c := range s.codes {
		if c.AppID == appID {
			delete(s.codes, k)
		}
	}
}

// ---- OpenID Connect provider

type discovery struct {
	Issuer                 string   `json:"issuer"`
	Authorization          string   `json:"authorization_endpoint"`
	Token                  string   `json:"token_endpoint"`
	UserInfo               string   `json:"userinfo_endpoint"`
	JWKS                   string   `json:"jwks_uri"`
	EndSession             string   `json:"end_session_endpoint"`
	ResponseTypes          []string `json:"response_types_supported"`
	SubjectTypes           []string `json:"subject_types_supported"`
	Algorithms             []string `json:"id_token_signing_alg_values_supported"`
	Scopes                 []string `json:"scopes_supported"`
	TokenAuth              []string `json:"token_endpoint_auth_methods_supported"`
	Claims                 []string `json:"claims_supported"`
	CodeChallengeMethods   []string `json:"code_challenge_methods_supported"`
	GrantTypes             []string `json:"grant_types_supported"`
	ResponseModes          []string `json:"response_modes_supported"`
	ClaimsParameterSupport bool     `json:"claims_parameter_supported"`
}

func (s *IdentityService) Discovery() (discovery, error) {
	iss := s.Issuer()
	if iss == "" {
		return discovery{}, ErrNotFound
	}
	return discovery{Issuer: iss, Authorization: iss + "/oauth2/authorize", Token: iss + "/oauth2/token", UserInfo: iss + "/oauth2/userinfo",
		JWKS: iss + "/oauth2/jwks", EndSession: iss + "/oauth2/logout", ResponseTypes: []string{"code"}, SubjectTypes: []string{"public"},
		Algorithms: []string{"RS256"}, Scopes: []string{"openid", "profile", "email", "groups"},
		TokenAuth:            []string{"client_secret_basic", "client_secret_post"},
		Claims:               []string{"sub", "iss", "aud", "exp", "iat", "auth_time", "nonce", "amr", "preferred_username", "name", "given_name", "family_name", "email", "email_verified", "groups", "umbrella_role", "umbrella_admin", "teams"},
		CodeChallengeMethods: []string{"S256"}, GrantTypes: []string{"authorization_code"}, ResponseModes: []string{"query"}}, nil
}

// JWKS is the public signing key.
func (s *IdentityService) JWKS(ctx context.Context) map[string]any {
	keys := []oidcx.JWK{}
	if k, kid, err := s.signingKey(ctx, false); err == nil {
		keys = append(keys, oidcx.RSAJWK(kid, &k.PublicKey))
	}
	return map[string]any{"keys": keys}
}

// AuthzError is an authorization request error. When Redirect is set it goes back to the
// application; otherwise it is shown to the person (an unknown client or redirect address).
type AuthzError struct {
	Code        string
	Description string
	Redirect    string
}

func (e *AuthzError) Error() string { return e.Code + ": " + e.Description }

func errorRedirect(redirect, state, code, desc, iss string) string {
	q := url.Values{"error": {code}, "error_description": {desc}}
	if state != "" {
		q.Set("state", state)
	}
	if iss != "" {
		q.Set("iss", iss)
	}
	return redirect + sep(redirect) + q.Encode()
}

func sep(u string) string {
	if strings.Contains(u, "?") {
		return "&"
	}
	return "?"
}

var idpHints = []string{"", "local", "passkey", "ldap", "entra", "google", "apple", "oidc"}

// Authorize checks an authorization request and keeps it; the person continues on the sign-in
// page of Umbrella with its ID.
func (s *IdentityService) Authorize(q url.Values) (string, error) {
	iss := s.Issuer()
	clientID, redirect := q.Get("client_id"), q.Get("redirect_uri")
	var app model.Application
	s.st.Read(func(d *store.Data) {
		for _, a := range d.Applications {
			if a.ClientID == clientID {
				app = *a
			}
		}
	})
	if iss == "" || app.ID == "" || app.Disabled {
		return "", &AuthzError{Code: "unauthorized_client", Description: "the application is not connected to Umbrella"}
	}
	if redirect != app.RedirectURI {
		return "", &AuthzError{Code: "invalid_request", Description: "the redirect address is not the one registered for the application"}
	}
	state := q.Get("state")
	fail := func(code, desc string) (string, error) {
		return "", &AuthzError{Code: code, Description: desc, Redirect: errorRedirect(redirect, state, code, desc, iss)}
	}
	if q.Get("response_type") != "code" {
		return fail("unsupported_response_type", "only the authorization code flow is supported")
	}
	if !slices.Contains(strings.Fields(q.Get("scope")), "openid") {
		return fail("invalid_scope", "the openid scope is required")
	}
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		return fail("invalid_request", "PKCE with S256 is required")
	}
	hint := q.Get("idp_hint")
	if !slices.Contains(idpHints, hint) {
		hint = ""
	}
	now := s.now()
	req := authzRequest{ID: randomID(18), AppID: app.ID, RedirectURI: redirect, State: state, Nonce: q.Get("nonce"),
		Challenge: q.Get("code_challenge"), Scope: q.Get("scope"), IDPHint: hint, LoginHint: q.Get("login_hint"),
		Prompt: q.Get("prompt"), At: now, Expiry: now.Add(authzRequestTTL)}
	if len(req.State) > 512 || len(req.Nonce) > 512 || len(req.Challenge) > 128 || len(req.LoginHint) > 256 {
		return fail("invalid_request", "a parameter is too long")
	}
	if req.Prompt == "none" {
		return fail("login_required", "Umbrella needs the person to sign in")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep(now)
	s.requests[req.ID] = req
	return req.ID, nil
}

func (s *IdentityService) sweep(now time.Time) {
	for k, r := range s.requests {
		if now.After(r.Expiry) {
			delete(s.requests, k)
		}
	}
	for k, c := range s.codes {
		if now.After(c.Expiry) {
			delete(s.codes, k)
		}
	}
	for k, t := range s.tokens {
		if now.After(t.Expiry) {
			delete(s.tokens, k)
		}
	}
	for _, m := range []int{len(s.requests), len(s.codes), len(s.tokens)} {
		if m > authzMax {
			s.requests, s.codes, s.tokens = map[string]authzRequest{}, map[string]authzCode{}, map[string]accessToken{}
			return
		}
	}
}

// RequestView is what the sign-in page shows about a pending request.
type RequestView struct {
	ID          string    `json:"id"`
	Application string    `json:"application"`
	Kind        string    `json:"kind"`
	URL         string    `json:"url"`
	IDPHint     string    `json:"idp_hint"`
	LoginHint   string    `json:"login_hint"`
	Prompt      string    `json:"prompt"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func (s *IdentityService) Request(id string) (RequestView, error) {
	s.mu.Lock()
	r, ok := s.requests[id]
	s.mu.Unlock()
	if !ok || s.now().After(r.Expiry) {
		return RequestView{}, errAuthzRequest
	}
	var app model.Application
	s.st.Read(func(d *store.Data) {
		if a := d.Applications[r.AppID]; a != nil {
			app = *a
		}
	})
	if app.ID == "" {
		return RequestView{}, errAuthzRequest
	}
	return RequestView{ID: id, Application: app.Name, Kind: app.Kind, URL: app.URL, IDPHint: r.IDPHint, LoginHint: r.LoginHint, Prompt: r.Prompt, ExpiresAt: r.Expiry}, nil
}

// Approve answers a pending request for the signed-in person: a code back to the application.
func (s *IdentityService) Approve(id string, u model.User, ss *auth.Session) (string, error) {
	now := s.now()
	s.mu.Lock()
	r, ok := s.requests[id]
	s.mu.Unlock()
	if !ok || now.After(r.Expiry) {
		return "", errAuthzRequest
	}
	if r.Prompt == "login" && ss.Created.Before(r.At) {
		return "", ErrReauthenticate
	}
	var app *model.Application
	allowed := false
	s.st.Write(func(d *store.Data) {
		a := d.Applications[r.AppID]
		x := d.Users[u.ID]
		if a == nil || a.Disabled || x == nil || x.Disabled {
			return
		}
		c := *a
		app = &c
		if allowed = appAllows(a, x); allowed {
			t := now
			a.LastSignInAt = &t
			a.SignIns++
			d.AddAudit(store.AuditEntry{At: now, Actor: u.Username, Action: "identity.sign_in", Object: a.ID, Detail: a.Name})
		} else {
			d.AddAudit(store.AuditEntry{At: now, Actor: u.Username, Action: "identity.sign_in_denied", Object: a.ID, Detail: a.Name})
		}
	})
	if app == nil {
		return "", errAuthzRequest
	}
	if !allowed {
		return "", ErrAppAccessDenied
	}
	code := randomID(32)
	s.mu.Lock()
	delete(s.requests, id)
	s.codes[code] = authzCode{AppID: r.AppID, RedirectURI: r.RedirectURI, UserID: u.ID, Nonce: r.Nonce, Challenge: r.Challenge,
		Scope: r.Scope, Method: ss.Method, AuthTime: ss.Created, Expiry: now.Add(authzCodeTTL)}
	s.mu.Unlock()
	q := url.Values{"code": {code}, "iss": {s.Issuer()}}
	if r.State != "" {
		q.Set("state", r.State)
	}
	return r.RedirectURI + sep(r.RedirectURI) + q.Encode(), nil
}

// TokenError is an error of the token endpoint (RFC 6749 5.2).
type TokenError struct {
	Status      int
	Code        string
	Description string
}

func (e *TokenError) Error() string { return e.Code }

// clientOf authenticates the application by its client ID and secret.
func (s *IdentityService) clientOf(ctx context.Context, clientID, secret string) (model.Application, error) {
	key, err := s.key.get(ctx, false)
	if err != nil {
		return model.Application{}, &TokenError{http.StatusUnauthorized, "invalid_client", "unknown client"}
	}
	h := tokenHash(key, secret)
	var app model.Application
	s.st.Read(func(d *store.Data) {
		for _, a := range d.Applications {
			if a.ClientID == clientID && hmac.Equal([]byte(a.SecretHash), []byte(h)) && !a.Disabled {
				app = *a
			}
		}
	})
	if app.ID == "" {
		return app, &TokenError{http.StatusUnauthorized, "invalid_client", "client authentication failed"}
	}
	return app, nil
}

// Tokens is the token endpoint's answer.
type Tokens struct {
	IDToken     string `json:"id_token"`
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
}

// Exchange redeems a code.
func (s *IdentityService) Exchange(ctx context.Context, clientID, secret, code, redirect, verifier string) (Tokens, error) {
	app, err := s.clientOf(ctx, clientID, secret)
	if err != nil {
		return Tokens{}, err
	}
	now := s.now()
	s.mu.Lock()
	c, ok := s.codes[code]
	delete(s.codes, code)
	s.mu.Unlock()
	if !ok || now.After(c.Expiry) || c.AppID != app.ID {
		return Tokens{}, &TokenError{http.StatusBadRequest, "invalid_grant", "the code is invalid, expired or already used"}
	}
	if c.RedirectURI != redirect {
		return Tokens{}, &TokenError{http.StatusBadRequest, "invalid_grant", "the redirect address differs"}
	}
	if subtle.ConstantTimeCompare([]byte(oidcx.Challenge(verifier)), []byte(c.Challenge)) != 1 {
		return Tokens{}, &TokenError{http.StatusBadRequest, "invalid_grant", "PKCE verification failed"}
	}
	key, kid, err := s.signingKey(ctx, true)
	if err != nil {
		return Tokens{}, err
	}
	var claims map[string]any
	s.st.Read(func(d *store.Data) {
		if u := d.Users[c.UserID]; u != nil && !u.Disabled && appAllows(d.Applications[app.ID], u) {
			claims = userClaims(d, u, c.Scope)
		}
	})
	if claims == nil {
		return Tokens{}, &TokenError{http.StatusBadRequest, "invalid_grant", "the account is no longer allowed"}
	}
	claims["iss"], claims["sub"], claims["aud"] = s.Issuer(), c.UserID, app.ClientID
	claims["iat"], claims["exp"], claims["auth_time"] = now.Unix(), now.Add(idTokenTTL).Unix(), c.AuthTime.Unix()
	claims["amr"] = amrOf(c.Method)
	if c.Nonce != "" {
		claims["nonce"] = c.Nonce
	}
	idt, err := oidcx.Sign(key, kid, claims)
	if err != nil {
		return Tokens{}, err
	}
	access := randomID(32)
	s.mu.Lock()
	s.tokens[access] = accessToken{AppID: app.ID, UserID: c.UserID, Scope: c.Scope, Expiry: now.Add(accessTokenTTL)}
	s.mu.Unlock()
	return Tokens{IDToken: idt, AccessToken: access, TokenType: "Bearer", ExpiresIn: int(accessTokenTTL / time.Second), Scope: c.Scope}, nil
}

func amrOf(method string) []string {
	switch method {
	case MethodPasskey:
		return []string{"hwk", "user", "mfa"}
	case "", MethodPassword, model.SourceLDAP:
		return []string{"pwd"}
	}
	return []string{"fed"}
}

// userClaims are the claims of a person for the scopes.
func userClaims(d *store.Data, u *model.User, scope string) map[string]any {
	scopes := strings.Fields(scope)
	out := map[string]any{}
	if slices.Contains(scopes, "profile") {
		out["preferred_username"] = u.Username
		out["name"] = cmpOr(u.Name, u.Username)
		if u.FirstName != "" {
			out["given_name"] = u.FirstName
		}
		if u.LastName != "" {
			out["family_name"] = u.LastName
		}
		if r := d.RoleOf(u); r != nil {
			out["umbrella_role"] = map[string]string{"id": r.ID, "name": r.Name}
		}
		out["umbrella_admin"] = u.Role == model.RoleAdmin
		teams := []string{}
		for _, t := range u.TeamIDs {
			if x := d.Teams[t]; x != nil {
				teams = append(teams, x.Name)
			}
		}
		out["teams"] = teams
	}
	if slices.Contains(scopes, "email") && u.Email != "" {
		out["email"], out["email_verified"] = u.Email, true
	}
	if slices.Contains(scopes, "groups") {
		out["groups"] = append([]string{}, u.Groups...)
	}
	return out
}

// UserInfo answers the claims of an access token.
func (s *IdentityService) UserInfo(token string) (map[string]any, error) {
	s.mu.Lock()
	t, ok := s.tokens[token]
	s.mu.Unlock()
	if !ok || s.now().After(t.Expiry) {
		return nil, ErrUnauthenticated
	}
	var out map[string]any
	s.st.Read(func(d *store.Data) {
		if u := d.Users[t.UserID]; u != nil && !u.Disabled {
			out = userClaims(d, u, t.Scope)
			out["sub"] = u.ID
		}
	})
	if out == nil {
		return nil, ErrUnauthenticated
	}
	return out, nil
}

// LogoutTarget checks where to send the browser after signing out: the address of a connected
// application only.
func (s *IdentityService) LogoutTarget(raw string) string {
	if raw == "" {
		return "/"
	}
	ok := false
	s.st.Read(func(d *store.Data) {
		for _, a := range d.Applications {
			if raw == a.URL || strings.HasPrefix(raw, a.URL+"/") || strings.HasPrefix(raw, a.URL+"?") {
				ok = true
			}
		}
	})
	if !ok {
		return "/"
	}
	return raw
}

// ---- Federation API

// application authenticates a federation call by the application's token.
func (s *IdentityService) application(ctx context.Context, token, ip string) (model.Application, error) {
	if !strings.HasPrefix(token, model.TokenPrefix) || len(token) > 200 {
		return model.Application{}, ErrUnauthenticated
	}
	key, err := s.key.get(ctx, false)
	if err != nil {
		return model.Application{}, ErrUnauthenticated
	}
	h := tokenHash(key, token)
	var app model.Application
	s.st.Read(func(d *store.Data) {
		for _, a := range d.Applications {
			if hmac.Equal([]byte(a.TokenHash), []byte(h)) {
				app = *a
			}
		}
	})
	if app.ID == "" || app.Disabled {
		return model.Application{}, ErrUnauthenticated
	}
	now := s.now()
	if app.LastSyncAt == nil || now.Sub(*app.LastSyncAt) >= syncTouchEvery || app.LastSyncIP != ip {
		s.st.Update(func(d *store.Data) bool {
			a := d.Applications[app.ID]
			if a == nil {
				return false
			}
			if a.LastSyncIP != ip {
				d.AddAudit(store.AuditEntry{At: now, Actor: "app:" + a.Name, Action: "identity.sync.new_address", Object: a.ID, Detail: ip})
			}
			a.LastSyncAt, a.LastSyncIP = &now, ip
			return true
		})
	}
	return app, nil
}

// FederationConfig is the answer of GET /api/identity/v1/config.
type FederationConfig struct {
	Version     int                   `json:"version"`
	Issuer      string                `json:"issuer"`
	Application FederationApp         `json:"application"`
	Policy      model.PasswordPolicy  `json:"password_policy"`
	Passkeys    model.PasskeySettings `json:"passkeys"`
	Methods     []SignInMethod        `json:"methods"`
}

type FederationApp struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	SyncPasskeys bool   `json:"sync_passkeys"`
}

// FederationUser is a person as members read them.
type FederationUser struct {
	ID        string              `json:"id"`
	Username  string              `json:"username"`
	Email     string              `json:"email"`
	Name      string              `json:"name"`
	FirstName string              `json:"first_name"`
	LastName  string              `json:"last_name"`
	Source    string              `json:"source"`
	Disabled  bool                `json:"disabled"`
	Role      map[string]string   `json:"role"`
	Admin     bool                `json:"admin"`
	Groups    []string            `json:"groups"`
	Teams     []string            `json:"teams"`
	Passkeys  []FederationPasskey `json:"passkeys"`
}

type FederationPasskey struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	CredentialID   string    `json:"credential_id"`
	PublicKey      string    `json:"public_key"`
	UserHandle     string    `json:"user_handle"`
	SignCount      uint32    `json:"sign_count"`
	AAGUID         string    `json:"aaguid"`
	Transports     []string  `json:"transports"`
	BackupEligible bool      `json:"backup_eligible"`
	CreatedAt      time.Time `json:"created_at"`
}

func (s *IdentityService) Config(app model.Application, methods []SignInMethod, rpID string) FederationConfig {
	out := FederationConfig{Version: 1, Issuer: s.Issuer(), Application: FederationApp{ID: app.ID, Name: app.Name, SyncPasskeys: app.SyncPasskeys}, Methods: methods}
	s.st.Read(func(d *store.Data) {
		out.Policy = d.Settings.Password
		out.Passkeys = model.PasskeySettings{Enabled: d.Settings.Passkeys.Enabled, RPID: rpID}
	})
	return out
}

func (s *IdentityService) Users(app model.Application, rpID string) []FederationUser {
	out := []FederationUser{}
	s.st.Read(func(d *store.Data) {
		a := d.Applications[app.ID]
		keys := a != nil && a.SyncPasskeys && d.Settings.Passkeys.Enabled && rpID != ""
		for _, u := range d.Users {
			if u.Source == model.SourceService || u.Source == model.SourceNetBox {
				continue
			}
			allowed := appAllows(a, u)
			fu := FederationUser{ID: u.ID, Username: u.Username, Email: u.Email, Name: cmpOr(u.Name, u.Username), FirstName: u.FirstName,
				LastName: u.LastName, Source: u.Source, Disabled: u.Disabled || !allowed, Admin: u.Role == model.RoleAdmin,
				Groups: append([]string{}, u.Groups...), Teams: []string{}, Passkeys: []FederationPasskey{}}
			if r := d.RoleOf(u); r != nil {
				fu.Role = map[string]string{"id": r.ID, "name": r.Name}
			}
			for _, t := range u.TeamIDs {
				if x := d.Teams[t]; x != nil {
					fu.Teams = append(fu.Teams, x.Name)
				}
			}
			if keys && !fu.Disabled {
				for _, k := range u.Passkeys {
					fu.Passkeys = append(fu.Passkeys, FederationPasskey{ID: k.ID, Name: k.Name, CredentialID: passkey.Encode(k.CredentialID),
						PublicKey: passkey.Encode(k.PublicKey), UserHandle: passkey.Encode(k.UserHandle), SignCount: k.SignCount,
						AAGUID: passkey.AAGUIDString(k.AAGUID), Transports: append([]string{}, k.Transports...), BackupEligible: k.BackupEligible, CreatedAt: k.CreatedAt})
				}
			}
			out = append(out, fu)
		}
	})
	slices.SortFunc(out, func(a, b FederationUser) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// ---- HTTP

func (a *App) registerIdentity(mux *http.ServeMux) {
	a.route(mux, "GET /api/identity/applications", "settings.identity:view", show(a.identity.Applications))
	a.route(mux, "POST /api/identity/applications", "settings.identity:edit", submit(http.StatusCreated, func(r *http.Request, in ApplicationInput) (Connection, error) {
		return a.identity.Connect(r.Context(), actorName(r), in)
	}))
	a.route(mux, "PUT /api/identity/applications/{id}", "settings.identity:edit", submit(http.StatusOK, func(r *http.Request, in ApplicationInput) (ApplicationView, error) {
		return a.identity.Update(actorName(r), r.PathValue("id"), in)
	}))
	a.route(mux, "POST /api/identity/applications/{id}/key", "settings.identity:edit", fetch(func(r *http.Request) (Connection, error) {
		return a.identity.Reissue(r.Context(), actorName(r), r.PathValue("id"))
	}))
	a.route(mux, "DELETE /api/identity/applications/{id}", "settings.identity:edit", remove(func(r *http.Request) error {
		return a.identity.Disconnect(actorName(r), r.PathValue("id"))
	}))

	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.identity.Discovery()
		w.Header().Set("Access-Control-Allow-Origin", "*")
		reply(w, http.StatusOK, v, err)
	})
	mux.HandleFunc("GET /oauth2/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "public, max-age=300")
		httpx.JSON(w, http.StatusOK, a.identity.JWKS(r.Context()))
	})
	mux.HandleFunc("GET /oauth2/authorize", a.oauthAuthorize)
	mux.HandleFunc("POST /oauth2/token", a.oauthToken)
	mux.HandleFunc("GET /oauth2/userinfo", a.oauthUserInfo)
	mux.HandleFunc("POST /oauth2/userinfo", a.oauthUserInfo)
	mux.HandleFunc("GET /oauth2/logout", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
			if ss := a.deps.Sessions.Get(c.Value); ss != nil {
				a.deps.Sessions.Delete(c.Value)
			}
		}
		a.setCookie(w, "", -1)
		http.Redirect(w, r, a.identity.LogoutTarget(r.URL.Query().Get("post_logout_redirect_uri")), http.StatusFound)
	})
	mux.HandleFunc("GET /api/oauth2/requests/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.identity.Request(r.PathValue("id"))
		reply(w, http.StatusOK, v, err)
	})
	mux.HandleFunc("POST /api/oauth2/requests/{id}/approve", a.authed(func(w http.ResponseWriter, r *http.Request) {
		s := current(r)
		target, err := a.identity.Approve(r.PathValue("id"), s.user, s.ss)
		reply(w, http.StatusOK, map[string]string{"redirect": target}, err)
	}))
	mux.HandleFunc("GET /api/identity/v1/config", a.federation(func(w http.ResponseWriter, r *http.Request, app model.Application) {
		methods := []SignInMethod{}
		if a.passkeys.Enabled() {
			methods = append(methods, SignInMethod{ID: "passkey", Title: "Passkey"})
		}
		s := a.settings.Get()
		if s.LDAP.Enabled {
			methods = append(methods, SignInMethod{ID: "ldap", Title: "Active Directory"})
		}
		if s.Entra.Enabled {
			methods = append(methods, SignInMethod{ID: "entra", Title: "Microsoft Entra ID"})
		}
		methods = append(methods, a.providers.Methods()...)
		methods = append(methods, SignInMethod{ID: "local", Title: "Umbrella"})
		writeETag(w, r, a.identity.Config(app, methods, a.passkeys.RPID()))
	}))
	mux.HandleFunc("GET /api/identity/v1/users", a.federation(func(w http.ResponseWriter, r *http.Request, app model.Application) {
		writeETag(w, r, map[string]any{"users": a.identity.Users(app, a.passkeys.RPID())})
	}))
}

// writeETag answers v with an ETag of its content, or 304 when the client has it.
func writeETag(w http.ResponseWriter, r *http.Request, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		writeError(w, err)
		return
	}
	h := sha256.Sum256(b)
	tag := `"` + hex.EncodeToString(h[:16]) + `"`
	w.Header().Set("ETag", tag)
	w.Header().Set("Cache-Control", "no-store")
	if r.Header.Get("If-None-Match") == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func (a *App) federation(next func(http.ResponseWriter, *http.Request, model.Application)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			writeError(w, ErrUnauthenticated)
			return
		}
		ip := a.clientIP(r)
		if a.passkeyRate.Locked("fed:" + ip) {
			writeError(w, ErrTooManyAttempts)
			return
		}
		app, err := a.identity.application(r.Context(), token, ip)
		if err != nil {
			a.passkeyRate.Fail("fed:" + ip)
			writeError(w, err)
			return
		}
		next(w, r, app)
	}
}

func (a *App) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	id, err := a.identity.Authorize(r.URL.Query())
	var ae *AuthzError
	switch {
	case errors.As(err, &ae) && ae.Redirect != "":
		http.Redirect(w, r, ae.Redirect, http.StatusFound)
		return
	case errors.As(err, &ae):
		http.Redirect(w, r, "/sso?error="+url.QueryEscape(ae.Code), http.StatusFound)
		return
	case err != nil:
		writeError(w, err)
		return
	}
	http.Redirect(w, r, "/sso?request="+url.QueryEscape(id), http.StatusFound)
}

func tokenFail(w http.ResponseWriter, err error) {
	var te *TokenError
	if !errors.As(err, &te) {
		te = &TokenError{http.StatusInternalServerError, "server_error", "try again"}
	}
	w.Header().Set("Cache-Control", "no-store")
	if te.Code == "invalid_client" {
		w.Header().Set("WWW-Authenticate", `Basic realm="umbrella"`)
	}
	httpx.JSON(w, te.Status, map[string]string{"error": te.Code, "error_description": te.Description})
}

func (a *App) oauthToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		tokenFail(w, &TokenError{http.StatusBadRequest, "invalid_request", "the form cannot be read"})
		return
	}
	if r.PostForm.Get("grant_type") != "authorization_code" {
		tokenFail(w, &TokenError{http.StatusBadRequest, "unsupported_grant_type", "only authorization_code is supported"})
		return
	}
	id, secret, ok := r.BasicAuth()
	if ok {
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	} else {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	ip := a.clientIP(r)
	if a.passkeyRate.Locked("token:" + ip) {
		tokenFail(w, &TokenError{http.StatusTooManyRequests, "slow_down", "too many attempts"})
		return
	}
	t, err := a.identity.Exchange(r.Context(), id, secret, r.PostForm.Get("code"), r.PostForm.Get("redirect_uri"), r.PostForm.Get("code_verifier"))
	if err != nil {
		a.passkeyRate.Fail("token:" + ip)
		tokenFail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, t)
}

func (a *App) oauthUserInfo(w http.ResponseWriter, r *http.Request) {
	token, ok := bearerToken(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		writeError(w, ErrUnauthenticated)
		return
	}
	v, err := a.identity.UserInfo(token)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	}
	reply(w, http.StatusOK, v, err)
}
