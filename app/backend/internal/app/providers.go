package app

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/oidcx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	ssoCookie        = "umbrella_sso"
	providerSecret   = "client_secret"
	providerKey      = "private_key"
	providerPending  = 10 * time.Minute
	providerMaxStart = 10000
)

var (
	errProviderState      = errors.New("the sign-in request has expired or was started in another browser")
	errProviderNotAllowed = errors.New("the account is not allowed to sign in to Umbrella")
	errNoPublicURL        = errors.New("set the public address of Umbrella first (Settings → Alerting)")
)

func providerSecretPath(kind, key string) string { return "identity/providers/" + kind + "/" + key }

type providerStart struct {
	kind   string
	req    oidcx.Request
	expiry time.Time
}

// ProvidersService keeps the external sign-in providers (Google, Apple, OpenID Connect) and
// runs their sign-in.
type ProvidersService struct {
	st       *store.Store
	secrets  Secrets
	users    *UserService
	sessions SessionRevoker

	mu      sync.Mutex
	pending map[string]providerStart
}

func NewProvidersService(st *store.Store, sec Secrets, users *UserService, sessions SessionRevoker) *ProvidersService {
	return &ProvidersService{st: st, secrets: sec, users: users, sessions: sessions, pending: map[string]providerStart{}}
}

// ProviderView is a provider as the settings page shows it.
type ProviderView struct {
	Kind            string         `json:"kind"`
	Config          model.Provider `json:"config"`
	ClientSecretSet bool           `json:"client_secret_set"`
	PrivateKeySet   bool           `json:"private_key_set"`
	RedirectURI     string         `json:"redirect_uri"`
	Users           int            `json:"users"`
}

// ProvidersView is every provider.
type ProvidersView struct {
	PublicURL string         `json:"public_url"`
	Providers []ProviderView `json:"providers"`
}

var providerKinds = []string{model.ProviderGoogle, model.ProviderApple, model.ProviderOIDC}

func (s *ProvidersService) View() ProvidersView {
	var out ProvidersView
	s.st.Read(func(d *store.Data) {
		out.PublicURL = d.Settings.Alerting.PublicURL
		for _, k := range providerKinds {
			p := *d.Settings.Providers.Get(k)
			v := ProviderView{Kind: k, Config: p, ClientSecretSet: p.ClientSecretRef != "", PrivateKeySet: p.PrivateKeyRef != ""}
			if out.PublicURL != "" {
				v.RedirectURI = redirectFor(out.PublicURL, k)
			}
			if v.Config.AllowedDomains == nil {
				v.Config.AllowedDomains = []string{}
			}
			for _, u := range d.Users {
				if u.Source == k {
					v.Users++
				}
			}
			out.Providers = append(out.Providers, v)
		}
	})
	return out
}

func redirectFor(public, kind string) string {
	return strings.TrimRight(public, "/") + "/api/auth/sso/" + kind + "/callback"
}

// Methods are the enabled providers: kind and button title.
func (s *ProvidersService) Methods() []SignInMethod {
	var out []SignInMethod
	s.st.Read(func(d *store.Data) {
		for _, k := range providerKinds {
			if p := d.Settings.Providers.Get(k); p.Enabled {
				out = append(out, SignInMethod{ID: k, Title: providerTitle(k, *p)})
			}
		}
	})
	return out
}

// SignInMethod is a sign-in button.
type SignInMethod struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func providerTitle(kind string, p model.Provider) string {
	switch kind {
	case model.ProviderGoogle:
		return "Google"
	case model.ProviderApple:
		return "Apple"
	}
	return cmpOr(p.Title, "OpenID Connect")
}

// ProviderInput saves a provider; empty secrets keep the stored ones.
type ProviderInput struct {
	Config       model.Provider `json:"config"`
	ClientSecret string         `json:"client_secret"`
	PrivateKey   string         `json:"private_key"`
}

func normalizeProvider(kind string, p model.Provider) (model.Provider, error) {
	p.Title, p.ClientID = strings.TrimSpace(p.Title), strings.TrimSpace(p.ClientID)
	p.TeamID, p.KeyID = strings.TrimSpace(p.TeamID), strings.TrimSpace(p.KeyID)
	p.Scopes, p.GroupsClaim, p.AdminGroup = strings.TrimSpace(p.Scopes), strings.TrimSpace(p.GroupsClaim), strings.TrimSpace(p.AdminGroup)
	var domains []string
	for _, d := range p.AllowedDomains {
		d = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(d), "@"))
		if d == "" {
			continue
		}
		if !strings.Contains(d, ".") || strings.ContainsAny(d, " /:@") {
			return p, invalid("provider_domain", fmt.Errorf("%q is not a domain name", d))
		}
		if !slices.Contains(domains, d) {
			domains = append(domains, d)
		}
	}
	p.AllowedDomains = domains
	switch kind {
	case model.ProviderGoogle:
		p.Issuer, p.Title = oidcx.GoogleIssuer, ""
	case model.ProviderApple:
		p.Issuer, p.Title = oidcx.AppleIssuer, ""
		if p.Enabled && (p.TeamID == "" || p.KeyID == "") {
			return p, invalid("provider_apple", errors.New("enter the Team ID and the Key ID"))
		}
	default:
		u, err := url.Parse(strings.TrimRight(strings.TrimSpace(p.Issuer), "/"))
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return p, invalid("provider_issuer", errors.New("the issuer must be an https:// address"))
		}
		p.Issuer = u.String()
		if p.Title == "" {
			p.Title = "OpenID Connect"
		}
	}
	if p.Enabled && p.ClientID == "" {
		return p, invalid("provider_client", errors.New("enter the client ID"))
	}
	// Corporate default: a public provider never opens Umbrella to every account on it.
	if p.Enabled && kind != model.ProviderOIDC && len(p.AllowedDomains) == 0 && !p.ExistingOnly {
		return p, invalid("provider_domains", errors.New("enter the allowed e-mail domains or allow only existing accounts"))
	}
	return p, nil
}

// Save checks the provider (discovery) and stores it; secrets go to OpenBao.
func (s *ProvidersService) Save(ctx context.Context, actor, kind string, in ProviderInput) (ProvidersView, error) {
	if !slices.Contains(providerKinds, kind) {
		return ProvidersView{}, ErrNotFound
	}
	cfg, err := normalizeProvider(kind, in.Config)
	if err != nil {
		return ProvidersView{}, err
	}
	var stored model.Provider
	var public string
	s.st.Read(func(d *store.Data) { stored, public = *d.Settings.Providers.Get(kind), d.Settings.Alerting.PublicURL })
	cfg.ClientSecretRef, cfg.PrivateKeyRef = stored.ClientSecretRef, stored.PrivateKeyRef
	if cfg.Enabled {
		if public == "" {
			return ProvidersView{}, invalid("identity_public_url", errNoPublicURL)
		}
		if _, err := oidcx.Discover(ctx, cfg.Issuer); err != nil {
			return ProvidersView{}, invalid("provider_unavailable", err)
		}
		if kind == model.ProviderApple && in.PrivateKey != "" {
			if _, err := oidcx.AppleSecret(cfg.TeamID, cfg.KeyID, cfg.ClientID, in.PrivateKey, time.Now()); err != nil {
				return ProvidersView{}, invalid("provider_key", err)
			}
		}
		if kind == model.ProviderApple && in.PrivateKey == "" && cfg.PrivateKeyRef == "" {
			return ProvidersView{}, invalid("provider_key", errors.New("upload the private key (.p8)"))
		}
		if kind != model.ProviderApple && in.ClientSecret == "" && cfg.ClientSecretRef == "" {
			return ProvidersView{}, invalid("provider_secret", errors.New("enter the client secret"))
		}
	} else if stored.Enabled {
		var admins int
		s.st.Read(func(d *store.Data) { admins = localAdmins(d) })
		if admins == 0 {
			return ProvidersView{}, ErrNoLocalAdmin
		}
	}
	if in.ClientSecret != "" {
		if cfg.ClientSecretRef, err = putSecret(ctx, s.secrets, providerSecretPath(kind, providerSecret), providerSecret, in.ClientSecret); err != nil {
			return ProvidersView{}, err
		}
	}
	if in.PrivateKey != "" {
		if cfg.PrivateKeyRef, err = putSecret(ctx, s.secrets, providerSecretPath(kind, providerKey), providerKey, in.PrivateKey); err != nil {
			return ProvidersView{}, err
		}
	}
	var signedOut []string
	s.st.Write(func(d *store.Data) {
		*d.Settings.Providers.Get(kind) = cfg
		detail := "disabled"
		if cfg.Enabled {
			detail = "enabled, client " + cfg.ClientID
			if len(cfg.AllowedDomains) > 0 {
				detail += ", domains " + strings.Join(cfg.AllowedDomains, " ")
			}
			if cfg.ExistingOnly {
				detail += ", existing accounts only"
			}
		}
		if in.ClientSecret != "" || in.PrivateKey != "" {
			detail += ", secret replaced"
		}
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.provider." + kind, Detail: detail})
		if !cfg.Enabled && stored.Enabled {
			for id, u := range d.Users {
				if u.Source == kind {
					signedOut = append(signedOut, id)
				}
			}
		}
	})
	for _, id := range signedOut {
		s.sessions.DeleteUser(id, "")
	}
	return s.View(), nil
}

func (s *ProvidersService) client(kind string, p model.Provider, public string) (oidcx.Client, error) {
	c := oidcx.Client{Issuer: p.Issuer, ClientID: p.ClientID, RedirectURI: redirectFor(public, kind),
		Scopes: []string{"openid", "email", "profile"}, Extra: url.Values{}}
	switch kind {
	case model.ProviderGoogle:
		if len(p.AllowedDomains) == 1 {
			c.Extra.Set("hd", p.AllowedDomains[0])
		}
		c.Extra.Set("prompt", "select_account")
	case model.ProviderApple:
		c.Scopes = []string{"openid", "email", "name"}
		c.Extra.Set("response_mode", "form_post")
	default:
		c.Scopes = append(c.Scopes, strings.Fields(p.Scopes)...)
	}
	return c, nil
}

func (s *ProvidersService) secret(kind string, p model.Provider) (string, error) {
	if kind == model.ProviderApple {
		key, err := s.secrets.Resolve(p.PrivateKeyRef)
		if err != nil {
			return "", err
		}
		return oidcx.AppleSecret(p.TeamID, p.KeyID, p.ClientID, key, time.Now())
	}
	return s.secrets.Resolve(p.ClientSecretRef)
}

// Start begins a sign-in and returns the provider's page and the state.
func (s *ProvidersService) Start(ctx context.Context, kind string) (string, string, error) {
	var p model.Provider
	var public string
	s.st.Read(func(d *store.Data) {
		if x := d.Settings.Providers.Get(kind); x != nil {
			p = *x
		}
		public = d.Settings.Alerting.PublicURL
	})
	if !p.Enabled || public == "" {
		return "", "", ErrNotFound
	}
	m, err := oidcx.Discover(ctx, p.Issuer)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrDirectoryUnavailable, err)
	}
	c, _ := s.client(kind, p, public)
	req := oidcx.NewRequest()
	now := time.Now()
	s.mu.Lock()
	for k, x := range s.pending {
		if now.After(x.expiry) {
			delete(s.pending, k)
		}
	}
	if len(s.pending) >= providerMaxStart {
		for k := range s.pending {
			delete(s.pending, k)
			break
		}
	}
	s.pending[req.State] = providerStart{kind: kind, req: req, expiry: now.Add(providerPending)}
	s.mu.Unlock()
	return oidcx.AuthURL(c, m, req), req.State, nil
}

// appleUser is the "user" form field Apple posts on the first sign-in only.
type appleUser struct {
	Name struct {
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
	} `json:"name"`
}

// Complete redeems the code and returns the Umbrella account.
func (s *ProvidersService) Complete(ctx context.Context, kind, state, code, userJSON string) (model.User, error) {
	s.mu.Lock()
	x, ok := s.pending[state]
	delete(s.pending, state)
	s.mu.Unlock()
	if !ok || x.kind != kind || time.Now().After(x.expiry) {
		return model.User{}, errProviderState
	}
	var p model.Provider
	var public string
	s.st.Read(func(d *store.Data) { p, public = *d.Settings.Providers.Get(kind), d.Settings.Alerting.PublicURL })
	if !p.Enabled {
		return model.User{}, ErrInvalidCredentials
	}
	m, err := oidcx.Discover(ctx, p.Issuer)
	if err != nil {
		return model.User{}, fmt.Errorf("%w: %v", ErrDirectoryUnavailable, err)
	}
	c, _ := s.client(kind, p, public)
	if c.Secret, err = s.secret(kind, p); err != nil {
		return model.User{}, fmt.Errorf("%w: client secret: %v", ErrDirectoryUnavailable, err)
	}
	t, err := oidcx.Exchange(ctx, c, m, code, x.req)
	if err != nil {
		return model.User{}, fmt.Errorf("%w: %v", ErrDirectoryUnavailable, err)
	}
	cl, err := oidcx.Verify(ctx, m, p.ClientID, t.IDToken, x.req.Nonce, time.Now())
	if err != nil {
		return model.User{}, fmt.Errorf("%w: %v", ErrInvalidCredentials, err)
	}
	if kind == model.ProviderOIDC && m.UserInfo != "" && t.AccessToken != "" && p.GroupsClaim != "" && cl[p.GroupsClaim] == nil {
		if extra, err := userInfo(ctx, m.UserInfo, t.AccessToken); err == nil {
			for k, v := range extra {
				if cl[k] == nil {
					cl[k] = v
				}
			}
		}
	}
	id, err := identityFromClaims(kind, p, cl, userJSON)
	if err != nil {
		return model.User{}, err
	}
	return s.signIn(kind, p, cl.String("sub"), id)
}

func userInfo(ctx context.Context, endpoint, access string) (oidcx.Claims, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	res, err := (&http.Client{Timeout: oidcx.Timeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo: HTTP %d", res.StatusCode)
	}
	var out oidcx.Claims
	return out, json.NewDecoder(res.Body).Decode(&out)
}

// identityFromClaims reads the person from the ID token and checks the e-mail domain.
func identityFromClaims(kind string, p model.Provider, cl oidcx.Claims, userJSON string) (directory.Identity, error) {
	email := strings.ToLower(strings.TrimSpace(cl.String("email")))
	verified := cl.Bool("email_verified") || cl.String("email_verified") == "true"
	if kind == model.ProviderOIDC && cl["email_verified"] == nil {
		// Corporate providers (ADFS, some Keycloak realms) do not send the claim: their
		// addresses come from the directory.
		verified = email != ""
	}
	if cl.String("sub") == "" || email == "" || !verified {
		return directory.Identity{}, fmt.Errorf("%w: the provider gave no verified e-mail address", errProviderNotAllowed)
	}
	if !p.EmailAllowed(email) {
		return directory.Identity{}, fmt.Errorf("%w: the domain of %s is not allowed", errProviderNotAllowed, email)
	}
	if kind == model.ProviderGoogle && len(p.AllowedDomains) > 0 && !slices.Contains(p.AllowedDomains, strings.ToLower(cl.String("hd"))) {
		return directory.Identity{}, fmt.Errorf("%w: %s is not a Google Workspace account of an allowed domain", errProviderNotAllowed, email)
	}
	id := directory.Identity{Username: email, Email: email, FirstName: cl.String("given_name"), LastName: cl.String("family_name"), Name: cl.String("name")}
	if kind == model.ProviderOIDC {
		if u := strings.ToLower(strings.TrimSpace(cl.String("preferred_username"))); u != "" && !strings.ContainsAny(u, " /\\") {
			id.Username = u
		}
		if p.GroupsClaim != "" {
			id.Groups, id.GroupsKnown = cl.Strings(p.GroupsClaim), true
			id.Admin = p.AdminGroup != "" && slices.ContainsFunc(id.Groups, func(g string) bool { return strings.EqualFold(g, p.AdminGroup) })
		}
	}
	if kind == model.ProviderApple && userJSON != "" {
		var au appleUser
		if json.Unmarshal([]byte(userJSON), &au) == nil {
			id.FirstName, id.LastName = au.Name.FirstName, au.Name.LastName
		}
	}
	if id.Name == "" {
		id.Name = strings.TrimSpace(id.FirstName + " " + id.LastName)
	}
	if id.Name == "" {
		id.Name = id.Username
	}
	return id, nil
}

// signIn finds or creates the account: an account already bound to the provider subject, an
// account with the same verified e-mail of an allowed domain (linked and written to the audit
// log) or, unless only existing accounts may sign in, a new account.
func (s *ProvidersService) signIn(kind string, p model.Provider, sub string, id directory.Identity) (model.User, error) {
	var bound, byEmail *model.User
	s.st.Read(func(d *store.Data) {
		for _, u := range d.Users {
			if u.Source == kind && u.ExternalID == sub {
				c := *u
				bound = &c
			}
		}
		if bound == nil {
			if u := userByEmail(d, id.Email); u != nil && u.Source != model.SourceService && u.Source != model.SourceNetBox {
				c := *u
				byEmail = &c
			}
		}
	})
	switch {
	case bound != nil:
		if bound.Disabled {
			return model.User{}, ErrInvalidCredentials
		}
		id.Username = bound.Username
		return s.users.syncExternal(kind, sub, id)
	case byEmail != nil:
		if byEmail.Disabled {
			return model.User{}, ErrInvalidCredentials
		}
		s.st.Write(func(d *store.Data) {
			d.AddAudit(store.AuditEntry{Actor: byEmail.Username, Action: "auth.provider_link", Object: byEmail.ID, Detail: kind + " account " + id.Email})
		})
		return *byEmail, nil
	case p.ExistingOnly:
		return model.User{}, fmt.Errorf("%w: %s has no Umbrella account", errProviderNotAllowed, id.Email)
	}
	var taken bool
	s.st.Read(func(d *store.Data) { taken = d.UserByName(id.Username) != nil })
	if taken {
		return model.User{}, fmt.Errorf("%w: the user name %s belongs to another account", errProviderNotAllowed, id.Username)
	}
	return s.users.syncExternal(kind, sub, id)
}

func (a *App) registerProviders(mux *http.ServeMux) {
	a.route(mux, "GET /api/settings/providers", "settings.identity:view", show(a.providers.View))
	a.route(mux, "PUT /api/settings/providers/{kind}", "settings.identity:edit", submit(http.StatusOK, func(r *http.Request, in ProviderInput) (ProvidersView, error) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		return a.providers.Save(ctx, actorName(r), r.PathValue("kind"), in)
	}))
	mux.HandleFunc("GET /api/auth/sso/{kind}/start", a.providerStart)
	mux.HandleFunc("GET /api/auth/sso/{kind}/callback", a.providerCallback)
	mux.HandleFunc("POST /api/auth/sso/{kind}/callback", a.providerCallback)
}

func (a *App) providerStart(w http.ResponseWriter, r *http.Request) {
	ip := a.clientIP(r)
	if a.entraRate.Locked(ip) {
		a.signInFailed(w, r, "too_many_attempts")
		return
	}
	a.entraRate.Fail(ip)
	kind := r.PathValue("kind")
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	target, state, err := a.providers.Start(ctx, kind)
	switch {
	case errors.Is(err, ErrNotFound):
		a.signInFailed(w, r, "provider_off")
		return
	case err != nil:
		slog.Error("sso: sign-in cannot start", "provider", kind, "err", err)
		a.signInFailed(w, r, "provider_unavailable")
		return
	}
	a.setSSOCookie(w, kind, state, int(providerPending/time.Second))
	http.Redirect(w, r, target, http.StatusFound)
}

func (a *App) providerCallback(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if err := r.ParseForm(); err != nil {
		a.signInFailed(w, r, "provider_state")
		return
	}
	if e := r.Form.Get("error"); e != "" {
		slog.Warn("sso: sign-in refused by the provider", "provider", kind, "error", e, "description", r.Form.Get("error_description"))
		a.signInFailed(w, r, "provider_denied")
		return
	}
	state := r.Form.Get("state")
	c, err := r.Cookie(ssoCookie)
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(state)) != 1 {
		a.signInFailed(w, r, "provider_state")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	u, err := a.providers.Complete(ctx, kind, state, r.Form.Get("code"), r.Form.Get("user"))
	a.setSSOCookie(w, kind, "", -1)
	switch {
	case errors.Is(err, errProviderState):
		a.signInFailed(w, r, "provider_state")
		return
	case errors.Is(err, errProviderNotAllowed):
		slog.Warn("sso: account refused", "provider", kind, "err", err)
		a.auth.Failed(kind, a.clientIP(r))
		a.signInFailed(w, r, "provider_not_allowed")
		return
	case errors.Is(err, ErrInvalidCredentials):
		a.auth.Failed(kind, a.clientIP(r))
		a.signInFailed(w, r, "provider_account")
		return
	case err != nil:
		slog.Error("sso: sign-in failed", "provider", kind, "err", err)
		a.signInFailed(w, r, "provider_unavailable")
		return
	}
	ss := a.deps.Sessions.CreateWith(u.ID, kind)
	a.auth.Signed(u.ID, a.clientIP(r))
	a.setCookie(w, ss.ID, int(auth.MaxLifetime/time.Second))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// setSSOCookie binds the sign-in to the browser. Apple answers with a cross-site POST, which
// carries only SameSite=None cookies.
func (a *App) setSSOCookie(w http.ResponseWriter, kind, value string, maxAge int) {
	same := http.SameSiteLaxMode
	secure := a.opt.SecureCookies
	if kind == model.ProviderApple {
		same, secure = http.SameSiteNoneMode, true
	}
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: value, Path: "/api/auth/sso/", HttpOnly: true, Secure: secure, SameSite: same, MaxAge: maxAge})
}
