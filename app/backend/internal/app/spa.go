package app

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/credentials"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/oidcx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/spa"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// SPA, the Single Point of Authorization (SPA's docs/protocol.md): people sign in there (OpenID
// Connect with PKCE), Umbrella reads the people who may use it and their roles every five
// minutes and publishes its roles (the system roles admin and user and the custom roles) as the
// catalogue SPA administrators assign.
//
// The connection key comes from SPA_CONNECTION_KEY, the file named by SPA_CONNECTION_KEY_FILE
// or OpenBao (spa#connection_key, written by the setup wizard). What SPA said last and how the
// last synchronization went is Settings.SPA.

const (
	// spaCookie keeps the PKCE verifier and the nonce of a sign-in, bound to the signed state.
	spaCookie     = "umbrella_spa"
	spaCookiePath = "/api/auth/spa/"
	spaFlowTTL    = 10 * time.Minute
	// spaActor is the actor of the synchronization in the audit log.
	spaActor = "spa"
	// MethodSPA is the sign-in method of sessions started through SPA.
	MethodSPA = "spa"
)

var errSPANotConnected = errors.New("Umbrella is not connected to SPA: set " + spa.Env + " (or " + spa.EnvFile +
	") to the connection key SPA shows in Applications → Connect an application")

var errSyncRunning = errors.New("a synchronization with SPA is running")

func init() {
	statuses = append(statuses,
		orgStatus{errSPANotConnected, http.StatusConflict, "spa_not_connected"},
		orgStatus{errSyncRunning, http.StatusConflict, "sync_running"},
	)
}

// spaState is the in-process part: one synchronization at a time, the key states are signed
// with and the states already used.
type spaState struct {
	mu      sync.Mutex // Settings.SPA read-modify-write, synchronization, publishing
	running sync.Mutex // held by a synchronization
	keyOnce sync.Once
	key     []byte
	used    sync.Map // state id → expiry (single use)
}

// stateKey signs the states of sign-ins. It lives in memory: a restart only ends the sign-ins
// that were on their way through SPA at that moment.
func (s *spaState) stateKey() []byte {
	s.keyOnce.Do(func() {
		s.key = make([]byte, 32)
		if _, err := rand.Read(s.key); err != nil {
			panic(err)
		}
	})
	return s.key
}

// spaKey returns the connection key and where it came from (env, file, openbao);
// errSPANotConnected when there is none.
func (a *App) spaKey() (spa.Key, string, error) {
	parse := func(raw, source string) (spa.Key, string, error) {
		k, err := spa.ParseKey(raw)
		if err != nil {
			return spa.Key{}, source, fmt.Errorf("%s: %w", source, err)
		}
		return k, source, nil
	}
	if strings.TrimSpace(a.opt.SPAKey) != "" {
		return parse(a.opt.SPAKey, "env")
	}
	if a.opt.SPAKeyFile != "" {
		b, err := os.ReadFile(a.opt.SPAKeyFile)
		if err != nil {
			return spa.Key{}, "file", fmt.Errorf("%s: %w", spa.EnvFile, err)
		}
		return parse(string(b), "file")
	}
	if a.deps.Vault == nil || !a.deps.Vault.Enabled() {
		return spa.Key{}, "", errSPANotConnected
	}
	raw, err := a.deps.Vault.Resolve(a.deps.Vault.Ref(spa.SecretPath, spa.SecretKey))
	switch {
	case errors.Is(err, secrets.ErrNotFound) || (err == nil && strings.TrimSpace(raw) == ""):
		return spa.Key{}, "", errSPANotConnected
	case err != nil:
		return spa.Key{}, "openbao", fmt.Errorf("the connection key in OpenBao: %w", err)
	}
	return parse(raw, "openbao")
}

func (a *App) spaStored() model.SPAState {
	var out model.SPAState
	a.deps.Store.Read(func(d *store.Data) { out = d.Settings.SPA })
	return out
}

// spaBaseURL is the address of Umbrella for the redirect addresses: SPA_PUBLIC_URL, else the
// public address of the alerting settings, else the address of the request (X-Forwarded-Proto
// and -Host from a trusted proxy).
func (a *App) spaBaseURL(r *http.Request) string {
	if u := strings.TrimRight(strings.TrimSpace(a.opt.SPAPublicURL), "/"); u != "" {
		return u
	}
	var public string
	a.deps.Store.Read(func(d *store.Data) { public = d.Settings.Alerting.PublicURL })
	if u := strings.TrimRight(strings.TrimSpace(public), "/"); u != "" {
		return u
	}
	trusted := false
	if ip, ok := remoteIP(r); ok {
		trusted = a.trustedProxies().contains(ip)
	}
	scheme := "http"
	if r.TLS != nil || a.opt.SecureCookies {
		scheme = "https"
	} else if p := r.Header.Get("X-Forwarded-Proto"); (p == "https" || p == "http") && trusted {
		scheme = p
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" && trusted {
		host = h
	}
	return scheme + "://" + host
}

func (a *App) registerSPA(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/spa", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, a.spaInfoOf(a.spaStored()))
	})
	mux.HandleFunc("GET "+spa.StartPath, a.spaStart)
	mux.HandleFunc("GET "+spa.CallbackPath, a.spaCallback)
	a.route(mux, "GET /api/settings/spa", "settings.spa:view", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, a.spaView(r))
	})
	a.route(mux, "POST /api/settings/spa/sync", "settings.spa:sync", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		if err := a.syncSPA(ctx, actorName(r), false); errors.Is(err, errSPANotConnected) || errors.Is(err, errSyncRunning) {
			writeError(w, err)
			return
		}
		// Other errors are in the status the page shows.
		httpx.JSON(w, http.StatusOK, a.spaView(r))
	})
}

// spaInfo tells the sign-in page whether Umbrella is connected to SPA.
type spaInfo struct {
	Connected   bool              `json:"connected"`
	Error       string            `json:"error,omitempty"`
	Issuer      string            `json:"issuer,omitempty"`
	Application string            `json:"application,omitempty"`
	Methods     []model.SPAMethod `json:"methods"`
}

func (a *App) spaInfoOf(st model.SPAState) spaInfo {
	out := spaInfo{Methods: []model.SPAMethod{}}
	k, _, err := a.spaKey()
	if err != nil {
		if !errors.Is(err, errSPANotConnected) {
			out.Error = "invalid_key"
		}
		return out
	}
	out.Connected, out.Issuer = true, k.Issuer
	if st.Issuer == k.Issuer {
		out.Application = st.AppName
		if st.Methods != nil {
			out.Methods = st.Methods
		}
	}
	return out
}

// spaSystemStatus is the SPA connection on the system status page.
func (a *App) spaSystemStatus(st model.SPAState) SPAStatus {
	k, _, err := a.spaKey()
	if err != nil {
		out := SPAStatus{}
		if !errors.Is(err, errSPANotConnected) {
			out.Error = err.Error()
		}
		return out
	}
	out := SPAStatus{Connected: true, Issuer: k.Issuer}
	if st.Issuer == k.Issuer && st.ClientID == k.ClientID {
		out.Application, out.LastSync, out.OK, out.Users, out.Error = st.AppName, st.LastSync, st.OK, st.Users, st.Error
	}
	return out
}

// ---- sign-in

// spaFlow is what the cookie keeps between start and callback.
type spaFlow struct {
	State    string `json:"s"`
	Verifier string `json:"v"`
	Nonce    string `json:"n"`
}

// statePayload is signed into the state: its id (single use), the page to return to and the
// expiry.
type statePayload struct {
	ID     string `json:"i"`
	Return string `json:"r"`
	Exp    int64  `json:"e"`
}

func (a *App) stateMAC(body string) []byte {
	m := hmac.New(sha256.New, a.spa.stateKey())
	m.Write([]byte("spa-state:" + body))
	return m.Sum(nil)
}

func (a *App) signState(p statePayload) string {
	b, _ := json.Marshal(p)
	body := base64.RawURLEncoding.EncodeToString(b)
	return body + "." + base64.RawURLEncoding.EncodeToString(a.stateMAC(body))
}

func (a *App) openState(state string, now time.Time) (statePayload, bool) {
	body, sig, ok := strings.Cut(state, ".")
	if !ok {
		return statePayload{}, false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, a.stateMAC(body)) {
		return statePayload{}, false
	}
	var p statePayload
	b, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || json.Unmarshal(b, &p) != nil || p.ID == "" || now.Unix() > p.Exp {
		return statePayload{}, false
	}
	if _, seen := a.spa.used.LoadOrStore(p.ID, p.Exp); seen {
		return statePayload{}, false
	}
	a.spa.used.Range(func(k, v any) bool {
		if v.(int64) < now.Unix() {
			a.spa.used.Delete(k)
		}
		return true
	})
	return p, true
}

// safeReturn keeps a return path inside Umbrella.
func safeReturn(p string) string {
	if p == "" || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") || strings.HasPrefix(p, "/api/") {
		return "/"
	}
	if u, err := url.Parse(p); err != nil || u.Host != "" || u.Scheme != "" {
		return "/"
	}
	return p
}

func (a *App) setSPACookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	secure := a.opt.SecureCookies || r.TLS != nil || strings.HasPrefix(a.spaBaseURL(r), "https://")
	http.SetCookie(w, &http.Cookie{Name: spaCookie, Value: value, Path: spaCookiePath, HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}

// spaFailed returns to the sign-in page with the error.
func (a *App) spaFailed(w http.ResponseWriter, r *http.Request, code, description string) {
	a.setSPACookie(w, r, "", -1)
	q := url.Values{"sso_error": {code}}
	if description != "" {
		if len(description) > 300 {
			description = description[:300]
		}
		q.Set("sso_error_description", description)
	}
	http.Redirect(w, r, "/login?"+q.Encode(), http.StatusSeeOther)
}

var idpHints = map[string]bool{"password": true, "passkey": true, "ldap": true, "entra": true, "google": true, "apple": true, "oidc": true}

// spaStart: GET /api/auth/spa/start?return=<path>[&login_hint=…][&idp_hint=…][&prompt=login].
func (a *App) spaStart(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	k, _, err := a.spaKey()
	if err != nil {
		if !errors.Is(err, errSPANotConnected) {
			slog.Error("spa: connection key", "err", err)
		}
		a.spaFailed(w, r, "not_connected", "")
		return
	}
	q := r.URL.Query()
	req := oidcx.NewRequest()
	req.State = a.signState(statePayload{ID: oidcx.Random(), Return: safeReturn(q.Get("return")), Exp: time.Now().Add(spaFlowTTL).Unix()})
	o := spa.AuthOptions{LoginHint: strings.TrimSpace(q.Get("login_hint"))}
	if h := q.Get("idp_hint"); idpHints[h] {
		o.IDPHint = h
	}
	if q.Get("prompt") == "login" {
		o.Prompt = "login"
	}
	target, err := k.AuthURL(ctx, a.spaBaseURL(r)+spa.CallbackPath, o, req)
	if err != nil {
		slog.Error("spa: sign-in cannot start", "err", err)
		a.spaFailed(w, r, "spa_unavailable", "")
		return
	}
	b, _ := json.Marshal(spaFlow{State: req.State, Verifier: req.Verifier, Nonce: req.Nonce})
	a.setSPACookie(w, r, base64.RawURLEncoding.EncodeToString(b), int(spaFlowTTL/time.Second))
	http.Redirect(w, r, target, http.StatusFound)
}

var errorCodeRe = regexp.MustCompile(`^[a-z_]{1,40}$`)

func (a *App) spaAudit(actor, action, detail string) {
	a.deps.Store.Write(func(d *store.Data) {
		d.AddAudit(store.AuditEntry{Actor: actor, Action: action, Detail: truncateText(detail, 500)})
	})
}

func truncateText(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func (a *App) spaCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ip := a.clientIP(r)
	if e := q.Get("error"); e != "" {
		if !errorCodeRe.MatchString(e) {
			e = "spa_error"
		}
		a.spaAudit("", "auth.login_failed", "spa "+e+" "+ip)
		a.spaFailed(w, r, e, q.Get("error_description"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	state := q.Get("state")
	var flow spaFlow
	if c, err := r.Cookie(spaCookie); err == nil {
		if b, err := base64.RawURLEncoding.DecodeString(c.Value); err == nil {
			_ = json.Unmarshal(b, &flow)
		}
	}
	if state == "" || flow.State == "" || subtle.ConstantTimeCompare([]byte(flow.State), []byte(state)) != 1 {
		a.spaFailed(w, r, "state", "")
		return
	}
	payload, ok := a.openState(state, time.Now())
	if !ok {
		a.spaFailed(w, r, "state", "")
		return
	}
	k, _, err := a.spaKey()
	if err != nil {
		a.spaFailed(w, r, "not_connected", "")
		return
	}
	id, err := k.Finish(ctx, a.spaBaseURL(r)+spa.CallbackPath, q.Get("code"), oidcx.Request{State: state, Nonce: flow.Nonce, Verifier: flow.Verifier})
	if err != nil {
		code := "spa_unavailable"
		if errors.Is(err, oidcx.ErrInvalidToken) {
			code = "invalid_token"
		}
		slog.Warn("spa: sign-in failed", "err", err)
		a.spaAudit("", "auth.login_failed", "spa "+code+" "+ip)
		a.spaFailed(w, r, code, "")
		return
	}
	u, _, err := a.applySPAUser(spa.User{ID: id.Subject, Username: id.Username, Name: id.Name, Email: id.Email, Roles: id.Roles, Groups: id.Groups}, time.Now().UTC())
	if err != nil {
		code := "account_conflict"
		if errors.Is(err, errNoRole) {
			code = "no_role"
		}
		slog.Warn("spa: account refused", "user", id.Username, "err", err)
		a.spaAudit(id.Username, "auth.login_failed", "spa "+code+" "+ip)
		a.spaFailed(w, r, code, "")
		return
	}
	ss := a.deps.Sessions.CreateWith(u.ID, MethodSPA)
	a.setSPACookie(w, r, "", -1)
	a.setCookie(w, ss.ID, int(sessionMaxAge/time.Second))
	a.refreshSPAConfig(ctx, k)
	now := time.Now().UTC()
	a.deps.Store.Write(func(d *store.Data) {
		if x := d.Users[u.ID]; x != nil {
			x.LastLoginAt = &now
		}
		d.AddAudit(store.AuditEntry{At: now, Actor: u.Username, Action: "auth.login", Detail: "spa " + strings.Join(id.Methods, "+") + " " + ip})
	})
	http.Redirect(w, r, payload.Return, http.StatusSeeOther)
}

// refreshSPAConfig reads SPA's configuration (account and sign-out addresses) when it was never
// read for this key, so the first person signed in before any synchronization gets "Account
// and security".
func (a *App) refreshSPAConfig(ctx context.Context, k spa.Key) {
	if st := a.spaStored(); st.Issuer == k.Issuer && st.ClientID == k.ClientID && st.AccountURL != "" {
		return
	}
	cfg, _, _, err := k.Config(ctx, "")
	if err != nil {
		slog.Warn("spa: configuration not read", "err", err)
		return
	}
	a.spa.mu.Lock()
	defer a.spa.mu.Unlock()
	a.deps.Store.Write(func(d *store.Data) {
		st := &d.Settings.SPA
		if st.Issuer != k.Issuer || st.ClientID != k.ClientID {
			*st = model.SPAState{Issuer: k.Issuer, ClientID: k.ClientID}
		}
		applyConfig(st, cfg)
	})
}

func applyConfig(st *model.SPAState, cfg spa.Config) {
	st.AppID, st.AppName, st.AppType = cfg.Application.ID, cfg.Application.Name, cfg.Application.Type
	st.AccountURL, st.SignOutURL = cfg.AccountURL, cfg.SignOutURL
	st.Methods = make([]model.SPAMethod, 0, len(cfg.Methods))
	for _, m := range cfg.Methods {
		st.Methods = append(st.Methods, model.SPAMethod{ID: m.ID, Title: m.Title})
	}
}

// spaSignOut is SPA's sign-out address (back to Umbrella's address), "" when not connected.
func (a *App) spaSignOut(r *http.Request) string {
	k, _, err := a.spaKey()
	if err != nil {
		return ""
	}
	st := a.spaStored()
	out := st.SignOutURL
	if out == "" || st.Issuer != k.Issuer {
		out = k.Issuer + "/oauth2/logout"
	}
	return spa.SignOutURL(out, k.ClientID, a.spaBaseURL(r)+"/")
}

// ---- people

var errNoRole = errors.New("the person has no role of Umbrella in SPA")

// spaRoleID is the ID of a role in the catalogue: SPA role IDs are lower case.
func spaRoleID(id string) string { return strings.ToLower(id) }

// pickRole maps SPA role IDs to the Umbrella role: administrators first, then of the roles
// named the one with the most permissions (Umbrella gives one role per person); "" = none known.
func pickRole(d *store.Data, ids []string) string {
	var best *model.Role
	for _, r := range d.Roles {
		if !slices.Contains(ids, spaRoleID(r.ID)) {
			continue
		}
		switch {
		case best == nil, r.ID == model.RoleAdmin,
			best.ID != model.RoleAdmin && len(r.Permissions) > len(best.Permissions),
			best.ID != model.RoleAdmin && len(r.Permissions) == len(best.Permissions) && r.ID < best.ID:
			best = r
		}
	}
	if best == nil {
		return ""
	}
	return best.ID
}

// legacySource: an account of the versions before SPA, bound to an SPA person with the same
// username once.
func legacySource(source string) bool {
	return source == model.SourceLocal || source == model.SourceLDAP || source == model.SourceEntra || source == model.SourceNetBox
}

const (
	spaCreated = "created"
	spaBound   = "bound"
	spaUpdated = "updated"
)

// upsertSPA creates or updates the record of an SPA person (bound to su.ID), or binds an
// account of an earlier version with the same username. ErrUsernameTaken: the username belongs
// to another SPA person or a service.
func upsertSPA(d *store.Data, su spa.User, role string, now time.Time) (*model.User, string, error) {
	username := strings.TrimSpace(su.Username)
	var u *model.User
	for _, x := range d.Users {
		if x.Source == model.SourceSPA && x.ExternalID == su.ID {
			u = x
			break
		}
	}
	how := spaUpdated
	if u == nil {
		if x := d.UserByName(username); x != nil {
			if !legacySource(x.Source) {
				return nil, "", ErrUsernameTaken
			}
			u, how = x, spaBound
			d.AddAudit(store.AuditEntry{At: now, Actor: spaActor, Action: "user.bound", Object: x.ID, Detail: x.Username + " (" + x.Source + ") → SPA " + su.ID})
			u.Source, u.ExternalID = model.SourceSPA, su.ID
		}
	} else if x := d.UserByName(username); x != nil && x != u {
		return nil, "", ErrUsernameTaken
	}
	if u == nil {
		u = &model.User{ID: d.NextID("USR"), Source: model.SourceSPA, ExternalID: su.ID, ScopeMode: model.ScopeAll, CreatedAt: now}
		d.Users[u.ID] = u
		how = spaCreated
		d.AddAudit(store.AuditEntry{At: now, Actor: spaActor, Action: "user.create", Object: u.ID, Detail: username + ", role " + role})
	}
	u.Username = username
	name := strings.Join(strings.Fields(su.Name), " ")
	if name == "" {
		name = username
	}
	if name != u.Profile.DisplayName(username) {
		if !splitName(u, name) {
			u.LastName, u.FirstName, u.MiddleName = "", "", ""
		}
	}
	u.Name = u.Profile.DisplayName(username)
	if email := strings.TrimSpace(su.Email); email != "" && model.ValidEmail(email) {
		u.Email = email
	}
	if u.Role != role && how == spaUpdated {
		d.AddAudit(store.AuditEntry{At: now, Actor: spaActor, Action: "user.update", Object: u.ID, Detail: "role " + role})
	}
	u.Role, u.Groups = role, slices.Clone(su.Groups)
	if u.Disabled {
		u.Disabled = false
		d.AddAudit(store.AuditEntry{At: now, Actor: spaActor, Action: "user.unlock", Object: u.ID, Detail: u.Username})
	}
	return u, how, nil
}

// applySPAUser applies one person (a sign-in or a synchronization): no known role, or disabled,
// is errNoRole (the account is not created or changed).
func (a *App) applySPAUser(su spa.User, now time.Time) (model.User, string, error) {
	if su.ID == "" || su.Username == "" || su.Disabled {
		return model.User{}, "", errNoRole
	}
	var out model.User
	var how string
	var err error
	a.deps.Store.Write(func(d *store.Data) {
		role := pickRole(d, su.Roles)
		if role == "" {
			err = errNoRole
			return
		}
		var u *model.User
		if u, how, err = upsertSPA(d, su, role, now); err == nil {
			out = *u
		}
	})
	return out, how, err
}

// ---- settings page

type spaView struct {
	Connected   bool   `json:"connected"`
	KeySource   string `json:"key_source,omitempty"`
	KeyError    string `json:"key_error,omitempty"`
	RedirectURI string `json:"redirect_uri"`
	SignOutURI  string `json:"post_logout_redirect_uri"`
	model.SPAState
	Counts   spaCounts  `json:"counts"`
	Roles    []spa.Role `json:"roles"`
	Interval string     `json:"interval"`
}

type spaCounts struct {
	SPA      int `json:"spa"`
	Disabled int `json:"disabled"`
	// Legacy are the accounts of versions before SPA not bound to an SPA person yet.
	Legacy int `json:"legacy"`
}

func (a *App) spaView(r *http.Request) spaView {
	v := spaView{SPAState: a.spaStored(), RedirectURI: a.spaBaseURL(r) + spa.CallbackPath, SignOutURI: a.spaBaseURL(r) + "/", Interval: spa.SyncEvery.String()}
	k, source, err := a.spaKey()
	v.KeySource = source
	switch {
	case err == nil:
		v.Connected = true
		if v.Issuer != k.Issuer || v.ClientID != k.ClientID {
			v.SPAState = model.SPAState{}
		}
		v.Issuer, v.ClientID = k.Issuer, k.ClientID
	case !errors.Is(err, errSPANotConnected):
		v.KeyError = err.Error()
	}
	if v.Methods == nil {
		v.Methods = []model.SPAMethod{}
	}
	if v.Conflicts == nil {
		v.Conflicts = []string{}
	}
	if v.NoRole == nil {
		v.NoRole = []string{}
	}
	a.deps.Store.Read(func(d *store.Data) {
		for _, u := range d.Users {
			switch {
			case u.Source == model.SourceSPA && u.Disabled:
				v.Counts.Disabled++
			case u.Source == model.SourceSPA:
				v.Counts.SPA++
			case u.Source != model.SourceNetBox && legacySource(u.Source) && !u.Disabled:
				v.Counts.Legacy++
			}
		}
		v.Roles = catalogueOf(d).Roles
	})
	return v
}

// ---- role catalogue

// catalogueOf is Umbrella's roles as SPA gets them: the system roles first, then the others by
// name.
func catalogueOf(d *store.Data) spa.Catalogue {
	roles := make([]*model.Role, 0, len(d.Roles))
	for _, r := range d.Roles {
		roles = append(roles, r)
	}
	slices.SortFunc(roles, func(x, y *model.Role) int {
		if x.System != y.System {
			if x.System {
				return -1
			}
			return 1
		}
		if x.System {
			return strings.Compare(x.ID, y.ID)
		}
		return byName(x.Name, y.Name)
	})
	c := spa.Catalogue{Roles: make([]spa.Role, 0, len(roles)), Default: model.RoleUser}
	for _, r := range roles {
		id := spaRoleID(r.ID)
		if !spa.RoleIDRe.MatchString(id) {
			continue
		}
		desc := r.Description
		if r.ID == model.RoleAdmin && desc == "" {
			desc = "Everything, including users, roles and settings"
		}
		c.Roles = append(c.Roles, spa.Role{ID: id, Name: r.Name, Description: desc})
	}
	if d.Roles[d.NewUserRole()] != nil {
		c.Default = spaRoleID(d.NewUserRole())
	}
	return c
}

// publishCatalogue sends the role catalogue to SPA when it changed since the last successful
// publication. It is called with a.spa.mu held; the result goes into st.
func (a *App) publishCatalogue(ctx context.Context, k spa.Key, st *model.SPAState) {
	var c spa.Catalogue
	a.deps.Store.Read(func(d *store.Data) { c = catalogueOf(d) })
	b, _ := json.Marshal(struct {
		Issuer, Client string
		C              spa.Catalogue
	}{k.Issuer, k.ClientID, c})
	sum := sha256.Sum256(b)
	hash := hex.EncodeToString(sum[:])
	if st.CatalogHash == hash && st.CatalogErr == "" {
		return
	}
	if err := k.PublishRoles(ctx, c); err != nil {
		st.CatalogErr = err.Error()
		slog.Warn("spa: role catalogue not published", "err", err)
		return
	}
	now := time.Now().UTC()
	st.CatalogHash, st.CatalogAt, st.CatalogRole, st.CatalogErr = hash, &now, len(c.Roles), ""
}

// rolesChanged publishes the catalogue after a role was created, changed or deleted (errors are
// kept in the status and retried by the schedule).
func (a *App) rolesChanged() {
	k, _, err := a.spaKey()
	if err != nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		a.spa.mu.Lock()
		defer a.spa.mu.Unlock()
		st := a.spaStored()
		if st.Issuer != k.Issuer || st.ClientID != k.ClientID {
			st = model.SPAState{Issuer: k.Issuer, ClientID: k.ClientID}
		}
		a.publishCatalogue(ctx, k, &st)
		a.deps.Store.Write(func(d *store.Data) { d.Settings.SPA = st })
	}()
}

// ---- synchronization

// syncSPA reads the configuration and the people of SPA and applies them (protocol §4).
// schedule tells a run of the schedule, which skips quietly while another one runs.
func (a *App) syncSPA(ctx context.Context, actor string, schedule bool) error {
	k, _, err := a.spaKey()
	if err != nil {
		return err
	}
	if !a.spa.running.TryLock() {
		if schedule {
			return nil
		}
		return errSyncRunning
	}
	defer a.spa.running.Unlock()
	a.spa.mu.Lock()
	defer a.spa.mu.Unlock()
	st := a.spaStored()
	if st.Issuer != k.Issuer || st.ClientID != k.ClientID {
		// Another SPA or a new key: read everything again.
		st = model.SPAState{Issuer: k.Issuer, ClientID: k.ClientID}
	}
	revoke, err := a.applySPA(ctx, k, &st)
	now := time.Now().UTC()
	st.LastSync, st.OK, st.Error = &now, err == nil, ""
	if err != nil {
		st.Error = err.Error()
		slog.Warn("spa: synchronization failed", "err", err)
	}
	a.deps.Store.Write(func(d *store.Data) {
		d.Settings.SPA = st
		if err != nil || st.Created+st.Bound+st.Disabled > 0 || !schedule {
			detail := fmt.Sprintf("users %d, created %d, bound %d, updated %d, disabled %d", st.Users, st.Created, st.Bound, st.Updated, st.Disabled)
			if len(st.Conflicts) > 0 {
				detail += ", conflicts " + strings.Join(st.Conflicts, " ")
			}
			if st.Error != "" {
				detail += ", error: " + st.Error
			}
			d.AddAudit(store.AuditEntry{At: now, Actor: actor, Action: "spa.sync", Object: k.Issuer, Detail: truncateText(detail, 1000)})
		}
	})
	for _, id := range revoke {
		a.deps.Sessions.DeleteUser(id, "")
	}
	return err
}

// applySPA applies SPA's configuration and people; it returns the users whose sessions end.
func (a *App) applySPA(ctx context.Context, k spa.Key, st *model.SPAState) ([]string, error) {
	cfg, tag, same, err := k.Config(ctx, st.ConfigETag)
	if err != nil {
		return nil, err
	}
	if !same {
		st.ConfigETag = tag
		applyConfig(st, cfg)
	}
	a.publishCatalogue(ctx, k, st)
	users, tag, same, err := k.Users(ctx, st.UsersETag)
	if err != nil {
		return nil, err
	}
	st.Created, st.Updated, st.Bound, st.Disabled = 0, 0, 0, 0
	if same {
		return nil, nil
	}
	st.Users, st.Conflicts, st.NoRole = 0, []string{}, []string{}
	now := time.Now().UTC()
	var revoke []string
	a.deps.Store.Write(func(d *store.Data) {
		keep := map[string]bool{}
		for _, su := range users {
			if su.ID == "" || su.Username == "" {
				continue
			}
			role := pickRole(d, su.Roles)
			if su.Disabled || role == "" {
				if role == "" && !su.Disabled {
					st.NoRole = append(st.NoRole, su.Username)
				}
				continue
			}
			u, how, err := upsertSPA(d, su, role, now)
			if err != nil {
				st.Conflicts = append(st.Conflicts, su.Username)
				continue
			}
			keep[u.ID] = true
			st.Users++
			switch how {
			case spaCreated:
				st.Created++
			case spaBound:
				st.Bound++
			default:
				st.Updated++
			}
		}
		// Everyone else who could sign in before is disabled: SPA people without access any
		// more, and the accounts of earlier versions SPA does not know (NetBox contacts never
		// signed in and stay).
		for id, u := range d.Users {
			if keep[id] || u.Disabled || u.Source == model.SourceNetBox || (u.Source != model.SourceSPA && !legacySource(u.Source)) {
				continue
			}
			u.Disabled = true
			revoke = append(revoke, id)
			st.Disabled++
			d.AddAudit(store.AuditEntry{At: now, Actor: spaActor, Action: "user.lock", Object: id, Detail: u.Username + " (" + u.Source + "): no access in SPA"})
		}
	})
	st.UsersETag = tag
	return revoke, nil
}

// runSPASchedule removes the secrets of the sign-in before SPA once, then publishes the
// catalogue and synchronizes at start and every five minutes.
func (a *App) runSPASchedule(ctx context.Context) {
	a.removeIdentitySecrets(ctx)
	t := time.NewTicker(spa.SyncEvery)
	defer t.Stop()
	for {
		sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		if err := a.syncSPA(sctx, "schedule", true); err != nil && !errors.Is(err, errSPANotConnected) && ctx.Err() == nil {
			slog.Warn("spa: scheduled synchronization failed", "err", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// removeIdentitySecrets deletes, once, the secrets of the sign-in Umbrella had before SPA:
// password hashes and their history, the Entra ID, provider and Umbrella Identity secrets. The
// LDAP service account stays: the NetBox synchronization reads computers with it.
func (a *App) removeIdentitySecrets(ctx context.Context) {
	v := a.deps.Vault
	if v == nil || !v.Enabled() {
		return
	}
	var done bool
	var paths []string
	a.deps.Store.Read(func(d *store.Data) {
		done = d.Settings.IdentityCleaned
		for id := range d.Users {
			paths = append(paths, credentials.Path(id), credentials.HistoryPath(id))
		}
	})
	if done {
		return
	}
	paths = append(paths, "entra", "identity/signing")
	for _, kind := range []string{"google", "apple", "oidc"} {
		paths = append(paths, "identity/providers/"+kind+"/client_secret", "identity/providers/"+kind+"/private_key")
	}
	for _, p := range paths {
		if err := v.Delete(ctx, p); err != nil && !errors.Is(err, secrets.ErrNotFound) {
			slog.Warn("old sign-in secret not deleted", "path", p, "err", err)
			return // OpenBao is away: try again at the next start
		}
	}
	a.deps.Store.Write(func(d *store.Data) {
		d.Settings.IdentityCleaned = true
		d.AddAudit(store.AuditEntry{Actor: "system", Action: "settings.identity", Detail: "secrets of the sign-in before SPA deleted from OpenBao"})
	})
}
