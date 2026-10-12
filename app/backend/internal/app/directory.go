package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/credentials"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

var errAccountChanged = errors.New("the server or the service account changed: enter the service account password again")

type SessionRevoker interface {
	DeleteUser(userID, keep string)
}

type DirectoryService struct {
	st       *store.Store
	secrets  Secrets
	dir      Directory
	sessions SessionRevoker
}

func NewDirectoryService(st *store.Store, secrets Secrets, dir Directory, sessions SessionRevoker) *DirectoryService {
	return &DirectoryService{st: st, secrets: secrets, dir: dir, sessions: sessions}
}

type DirectoryUsers struct {
	Total      int        `json:"total"`
	Admins     int        `json:"admins"`
	Disabled   int        `json:"disabled"`
	LastSignIn *time.Time `json:"last_sign_in,omitempty"`
}

type DirectoryView struct {
	Config          directory.Config `json:"config"`
	BindPasswordSet bool             `json:"bind_password_set"`
	Users           DirectoryUsers   `json:"users"`
	LocalAdmins     int              `json:"local_admins"`
}

func (s *DirectoryService) View() DirectoryView {
	var out DirectoryView
	s.st.Read(func(d *store.Data) { out = directoryView(d) })
	return out
}

func (s *DirectoryService) stored() directory.Config {
	var out directory.Config
	s.st.Read(func(d *store.Data) { out = d.Settings.LDAP })
	return out
}

func (s *DirectoryService) Test(in directory.TestRequest) (directory.TestReport, error) {
	cfg, err := in.Config.Normalize()
	if err != nil {
		return directory.Report(directory.Probe{}, err), nil
	}
	bind, err := s.bindPassword(cfg, in.BindPassword)
	if err != nil {
		return directory.TestReport{}, err
	}
	return directory.Report(s.dir.Test(cfg, bind, in.TestUsername, in.TestPassword)), nil
}

func (s *DirectoryService) Save(ctx context.Context, actor string, in directory.TestRequest) (DirectoryView, error) {
	if !in.Config.Enabled {
		return s.disable(actor)
	}
	cfg, err := in.Config.Normalize()
	if err != nil {
		return DirectoryView{}, invalid("ldap_invalid", err)
	}
	bind, err := s.bindPassword(cfg, in.BindPassword)
	if err != nil {
		return DirectoryView{}, err
	}
	if _, err := s.dir.Test(cfg, bind, "", ""); err != nil {
		return DirectoryView{}, invalid("ldap_unavailable", err)
	}
	cfg.Enabled, cfg.BindPasswordRef = true, s.stored().BindPasswordRef
	if in.BindPassword != "" {
		if cfg.BindPasswordRef, err = s.secrets.PutRef(ctx, directory.SecretPath, directory.SecretKey, in.BindPassword); err != nil {
			return DirectoryView{}, fmt.Errorf("%w: %v", credentials.ErrUnavailable, err)
		}
	}
	var out DirectoryView
	s.st.Write(func(d *store.Data) {
		d.Settings.LDAP = cfg
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.ldap", Detail: "enabled " + cfg.Kind + " " + cfg.URL + passwordNote(in.BindPassword)})
		out = directoryView(d)
	})
	return out, nil
}

func (s *DirectoryService) disable(actor string) (DirectoryView, error) {
	var out DirectoryView
	var err error
	var signedOut []string
	s.st.Write(func(d *store.Data) {
		if !d.Settings.LDAP.Enabled {
			out = directoryView(d)
			return
		}
		if localAdmins(d) == 0 {
			err = ErrNoLocalAdmin
			return
		}
		d.Settings.LDAP.Enabled = false
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.ldap", Detail: "disabled"})
		out = directoryView(d)
		for id, u := range d.Users {
			if u.Source == model.SourceLDAP {
				signedOut = append(signedOut, id)
			}
		}
	})
	for _, id := range signedOut {
		s.sessions.DeleteUser(id, "")
	}
	return out, err
}

func (s *DirectoryService) bindPassword(cfg directory.Config, given string) (string, error) {
	if given != "" {
		return given, nil
	}
	stored := s.stored()
	if stored.BindPasswordRef == "" {
		return "", invalid("ldap_bind_password_required", nil)
	}
	if !sameAccount(stored, cfg) {
		return "", invalid("ldap_bind_password_required", errAccountChanged)
	}
	bind, err := s.secrets.Resolve(stored.BindPasswordRef)
	if err != nil {
		return "", fmt.Errorf("%w: %v", credentials.ErrUnavailable, err)
	}
	return bind, nil
}

func sameAccount(a, b directory.Config) bool {
	return strings.EqualFold(a.URL, b.URL) && strings.EqualFold(a.BindDN, b.BindDN)
}

func passwordNote(given string) string {
	if given == "" {
		return ""
	}
	return ", service account password replaced"
}

func directoryView(d *store.Data) DirectoryView {
	return DirectoryView{Config: d.Settings.LDAP.Public(), BindPasswordSet: d.Settings.LDAP.BindPasswordRef != "", LocalAdmins: localAdmins(d),
		Users: sourceUsers(d, model.SourceLDAP)}
}

func sourceUsers(d *store.Data, source string) DirectoryUsers {
	var out DirectoryUsers
	for _, u := range d.Users {
		if u.Source != source {
			continue
		}
		out.Total++
		if u.Role == model.RoleAdmin {
			out.Admins++
		}
		if u.Disabled {
			out.Disabled++
		}
		if u.LastLoginAt != nil && (out.LastSignIn == nil || u.LastLoginAt.After(*out.LastSignIn)) {
			at := *u.LastLoginAt
			out.LastSignIn = &at
		}
	}
	return out
}

func localAdmins(d *store.Data) int {
	n := 0
	for _, u := range d.Users {
		if u.Source == model.SourceLocal && u.Role == model.RoleAdmin && !u.Disabled && u.PasswordRef != "" {
			n++
		}
	}
	return n
}
