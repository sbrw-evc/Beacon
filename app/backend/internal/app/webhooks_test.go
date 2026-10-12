package app_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	teamsHook = "https://example.webhook.office.com/webhookb2/abc@def/IncomingWebhook/123/secret-a1b2"
	zoomHook  = "https://integrations.zoom.us/chat/webhooks/incomingwebhook/secret-c3d4?format=full"
)

// Teams and Zoom settings: webhook URLs are https, Zoom needs its verification token, which is
// kept in OpenBao and never shown.
func TestTeamsZoomSettings(t *testing.T) {
	f := newConnFixture(t, false)
	var v app.NotifyView
	f.expect(f.admin, http.MethodGet, "/api/notifications", nil, http.StatusOK, &v)
	if v.Teams.Enabled || v.Zoom.Enabled || v.HasZoomToken || v.ExtraTeams == nil || v.ExtraZoom == nil {
		t.Fatalf("defaults = %+v", v)
	}
	settings := map[string]any{
		"teams":       map[string]any{"enabled": true},
		"zoom":        map[string]any{"enabled": true},
		"extra_teams": []string{teamsHook}, "extra_zoom": []string{zoomHook},
	}
	f.expect(f.admin, http.MethodPut, "/api/notifications", settings, http.StatusBadRequest, nil) // no Zoom token
	settings["zoom"] = map[string]any{"enabled": true, "token": "verify-me"}
	settings["extra_teams"] = []string{"http://example.webhook.office.com/x"}
	f.expect(f.admin, http.MethodPut, "/api/notifications", settings, http.StatusBadRequest, nil) // plain http
	settings["extra_teams"] = []string{teamsHook}
	settings["extra_zoom"] = []string{"not a url"}
	f.expect(f.admin, http.MethodPut, "/api/notifications", settings, http.StatusBadRequest, nil)
	settings["extra_zoom"] = []string{zoomHook}
	f.expect(f.admin, http.MethodPut, "/api/notifications", settings, http.StatusOK, &v)
	if !v.Teams.Enabled || !v.Zoom.Enabled || !v.HasZoomToken || len(v.ExtraTeams) != 1 || len(v.ExtraZoom) != 1 {
		t.Fatalf("saved = %+v", v)
	}
	var ref string
	f.h.st.Read(func(d *store.Data) { ref = d.Settings.Alerting.Notify.Zoom.TokenRef })
	if ref == "" || strings.Contains(ref, "verify-me") || f.h.bao.Get("umbrella/notify") == nil {
		t.Fatalf("the token is not in OpenBao: %q", ref)
	}
	// An empty token keeps the stored one.
	settings["zoom"] = map[string]any{"enabled": true}
	f.expect(f.admin, http.MethodPut, "/api/notifications", settings, http.StatusOK, &v)
	if !v.HasZoomToken {
		t.Fatalf("kept = %+v", v)
	}
	// A test goes to an https webhook only.
	f.expect(f.admin, http.MethodPost, "/api/notifications/test", map[string]any{"channel": "teams"}, http.StatusBadRequest, nil)
	f.expect(f.admin, http.MethodPost, "/api/notifications/test", map[string]any{"channel": "zoom", "to": "http://127.0.0.1/x"}, http.StatusBadRequest, nil)
}

// A team's own Teams and Zoom channels: https only, redacted in the audit and for people who
// cannot edit teams.
func TestTeamWebhookChannels(t *testing.T) {
	f := newConnFixture(t, false)
	var team app.TeamView
	f.expect(f.admin, http.MethodPost, "/api/teams", map[string]any{"name": "SRE", "teams": "http://example.com/x"}, http.StatusBadRequest, nil)
	f.expect(f.admin, http.MethodPost, "/api/teams", map[string]any{"name": "SRE", "zoom": "ftp://example.com/x"}, http.StatusBadRequest, nil)
	f.expect(f.admin, http.MethodPost, "/api/teams", map[string]any{"name": "SRE"}, http.StatusCreated, &team)
	f.expect(f.admin, http.MethodPut, "/api/teams/"+team.ID, map[string]any{"teams": teamsHook, "zoom": zoomHook}, http.StatusOK, &team)
	if team.Teams != teamsHook || team.Zoom != zoomHook {
		t.Fatalf("team = %+v", team)
	}
	f.h.st.Read(func(d *store.Data) {
		for _, e := range d.Audit {
			if strings.Contains(e.Detail, "secret-") {
				t.Errorf("the audit has a webhook URL: %s", e.Detail)
			}
		}
	})

	f.h.addRole("viewer", "teams:view")
	f.h.addLocal("viewer", "Viewer-pass-2026", "viewer", time.Now())
	viewer := f.h.client()
	viewer.login("viewer", "Viewer-pass-2026")
	var seen app.TeamView
	f.expect(viewer, http.MethodGet, "/api/teams/"+team.ID, nil, http.StatusOK, &seen)
	if seen.Teams != model.RedactURL(teamsHook) || strings.Contains(seen.Zoom, "secret") || seen.Zoom == "" {
		t.Fatalf("viewer sees %q %q", seen.Teams, seen.Zoom)
	}
	var all []app.TeamView
	f.expect(viewer, http.MethodGet, "/api/teams", nil, http.StatusOK, &all)
	if len(all) != 1 || strings.Contains(all[0].Teams, "secret") {
		t.Fatalf("viewer list: %+v", all)
	}
}
