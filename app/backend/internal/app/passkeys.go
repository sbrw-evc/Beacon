package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/passkey"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// Sign-in methods of a session (the amr claim of Umbrella Identity).
const (
	MethodPassword = "pwd"
	MethodPasskey  = "passkey"
)

var (
	ErrPasskeysOff   = errors.New("passkeys are turned off")
	ErrPasskeyOrigin = errors.New("this address is outside the passkey domain")
	ErrPasskeyFailed = errors.New("the passkey could not be verified")
	ErrPasskeyLimit  = errors.New("too many passkeys")
	// passkeyStartsPerMinute limits passkey sign-in starts from one address.
	passkeyStartsPerMinute = 60
)

func init() {
	statuses = append(statuses,
		orgStatus{ErrPasskeysOff, http.StatusConflict, "passkeys_off"},
		orgStatus{ErrPasskeyOrigin, http.StatusBadRequest, "passkey_origin"},
		orgStatus{ErrPasskeyFailed, http.StatusUnauthorized, "passkey_failed"},
		orgStatus{ErrPasskeyLimit, http.StatusConflict, "passkey_limit"},
	)
}

// PasskeyService runs passkey sign-in and registration and keeps the passkeys of users.
type PasskeyService struct {
	st  *store.Store
	cer *passkey.Ceremonies
	now func() time.Time
}

func NewPasskeyService(st *store.Store) *PasskeyService {
	return &PasskeyService{st: st, cer: passkey.NewCeremonies(), now: func() time.Time { return time.Now().UTC() }}
}

func (s *PasskeyService) settings() (model.PasskeySettings, string) {
	var p model.PasskeySettings
	var public string
	s.st.Read(func(d *store.Data) { p, public = d.Settings.Passkeys, d.Settings.Alerting.PublicURL })
	return p, public
}

// RPID is the passkey domain in effect: the configured one, else the host of the public address.
func (s *PasskeyService) RPID() string {
	p, public := s.settings()
	if p.RPID != "" {
		return p.RPID
	}
	if u, err := url.Parse(public); err == nil {
		return u.Hostname()
	}
	return ""
}

// Enabled reports whether passkeys are on.
func (s *PasskeyService) Enabled() bool {
	p, _ := s.settings()
	return p.Enabled
}

// party is the relying party of a request: the page origin must be inside the passkey domain.
func (s *PasskeyService) party(r *http.Request) (passkey.Party, error) {
	p, _ := s.settings()
	if !p.Enabled {
		return passkey.Party{}, ErrPasskeysOff
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return passkey.Party{}, ErrPasskeyOrigin
	}
	party, err := passkey.NewParty(s.RPID(), "Umbrella", origin)
	if err != nil {
		return passkey.Party{}, ErrPasskeyOrigin
	}
	return party, nil
}

func accountOf(u *model.User) passkey.Account {
	acc := passkey.Account{Handle: []byte(u.ID), Name: u.Username, Display: cmpOr(u.Name, u.Username)}
	for _, k := range u.Passkeys {
		if bytes.Equal(k.UserHandle, acc.Handle) {
			acc.Keys = append(acc.Keys, k)
		}
	}
	return acc
}

func cmpOr(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// BeginSignIn starts a passkey sign-in.
func (s *PasskeyService) BeginSignIn(r *http.Request) (passkey.Started, error) {
	p, err := s.party(r)
	if err != nil {
		return passkey.Started{}, err
	}
	return s.cer.BeginLogin(p)
}

// FinishSignIn verifies the answer and returns the account.
func (s *PasskeyService) FinishSignIn(id string, body []byte) (model.User, string, error) {
	var owner string
	acc, key, err := s.cer.FinishLogin(id, body, func(credID, handle []byte) (passkey.Account, error) {
		var out passkey.Account
		found := false
		s.st.Read(func(d *store.Data) {
			for _, u := range d.Users {
				if u.Disabled || u.Source == model.SourceService {
					continue
				}
				if a, ok := passkey.AccountFor(u.Username, u.Name, u.Passkeys, credID, handle); ok {
					out, owner, found = a, u.ID, true
					return
				}
			}
		})
		if !found {
			return passkey.Account{}, passkey.ErrUnknown
		}
		return out, nil
	}, s.now())
	if err != nil {
		if errors.Is(err, passkey.ErrCeremony) {
			return model.User{}, "", invalid("passkey_expired", err)
		}
		return model.User{}, "", ErrPasskeyFailed
	}
	var u model.User
	s.st.Write(func(d *store.Data) {
		x := d.Users[owner]
		if x == nil || x.Disabled {
			return
		}
		for i := range x.Passkeys {
			if bytes.Equal(x.Passkeys[i].CredentialID, key.CredentialID) {
				x.Passkeys[i].SignCount, x.Passkeys[i].BackupState, x.Passkeys[i].LastUsedAt = key.SignCount, key.BackupState, key.LastUsedAt
			}
		}
		u = *x
	})
	if u.ID == "" {
		return model.User{}, "", ErrPasskeyFailed
	}
	_ = acc
	return u, key.Name, nil
}

// List is the passkeys of a user, newest first.
func (s *PasskeyService) List(userID string) []model.Passkey {
	out := []model.Passkey{}
	s.st.Read(func(d *store.Data) {
		if u := d.Users[userID]; u != nil {
			out = append(out, u.Passkeys...)
		}
	})
	slices.SortFunc(out, func(a, b model.Passkey) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out
}

// BeginRegistration starts adding a passkey to the signed-in user.
func (s *PasskeyService) BeginRegistration(r *http.Request, userID string) (passkey.Started, error) {
	p, err := s.party(r)
	if err != nil {
		return passkey.Started{}, err
	}
	var acc passkey.Account
	var n int
	s.st.Read(func(d *store.Data) {
		if u := d.Users[userID]; u != nil {
			acc, n = accountOf(u), len(u.Passkeys)
		}
	})
	if acc.Handle == nil {
		return passkey.Started{}, ErrNotFound
	}
	if n >= model.MaxPasskeys {
		return passkey.Started{}, ErrPasskeyLimit
	}
	return s.cer.BeginRegistration(p, userID, acc)
}

// PasskeyAnswer is the browser's answer to a ceremony.
type PasskeyAnswer struct {
	Ceremony   string          `json:"ceremony"`
	Credential json.RawMessage `json:"credential"`
	Name       string          `json:"name"`
}

func passkeyName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > 64 || strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 }) {
		return "", invalid("passkey_name", errors.New("the name is too long"))
	}
	return name, nil
}

// FinishRegistration stores the new passkey.
func (s *PasskeyService) FinishRegistration(actor model.User, in PasskeyAnswer) (model.Passkey, error) {
	name, err := passkeyName(in.Name)
	if err != nil {
		return model.Passkey{}, err
	}
	var acc passkey.Account
	s.st.Read(func(d *store.Data) {
		if u := d.Users[actor.ID]; u != nil {
			acc = accountOf(u)
		}
	})
	k, err := s.cer.FinishRegistration(in.Ceremony, actor.ID, acc, in.Credential, s.now())
	if err != nil {
		if errors.Is(err, passkey.ErrCeremony) {
			return model.Passkey{}, invalid("passkey_expired", err)
		}
		slog.Warn("passkey: registration rejected", "user", actor.Username, "err", err)
		return model.Passkey{}, ErrPasskeyFailed
	}
	if name != "" {
		k.Name = name
	}
	err = ErrNotFound
	s.st.Write(func(d *store.Data) {
		u := d.Users[actor.ID]
		if u == nil {
			return
		}
		if len(u.Passkeys) >= model.MaxPasskeys {
			err = ErrPasskeyLimit
			return
		}
		k.ID = d.NextID("PK")
		u.Passkeys = append(u.Passkeys, k)
		d.AddAudit(store.AuditEntry{Actor: actor.Username, Action: "passkey.add", Object: u.ID, Detail: k.Name})
		err = nil
	})
	return k, err
}

// Rename renames a passkey of the user.
func (s *PasskeyService) Rename(actor model.User, id, name string) (model.Passkey, error) {
	name, err := passkeyName(name)
	if err != nil || name == "" {
		return model.Passkey{}, invalid("passkey_name", errors.New("enter a name"))
	}
	var out model.Passkey
	err = ErrNotFound
	s.st.Write(func(d *store.Data) {
		u := d.Users[actor.ID]
		if u == nil {
			return
		}
		for i := range u.Passkeys {
			if u.Passkeys[i].ID == id {
				u.Passkeys[i].Name = name
				out, err = u.Passkeys[i], nil
				return
			}
		}
	})
	return out, err
}

// Remove deletes a passkey of a user; actor is the user or an administrator.
func (s *PasskeyService) Remove(actor model.User, userID, id string) error {
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		u := d.Users[userID]
		if u == nil {
			return
		}
		if id == "*" {
			if len(u.Passkeys) > 0 {
				d.AddAudit(store.AuditEntry{Actor: actor.Username, Action: "passkey.remove", Object: u.ID, Detail: fmt.Sprintf("%s: all %d passkeys", u.Username, len(u.Passkeys))})
			}
			u.Passkeys, err = nil, nil
			return
		}
		for i, k := range u.Passkeys {
			if k.ID == id {
				u.Passkeys = slices.Delete(slices.Clone(u.Passkeys), i, i+1)
				d.AddAudit(store.AuditEntry{Actor: actor.Username, Action: "passkey.remove", Object: u.ID, Detail: u.Username + ": " + k.Name})
				err = nil
				return
			}
		}
	})
	return err
}

// PasskeySettingsView is the passkey settings with the domain in effect.
type PasskeySettingsView struct {
	model.PasskeySettings
	Effective string `json:"effective_rp_id"`
	Users     int    `json:"users"`
	Keys      int    `json:"keys"`
}

func (s *PasskeyService) View() PasskeySettingsView {
	p, _ := s.settings()
	out := PasskeySettingsView{PasskeySettings: p, Effective: s.RPID()}
	s.st.Read(func(d *store.Data) {
		for _, u := range d.Users {
			if len(u.Passkeys) > 0 {
				out.Users++
				out.Keys += len(u.Passkeys)
			}
		}
	})
	return out
}

// Save changes the passkey settings. A new domain makes the passkeys of the old one useless,
// so they are removed.
func (s *PasskeyService) Save(actor string, in model.PasskeySettings) (PasskeySettingsView, error) {
	in, ok := in.Normalize()
	if !ok {
		return PasskeySettingsView{}, invalid("passkey_domain", errors.New("enter a domain name such as example.com"))
	}
	before := s.RPID()
	s.st.Write(func(d *store.Data) {
		d.Settings.Passkeys = in
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.passkeys", Detail: describePasskeys(in)})
	})
	if after := s.RPID(); after != before && before != "" {
		s.st.Write(func(d *store.Data) {
			n := 0
			for _, u := range d.Users {
				n += len(u.Passkeys)
				u.Passkeys = nil
			}
			if n > 0 {
				d.AddAudit(store.AuditEntry{Actor: actor, Action: "passkey.remove", Detail: "passkey domain changed from " + before + " to " + after + ": all passkeys removed"})
			}
		})
	}
	return s.View(), nil
}

func describePasskeys(p model.PasskeySettings) string {
	if !p.Enabled {
		return "disabled"
	}
	if p.RPID == "" {
		return "enabled"
	}
	return "enabled, domain " + p.RPID
}

func (a *App) registerPasskeys(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/passkey/begin", func(w http.ResponseWriter, r *http.Request) {
		if a.passkeyRate.Locked(a.clientIP(r)) {
			writeError(w, ErrTooManyAttempts)
			return
		}
		a.passkeyRate.Fail(a.clientIP(r))
		v, err := a.passkeys.BeginSignIn(r)
		reply(w, http.StatusOK, v, err)
	})
	mux.HandleFunc("POST /api/auth/passkey/finish", a.passkeySignIn)
	mux.HandleFunc("GET /api/auth/me/passkeys", a.authed(func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, a.passkeys.List(current(r).user.ID))
	}))
	mux.HandleFunc("POST /api/auth/me/passkeys/begin", a.authed(func(w http.ResponseWriter, r *http.Request) {
		v, err := a.passkeys.BeginRegistration(r, current(r).user.ID)
		reply(w, http.StatusOK, v, err)
	}))
	mux.HandleFunc("POST /api/auth/me/passkeys/finish", a.authed(submit(http.StatusCreated, func(r *http.Request, in PasskeyAnswer) (model.Passkey, error) {
		return a.passkeys.FinishRegistration(current(r).user, in)
	})))
	mux.HandleFunc("PATCH /api/auth/me/passkeys/{id}", a.authed(submit(http.StatusOK, func(r *http.Request, in struct {
		Name string `json:"name"`
	}) (model.Passkey, error) {
		return a.passkeys.Rename(current(r).user, r.PathValue("id"), in.Name)
	})))
	mux.HandleFunc("DELETE /api/auth/me/passkeys/{id}", a.authed(remove(func(r *http.Request) error {
		u := current(r).user
		return a.passkeys.Remove(u, u.ID, r.PathValue("id"))
	})))
	a.route(mux, "GET /api/users/{id}/passkeys", "users:view", fetch(func(r *http.Request) ([]model.Passkey, error) {
		return a.passkeys.List(r.PathValue("id")), nil
	}))
	a.route(mux, "DELETE /api/users/{id}/passkeys/{pk}", "users:lock", remove(func(r *http.Request) error {
		return a.passkeys.Remove(current(r).user, r.PathValue("id"), r.PathValue("pk"))
	}))
	a.route(mux, "GET /api/settings/passkeys", "settings.identity:view", show(a.passkeys.View))
	a.route(mux, "PUT /api/settings/passkeys", "settings.identity:edit", submit(http.StatusOK, func(r *http.Request, in model.PasskeySettings) (PasskeySettingsView, error) {
		return a.passkeys.Save(actorName(r), in)
	}))
}

func (a *App) passkeySignIn(w http.ResponseWriter, r *http.Request) {
	var in PasskeyAnswer
	if !httpx.Decode(w, r, &in) {
		return
	}
	ip := a.clientIP(r)
	u, name, err := a.passkeys.FinishSignIn(in.Ceremony, in.Credential)
	if err != nil {
		if errors.Is(err, ErrPasskeyFailed) {
			a.auth.Failed("passkey", ip)
		}
		writeError(w, err)
		return
	}
	ss := a.deps.Sessions.CreateWith(u.ID, MethodPasskey)
	u = a.auth.Signed(u.ID, ip)
	a.deps.Store.Write(func(d *store.Data) {
		d.AddAudit(store.AuditEntry{Actor: u.Username, Action: "auth.passkey", Object: u.ID, Detail: name + " from " + ip})
	})
	a.setCookie(w, ss.ID, int(auth.MaxLifetime/time.Second))
	httpx.JSON(w, http.StatusOK, a.view(u, ss.CSRF))
}
