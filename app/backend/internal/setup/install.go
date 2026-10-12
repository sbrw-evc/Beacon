package setup

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/config"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/credentials"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type adminInput struct {
	model.Profile
	Username string `json:"username"`
	Password string `json:"password"`
}

type completeInput struct {
	Locale   string `json:"locale"`
	Theme    string `json:"theme"`
	Timezone string `json:"timezone"`
	// PublicURL is the address people reach Umbrella at (links in notifications, PagerDuty
	// webhooks); the wizard fills it from the browser address. Empty keeps what is stored.
	PublicURL string                `json:"public_url"`
	Policy    model.PasswordPolicy  `json:"password_policy"`
	OpenBao   config.OpenBao        `json:"openbao"`
	Postgres  pgInput               `json:"postgres"`
	LDAP      directory.TestRequest `json:"ldap"`
	Admin     adminInput            `json:"admin"`
}

type stepError struct {
	status     int
	code       string
	err        error
	violations []string
}

func (e *stepError) Error() string {
	if e.err == nil {
		return e.code
	}
	return e.code + ": " + e.err.Error()
}

func fail(status int, code string, err error) *stepError {
	return &stepError{status: status, code: code, err: err}
}

func (m *Module) apply(ctx context.Context, in completeInput) (Result, error) {
	var res Result
	if !model.ValidLocale(in.Locale) {
		return res, fail(http.StatusBadRequest, "invalid_locale", nil)
	}
	if !model.ValidTheme(in.Theme) {
		return res, fail(http.StatusBadRequest, "invalid_theme", nil)
	}
	if !model.ValidTimezone(in.Timezone) {
		return res, fail(http.StatusBadRequest, "invalid_timezone", nil)
	}
	publicURL, err := model.NormalizePublicURL(in.PublicURL)
	if err != nil {
		return res, fail(http.StatusBadRequest, "public_url_invalid", err)
	}
	policy, err := in.Policy.Normalize()
	if err != nil {
		return res, fail(http.StatusBadRequest, "invalid_password_policy", err)
	}
	admin, err := checkAdmin(in.Admin, policy)
	if err != nil {
		return res, err
	}

	ob, err := in.OpenBao.Normalize()
	if err != nil {
		return res, fail(http.StatusBadRequest, "openbao_invalid", err)
	}
	vault, rep := ob.Check(ctx)
	if !rep.OK {
		return res, fail(http.StatusBadRequest, "openbao_unavailable", errors.New(rep.Error))
	}

	ldapCfg := directory.Config{}
	if in.LDAP.Config.Enabled {
		ldapCfg, err = in.LDAP.Config.Normalize()
		if err != nil {
			return res, fail(http.StatusBadRequest, "ldap_invalid", err)
		}
		if _, err := directory.Test(ldapCfg, in.LDAP.BindPassword, "", ""); err != nil {
			return res, fail(http.StatusBadRequest, "ldap_unavailable", err)
		}
		ldapCfg.Enabled = true
	}

	pg, err := in.Postgres.Config().Normalize()
	if err != nil {
		return res, fail(http.StatusBadRequest, "postgres_invalid", err)
	}
	probe, err := store.ProbePostgres(ctx, pg)
	if err != nil {
		return res, fail(http.StatusBadRequest, "postgres_unavailable", err)
	}
	if probe.HasState && !in.Postgres.ReuseExisting {
		return res, fail(http.StatusConflict, "postgres_has_state", nil)
	}
	if !probe.CanCreate && !probe.HasState {
		return res, fail(http.StatusBadRequest, "postgres_no_create", nil)
	}

	pgRef := ""
	if pg.Password != "" {
		if pgRef, err = vault.PutRef(ctx, secretPostgres, "password", pg.Password); err != nil {
			return res, fail(http.StatusBadGateway, "openbao_write_failed", err)
		}
	} else if err := vault.Delete(ctx, secretPostgres); err != nil {
		return res, fail(http.StatusBadGateway, "openbao_write_failed", err)
	}
	if ldapCfg.Enabled {
		ref, err := vault.PutRef(ctx, directory.SecretPath, directory.SecretKey, in.LDAP.BindPassword)
		if err != nil {
			return res, fail(http.StatusBadGateway, "openbao_write_failed", err)
		}
		ldapCfg.BindPasswordRef = ref
	} else if err := vault.Delete(ctx, directory.SecretPath); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		slog.Warn("setup: old directory secret not removed", "err", err)
	}

	backend, err := store.OpenPostgres(ctx, pg)
	if err != nil {
		return res, fail(http.StatusBadGateway, "postgres_unavailable", err)
	}
	var adminID string
	st := store.New()
	if _, err := st.Attach(ctx, backend); err != nil {
		backend.Close()
		return res, fail(http.StatusConflict, "postgres_state_unreadable", err)
	}
	now := time.Now().UTC()
	st.Write(func(d *store.Data) {
		d.Settings.DefaultTheme = in.Theme
		d.Settings.DefaultLocale = in.Locale
		d.Settings.DefaultTZ = in.Timezone
		d.Settings.Password = policy
		if publicURL != "" {
			d.Settings.Alerting.PublicURL = publicURL
		}
		d.Settings.LDAP = ldapCfg
		d.Settings.SetupAt = now
		d.Settings.SetupBy = admin.Username
		// A new install wakes the on-call person in PagerDuty for errors and critical alerts only,
		// the same threshold backup notification has; settings saved before stay as they are.
		if pd := &d.Settings.Alerting.PagerDuty; pd.UpdatedAt == nil && pd.MinSeverity == "" {
			pd.MinSeverity = "error"
		}
		d.EnsureSystemRoles(now)
		d.EnsurePresetRoles(in.Locale, now)
		u := d.UserByName(admin.Username)
		if u == nil {
			u = &model.User{ID: d.NextID("USR"), CreatedAt: now}
			d.Users[u.ID] = u
		}
		u.Username, u.Profile, u.Name = admin.Username, admin.Profile, admin.Profile.DisplayName(admin.Username)
		u.Source, u.Role, u.Disabled = model.SourceLocal, model.RoleAdmin, false
		adminID = u.ID
		d.AddAudit(store.AuditEntry{At: now, Actor: admin.Username, Action: "setup.complete",
			Detail: "storage " + pg.Where() + ", directory " + onOff(ldapCfg.Enabled)})
	})
	passRef, err := credentials.NewPasswords(vault).Set(ctx, adminID, admin.Password)
	if err != nil {
		backend.Close()
		return res, fail(http.StatusBadGateway, "openbao_write_failed", err)
	}
	st.Write(func(d *store.Data) { d.Users[adminID].PasswordRef, d.Users[adminID].PasswordChangedAt = passRef, now })
	if err := st.Flush(); err != nil {
		backend.Close()
		return res, fail(http.StatusBadGateway, "postgres_write_failed", err)
	}

	stored := pg
	stored.Password = ""
	file := config.File{OpenBao: ob, Postgres: stored, PostgresPasswordRef: pgRef, CompletedAt: &now}
	if err := config.Write(m.opt.DataDir, file); err != nil {
		backend.Close()
		return res, fail(http.StatusInternalServerError, "config_write_failed", err)
	}
	return Result{Config: file, Vault: vault, Backend: backend, Store: st}, nil
}

type admin struct {
	Username string
	Profile  model.Profile
	Password string
}

func checkAdmin(in adminInput, policy model.PasswordPolicy) (admin, error) {
	a := admin{Username: strings.TrimSpace(in.Username)}
	if !usernameRe.MatchString(a.Username) {
		return a, fail(http.StatusBadRequest, "invalid_username", nil)
	}
	p, err := in.Profile.Normalize()
	if err != nil {
		return a, fail(http.StatusBadRequest, "invalid_name", err)
	}
	if !model.ValidEmail(p.Email) {
		return a, fail(http.StatusBadRequest, "invalid_email", nil)
	}
	a.Profile = p
	if err := policy.Validate(in.Password, a.Username); err != nil {
		var pe *model.PolicyError
		errors.As(err, &pe)
		return a, &stepError{status: http.StatusBadRequest, code: "weak_password", err: err, violations: pe.Violations}
	}
	a.Password = in.Password
	return a, nil
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}
