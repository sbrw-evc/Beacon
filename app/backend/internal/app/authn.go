package app

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/credentials"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type AuthService struct {
	st        *store.Store
	passwords *credentials.Passwords
	secrets   Secrets
	dir       Directory
	users     *UserService
}

func NewAuthService(st *store.Store, passwords *credentials.Passwords, secrets Secrets, dir Directory, users *UserService) *AuthService {
	return &AuthService{st: st, passwords: passwords, secrets: secrets, dir: dir, users: users}
}

func (s *AuthService) Authenticate(name, password string) (model.User, error) {
	name = strings.TrimSpace(name)
	if name == "" || password == "" {
		auth.CheckPassword("", password)
		return model.User{}, ErrInvalidCredentials
	}
	var u *model.User
	var ldap directory.Config
	s.st.Read(func(d *store.Data) {
		if x := d.UserByName(name); x != nil {
			c := *x
			u = &c
		}
		ldap = d.Settings.LDAP
	})
	if u != nil && u.Source == model.SourceLocal {
		ok, err := s.passwords.Verify(u.PasswordRef, password)
		if err != nil {
			slog.Error("local sign-in: password hash unavailable", "user", u.Username, "err", err)
			return model.User{}, err
		}
		if !ok || u.Disabled {
			return model.User{}, ErrInvalidCredentials
		}
		return *u, nil
	}
	if !ldap.Enabled || (u != nil && (u.Disabled || u.Source == model.SourceEntra)) {
		auth.CheckPassword("", password)
		return model.User{}, ErrInvalidCredentials
	}
	id, err := s.directoryIdentity(ldap, name, password)
	if err != nil {
		return model.User{}, err
	}
	return s.users.SyncDirectory(id)
}

func (s *AuthService) directoryIdentity(cfg directory.Config, name, password string) (directory.Identity, error) {
	bind, err := s.secrets.Resolve(cfg.BindPasswordRef)
	if err != nil {
		slog.Error("directory: service account password unavailable", "err", err)
		return directory.Identity{}, ErrDirectoryUnavailable
	}
	id, err := s.dir.Authenticate(cfg, bind, name, password)
	switch {
	case errors.Is(err, directory.ErrInvalidCredentials), errors.Is(err, directory.ErrUserNotFound), errors.Is(err, directory.ErrAmbiguousUser):
		return id, ErrInvalidCredentials
	case err != nil:
		slog.Error("directory: sign-in failed", "err", err)
		return id, ErrDirectoryUnavailable
	}
	return id, nil
}

func (s *AuthService) Signed(id, ip string) model.User {
	now := s.users.now()
	var out model.User
	s.st.Write(func(d *store.Data) {
		u := d.Users[id]
		if u == nil {
			return
		}
		u.LastLoginAt = &now
		out = *u
		d.AddAudit(store.AuditEntry{At: now, Actor: u.Username, Action: "auth.login", Detail: u.Source + " " + ip})
	})
	return out
}

func (s *AuthService) Failed(name, ip string) {
	s.st.Write(func(d *store.Data) {
		d.AddAudit(store.AuditEntry{Actor: name, Action: "auth.login_failed", Detail: ip})
	})
}

func (s *AuthService) SignedOut(username string) {
	s.st.Write(func(d *store.Data) {
		d.AddAudit(store.AuditEntry{Actor: username, Action: "auth.logout"})
	})
}
