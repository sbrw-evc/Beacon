package app

import (
	"fmt"
	"net/http"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// AlertPolicyView is the alerting policy of the settings: what is set (zero is the default),
// the defaults and the longest values.
type AlertPolicyView struct {
	Policy   model.AlertPolicy `json:"policy"`
	Defaults model.AlertPolicy `json:"defaults"`
	Limits   model.AlertPolicy `json:"limits"`
}

type AlertPolicyInput struct {
	ReopenWindowSeconds  int `json:"reopen_window_seconds"`
	FallbackDelaySeconds int `json:"fallback_delay_seconds"`
	FallbackRetrySeconds int `json:"fallback_retry_seconds"`
	RetentionDays        int `json:"retention_days"`
	TestLifetimeSeconds  int `json:"test_lifetime_seconds"`
}

func (a *App) registerAlertPolicy(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/alerting/policy", a.authed(a.can("settings.alerting:view", a.alertPolicyView)))
	mux.HandleFunc("PUT /api/alerting/policy", a.authed(a.can("settings.alerting:edit", a.alertPolicySave)))
}

func (a *App) alertPolicy() AlertPolicyView {
	out := AlertPolicyView{Defaults: alert.DefaultPolicy(), Limits: alert.PolicyLimits()}
	a.deps.Store.Read(func(d *store.Data) {
		if p := d.Settings.Alerting.Policy; p != nil {
			out.Policy = *p
		}
	})
	return out
}

func (a *App) alertPolicyView(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, a.alertPolicy())
}

// validAlertPolicy checks every value is from 0 (the default) up to its limit.
func validAlertPolicy(in AlertPolicyInput) error {
	lim := alert.PolicyLimits()
	for _, f := range []struct {
		code   string
		v, max int
	}{
		{"policy_window_invalid", in.ReopenWindowSeconds, lim.ReopenWindowSeconds},
		{"policy_delay_invalid", in.FallbackDelaySeconds, lim.FallbackDelaySeconds},
		{"policy_retry_invalid", in.FallbackRetrySeconds, lim.FallbackRetrySeconds},
		{"policy_retention_invalid", in.RetentionDays, lim.RetentionDays},
		{"policy_test_invalid", in.TestLifetimeSeconds, lim.TestLifetimeSeconds},
	} {
		if f.v < 0 || f.v > f.max {
			return invalid(f.code, fmt.Errorf("the value must be from 0 to %d", f.max))
		}
	}
	return nil
}

func (a *App) alertPolicySave(w http.ResponseWriter, r *http.Request) {
	var in AlertPolicyInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	if err := validAlertPolicy(in); err != nil {
		writeError(w, err)
		return
	}
	now := time.Now().UTC()
	actor := current(r).user.Username
	p := model.AlertPolicy{ReopenWindowSeconds: in.ReopenWindowSeconds, FallbackDelaySeconds: in.FallbackDelaySeconds,
		FallbackRetrySeconds: in.FallbackRetrySeconds, RetentionDays: in.RetentionDays, TestLifetimeSeconds: in.TestLifetimeSeconds,
		UpdatedAt: &now, UpdatedBy: actor}
	a.deps.Store.Write(func(d *store.Data) {
		d.Settings.Alerting.Policy = &p
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.alert_policy", Detail: fmt.Sprintf("window %ds, delay %ds, retry %ds, retention %dd, test %ds",
			p.ReopenWindowSeconds, p.FallbackDelaySeconds, p.FallbackRetrySeconds, p.RetentionDays, p.TestLifetimeSeconds)})
	})
	httpx.JSON(w, http.StatusOK, a.alertPolicy())
}
