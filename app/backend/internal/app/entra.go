package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/credentials"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/entra"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

var errAppChanged = errors.New("the tenant or the application changed: enter the client secret again")

const (
	entraPendingTTL = 10 * time.Minute
	entraPendingMax = 10000
	// entraStartsPerMinute limits the sign-in starts from one client address.
	entraStartsPerMinute = 60
)

type entraPending struct {
	req    entra.Request
	expiry time.Time
}

// EntraService keeps the Microsoft Entra ID settings and runs the sign-in flow.
type EntraService struct {
	st       *store.Store
	secrets  Secrets
	users    *UserService
	sessions SessionRevoker

	mu      sync.Mutex
	pending map[string]entraPending
}

func NewEntraService(st *store.Store, secrets Secrets, users *UserService, sessions SessionRevoker) *EntraService {
	return &EntraService{st: st, secrets: secrets, users: users, sessions: sessions, pending: map[string]entraPending{}}
}

type EntraView struct {
	Config          entra.Config   `json:"config"`
	ClientSecretSet bool           `json:"client_secret_set"`
	Users           DirectoryUsers `json:"users"`
	LocalAdmins     int            `json:"local_admins"`
}

func (s *EntraService) View() EntraView {
	var out EntraView
	s.st.Read(func(d *store.Data) { out = entraView(d) })
	return out
}

func (s *EntraService) stored() entra.Config {
	var out entra.Config
	s.st.Read(func(d *store.Data) { out = d.Settings.Entra })
	return out
}

func (s *EntraService) Enabled() bool { return s.stored().Enabled }

func (s *EntraService) Test(ctx context.Context, in entra.TestRequest) (entra.TestReport, error) {
	cfg, err := in.Config.Normalize()
	if err != nil {
		return entra.Report(entra.Probe{}, err), nil
	}
	secret, err := s.clientSecret(cfg, in.ClientSecret)
	if err != nil {
		return entra.TestReport{}, err
	}
	return entra.Report(entra.Test(ctx, cfg, secret)), nil
}

func (s *EntraService) Save(ctx context.Context, actor string, in entra.TestRequest) (EntraView, error) {
	if !in.Config.Enabled {
		return s.disable(actor)
	}
	cfg, err := in.Config.Normalize()
	if err != nil {
		return EntraView{}, invalid("entra_invalid", err)
	}
	secret, err := s.clientSecret(cfg, in.ClientSecret)
	if err != nil {
		return EntraView{}, err
	}
	if _, err := entra.Test(ctx, cfg, secret); err != nil {
		return EntraView{}, invalid("entra_unavailable", err)
	}
	cfg.Enabled, cfg.ClientSecretRef = true, s.stored().ClientSecretRef
	if in.ClientSecret != "" {
		if cfg.ClientSecretRef, err = s.secrets.PutRef(ctx, entra.SecretPath, entra.SecretKey, in.ClientSecret); err != nil {
			return EntraView{}, fmt.Errorf("%w: %v", credentials.ErrUnavailable, err)
		}
	}
	var out EntraView
	s.st.Write(func(d *store.Data) {
		d.Settings.Entra = cfg
		note := ""
		if in.ClientSecret != "" {
			note = ", client secret replaced"
		}
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.entra", Detail: "enabled tenant " + cfg.TenantID + " app " + cfg.ClientID + note})
		out = entraView(d)
	})
	return out, nil
}

func (s *EntraService) disable(actor string) (EntraView, error) {
	var out EntraView
	var err error
	var signedOut []string
	s.st.Write(func(d *store.Data) {
		if !d.Settings.Entra.Enabled {
			out = entraView(d)
			return
		}
		if localAdmins(d) == 0 {
			err = ErrNoLocalAdmin
			return
		}
		d.Settings.Entra.Enabled = false
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.entra", Detail: "disabled"})
		out = entraView(d)
		for id, u := range d.Users {
			if u.Source == model.SourceEntra {
				signedOut = append(signedOut, id)
			}
		}
	})
	for _, id := range signedOut {
		s.sessions.DeleteUser(id, "")
	}
	return out, err
}

func (s *EntraService) clientSecret(cfg entra.Config, given string) (string, error) {
	if given != "" {
		return given, nil
	}
	stored := s.stored()
	if stored.ClientSecretRef == "" {
		return "", invalid("entra_secret_required", nil)
	}
	if !strings.EqualFold(stored.TenantID, cfg.TenantID) || !strings.EqualFold(stored.ClientID, cfg.ClientID) || stored.Cloud != cfg.Cloud {
		return "", invalid("entra_secret_required", errAppChanged)
	}
	secret, err := s.secrets.Resolve(stored.ClientSecretRef)
	if err != nil {
		return "", fmt.Errorf("%w: %v", credentials.ErrUnavailable, err)
	}
	return secret, nil
}

// Start begins a sign-in and returns the Microsoft sign-in page address and the state that
// the browser must bring back.
func (s *EntraService) Start(ctx context.Context) (string, string, error) {
	cfg := s.stored()
	if !cfg.Enabled {
		return "", "", ErrNotFound
	}
	m, err := entra.Discover(ctx, cfg)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrDirectoryUnavailable, err)
	}
	req := entra.NewRequest()
	s.remember(req, time.Now())
	return entra.AuthURL(cfg, m, req), req.State, nil
}

// remember keeps a started sign-in until its callback. Expired sign-ins are dropped; when the
// table is full, the oldest started sign-in gives way, so a flood of starts never refuses a
// new one (the flood itself is limited per address in the handler).
func (s *EntraService) remember(req entra.Request, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, p := range s.pending {
		if now.After(p.expiry) {
			delete(s.pending, k)
		}
	}
	for len(s.pending) >= entraPendingMax {
		var oldest string
		var at time.Time
		for k, p := range s.pending {
			if oldest == "" || p.expiry.Before(at) {
				oldest, at = k, p.expiry
			}
		}
		delete(s.pending, oldest)
	}
	s.pending[req.State] = entraPending{req: req, expiry: now.Add(entraPendingTTL)}
}

func (s *EntraService) take(state string) (entra.Request, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[state]
	delete(s.pending, state)
	if !ok || time.Now().After(p.expiry) {
		return entra.Request{}, false
	}
	return p.req, true
}

// Complete finishes the sign-in started with state and returns the Umbrella account.
func (s *EntraService) Complete(ctx context.Context, state, code string) (model.User, error) {
	req, ok := s.take(state)
	if !ok {
		return model.User{}, errEntraState
	}
	cfg := s.stored()
	if !cfg.Enabled {
		return model.User{}, ErrInvalidCredentials
	}
	secret, err := s.secrets.Resolve(cfg.ClientSecretRef)
	if err != nil {
		return model.User{}, fmt.Errorf("%w: client secret: %v", ErrDirectoryUnavailable, err)
	}
	s.st.Read(func(d *store.Data) {
		cfg.ReadGroups = slices.ContainsFunc(d.Settings.Groups.Mappings, func(m model.GroupMapping) bool { return m.Source == model.SourceEntra })
	})
	acc, err := entra.SignIn(ctx, cfg, secret, code, req, time.Now())
	switch {
	case errors.Is(err, entra.ErrNotAllowed):
		return model.User{}, errEntraNotAllowed
	case errors.Is(err, entra.ErrInvalidUser):
		return model.User{}, ErrInvalidCredentials
	case err != nil:
		return model.User{}, fmt.Errorf("%w: %v", ErrDirectoryUnavailable, err)
	}
	return s.users.SyncEntra(acc)
}

var (
	errEntraState      = errors.New("the sign-in request has expired or was started in another browser")
	errEntraNotAllowed = errors.New("the account is not allowed to sign in to Umbrella")
)

func entraView(d *store.Data) EntraView {
	out := EntraView{Config: d.Settings.Entra.Public(), ClientSecretSet: d.Settings.Entra.ClientSecretRef != "", LocalAdmins: localAdmins(d)}
	out.Users = sourceUsers(d, model.SourceEntra)
	return out
}
