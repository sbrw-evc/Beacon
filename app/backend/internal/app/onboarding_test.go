package app_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func (f connFixture) onboarding(c *client) (app.Onboarding, map[string]app.OnboardingStep) {
	f.h.t.Helper()
	var out app.Onboarding
	f.expect(c, http.MethodGet, "/api/onboarding", nil, http.StatusOK, &out)
	steps := map[string]app.OnboardingStep{}
	for _, s := range out.Steps {
		steps[s.ID] = s
	}
	return out, steps
}

func TestOnboardingStepsFollowState(t *testing.T) {
	f := newConnFixture(t, true)
	now := time.Now()
	f.h.st.Write(func(d *store.Data) {
		d.EnsureSystemRoles(now)
		d.Roles["R-src"] = &model.Role{ID: "R-src", Name: "Sources", Permissions: []string{
			"monitoring:view", "connectors:view", "connectors:edit", "connectors:publish", "credentials:view", "credentials:edit"}}
	})
	f.h.addLocal("viewer", "Viewer-pass-2026", model.RoleUser, now)
	srcID := f.h.addLocal("sources", "Sources-pass-2026", "R-src", now)
	viewer := f.h.client()
	viewer.login("viewer", "Viewer-pass-2026")
	sources := f.h.client()
	sources.login("sources", "Sources-pass-2026")

	// A fresh installation: nothing is done; the administrator can fix everything.
	got, steps := f.onboarding(f.admin)
	if got.Done || len(got.Steps) != 6 {
		t.Fatalf("fresh = %+v", got)
	}
	order := []string{app.StepSource, app.StepEvent, app.StepTeam, app.StepCatalog, app.StepDelivery, app.StepPublicURL}
	for i, s := range got.Steps {
		if s.ID != order[i] || s.Done || !s.CanFix || s.Path == "" || s.Optional != (s.ID == app.StepCatalog) {
			t.Errorf("fresh step %d = %+v", i, s)
		}
	}
	if steps[app.StepSource].Path != "/monitoring?connect=1" || steps[app.StepTeam].Path != "/teams" || steps[app.StepPublicURL].Path != "/settings/alerting" {
		t.Errorf("paths = %+v", got.Steps)
	}

	// Any signed-in user sees the checklist; without permissions nothing is fixable, and quick
	// connect falls back to the connectors page.
	_, vs := f.onboarding(viewer)
	for _, s := range vs {
		if s.CanFix {
			t.Errorf("a user without permissions can fix %+v", s)
		}
	}
	if vs[app.StepSource].Path != "/connectors?connect=1" {
		t.Errorf("source path without monitoring:view = %q", vs[app.StepSource].Path)
	}
	_, ss := f.onboarding(sources)
	if !ss[app.StepSource].CanFix || !ss[app.StepEvent].CanFix || ss[app.StepTeam].CanFix || ss[app.StepDelivery].CanFix || ss[app.StepCatalog].CanFix {
		t.Errorf("source admin = %+v", ss)
	}

	// A published connector is a source; with one connector the event step opens it.
	auth := f.webhookConnector()
	_, steps = f.onboarding(f.admin)
	if !steps[app.StepSource].Done || steps[app.StepEvent].Done || !strings.HasPrefix(steps[app.StepEvent].Path, "/connectors/") {
		t.Fatalf("after publishing = %+v", steps)
	}
	if p := f.ingest("hook", `{"id":"a1","title":"Disk full","host":"db-01","signal":"disk","severity":"critical","status":"firing"}`, auth); p.status != http.StatusAccepted {
		t.Fatalf("ingest = %d %v", p.status, p.body)
	}
	waitFor(t, "the event step", func() bool {
		_, s := f.onboarding(f.admin)
		return s[app.StepEvent].Done
	})

	// A team counts only with an active member.
	f.h.st.Write(func(d *store.Data) {
		d.Teams["T-1"] = &model.Team{ID: "T-1", Name: "DBA"}
		d.Users[srcID].Disabled = true
		d.Users[srcID].TeamIDs = []string{"T-1"}
	})
	if _, s := f.onboarding(f.admin); s[app.StepTeam].Done {
		t.Fatal("a team with only a disabled member is not staffed")
	}
	f.h.st.Write(func(d *store.Data) { d.Users[srcID].Disabled = false })
	if _, s := f.onboarding(f.admin); !s[app.StepTeam].Done {
		t.Fatal("a team with a member is staffed")
	}

	// The catalog step needs a CI bound to a service owned by a team.
	f.h.st.Write(func(d *store.Data) {
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "db-01", Status: model.CIStatusActive}
		d.Services["S-1"] = &model.Service{ID: "S-1", Name: "Billing", Status: model.ServiceActive, CIIDs: []string{"CI-1"}}
	})
	if _, s := f.onboarding(f.admin); s[app.StepCatalog].Done {
		t.Fatal("a service without an owner team does not route")
	}
	f.h.st.Write(func(d *store.Data) { d.Services["S-1"].OwnerTeamID = "T-1" })
	if _, s := f.onboarding(f.admin); !s[app.StepCatalog].Done {
		t.Fatal("a bound CI of an owned service is a catalog")
	}

	// Delivery: e-mail without anybody to write to is not enough; a member's address is.
	f.h.st.Write(func(d *store.Data) {
		d.Settings.Alerting.Notify.Email = model.EmailChannel{Enabled: true, Host: "smtp.example.org", Port: 25, From: "umbrella@example.org"}
	})
	if _, s := f.onboarding(f.admin); s[app.StepDelivery].Done {
		t.Fatal("e-mail without recipients delivers nothing")
	}
	f.h.st.Write(func(d *store.Data) { d.Users[srcID].Email = "dba@example.org" })
	if _, s := f.onboarding(f.admin); !s[app.StepDelivery].Done {
		t.Fatal("e-mail to a team member delivers")
	}
	// PagerDuty with a key delivers on its own.
	f.h.st.Write(func(d *store.Data) {
		d.Users[srcID].Email = ""
		d.Settings.Alerting.Notify.Email.Enabled = false
		d.Settings.Alerting.PagerDuty = model.PagerDuty{Enabled: true}
	})
	if _, s := f.onboarding(f.admin); s[app.StepDelivery].Done {
		t.Fatal("PagerDuty without a key delivers nothing")
	}
	f.h.st.Write(func(d *store.Data) {
		d.Settings.Alerting.PagerDuty.RoutingKeyRef = "openbao://umbrella/pagerduty#routing_key"
	})
	got, steps = f.onboarding(f.admin)
	if !steps[app.StepDelivery].Done || got.Done {
		t.Fatalf("PagerDuty with a key = %+v, done %v", steps[app.StepDelivery], got.Done)
	}

	// The public address: its own setting, checked and kept by the PagerDuty form.
	var pub map[string]string
	if code := f.admin.call(http.MethodPut, "/api/settings/public-url", map[string]string{"public_url": "umbrella.example.org"}, &pub); code != http.StatusBadRequest || pub["error"] != "public_url_invalid" {
		t.Fatalf("bad public address = %d %v", code, pub)
	}
	if code := viewer.call(http.MethodPut, "/api/settings/public-url", map[string]string{"public_url": "https://x.example"}, nil); code != http.StatusForbidden {
		t.Fatalf("public address without settings.alerting:edit = %d", code)
	}
	f.expect(f.admin, http.MethodPut, "/api/settings/public-url", map[string]string{"public_url": "https://umbrella.example.org/"}, http.StatusOK, &pub)
	if pub["public_url"] != "https://umbrella.example.org" {
		t.Fatalf("saved = %v", pub)
	}
	f.expect(f.admin, http.MethodPut, "/api/pagerduty", map[string]any{"enabled": false}, http.StatusOK, nil)
	f.expect(f.admin, http.MethodGet, "/api/settings/public-url", nil, http.StatusOK, &pub)
	if pub["public_url"] != "https://umbrella.example.org" {
		t.Fatalf("saving PagerDuty without the address must keep it, got %v", pub)
	}
	// PagerDuty is off again: deliver by e-mail to a duty mailbox.
	f.h.st.Write(func(d *store.Data) {
		d.Settings.Alerting.Notify.Email.Enabled = true
		d.Settings.Alerting.Notify.ExtraEmails = []string{"duty@example.org"}
	})
	got, steps = f.onboarding(f.admin)
	if !got.Done || !steps[app.StepPublicURL].Done || !steps[app.StepDelivery].Done {
		t.Fatalf("everything set = %+v", got)
	}
	// The optional step does not hold the checklist open.
	f.h.st.Write(func(d *store.Data) { delete(d.Services, "S-1") })
	if got, steps = f.onboarding(f.admin); !got.Done || steps[app.StepCatalog].Done {
		t.Fatalf("without the catalog = %+v", got)
	}
}

func TestConnectorTokenFromDialog(t *testing.T) {
	f := newConnFixture(t, true)
	now := time.Now()
	f.h.st.Write(func(d *store.Data) {
		d.EnsureSystemRoles(now)
		d.Roles["R-cn"] = &model.Role{ID: "R-cn", Name: "Connectors only", Permissions: []string{"connectors:view", "connectors:edit"}}
	})
	f.h.addLocal("editor", "Editor-pass-2026", "R-cn", now)
	editor := f.h.client()
	editor.login("editor", "Editor-pass-2026")
	if code := editor.call(http.MethodPost, "/api/connectors/credentials/token", map[string]string{"name": "x"}, nil); code != http.StatusForbidden {
		t.Fatalf("token without credentials:edit = %d", code)
	}

	var made struct {
		Credential struct{ ID, Name, Type string } `json:"credential"`
		Token      string                          `json:"token"`
	}
	f.expect(f.admin, http.MethodPost, "/api/connectors/credentials/token", map[string]string{"name": " Zabbix token "}, http.StatusCreated, &made)
	if made.Credential.ID == "" || made.Credential.Name != "Zabbix token" || made.Credential.Type != flow.CredBearer || !strings.HasPrefix(made.Token, "umb_") || len(made.Token) < 40 {
		t.Fatalf("token = %+v", made)
	}
	if got := f.h.bao.Get("umbrella/credentials/" + made.Credential.ID); got["token"] != made.Token {
		t.Fatalf("the token is kept in OpenBao: %v", got)
	}
	var again struct{ Token string }
	f.expect(f.admin, http.MethodPost, "/api/connectors/credentials/token", nil, http.StatusCreated, &again)
	if again.Token == made.Token {
		t.Fatal("tokens must be random")
	}
	var choices []map[string]string
	f.expect(f.admin, http.MethodGet, "/api/connectors/credentials", nil, http.StatusOK, &choices)
	if len(choices) != 2 {
		t.Fatalf("choices = %v", choices)
	}

	// The connector takes the new credential and accepts exactly that token.
	var c app.ConnectorView
	f.expect(f.admin, http.MethodPost, "/api/connectors", map[string]any{"name": "Zabbix", "slug": "zbx", "preset": "webhook",
		"credentials": map[string]string{"webhook.credential": made.Credential.ID}}, http.StatusCreated, &c)
	f.expect(f.admin, http.MethodPost, "/api/connectors/"+c.ID+"/publish", map[string]any{}, http.StatusOK, nil)
	body := `{"id":"z1","title":"CPU","host":"web-01","signal":"cpu","severity":"warning","status":"firing"}`
	if p := f.ingest("zbx", body, map[string]string{"Authorization": "Bearer wrong", "Content-Type": "application/json"}); p.status != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d", p.status)
	}
	if p := f.ingest("zbx", body, map[string]string{"Authorization": "Bearer " + made.Token, "Content-Type": "application/json"}); p.status != http.StatusAccepted {
		t.Fatalf("generated token = %d %v", p.status, p.body)
	}
}
