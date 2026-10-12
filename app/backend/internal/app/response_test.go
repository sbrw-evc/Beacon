package app_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/response"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func TestResponseSettingsAndIncidentActions(t *testing.T) {
	f := newConnFixture(t, true)
	f.h.st.Write(func(d *store.Data) {
		d.Teams["T-1"] = &model.Team{ID: "T-1", Name: "Payments SRE"}
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "pay-01", Status: model.CIStatusActive}
		d.Services["S-1"] = &model.Service{ID: "S-1", Name: "Payments", OwnerTeamID: "T-1", Criticality: model.CriticalityCritical, Status: model.ServiceActive, CIIDs: []string{"CI-1"}}
		for _, u := range d.Users {
			if u.Username == "admin" {
				u.Email, u.TeamIDs = "admin@example.com", []string{"T-1"}
				d.Teams["T-1"].LeadID = u.ID
			}
		}
	})
	var v app.ResponseView
	f.expect(f.admin, http.MethodGet, "/api/response", nil, http.StatusOK, &v)
	if v.Mode != model.ModeOff || len(v.Policies) != 5 || v.Impact.Matrix[model.ImpactExtensive][model.SeverityError] != model.SeverityCritical || v.Jira.Mode != model.ModeOff {
		t.Fatalf("defaults = %+v", v)
	}

	// The mode, the impact policy and the policies are checked.
	f.expect(f.admin, http.MethodPut, "/api/response", map[string]any{"mode": "loud"}, http.StatusBadRequest, nil)
	bad := []model.ResponsePolicy{{Priority: model.SeverityCritical, Steps: []model.EscalationStep{{Methods: []string{"pigeon"}}}}}
	f.expect(f.admin, http.MethodPut, "/api/response", map[string]any{"mode": "live", "policies": bad}, http.StatusBadRequest, nil)
	pols := v.Policies
	pols[0].Steps[0].UserIDs = []string{"USR-NOPE"}
	f.expect(f.admin, http.MethodPut, "/api/response", app.ResponsePolicyInput{Mode: model.ModeDryRun, Impact: v.Impact, Policies: pols}, http.StatusOK, &v)
	if v.Mode != model.ModeDryRun || v.ActiveSince == nil || len(v.Policies[0].Steps[0].UserIDs) != 0 {
		t.Fatalf("saved = %+v", v)
	}

	// Jira: live needs the site, the account, the project and the token; the token goes to OpenBao.
	jira := map[string]any{"mode": "live", "base_url": "https://corp.atlassian.net", "email": "bot@example.com", "project": "ops"}
	f.expect(f.admin, http.MethodPut, "/api/response/jira", jira, http.StatusBadRequest, nil)
	jira["base_url"] = "http://corp.atlassian.net"
	jira["token"] = "jira-token"
	f.expect(f.admin, http.MethodPut, "/api/response/jira", jira, http.StatusBadRequest, nil)
	jira["base_url"] = "https://corp.atlassian.net/"
	f.expect(f.admin, http.MethodPut, "/api/response/jira", jira, http.StatusOK, &v)
	if !v.Jira.HasToken || v.Jira.Project != "OPS" || v.Jira.BaseURL != "https://corp.atlassian.net" || v.Jira.TaskType != "Task" {
		t.Fatalf("jira = %+v", v.Jira)
	}
	if f.h.bao.Get("umbrella/response") == nil {
		t.Errorf("the token is in OpenBao: %v", f.h.bao.Paths())
	}
	delete(jira, "token")
	jira["mode"] = "dry_run"
	f.expect(f.admin, http.MethodPut, "/api/response/jira", jira, http.StatusOK, &v)
	if !v.Jira.HasToken || v.Jira.Mode != model.ModeDryRun {
		t.Fatalf("the token is kept: %+v", v.Jira)
	}
	f.expect(f.admin, http.MethodPut, "/api/response/graph", map[string]any{"mode": "live", "tenant_id": "corp"}, http.StatusBadRequest, nil)
	f.expect(f.admin, http.MethodPut, "/api/response/graph", map[string]any{"mode": "dry_run", "tenant_id": "corp.onmicrosoft.com", "client_id": "00000000-0000-0000-0000-000000000001",
		"client_secret": "s", "refresh_token": "r"}, http.StatusOK, &v)
	if !v.Graph.HasSecret || !v.Graph.HasRefresh || v.Graph.Mode != model.ModeDryRun {
		t.Fatalf("graph = %+v", v.Graph)
	}
	f.expect(f.admin, http.MethodPut, "/api/response/zoom", map[string]any{"mode": "dry_run", "account_id": "acc", "client_id": "cid", "client_secret": "z"}, http.StatusOK, &v)
	if !v.Zoom.HasSecret || v.Zoom.User != "me" {
		t.Fatalf("zoom = %+v", v.Zoom)
	}

	// The simulation of an incident of Payments: critical service, a RED high incident is P1.
	var sim app.SimulateView
	f.expect(f.admin, http.MethodPost, "/api/response/simulate", map[string]any{"severity": "error", "service_id": "S-1", "method": "red"}, http.StatusOK, &sim)
	if sim.Assessment.Priority != model.SeverityCritical || sim.Policy == nil || len(sim.Steps) != 3 || len(sim.Steps[0].People) != 1 || len(sim.Room) != 1 {
		t.Fatalf("simulation = %+v", sim)
	}
	f.expect(f.admin, http.MethodPost, "/api/response/simulate", map[string]any{"severity": "loud"}, http.StatusBadRequest, nil)

	// An incident: response assesses it in dry run and a person asks for the postmortem by hand.
	auth := f.webhookConnector()
	f.ingest("hook", `{"id":"a1","title":"5xx","host":"pay-01","signal":"http","severity":"error","status":"firing"}`, auth)
	var page struct {
		Alerts []alert.Alert `json:"alerts"`
	}
	waitFor(t, "the incident", func() bool {
		f.admin.call(http.MethodGet, "/api/incidents", nil, &page)
		return len(page.Alerts) == 1
	})
	id := page.Alerts[0].ID
	var ir struct {
		Mode  string          `json:"mode"`
		State *response.State `json:"state"`
	}
	f.expect(f.admin, http.MethodPost, "/api/incidents/"+id+"/response/assess", nil, http.StatusOK, &ir)
	if ir.State == nil || ir.State.Priority != model.SeverityCritical || ir.State.Task == nil || !ir.State.Task.DryRun || ir.State.Room == nil {
		t.Fatalf("state = %+v", ir.State)
	}
	f.expect(f.admin, http.MethodPost, "/api/incidents/"+id+"/response/fly", nil, http.StatusBadRequest, nil)
	f.expect(f.admin, http.MethodPost, "/api/incidents/"+id+"/response/postmortem", nil, http.StatusOK, &ir)
	f.expect(f.admin, http.MethodPost, "/api/incidents/"+id+"/resolve", map[string]any{}, http.StatusOK, nil)
	f.expect(f.admin, http.MethodPost, "/api/incidents/"+id+"/response/postmortem", nil, http.StatusOK, &ir)
	if ir.State.Postmortem == nil || ir.State.Postmortem.Key != "DRY-PM-"+id {
		t.Fatalf("postmortem = %+v", ir.State)
	}
	f.expect(f.admin, http.MethodGet, "/api/incidents/"+id+"/response", nil, http.StatusOK, &ir)
	if ir.Mode != model.ModeDryRun || ir.State == nil {
		t.Fatalf("get = %+v", ir)
	}
	f.expect(f.admin, http.MethodGet, "/api/incidents/INC-404/response", nil, http.StatusNotFound, nil)

	// Voice: the engine, the voices and the templates are checked; the key goes to OpenBao.
	if v.Voice.Mode != model.ModeOff || v.Voice.TTS.Provider != model.TTSPiper || v.Voice.DefaultTemplates["ru"] == "" || v.Voice.Repeat != 2 {
		t.Fatalf("voice defaults = %+v", v.Voice)
	}
	voice := map[string]any{"mode": "dry_run", "provider": "espeak", "url": "http://piper:5000", "repeat": 2}
	f.expect(f.admin, http.MethodPut, "/api/response/voice", voice, http.StatusBadRequest, nil)
	voice["provider"], voice["url"] = "openai", "ftp://tts"
	f.expect(f.admin, http.MethodPut, "/api/response/voice", voice, http.StatusBadRequest, nil)
	voice["url"], voice["voices"], voice["repeat"] = "http://tts:8000/", map[string]string{"ru": "bad voice!"}, 2
	f.expect(f.admin, http.MethodPut, "/api/response/voice", voice, http.StatusBadRequest, nil)
	voice["voices"], voice["api_key"] = map[string]string{"ru": "ru-anna", "en": "alloy"}, "tts-key"
	voice["templates"] = map[string]string{"ru": "Инцидент {number}: {title}", "en": model.DefaultVoiceTemplates["en"]}
	f.expect(f.admin, http.MethodPut, "/api/response/voice", voice, http.StatusOK, &v)
	if !v.Voice.HasKey || v.Voice.TTS.URL != "http://tts:8000" || v.Voice.Templates["ru"] != "Инцидент {number}: {title}" || v.Voice.Templates["en"] != "" {
		t.Fatalf("voice = %+v", v.Voice)
	}
	var text map[string]string
	f.expect(f.admin, http.MethodGet, "/api/response/voice/preview?format=text&locale=ru", nil, http.StatusOK, &text)
	if text["text"] != "Инцидент 1042: Рост ошибок HTTP 5xx на pay-01" {
		t.Fatalf("preview = %v", text)
	}
	// In dry run a call by hand is planned for the route (the admin, lead of the owning team).
	var withCalls struct {
		Voice string               `json:"voice"`
		Calls []response.VoiceCall `json:"calls"`
	}
	f.expect(f.admin, http.MethodPost, "/api/incidents/"+id+"/response/call", map[string]any{"via": "teams"}, http.StatusOK, &withCalls)
	if withCalls.Voice != model.ModeDryRun || len(withCalls.Calls) != 1 || withCalls.Calls[0].State != response.VoicePlanned || withCalls.Calls[0].Address != "admin@example.com" {
		t.Fatalf("calls = %+v", withCalls)
	}
	f.expect(f.admin, http.MethodPost, "/api/incidents/"+id+"/response/call", map[string]any{"via": "pigeon"}, http.StatusBadRequest, nil)
	// The audio and the call reports of Microsoft are reached by the token of a call only.
	anon := f.h.client()
	if got := anon.call(http.MethodGet, "/api/voice/audio/"+strings.Repeat("a", 48)+"/main.wav", nil, nil); got != http.StatusNotFound {
		t.Fatalf("audio of no call = %d", got)
	}
	if got := anon.call(http.MethodPost, "/api/voice/teams/short", map[string]any{}, nil); got != http.StatusNotFound {
		t.Fatalf("report with a bad token = %d", got)
	}
	// The interface language of a user is kept for voice.
	var me model.User
	f.expect(f.admin, http.MethodPut, "/api/auth/me/preferences", map[string]any{"locale": "en"}, http.StatusOK, &me)
	f.expect(f.admin, http.MethodPut, "/api/auth/me/preferences", map[string]any{"locale": "de"}, http.StatusBadRequest, nil)
	if me.Locale != "en" {
		t.Fatalf("locale = %q", me.Locale)
	}

	// A viewer sees neither the settings nor the actions.
	f.h.addLocal("viewer", "Viewer-pass-2026", model.RoleViewer, time.Now())
	viewer := f.h.client()
	viewer.login("viewer", "Viewer-pass-2026")
	if got := viewer.call(http.MethodPut, "/api/response", map[string]any{"mode": "off"}, nil); got != http.StatusForbidden {
		t.Fatalf("viewer PUT = %d", got)
	}
}
