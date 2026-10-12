package response_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/notify"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/notify/notifytest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/response"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store/storetest"
)

// call is a request one of the fake APIs took.
type call struct {
	Method, Path, Auth string
	Body               map[string]any
}

// fakeAPI plays Jira Cloud, Microsoft Graph (with its sign-in) and Zoom at once.
type fakeAPI struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []call
	seq   int
	// failIssue makes issue creation fail with 500 this many times.
	failIssue int
	// noPriority makes Jira refuse the priority field, as a project without it on its screen does.
	noPriority bool
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	c := call{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization")}
	if r.Header.Get("Content-Type") == "application/json" {
		_ = json.Unmarshal(raw, &c.Body)
	} else if len(raw) > 0 {
		c.Body = map[string]any{"form": string(raw)}
	}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.seq++
	n := f.seq
	fail := false
	if r.URL.Path == "/rest/api/3/issue" && f.failIssue > 0 {
		f.failIssue--
		fail = true
	}
	noPrio := f.noPriority
	f.mu.Unlock()
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	switch p := r.URL.Path; {
	case strings.HasSuffix(p, "/oauth2/v2.0/token"):
		reply(200, map[string]any{"access_token": "graph-access", "refresh_token": fmt.Sprintf("rotated-%d", n), "expires_in": 3600})
	case p == "/oauth/token":
		reply(200, map[string]any{"access_token": "zoom-access", "expires_in": 3600})
	case p == "/me":
		reply(200, map[string]any{"id": "svc-id", "userPrincipalName": "umbrella@corp.example", "displayName": "Umbrella"})
	case strings.HasPrefix(p, "/users/") && strings.Contains(p, "@") && r.Method == http.MethodGet && !strings.HasSuffix(p, "/meetings"):
		upn := strings.TrimPrefix(p, "/users/")
		if strings.HasPrefix(upn, "nobody") {
			reply(404, map[string]any{"error": map[string]string{"code": "Request_ResourceNotFound", "message": "not found"}})
			return
		}
		reply(200, map[string]any{"id": "id-" + upn, "userPrincipalName": upn, "email": upn})
	case p == "/chats":
		reply(201, map[string]any{"id": "19:chat-1", "webUrl": "https://teams.example/l/chat/19:chat-1"})
	case strings.HasPrefix(p, "/chats/"):
		reply(201, map[string]any{"id": "m1"})
	case p == "/communications/calls":
		reply(201, map[string]any{"id": fmt.Sprintf("call-%d", n)})
	case p == "/me/onlineMeetings":
		reply(201, map[string]any{"id": "meet-1", "joinWebUrl": "https://teams.example/l/meetup-join/1"})
	case strings.HasSuffix(p, "/meetings"):
		reply(201, map[string]any{"id": 8123, "join_url": "https://zoom.example/j/8123"})
	case p == "/rest/api/3/myself":
		reply(200, map[string]any{"displayName": "Umbrella bot"})
	case strings.HasPrefix(p, "/rest/api/3/project/"):
		reply(200, map[string]any{"name": "Operations"})
	case p == "/rest/api/3/issue":
		if fail {
			reply(500, map[string]any{"errorMessages": []string{"try later"}})
			return
		}
		fields, _ := c.Body["fields"].(map[string]any)
		if _, ok := fields["priority"]; ok && noPrio {
			reply(400, map[string]any{"errors": map[string]string{"priority": "Field 'priority' cannot be set."}})
			return
		}
		reply(201, map[string]any{"id": fmt.Sprint(n), "key": fmt.Sprintf("OPS-%d", n)})
	case strings.HasSuffix(p, "/transitions") && r.Method == http.MethodGet:
		reply(200, map[string]any{"transitions": []map[string]any{{"id": "31", "name": "Done", "to": map[string]string{"name": "Done"}}}})
	default:
		reply(201, map[string]any{})
	}
}

func (f *fakeAPI) URL() string { return f.srv.URL }

func (f *fakeAPI) take(match func(call) bool) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if match(c) {
			out = append(out, c)
		}
	}
	return out
}

func path(p string) func(call) bool {
	return func(c call) bool { return c.Path == p && c.Method != http.MethodGet }
}

// secrets is a fake OpenBao.
type secrets struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *secrets) Resolve(ref string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[ref]
	if !ok {
		return "", errors.New("no such secret")
	}
	return v, nil
}

func (s *secrets) PutRef(_ context.Context, path, key, value string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref := path + "/" + key
	s.m[ref] = value
	return ref, nil
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type env struct {
	e   *alert.Engine
	r   *response.Service
	st  *store.Store
	api *fakeAPI
	tg  *notifytest.Telegram
	clk *clock
	sec *secrets
}

func setup(t *testing.T, mode string) *env {
	t.Helper()
	ctx := context.Background()
	cfg := storetest.TempDatabase(t)
	db, err := pgxpool.New(ctx, cfg.DSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := alert.EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := response.EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := response.EnsureSchema(ctx, db); err != nil {
		t.Fatalf("the schema is created idempotently: %v", err)
	}
	api := newFakeAPI(t)
	tg := notifytest.NewTelegram(t)
	sec := &secrets{m: map[string]string{"tg": "123:bot-token", "jira": "jira-token", "gs": "graph-secret", "gr": "graph-refresh", "zs": "zoom-secret"}}
	st := store.New()
	st.Write(func(d *store.Data) {
		d.Users["U-1"] = &model.User{ID: "U-1", Username: "lead", Name: "Lead One", TeamIDs: []string{"T-2"}, Profile: model.Profile{Email: "lead@corp.example"}, Telegram: "1001"}
		d.Users["U-2"] = &model.User{ID: "U-2", Username: "eng", Name: "Engineer Two", TeamIDs: []string{"T-2"}, Profile: model.Profile{Email: "eng@corp.example"}, Telegram: "1002"}
		d.Users["U-3"] = &model.User{ID: "U-3", Username: "boss", Name: "Boss Three", TeamIDs: []string{"T-1"}, Profile: model.Profile{Email: "boss@corp.example"}, Telegram: "1003"}
		d.Users["U-4"] = &model.User{ID: "U-4", Username: "bank", Name: "Banking Lead", TeamIDs: []string{"T-3"}, Profile: model.Profile{Email: "nobody@corp.example"}, Telegram: "1004"}
		d.Teams["T-1"] = &model.Team{ID: "T-1", Name: "Platform", LeadID: "U-3"}
		d.Teams["T-2"] = &model.Team{ID: "T-2", Name: "Payments SRE", ParentID: "T-1", LeadID: "U-1"}
		d.Teams["T-3"] = &model.Team{ID: "T-3", Name: "Banking", LeadID: "U-4"}
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "pay-01", Kind: model.CIKindVM, Status: model.CIStatusActive}
		d.ConfigItems["CI-9"] = &model.ConfigItem{ID: "CI-9", Name: "wiki-01", Kind: model.CIKindVM, Status: model.CIStatusActive}
		d.Services["S-1"] = &model.Service{ID: "S-1", Name: "Payments", OwnerTeamID: "T-2", Criticality: model.CriticalityHigh, Status: model.ServiceActive, CIIDs: []string{"CI-1"}}
		d.Services["S-2"] = &model.Service{ID: "S-2", Name: "Online banking", OwnerTeamID: "T-3", Criticality: model.CriticalityCritical, Status: model.ServiceActive, DependsOn: []string{"S-1"}}
		d.Services["S-9"] = &model.Service{ID: "S-9", Name: "Wiki", OwnerTeamID: "T-2", Criticality: model.CriticalityLow, Status: model.ServiceActive, CIIDs: []string{"CI-9"}}
		d.Settings.DefaultLocale = "en"
		d.Settings.Alerting.PublicURL = "https://umbrella.example"
		d.Settings.Alerting.Notify.Telegram = model.TelegramChannel{Enabled: true, TokenRef: "tg", APIURL: tg.URL()}
		// Backup notification waits long, so only response messages are seen here.
		delay := 3600
		d.Settings.Alerting.Notify.DelaySeconds = &delay
		d.Settings.Response = model.Response{Mode: mode, Impact: model.DefaultImpactPolicy(), Policies: model.DefaultResponsePolicies(),
			Jira: model.JiraSettings{Mode: model.ModeLive, BaseURL: api.URL(), Email: "bot@corp.example", TokenRef: "jira", Project: "OPS", TaskType: "Task", PostmortemType: "Postmortem",
				Priorities: model.DefaultJira().Priorities, Labels: []string{"umbrella"}, LinkType: "Relates", DoneTransition: "Done"},
			Graph:   model.GraphSettings{Mode: model.ModeLive, TenantID: "corp", ClientID: "app", ClientSecretRef: "gs", RefreshTokenRef: "gr", LoginURL: api.URL(), GraphURL: api.URL()},
			ZoomAPI: model.ZoomAPISettings{Mode: model.ModeLive, AccountID: "acc", ClientID: "zc", ClientSecretRef: "zs", User: "me", OAuthURL: api.URL(), APIURL: api.URL()},
		}
	})
	clk := &clock{t: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)}
	e := alert.New(db, st)
	e.SetClock(clk.now)
	n := notify.New(st, sec)
	n.Backoff = time.Millisecond
	n.SetLinks(notify.NewLinks([]byte("0123456789abcdef0123456789abcdef")))
	r := response.New(db, st, e, n, sec)
	r.SetClock(clk.now)
	return &env{e: e, r: r, st: st, api: api, tg: tg, clk: clk, sec: sec}
}

func (v *env) fire(t *testing.T, ci, sev, method, status string) alert.Alert {
	t.Helper()
	in := alert.Incoming{ConnectorID: "CON-1", Key: ci + "-k", Title: "HTTP 5xx on " + ci, CI: ci, Signal: "http.errors", Method: method, Severity: sev, Status: status}
	if err := v.e.Ingest(context.Background(), []alert.Incoming{in}); err != nil {
		t.Fatal(err)
	}
	p, err := v.e.List(context.Background(), alert.Filter{Status: "all"})
	if err != nil || len(p.Alerts) == 0 {
		t.Fatalf("no alert: %v", err)
	}
	return p.Alerts[0]
}

func (v *env) tick(t *testing.T) *response.State {
	t.Helper()
	if err := v.r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	return nil
}

func (v *env) state(t *testing.T, id string) *response.State {
	t.Helper()
	st, err := v.r.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func codes(t *testing.T, e *alert.Engine, id string) []string {
	t.Helper()
	_, entries, err := e.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, en := range entries {
		if en.Kind == response.KindResponse {
			out = append(out, en.Code)
		}
	}
	return out
}

func count(list []string, v string) int {
	n := 0
	for _, x := range list {
		if x == v {
			n++
		}
	}
	return n
}

func TestResponseLifecycle(t *testing.T) {
	ctx := context.Background()
	v := setup(t, model.ModeLive)
	// A high RED alert on Payments: Online banking (critical) depends on it, so impact is
	// extensive and the response priority P1, above the severity of the events.
	a := v.fire(t, "pay-01", model.SeverityError, model.MethodRED, alert.SourceFiring)
	v.tick(t)
	st := v.state(t, a.ID)
	if st == nil || st.Priority != model.SeverityCritical || st.Assessment.Impact != model.ImpactExtensive || st.Assessment.Urgency != model.SeverityError {
		t.Fatalf("state = %+v", st)
	}
	if st.Bridge == nil || st.Bridge.URL != "https://teams.example/l/meetup-join/1" || st.Task == nil || st.Task.Key == "" || st.Room == nil || st.Room.ID != "19:chat-1" {
		t.Fatalf("bridge %+v task %+v room %+v", st.Bridge, st.Task, st.Room)
	}
	// The chat has the route (lead and engineer) and the owners of the affected services; the
	// banking lead has no Microsoft account and is left out.
	chats := v.api.take(path("/chats"))
	if len(chats) != 1 {
		t.Fatalf("chats = %+v", chats)
	}
	members := chats[0].Body["members"].([]any)
	if len(members) != 3 || !strings.Contains(chats[0].Body["topic"].(string), "P1 "+a.ID) {
		t.Fatalf("chat = %+v", chats[0].Body)
	}
	if st.Room.Members[0] != "eng@corp.example" || len(st.Room.Members) != 2 {
		t.Fatalf("room members = %v", st.Room.Members)
	}
	// The first message of the chat is the summary of the incident.
	posts := v.api.take(path("/chats/19:chat-1/messages"))
	if len(posts) == 0 || !strings.Contains(fmt.Sprint(posts[0].Body), "Online banking") {
		t.Fatalf("posts = %+v", posts)
	}
	// The Jira task: basic authentication, the P1 priority, the labels and the summary.
	issues := v.api.take(path("/rest/api/3/issue"))
	if len(issues) != 1 || !strings.HasPrefix(issues[0].Auth, "Basic ") {
		t.Fatalf("issues = %+v", issues)
	}
	fields := issues[0].Body["fields"].(map[string]any)
	if fields["priority"].(map[string]any)["name"] != "Highest" || fields["summary"] != "[P1] "+a.ID+": HTTP 5xx on pay-01" || fields["project"].(map[string]any)["key"] != "OPS" {
		t.Fatalf("fields = %+v", fields)
	}
	// Step 1 reached the route by Telegram with the chat, call and Jira links.
	msgs := v.tg.Messages()
	if len(msgs) != 2 {
		t.Fatalf("telegram = %+v steps %+v", msgs, st.Steps)
	}
	for _, m := range msgs {
		if !strings.Contains(m.Text, "teams.example/l/chat") || !strings.Contains(m.Text, "meetup-join") || !strings.Contains(m.Text, st.Task.Key) || !strings.Contains(m.Text, "/ack/") {
			t.Fatalf("message = %s", m.Text)
		}
	}
	if len(st.Steps) != 1 || st.Steps[0].Index != 0 {
		t.Fatalf("steps = %+v", st.Steps)
	}

	// Nothing new within the step delay.
	v.clk.advance(5 * time.Minute)
	v.tick(t)
	if n := len(v.tg.Messages()); n != 2 {
		t.Fatalf("no step is due yet, got %d messages", n)
	}
	// After 10 minutes the lead and the service owners are called in; the banking lead gets Telegram.
	v.clk.advance(5 * time.Minute)
	v.tick(t)
	st = v.state(t, a.ID)
	if len(st.Steps) != 2 {
		t.Fatalf("steps = %+v", st.Steps)
	}
	var toBank bool
	for _, m := range v.tg.Messages()[2:] {
		if m.ChatID == "1004" && strings.Contains(m.Text, "Escalation level 2") {
			toBank = true
		}
	}
	if !toBank {
		t.Fatalf("escalation not sent to the banking lead: %+v", v.tg.Messages())
	}

	// Acknowledged: the chat and the task hear about it. P1 escalates until resolved.
	if _, err := v.e.Act(ctx, a.ID, "ack", "lead", ""); err != nil {
		t.Fatal(err)
	}
	v.tick(t)
	if c := v.api.take(path("/rest/api/3/issue/" + st.Task.Key + "/comment")); len(c) == 0 || !strings.Contains(fmt.Sprint(c[len(c)-1].Body), "acknowledged") {
		t.Fatalf("comments = %+v", c)
	}
	v.clk.advance(20 * time.Minute)
	v.tick(t)
	if st = v.state(t, a.ID); len(st.Steps) != 3 {
		t.Fatalf("P1 keeps escalating after acknowledgement: %+v", st.Steps)
	}

	// Resolved: a postmortem due in 5 days, linked to the task, the task moved to Done.
	if _, err := v.e.Act(ctx, a.ID, "resolve", "lead", ""); err != nil {
		t.Fatal(err)
	}
	v.tick(t)
	st = v.state(t, a.ID)
	if st.Postmortem == nil || !st.Finished || !st.Transitioned {
		t.Fatalf("state after resolve = %+v", st)
	}
	issues = v.api.take(path("/rest/api/3/issue"))
	pm := issues[len(issues)-1].Body["fields"].(map[string]any)
	if pm["issuetype"].(map[string]any)["name"] != "Postmortem" || pm["duedate"] != "2026-10-12" || !strings.Contains(fmt.Sprint(pm["description"]), "Timeline") {
		t.Fatalf("postmortem = %+v", pm)
	}
	if l := v.api.take(path("/rest/api/3/issueLink")); len(l) != 1 {
		t.Fatalf("links = %+v", l)
	}
	if tr := v.api.take(path("/rest/api/3/issue/" + st.Task.Key + "/transitions")); len(tr) != 1 {
		t.Fatalf("transitions = %+v", tr)
	}
	// The refresh token Microsoft rotated is stored.
	var ref string
	v.st.Read(func(d *store.Data) { ref = d.Settings.Response.Graph.RefreshTokenRef })
	if ref != "response/graph_refresh_token" {
		t.Fatalf("rotated refresh token not stored")
	}

	// A finished response is not touched again.
	before := len(v.api.take(func(call) bool { return true }))
	v.clk.advance(time.Hour)
	v.tick(t)
	if after := len(v.api.take(func(call) bool { return true })); after != before {
		t.Fatalf("finished response called the APIs again: %d -> %d", before, after)
	}
	cs := codes(t, v.e, a.ID)
	for _, c := range []string{"response_assessed", "response_bridge", "response_jira_task", "response_room", "response_jira_postmortem", "response_jira_transition"} {
		if count(cs, c) != 1 {
			t.Errorf("timeline has %d %s: %v", count(cs, c), c, cs)
		}
	}
	if count(cs, "response_step") != 3 {
		t.Errorf("timeline steps: %v", cs)
	}
}

func TestResponseStopsOnAckAndRetries(t *testing.T) {
	v := setup(t, model.ModeLive)
	v.api.failIssue = 1
	v.api.noPriority = true
	// A medium USE alert on the low Wiki: minor × medium = P4, which only mails the route.
	// Make P4 escalate by Telegram and create a task to see the retry and stop-on-ack.
	v.st.Write(func(d *store.Data) {
		for i := range d.Settings.Response.Policies {
			if d.Settings.Response.Policies[i].Priority == model.SeverityLow {
				d.Settings.Response.Policies[i].Steps = []model.EscalationStep{
					{AfterMinutes: 0, Targets: []string{model.TargetRoute}, Methods: []string{model.CommTelegram}},
					{AfterMinutes: 5, Targets: []string{model.TargetParentLead}, Methods: []string{model.CommTelegram}},
				}
				d.Settings.Response.Policies[i].Jira.Task = true
			}
		}
	})
	a := v.fire(t, "wiki-01", model.SeverityWarning, model.MethodUSE, alert.SourceFiring)
	v.tick(t)
	st := v.state(t, a.ID)
	if st.Priority != model.SeverityLow || st.Room != nil || st.Bridge != nil || st.Task != nil || st.Failures["task"] == nil {
		t.Fatalf("state = %+v", st)
	}
	// The task is tried again after the retry delay, without the priority Jira refuses.
	v.clk.advance(3 * time.Minute)
	if _, err := v.e.Act(context.Background(), a.ID, "ack", "eng", ""); err != nil {
		t.Fatal(err)
	}
	v.tick(t)
	st = v.state(t, a.ID)
	if st.Task == nil || len(st.Failures) != 0 {
		t.Fatalf("task not retried: %+v", st)
	}
	// Acknowledged before step 2: P4 stops escalating.
	v.clk.advance(10 * time.Minute)
	v.tick(t)
	if st = v.state(t, a.ID); len(st.Steps) != 1 {
		t.Fatalf("steps after ack = %+v", st.Steps)
	}
	if n := len(v.tg.Messages()); n != 2 {
		t.Fatalf("telegram = %d", n)
	}
	cs := codes(t, v.e, a.ID)
	if count(cs, "response_failed") != 1 {
		t.Fatalf("timeline = %v", cs)
	}
}

func TestResponseDryRunSendsNothing(t *testing.T) {
	v := setup(t, model.ModeDryRun)
	a := v.fire(t, "pay-01", model.SeverityCritical, model.MethodRED, alert.SourceFiring)
	v.tick(t)
	st := v.state(t, a.ID)
	if st.Priority != model.SeverityCritical || st.Room == nil || !st.Room.DryRun || st.Task == nil || !st.Task.DryRun || st.Bridge == nil || !st.Bridge.DryRun {
		t.Fatalf("state = %+v", st)
	}
	if len(st.Steps) != 1 || !st.Steps[0].DryRun || len(st.Steps[0].Reached) == 0 {
		t.Fatalf("steps = %+v", st.Steps)
	}
	if calls := v.api.take(func(call) bool { return true }); len(calls) != 0 {
		t.Fatalf("dry run called the APIs: %+v", calls)
	}
	if n := len(v.tg.Messages()); n != 0 {
		t.Fatalf("dry run sent %d messages", n)
	}
}

func TestResponseOffAndOldIncidents(t *testing.T) {
	v := setup(t, model.ModeOff)
	a := v.fire(t, "pay-01", model.SeverityCritical, model.MethodRED, alert.SourceFiring)
	v.tick(t)
	if st := v.state(t, a.ID); st != nil {
		t.Fatalf("response is off, got %+v", st)
	}
	if _, err := v.r.Force(context.Background(), a.ID, response.ActionRoom, "admin"); !errors.Is(err, response.ErrOff) {
		t.Fatalf("force while off: %v", err)
	}
	// Turned on later: the incident opened before is left alone.
	since := v.clk.now().Add(time.Minute)
	v.st.Write(func(d *store.Data) {
		d.Settings.Response.Mode = model.ModeLive
		d.Settings.Response.ActiveSince = &since
	})
	v.clk.advance(2 * time.Minute)
	v.tick(t)
	if st := v.state(t, a.ID); st != nil {
		t.Fatalf("an old incident is handled: %+v", st)
	}
	// By hand it is.
	st, err := v.r.Force(context.Background(), a.ID, response.ActionTask, "admin")
	if err != nil || st.Task == nil {
		t.Fatalf("force task: %v %+v", err, st)
	}
}

func TestIntegrationChecks(t *testing.T) {
	v := setup(t, model.ModeLive)
	ctx := context.Background()
	if info, err := v.r.TestJira(ctx); err != nil || info != "Umbrella bot · Operations" {
		t.Fatalf("jira: %q %v", info, err)
	}
	if info, err := v.r.TestGraph(ctx); err != nil || info != "umbrella@corp.example" {
		t.Fatalf("graph: %q %v", info, err)
	}
	if _, err := v.r.TestZoom(ctx); err != nil {
		t.Fatalf("zoom: %v", err)
	}
	// A Zoom bridge.
	v.st.Write(func(d *store.Data) { d.Settings.Response.Policies[0].Bridge = "zoom" })
	a := v.fire(t, "pay-01", model.SeverityCritical, model.MethodRED, alert.SourceFiring)
	v.tick(t)
	if st := v.state(t, a.ID); st.Bridge == nil || st.Bridge.Provider != "zoom" || st.Bridge.URL != "https://zoom.example/j/8123" {
		t.Fatalf("zoom bridge = %+v", st.Bridge)
	}
	if m := v.api.take(path("/users/me/meetings")); len(m) != 1 || m[0].Auth != "Bearer zoom-access" {
		t.Fatalf("zoom meetings = %+v", m)
	}
}
