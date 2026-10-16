package app

import (
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/sbrw-evc/beacon/app/backend/internal/credentials"
	"github.com/sbrw-evc/beacon/app/backend/internal/model"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
)

const (
	// tokenKeyPath and tokenKeyName are where in OpenBao the HMAC key of API tokens is kept.
	tokenKeyPath = "system/api-tokens"
	tokenKeyName = "hmac_key"

	maxServiceAccounts   = 200
	maxTokensPerAccount  = 20
	maxTokenName         = 100
	maxAccountDesc       = 500
	maxTokenIPs          = 20
	tokenTouchEvery      = time.Minute
	maxTokenLifetimeDays = 3650
)

var (
	ErrAccountNameTaken = errors.New("a service account with this name exists")
	ErrTokenRevoked     = errors.New("the token is already revoked")
)

func init() {
	statuses = append(statuses,
		orgStatus{ErrAccountNameTaken, http.StatusConflict, "account_name_taken"},
		orgStatus{ErrTokenRevoked, http.StatusConflict, "token_revoked"},
	)
}

// tokenKey keeps the HMAC key API tokens are hashed with. The key lives in OpenBao like every
// Beacon secret; the snapshot keeps only its reference. It is made when the first token is
// issued.
type tokenKey struct {
	mu  sync.Mutex
	st  *store.Store
	sec Secrets
}

// get reads the key; with create, a missing key is generated and saved. Without a key and
// without create it answers ErrUnauthenticated: no token was ever issued.
func (k *tokenKey) get(ctx context.Context, create bool) ([]byte, error) {
	var ref string
	k.st.Read(func(d *store.Data) { ref = d.Settings.APITokenKeyRef })
	if ref == "" {
		if !create {
			return nil, ErrUnauthenticated
		}
		k.mu.Lock()
		defer k.mu.Unlock()
		k.st.Read(func(d *store.Data) { ref = d.Settings.APITokenKeyRef })
		if ref == "" {
			raw := make([]byte, 32)
			if _, err := rand.Read(raw); err != nil {
				return nil, err
			}
			made, err := putSecret(ctx, k.sec, tokenKeyPath, tokenKeyName, hex.EncodeToString(raw))
			if err != nil {
				return nil, err
			}
			k.st.Write(func(d *store.Data) { d.Settings.APITokenKeyRef = made })
			return raw, nil
		}
	}
	if k.sec == nil {
		return nil, credentials.ErrUnavailable
	}
	v, err := k.sec.Resolve(ref)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", credentials.ErrUnavailable, err)
	}
	raw, err := hex.DecodeString(v)
	if err != nil || len(raw) < 16 {
		return nil, fmt.Errorf("%w: the API token key is malformed", credentials.ErrUnavailable)
	}
	return raw, nil
}

func tokenHash(key []byte, token string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(token))
	return hex.EncodeToString(m.Sum(nil))
}

// newToken is umb_ and 32 random bytes in unpadded base64url.
func newToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return model.TokenPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func tokenHint(token string) string {
	if len(token) < 12 {
		return model.TokenPrefix + "…"
	}
	return token[:8] + "…" + token[len(token)-4:]
}

// ServiceAccountsService manages service accounts and their API tokens, and signs requests in
// with a token.
type ServiceAccountsService struct {
	st  *store.Store
	key *tokenKey
	now func() time.Time
}

func NewServiceAccountsService(st *store.Store, sec Secrets) *ServiceAccountsService {
	return &ServiceAccountsService{st: st, key: &tokenKey{st: st, sec: sec}, now: func() time.Time { return time.Now().UTC() }}
}

// ServiceAccountInput creates or changes a service account.
type ServiceAccountInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	RoleID      string   `json:"role_id"`
	TeamIDs     []string `json:"team_ids"`
	ScopeMode   string   `json:"scope_mode"`
	ServiceIDs  []string `json:"service_ids"`
	Disabled    bool     `json:"disabled"`
	// OwnerID is the person the account belongs to; empty on creation is the person creating it.
	OwnerID string `json:"owner_id"`
}

// TokenInput issues a token: a name and an optional expiry.
type TokenInput struct {
	Name      string     `json:"name"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// ServiceAccountView is an account with its role name and tokens, newest first.
type ServiceAccountView struct {
	model.ServiceAccount
	RoleName string           `json:"role_name"`
	Tokens   []model.APIToken `json:"tokens"`
	// OwnerName names the owner; OwnerDisabled: Keystone disabled the owner, the tokens do not work.
	OwnerName     string `json:"owner_name,omitempty"`
	OwnerDisabled bool   `json:"owner_disabled,omitempty"`
}

// IssuedToken is a new token: the plaintext is in this answer only.
type IssuedToken struct {
	Token  model.APIToken `json:"token"`
	Secret string         `json:"secret"`
}

func (s *ServiceAccountsService) viewOf(d *store.Data, a *model.ServiceAccount) ServiceAccountView {
	v := ServiceAccountView{ServiceAccount: *a, Tokens: []model.APIToken{}}
	v.TeamIDs = append([]string{}, a.TeamIDs...)
	v.ServiceIDs = append([]string{}, a.ServiceIDs...)
	u := a.User()
	if r := d.RoleOf(&u); r != nil {
		v.RoleName = r.Name
	}
	if o := d.Users[a.OwnerID]; o != nil {
		v.OwnerName, v.OwnerDisabled = o.Profile.DisplayName(o.Username), o.Disabled
	}
	for _, t := range d.APITokens {
		if t.AccountID == a.ID {
			v.Tokens = append(v.Tokens, *t)
		}
	}
	slices.SortFunc(v.Tokens, func(x, y model.APIToken) int {
		return cmp.Or(y.CreatedAt.Compare(x.CreatedAt), cmp.Compare(x.ID, y.ID))
	})
	return v
}

func (s *ServiceAccountsService) List() []ServiceAccountView {
	out := []ServiceAccountView{}
	s.st.Read(func(d *store.Data) {
		for _, a := range d.ServiceAccounts {
			out = append(out, s.viewOf(d, a))
		}
	})
	slices.SortFunc(out, func(x, y ServiceAccountView) int { return byName(x.Name, y.Name) })
	return out
}

func (s *ServiceAccountsService) Get(id string) (ServiceAccountView, error) {
	var out ServiceAccountView
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		if a := d.ServiceAccounts[id]; a != nil {
			out, err = s.viewOf(d, a), nil
		}
	})
	return out, err
}

// normalize checks the input against the catalog: the role and teams exist, the scope is
// valid, and only administrators give the administrator role.
func (in ServiceAccountInput) normalize(d *store.Data, actor model.User, self string) (model.ServiceAccount, error) {
	name := strings.TrimSpace(in.Name)
	if !ValidUsername(name) {
		return model.ServiceAccount{}, invalid("invalid_username", nil)
	}
	for id, a := range d.ServiceAccounts {
		if id != self && strings.EqualFold(a.Name, name) {
			return model.ServiceAccount{}, ErrAccountNameTaken
		}
	}
	desc := strings.TrimSpace(in.Description)
	if len([]rune(desc)) > maxAccountDesc || strings.ContainsFunc(desc, func(r rune) bool { return unicode.IsControl(r) && r != '\n' }) {
		return model.ServiceAccount{}, invalid("invalid_description", nil)
	}
	if in.RoleID == model.RoleAdmin && !d.IsAdmin(&actor) {
		return model.ServiceAccount{}, ErrAdminOnly
	}
	if d.Roles[in.RoleID] == nil && in.RoleID != model.RoleAdmin && in.RoleID != model.RoleUser {
		return model.ServiceAccount{}, invalid("unknown_role", nil)
	}
	teams, err := userTeams(d, in.TeamIDs)
	if err != nil {
		return model.ServiceAccount{}, err
	}
	mode := cmp.Or(in.ScopeMode, model.ScopeAll)
	if !model.ValidScope(mode) {
		return model.ServiceAccount{}, invalid("invalid_scope", nil)
	}
	var services []string
	if mode == model.ScopeServices {
		if services, err = userServiceScope(d, in.ServiceIDs); err != nil {
			return model.ServiceAccount{}, err
		}
		if len(services) == 0 {
			return model.ServiceAccount{}, invalid("scope_services_required", nil)
		}
	}
	owner := strings.TrimSpace(in.OwnerID)
	if owner == "" && d.Users[actor.ID] != nil {
		owner = actor.ID
	}
	if owner != "" {
		if o := d.Users[owner]; o == nil || o.Source == model.SourceService {
			return model.ServiceAccount{}, invalid("unknown_owner", nil)
		}
	}
	return model.ServiceAccount{Name: name, Description: desc, Role: in.RoleID, TeamIDs: teams, ScopeMode: mode, ServiceIDs: services, Disabled: in.Disabled, OwnerID: owner}, nil
}

// tokenOwnerActive: the account has no owner (made before owners existed) or its owner is
// enabled in Keystone.
func tokenOwnerActive(d *store.Data, a *model.ServiceAccount) bool {
	if a.OwnerID == "" {
		return true
	}
	o := d.Users[a.OwnerID]
	return o != nil && !o.Disabled
}

func describeAccount(a *model.ServiceAccount) string {
	out := a.Name + ", role " + a.Role + ", scope " + a.ScopeMode
	if a.OwnerID != "" {
		out += ", owner " + a.OwnerID
	}
	if a.Disabled {
		out += ", disabled"
	}
	return out
}

func (s *ServiceAccountsService) Create(actor model.User, in ServiceAccountInput) (ServiceAccountView, error) {
	var out ServiceAccountView
	var err error
	s.st.Write(func(d *store.Data) {
		var a model.ServiceAccount
		if a, err = in.normalize(d, actor, ""); err != nil {
			return
		}
		if len(d.ServiceAccounts) >= maxServiceAccounts {
			err = invalid("too_many_accounts", nil)
			return
		}
		now := s.now()
		a.ID, a.CreatedAt, a.CreatedBy, a.UpdatedAt, a.UpdatedBy = d.NextID("SA"), now, actor.Username, now, actor.Username
		d.ServiceAccounts[a.ID] = &a
		d.AddAudit(store.AuditEntry{At: now, Actor: actor.Username, Action: "serviceaccount.create", Object: a.ID, Detail: describeAccount(&a)})
		out = s.viewOf(d, &a)
	})
	return out, err
}

func (s *ServiceAccountsService) Update(actor model.User, id string, in ServiceAccountInput) (ServiceAccountView, error) {
	var out ServiceAccountView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		cur := d.ServiceAccounts[id]
		if cur == nil {
			return
		}
		// Only administrators change an account that has the administrator role.
		if cur.Role == model.RoleAdmin && !d.IsAdmin(&actor) {
			err = ErrAdminOnly
			return
		}
		var a model.ServiceAccount
		if a, err = in.normalize(d, actor, id); err != nil {
			return
		}
		a.ID, a.CreatedAt, a.CreatedBy, a.UpdatedAt, a.UpdatedBy = cur.ID, cur.CreatedAt, cur.CreatedBy, s.now(), actor.Username
		*cur = a
		d.AddAudit(store.AuditEntry{At: a.UpdatedAt, Actor: actor.Username, Action: "serviceaccount.update", Object: id, Detail: describeAccount(cur)})
		out = s.viewOf(d, cur)
	})
	return out, err
}

// Delete removes the account and all its tokens.
func (s *ServiceAccountsService) Delete(actor model.User, id string) error {
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		a := d.ServiceAccounts[id]
		if a == nil {
			return
		}
		if a.Role == model.RoleAdmin && !d.IsAdmin(&actor) {
			err = ErrAdminOnly
			return
		}
		err = nil
		n := 0
		for tid, t := range d.APITokens {
			if t.AccountID == id {
				delete(d.APITokens, tid)
				n++
			}
		}
		delete(d.ServiceAccounts, id)
		d.AddAudit(store.AuditEntry{Actor: actor.Username, Action: "serviceaccount.delete", Object: id, Detail: fmt.Sprintf("%s, %d tokens", a.Name, n)})
	})
	return err
}

// IssueToken makes a token for the account. The plaintext is returned once; only its keyed
// hash is kept.
func (s *ServiceAccountsService) IssueToken(ctx context.Context, actor model.User, id string, in TokenInput) (IssuedToken, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len([]rune(name)) > maxTokenName || strings.ContainsFunc(name, unicode.IsControl) {
		return IssuedToken{}, invalid("invalid_token_name", nil)
	}
	now := s.now()
	if in.ExpiresAt != nil {
		at := in.ExpiresAt.UTC()
		if !at.After(now) || at.After(now.AddDate(0, 0, maxTokenLifetimeDays)) {
			return IssuedToken{}, invalid("invalid_expiry", nil)
		}
		in.ExpiresAt = &at
	}
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		a := d.ServiceAccounts[id]
		switch {
		case a == nil:
		case a.Role == model.RoleAdmin && !d.IsAdmin(&actor):
			err = ErrAdminOnly
		default:
			err = nil
		}
	})
	if err != nil {
		return IssuedToken{}, err
	}
	key, err := s.key.get(ctx, true)
	if err != nil {
		return IssuedToken{}, err
	}
	secret, err := newToken()
	if err != nil {
		return IssuedToken{}, err
	}
	var out IssuedToken
	err = ErrNotFound
	s.st.Write(func(d *store.Data) {
		a := d.ServiceAccounts[id]
		if a == nil {
			return
		}
		active := 0
		for _, t := range d.APITokens {
			if t.AccountID == id && t.Active(now) {
				active++
			}
		}
		if active >= maxTokensPerAccount {
			err = invalid("too_many_tokens", nil)
			return
		}
		err = nil
		t := &model.APIToken{ID: d.NextID("TOK"), AccountID: id, Name: name, Hash: tokenHash(key, secret), Hint: tokenHint(secret),
			ExpiresAt: in.ExpiresAt, CreatedAt: now, CreatedBy: actor.Username}
		d.APITokens[t.ID] = t
		detail := a.Name + ": " + name
		if t.ExpiresAt != nil {
			detail += ", expires " + t.ExpiresAt.Format(time.RFC3339)
		}
		d.AddAudit(store.AuditEntry{At: now, Actor: actor.Username, Action: "serviceaccount.token.create", Object: t.ID, Detail: detail})
		out = IssuedToken{Token: *t, Secret: secret}
	})
	return out, err
}

// RevokeToken stops a token; it stays listed as revoked.
func (s *ServiceAccountsService) RevokeToken(actor model.User, id, tokenID string) (model.APIToken, error) {
	var out model.APIToken
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		a, t := d.ServiceAccounts[id], d.APITokens[tokenID]
		if a == nil || t == nil || t.AccountID != id {
			return
		}
		if a.Role == model.RoleAdmin && !d.IsAdmin(&actor) {
			err = ErrAdminOnly
			return
		}
		if t.RevokedAt != nil {
			err = ErrTokenRevoked
			return
		}
		now := s.now()
		t.RevokedAt, t.RevokedBy, err = &now, actor.Username, nil
		d.AddAudit(store.AuditEntry{At: now, Actor: actor.Username, Action: "serviceaccount.token.revoke", Object: t.ID, Detail: a.Name + ": " + t.Name})
		out = *t
	})
	return out, err
}

// Authenticate finds the account of a bearer token used from ip. A token that is unknown,
// revoked or expired, or whose account is disabled, is ErrUnauthenticated. The use is recorded
// at most once a minute per address; a new address is written to the audit log.
func (s *ServiceAccountsService) Authenticate(ctx context.Context, token, ip string) (model.User, error) {
	if !strings.HasPrefix(token, model.TokenPrefix) || len(token) > 200 {
		return model.User{}, ErrUnauthenticated
	}
	key, err := s.key.get(ctx, false)
	if err != nil {
		return model.User{}, err
	}
	hash := tokenHash(key, token)
	now := s.now()
	var u model.User
	var id string
	err = ErrUnauthenticated
	s.st.Read(func(d *store.Data) {
		for tid, t := range d.APITokens {
			if !hmac.Equal([]byte(t.Hash), []byte(hash)) {
				continue
			}
			a := d.ServiceAccounts[t.AccountID]
			if a == nil || a.Disabled || !t.Active(now) || !tokenOwnerActive(d, a) {
				return
			}
			u, id, err = a.User(), tid, nil
			return
		}
	})
	if err != nil {
		return model.User{}, err
	}
	s.st.Update(func(d *store.Data) bool {
		t := d.APITokens[id]
		if t == nil {
			return false
		}
		changed := false
		if !slices.Contains(t.IPs, ip) {
			t.IPs = append(t.IPs, ip)
			if len(t.IPs) > maxTokenIPs {
				t.IPs = slices.Clone(t.IPs[len(t.IPs)-maxTokenIPs:])
			}
			d.AddAudit(store.AuditEntry{At: now, Actor: u.Username, Action: "serviceaccount.token.new_address", Object: id, Detail: t.Name + " from " + ip})
			changed = true
		}
		if t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) >= tokenTouchEvery || t.LastUsedIP != ip {
			t.LastUsedAt, t.LastUsedIP = &now, ip
			changed = true
		}
		return changed
	})
	return u, nil
}

func (a *App) registerServiceAccounts(mux *http.ServeMux) {
	a.route(mux, "GET /api/service-accounts", "serviceaccounts:view", show(a.serviceAccounts.List))
	a.route(mux, "GET /api/service-accounts/{id}", "serviceaccounts:view", fetch(func(r *http.Request) (ServiceAccountView, error) {
		return a.serviceAccounts.Get(r.PathValue("id"))
	}))
	a.route(mux, "POST /api/service-accounts", "serviceaccounts:edit", submit(http.StatusCreated, func(r *http.Request, in ServiceAccountInput) (ServiceAccountView, error) {
		return a.serviceAccounts.Create(current(r).user, in)
	}))
	a.route(mux, "PUT /api/service-accounts/{id}", "serviceaccounts:edit", submit(http.StatusOK, func(r *http.Request, in ServiceAccountInput) (ServiceAccountView, error) {
		return a.serviceAccounts.Update(current(r).user, r.PathValue("id"), in)
	}))
	a.route(mux, "DELETE /api/service-accounts/{id}", "serviceaccounts:edit", remove(func(r *http.Request) error {
		return a.serviceAccounts.Delete(current(r).user, r.PathValue("id"))
	}))
	a.route(mux, "POST /api/service-accounts/{id}/tokens", "serviceaccounts:edit", submit(http.StatusCreated, func(r *http.Request, in TokenInput) (IssuedToken, error) {
		return a.serviceAccounts.IssueToken(r.Context(), current(r).user, r.PathValue("id"), in)
	}))
	a.route(mux, "POST /api/service-accounts/{id}/tokens/{token}/revoke", "serviceaccounts:edit", fetch(func(r *http.Request) (model.APIToken, error) {
		return a.serviceAccounts.RevokeToken(current(r).user, r.PathValue("id"), r.PathValue("token"))
	}))
}
