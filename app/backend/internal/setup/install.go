package setup

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/config"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/spa"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type completeInput struct {
	Locale   string `json:"locale"`
	Theme    string `json:"theme"`
	Timezone string `json:"timezone"`
	// PublicURL is the address people reach Umbrella at (links in notifications, PagerDuty
	// webhooks); the wizard fills it from the browser address. Empty keeps what is stored.
	PublicURL string         `json:"public_url"`
	OpenBao   config.OpenBao `json:"openbao"`
	Postgres  pgInput        `json:"postgres"`
	// SPAKey is the connection key SPA shows for Umbrella (optional: it can also come from
	// SPA_CONNECTION_KEY). Nobody signs in to Umbrella before it is connected to SPA; the first
	// administrator is whoever SPA gives the role admin.
	SPAKey string `json:"spa_connection_key"`
}

type stepError struct {
	status int
	code   string
	err    error
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
	var spaKey string
	if strings.TrimSpace(in.SPAKey) != "" {
		k, err := spa.ParseKey(in.SPAKey)
		if err != nil {
			return res, fail(http.StatusBadRequest, "spa_key_invalid", err)
		}
		spaKey = k.String()
	}

	ob, err := in.OpenBao.Normalize()
	if err != nil {
		return res, fail(http.StatusBadRequest, "openbao_invalid", err)
	}
	vault, rep := ob.Check(ctx)
	if !rep.OK {
		return res, fail(http.StatusBadRequest, "openbao_unavailable", errors.New(rep.Error))
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
	if spaKey != "" {
		if _, err := vault.PutRef(ctx, spa.SecretPath, spa.SecretKey, spaKey); err != nil {
			return res, fail(http.StatusBadGateway, "openbao_write_failed", err)
		}
	}

	backend, err := store.OpenPostgres(ctx, pg)
	if err != nil {
		return res, fail(http.StatusBadGateway, "postgres_unavailable", err)
	}
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
		if publicURL != "" {
			d.Settings.Alerting.PublicURL = publicURL
		}
		d.Settings.SetupAt = now
		d.Settings.SetupBy = "setup"
		// A new install wakes the on-call person in PagerDuty for errors and critical alerts only,
		// the same threshold backup notification has; settings saved before stay as they are.
		if pd := &d.Settings.Alerting.PagerDuty; pd.UpdatedAt == nil && pd.MinSeverity == "" {
			pd.MinSeverity = "error"
		}
		d.EnsureSystemRoles(now)
		d.EnsurePresetRoles(in.Locale, now)
		d.AddAudit(store.AuditEntry{At: now, Actor: "setup", Action: "setup.complete",
			Detail: "storage " + pg.Where() + ", SPA key " + onOff(spaKey != "")})
	})
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

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}
