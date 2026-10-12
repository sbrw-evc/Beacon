package app_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/notify/notifytest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func TestBackupNotificationRoundTrip(t *testing.T) {
	f := newConnFixture(t, true)
	smtp := notifytest.NewSMTP(t)
	tg := notifytest.NewTelegram(t)
	f.h.st.Write(func(d *store.Data) {
		d.Settings.Alerting.PublicURL = f.h.srv.URL
		d.Teams["T-1"] = &model.Team{ID: "T-1", Name: "DBA"}
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "db-01", Status: model.CIStatusActive}
		d.Services["S-1"] = &model.Service{ID: "S-1", Name: "Billing", OwnerTeamID: "T-1", Status: model.ServiceActive, CIIDs: []string{"CI-1"}}
		for _, u := range d.Users {
			if u.Username == "admin" {
				u.Email, u.TeamIDs = "admin@example.com", []string{"T-1"}
				d.Teams["T-1"].LeadID = u.ID
			}
		}
	})
	auth := f.webhookConnector()

	// The user sets the chat of their own.
	f.expect(f.admin, http.MethodPut, "/api/auth/me/preferences", map[string]any{"telegram": "bad chat"}, http.StatusBadRequest, nil)
	f.expect(f.admin, http.MethodPut, "/api/auth/me/preferences", map[string]any{"telegram": "4242"}, http.StatusOK, nil)

	var v app.NotifyView
	settings := map[string]any{
		"email":        map[string]any{"enabled": true, "host": smtp.Host(), "port": smtp.Port(), "security": "none", "username": "relay", "password": "smtp-pw", "from": "Umbrella <umbrella@example.com>"},
		"telegram":     map[string]any{"enabled": true, "token": tg.Token, "api_url": tg.URL()},
		"extra_emails": []string{"duty@example.com", ""}, "extra_telegram": []string{},
	}
	f.expect(f.admin, http.MethodPut, "/api/notifications", map[string]any{"email": map[string]any{"enabled": true}}, http.StatusBadRequest, nil)
	f.expect(f.admin, http.MethodPut, "/api/notifications", map[string]any{"extra_emails": []string{"not an address"}}, http.StatusBadRequest, nil)
	f.expect(f.admin, http.MethodPut, "/api/notifications", settings, http.StatusOK, &v)
	if !v.HasPassword || !v.HasToken || !v.Links || len(v.ExtraEmails) != 1 || v.Email.Username != "relay" {
		t.Fatalf("view = %+v", v)
	}
	if f.h.bao.Get("umbrella/notify") == nil {
		t.Errorf("secrets are in OpenBao: %v", f.h.bao.Paths())
	}
	// Saving without the secrets keeps them.
	delete(settings["email"].(map[string]any), "password")
	delete(settings["telegram"].(map[string]any), "token")
	f.expect(f.admin, http.MethodPut, "/api/notifications", settings, http.StatusOK, &v)
	if !v.HasPassword || !v.HasToken {
		t.Fatalf("kept = %+v", v)
	}
	// Delay and minimum severity of backup notification.
	if v.DelaySeconds != nil || v.MinSeverity != "error" || v.PDEnabled || v.AutoDelaySeconds != 0 {
		t.Fatalf("defaults = %+v", v)
	}
	settings["delay_seconds"] = -1
	f.expect(f.admin, http.MethodPut, "/api/notifications", settings, http.StatusBadRequest, nil)
	settings["delay_seconds"], settings["min_severity"] = 90, "loud"
	f.expect(f.admin, http.MethodPut, "/api/notifications", settings, http.StatusBadRequest, nil)
	settings["min_severity"] = "warning"
	f.expect(f.admin, http.MethodPut, "/api/notifications", settings, http.StatusOK, &v)
	if v.DelaySeconds == nil || *v.DelaySeconds != 90 || v.MinSeverity != "warning" {
		t.Fatalf("saved = %+v", v)
	}
	delete(settings, "delay_seconds")
	delete(settings, "min_severity")
	f.expect(f.admin, http.MethodPut, "/api/notifications", settings, http.StatusOK, &v)
	if v.DelaySeconds != nil || v.MinSeverity != "error" {
		t.Fatalf("automatic again = %+v", v)
	}

	var test map[string]any
	f.expect(f.admin, http.MethodPost, "/api/notifications/test", map[string]any{"channel": "email"}, http.StatusOK, &test)
	if test["to"] != "admin@example.com" || len(smtp.Mails()) != 1 || smtp.Mails()[0].Password != "smtp-pw" {
		t.Fatalf("email test: %v %+v", test, smtp.Mails())
	}
	f.expect(f.admin, http.MethodPost, "/api/notifications/test", map[string]any{"channel": "telegram"}, http.StatusOK, &test)
	if test["bot"] != "umbrella_test_bot" || len(tg.Messages()) != 1 || tg.Messages()[0].ChatID != "4242" {
		t.Fatalf("telegram test: %v %+v", test, tg.Messages())
	}

	// PagerDuty is off: it is not used (not a failure), and a critical incident goes to backup
	// notification at once, without waiting for PagerDuty.
	f.ingest("hook", `{"id":"a1","title":"Disk full","host":"db-01","signal":"disk","severity":"critical","status":"firing"}`, auth)
	var page struct {
		Alerts []alert.Alert `json:"alerts"`
		Counts alert.Counts  `json:"counts"`
	}
	waitFor(t, "the incident", func() bool {
		f.admin.call(http.MethodGet, "/api/incidents", nil, &page)
		return len(page.Alerts) == 1 && page.Alerts[0].PD.State == alert.PDOff
	})
	if page.Counts.PDEnabled || page.Counts.PDNotTaken != 0 || page.Alerts[0].PD.Error != "" {
		t.Errorf("PagerDuty off is not a failure: %+v %+v", page.Counts, page.Alerts[0].PD)
	}
	id := page.Alerts[0].ID
	waitFor(t, "backup notification", func() bool { return len(smtp.Mails()) == 3 && len(tg.Messages()) == 2 })
	var detail struct {
		Alert    alert.Alert   `json:"alert"`
		Timeline []alert.Entry `json:"timeline"`
	}
	waitFor(t, "the timeline", func() bool {
		f.admin.call(http.MethodGet, "/api/incidents/"+id, nil, &detail)
		n := 0
		for _, e := range detail.Timeline {
			if e.Code == "notify_sent" {
				n++
			}
		}
		return n == 2
	})
	if !detail.Alert.Fallback {
		t.Errorf("fallback = %+v", detail.Alert)
	}
	for _, e := range detail.Timeline {
		if e.Kind == alert.KindPagerDuty {
			t.Errorf("nothing about PagerDuty on the timeline while it is off: %+v", e)
		}
	}
	for _, m := range smtp.Mails()[1:] {
		if strings.Contains(m.Body, "PagerDuty") {
			t.Errorf("the message does not blame PagerDuty that is off: %s", m.Body)
		}
	}

	// The link in the mail acknowledges the incident in the name of the person.
	var body string
	for _, m := range smtp.Mails() {
		if m.To == "admin@example.com" && strings.Contains(m.Body, id) {
			body = m.Body
		}
	}
	i := strings.Index(body, f.h.srv.URL+"/ack/")
	if i < 0 {
		t.Fatalf("no link in %q", body)
	}
	link := body[i:]
	link = link[:strings.IndexByte(link, '\n')]
	get := func(method, url string) (int, string) {
		req, _ := http.NewRequest(method, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, page := get(http.MethodGet, link); code != http.StatusOK || !strings.Contains(page, "<form method=\"post\">") {
		t.Fatalf("GET %d %s", code, page)
	}
	f.admin.call(http.MethodGet, "/api/incidents/"+id, nil, &detail)
	if detail.Alert.Status != alert.StatusOpen {
		t.Fatal("opening the link acknowledged the incident")
	}
	if code, _ := get(http.MethodPost, link); code != http.StatusOK {
		t.Fatalf("POST %d", code)
	}
	f.admin.call(http.MethodGet, "/api/incidents/"+id, nil, &detail)
	if detail.Alert.Status != alert.StatusAcknowledged || detail.Alert.AckedBy != "admin" {
		t.Fatalf("acknowledged: %+v", detail.Alert)
	}
	// Everybody the backup notification reached learns who took the incident.
	waitFor(t, "the follow-up", func() bool { return len(smtp.Mails()) == 5 && len(tg.Messages()) == 3 })
	for _, m := range smtp.Mails()[3:] {
		if !strings.Contains(m.Body, id) || !strings.Contains(m.Body, "admin") || !strings.Contains(m.Body, f.h.srv.URL+"/incidents?id="+id) {
			t.Errorf("follow-up: %s", m.Body)
		}
	}
	if msg := tg.Messages()[2]; msg.ChatID != "4242" || !strings.Contains(msg.Text, "admin") {
		t.Errorf("telegram follow-up: %+v", msg)
	}
	if code, page := get(http.MethodGet, link); code != http.StatusOK || strings.Contains(page, "<form") {
		t.Fatalf("again: %d %s", code, page)
	}
	if code, _ := get(http.MethodPost, link[:len(link)-3]+"abc"); code != http.StatusForbidden {
		t.Fatalf("forged link: %d", code)
	}
}
