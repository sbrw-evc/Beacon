package alert_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store/storetest"
)

type recorder struct {
	mu       sync.Mutex
	cmds     []alert.Command
	fallback []alert.Alert
	followUp []alert.Alert
	// channels: the notifier has a channel turned on (alert.Notifier.On).
	channels bool
}

func (r *recorder) On() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.channels
}

func (r *recorder) FollowUp(a alert.Alert) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.followUp = append(r.followUp, a)
}

func (r *recorder) Send(c alert.Command) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmds = append(r.cmds, c)
}

func (r *recorder) Fallback(a alert.Alert) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fallback = append(r.fallback, a)
}

func (r *recorder) take() []alert.Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.cmds
	r.cmds = nil
	return out
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func catalog() *store.Store {
	st := store.New()
	st.Write(func(d *store.Data) {
		d.Users["U-1"] = &model.User{ID: "U-1", Username: "lead", Name: "Lead One", TeamIDs: []string{"T-2"}, Profile: model.Profile{Email: "lead@example.com"}, Telegram: "1001"}
		d.Users["U-2"] = &model.User{ID: "U-2", Username: "eng", Name: "Engineer Two", TeamIDs: []string{"T-2"}, Profile: model.Profile{Email: "eng@example.com"}}
		d.Users["U-3"] = &model.User{ID: "U-3", Username: "owner", Name: "Owner Three", Profile: model.Profile{Email: "owner@example.com"}}
		d.Users["U-4"] = &model.User{ID: "U-4", Username: "gone", Name: "Gone", TeamIDs: []string{"T-2"}, Disabled: true}
		d.Teams["T-1"] = &model.Team{ID: "T-1", Name: "Platform"}
		d.Teams["T-2"] = &model.Team{ID: "T-2", Name: "Payments SRE", ParentID: "T-1", LeadID: "U-1"}
		d.Teams["T-3"] = &model.Team{ID: "T-3", Name: "Empty", ParentID: "T-2"}
		d.ConfigItems["CI-1"] = &model.ConfigItem{ID: "CI-1", Name: "db-01.example.com", Kind: model.CIKindVM, Status: model.CIStatusActive,
			IPs: []string{"10.0.0.11"}, Owners: []model.CIOwner{{UserID: "U-3", Role: "technical"}}}
		d.ConfigItems["CI-2"] = &model.ConfigItem{ID: "CI-2", Name: "app-01", Kind: model.CIKindVM, Status: model.CIStatusActive,
			Owners: []model.CIOwner{{UserID: "U-3", Role: "technical"}}}
		d.ConfigItems["CI-3"] = &model.ConfigItem{ID: "CI-3", Name: "lonely", Kind: model.CIKindDevice, Status: model.CIStatusActive,
			Owners: []model.CIOwner{{UserID: "U-3", Role: "admin"}}}
		d.Services["S-1"] = &model.Service{ID: "S-1", Name: "Payments", OwnerTeamID: "T-2", Criticality: model.CriticalityCritical,
			Status: model.ServiceActive, CIIDs: []string{"CI-1", "CI-2"}}
		d.Services["S-2"] = &model.Service{ID: "S-2", Name: "Reports", OwnerTeamID: "T-3", Criticality: model.CriticalityLow,
			Status: model.ServiceActive, CIIDs: []string{"CI-1"}}
		d.Settings.Alerting.PagerDuty.Enabled = true
	})
	return st
}

func setup(t *testing.T) (*alert.Engine, *store.Store, *recorder, *clock) {
	t.Helper()
	cfg := storetest.TempDatabase(t)
	db, err := pgxpool.New(context.Background(), cfg.DSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := alert.EnsureSchema(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := alert.EnsureSchema(context.Background(), db); err != nil {
		t.Fatalf("the schema is created idempotently: %v", err)
	}
	st := catalog()
	e := alert.New(db, st)
	rec := &recorder{}
	c := &clock{t: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	e.SetSender(rec)
	e.SetNotifier(rec)
	e.SetClock(c.now)
	return e, st, rec, c
}

func ev(conn, key, ci, signal, sev, status string) alert.Incoming {
	return alert.Incoming{ConnectorID: conn, Key: key, Title: signal + " on " + ci, CI: ci, Signal: signal, Method: "other", Severity: sev, Status: status}
}

func active(t *testing.T, e *alert.Engine) []alert.Alert {
	t.Helper()
	p, err := e.List(context.Background(), alert.Filter{Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	return p.Alerts
}

func TestFoldAcrossSourcesAndResolve(t *testing.T) {
	ctx := context.Background()
	e, _, rec, c := setup(t)

	// Zabbix and Prometheus see the same problem on the same item under different names.
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", "z1", "db-01.example.com", "cpu", "warning", "firing")}); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Minute)
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-2", "p1", "10.0.0.11:9100", "CPU", "critical", "firing")}); err != nil {
		t.Fatal(err)
	}
	list := active(t, e)
	if len(list) != 1 {
		t.Fatalf("one alert per item and signal, got %d", len(list))
	}
	a := list[0]
	if a.CIID != "CI-1" || a.Severity != "critical" || len(a.Sources) != 2 || a.Count != 2 || a.PD.Key != "umb-"+a.ID {
		t.Errorf("alert = %+v", a)
	}
	cmds := rec.take()
	if len(cmds) != 2 || cmds[0].Action != alert.PDTrigger || cmds[1].Action != alert.PDTrigger || cmds[1].Alert.Severity != "critical" {
		t.Errorf("trigger on open and on severity raise: %+v", cmds)
	}

	// Repeated delivery touches the alert without a new trigger.
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-2", "p1", "10.0.0.11:9100", "CPU", "critical", "firing")}); err != nil {
		t.Fatal(err)
	}
	if cmds := rec.take(); len(cmds) != 0 {
		t.Errorf("a duplicate sends nothing: %+v", cmds)
	}

	// One source back to normal: still open, severity follows the one that fires.
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-2", "p1", "10.0.0.11:9100", "CPU", "critical", "resolved")}); err != nil {
		t.Fatal(err)
	}
	if list := active(t, e); len(list) != 1 || list[0].Severity != "warning" {
		t.Fatalf("open while a source fires: %+v", list)
	}
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", "z1", "db-01.example.com", "cpu", "warning", "resolved")}); err != nil {
		t.Fatal(err)
	}
	if list := active(t, e); len(list) != 0 {
		t.Fatalf("resolved when all sources are normal: %+v", list)
	}
	if cmds := rec.take(); len(cmds) != 1 || cmds[0].Action != alert.PDResolve {
		t.Errorf("resolve goes to PagerDuty: %+v", cmds)
	}

	// Firing again within 10 minutes reopens the same alert.
	c.advance(5 * time.Minute)
	if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", "z1", "db-01.example.com", "cpu", "warning", "firing")}); err != nil {
		t.Fatal(err)
	}
	if list := active(t, e); len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("reopened in the window: %+v", list)
	}
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "z1", "db-01.example.com", "cpu", "warning", "resolved")})
	c.advance(11 * time.Minute)
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "z1", "db-01.example.com", "cpu", "warning", "firing")})
	if list := active(t, e); len(list) != 1 || list[0].ID == a.ID {
		t.Fatalf("a new alert after the window: %+v", list)
	}

	_, entries, err := e.Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]int{}
	for _, en := range entries {
		codes[en.Code]++
	}
	if codes["opened"] != 1 || codes["reopened"] != 1 || codes["resolved"] != 2 || codes["severity_raised"] != 1 || codes["routed"] != 1 {
		t.Errorf("timeline = %v", codes)
	}
}

func TestRouting(t *testing.T) {
	ctx := context.Background()
	e, st, _, _ := setup(t)
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "db-01", "disk", "error", "firing")})
	a := active(t, e)[0]
	r := a.Route
	if r.Via != alert.ViaService || r.Team == nil || r.Team.ID != "T-2" || len(r.Services) != 2 || r.Services[0].ID != "S-1" {
		t.Fatalf("the most critical service decides the team: %+v", r)
	}
	if len(r.People) != 2 || r.People[0].UserID != "U-1" || r.People[0].Role != "lead" || r.People[0].Telegram != "1001" || r.People[1].UserID != "U-2" {
		t.Errorf("lead first, then members, disabled users left out: %+v", r.People)
	}
	if len(r.Owners) != 1 || r.Owners[0].UserID != "U-3" {
		t.Errorf("CI owners are kept: %+v", r.Owners)
	}

	// An item without a service goes to its NetBox owners.
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "b", "lonely", "power", "critical", "firing")})
	p, _ := e.List(ctx, alert.Filter{Status: "active", CIID: "CI-3"})
	if len(p.Alerts) != 1 || p.Alerts[0].Route.Via != alert.ViaCIOwners || p.Alerts[0].Route.Recipients()[0].UserID != "U-3" {
		t.Errorf("owners route: %+v", p.Alerts)
	}

	// A team with nobody hands the alert to its parent.
	st.Write(func(d *store.Data) {
		d.Services["S-1"].OwnerTeamID = "T-3"
		d.Services["S-2"].Status = model.ServiceRetired
	})
	if err := e.Reroute(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ = e.List(ctx, alert.Filter{Status: "active", TeamID: "T-3"})
	if len(p.Alerts) != 1 || len(p.Alerts[0].Route.People) != 2 || len(p.Alerts[0].Route.Services) != 1 {
		t.Errorf("reroute to the parent's people: %+v", p.Alerts)
	}
	p, _ = e.List(ctx, alert.Filter{Status: "active", ServiceID: "S-1", Query: "payments"})
	if len(p.Alerts) != 1 {
		t.Errorf("filter by service and search by service name: %+v", p.Alerts)
	}
}

func TestUnknownItemIsBoundLater(t *testing.T) {
	ctx := context.Background()
	e, st, _, _ := setup(t)
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "new-host", "ping", "critical", "firing")})
	a := active(t, e)[0]
	if a.CIID != "" || a.Route.Via != alert.ViaNone {
		t.Fatalf("not in the catalog yet: %+v", a)
	}
	st.Write(func(d *store.Data) {
		d.ConfigItems["CI-9"] = &model.ConfigItem{ID: "CI-9", Name: "new-host", Status: model.CIStatusActive}
		d.Services["S-1"].CIIDs = append(d.Services["S-1"].CIIDs, "CI-9")
	})
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "new-host", "ping", "critical", "firing")})
	list := active(t, e)
	if len(list) != 1 || list[0].ID != a.ID || list[0].CIID != "CI-9" || list[0].Route.Team == nil {
		t.Fatalf("the same alert is bound to the item: %+v", list)
	}
}

func TestBindUnknownNow(t *testing.T) {
	ctx := context.Background()
	e, st, rec, _ := setup(t)
	e.Ingest(ctx, []alert.Incoming{
		ev("CON-1", "a", "edge-07:9100", "ping", "critical", "firing"),
		ev("CON-1", "b", "edge-07:9100", "disk", "warning", "firing"),
		ev("CON-1", "c", "other-host", "ping", "critical", "firing"),
	})
	rec.take()
	if bound, err := e.BindUnknown(ctx, "admin"); err != nil || len(bound) != 0 {
		t.Fatalf("nothing to bind yet: %v %v", bound, err)
	}

	// The event name becomes an alias of an item already in a service: both alerts of the name
	// are bound and routed at once, without waiting for another event.
	st.Write(func(d *store.Data) { d.ConfigItems["CI-2"].Aliases = []string{"edge-07:9100"} })
	bound, err := e.BindUnknown(ctx, "admin")
	if err != nil || len(bound) != 2 {
		t.Fatalf("bound = %v, %v", bound, err)
	}
	p, _ := e.List(ctx, alert.Filter{Status: "active", CIID: "CI-2"})
	if len(p.Alerts) != 2 || p.Alerts[0].Route.Team == nil || p.Alerts[0].Route.Team.ID != "T-2" || p.Alerts[0].CIName != "app-01" {
		t.Fatalf("bound alerts = %+v", p.Alerts)
	}
	_, entries, _ := e.Get(ctx, p.Alerts[0].ID)
	codes := map[string]int{}
	for _, en := range entries {
		codes[en.Code]++
	}
	if codes["ci_bound"] != 1 || codes["routed"] != 1 {
		t.Errorf("timeline = %v", codes)
	}

	// Later events under the alias fold into the same alert.
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "edge-07:9100", "ping", "critical", "firing")})
	if p2, _ := e.List(ctx, alert.Filter{Status: "active", CIID: "CI-2"}); len(p2.Alerts) != 2 {
		t.Errorf("future events match the alias: %+v", p2.Alerts)
	}
	if list := active(t, e); len(list) != 3 {
		t.Errorf("no new alert: %d", len(list))
	}

	// An alert of a name that turns out to be an item that already has an alert of the signal
	// is merged into it.
	e.Ingest(ctx, []alert.Incoming{ev("CON-2", "x", "lonely", "ping", "error", "firing")})
	rec.take()
	st.Write(func(d *store.Data) { d.ConfigItems["CI-3"].Aliases = []string{"other-host"} })
	if bound, err = e.BindUnknown(ctx, ""); err != nil || len(bound) != 1 {
		t.Fatalf("merge = %v %v", bound, err)
	}
	p, _ = e.List(ctx, alert.Filter{Status: "active", CIID: "CI-3"})
	if len(p.Alerts) != 1 || len(p.Alerts[0].Sources) != 2 || p.Alerts[0].Severity != "critical" {
		t.Fatalf("merged = %+v", p.Alerts)
	}
	if list := active(t, e); len(list) != 3 {
		t.Errorf("the merged alert is resolved: %d", len(list))
	}
	if cmds := rec.take(); len(cmds) != 1 || cmds[0].Action != alert.PDResolve {
		t.Errorf("the merged alert is resolved in PagerDuty: %+v", cmds)
	}
}

func TestActionsAndPagerDuty(t *testing.T) {
	ctx := context.Background()
	e, _, rec, c := setup(t)
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "app-01", "errors", "error", "firing")})
	a := active(t, e)[0]
	rec.take()

	if _, err := e.Act(ctx, a.ID, "resolve", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Act(ctx, a.ID, "ack", "eng", ""); !errors.Is(err, alert.ErrNotOpen) {
		t.Errorf("a resolved alert is not acknowledged: %v", err)
	}
	rec.take()

	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "b", "app-01", "latency", "critical", "firing")})
	b := active(t, e)[0]
	rec.take()
	// PagerDuty is down: the trigger fails and is retried; after 2 minutes backup notification.
	e.PDResult(ctx, b.ID, alert.PDTrigger, "default", "", errors.New("Events API answered 503"))
	got, _, _ := e.Get(ctx, b.ID)
	if got.PD.State != alert.PDFailed || got.PD.Retry != "trigger" {
		t.Fatalf("pd = %+v", got.PD)
	}
	c.advance(30 * time.Second)
	e.Tick(ctx)
	if cmds := rec.take(); len(cmds) != 0 || len(rec.fallback) != 0 {
		t.Errorf("nothing before the retry interval: %+v", cmds)
	}
	c.advance(90 * time.Second)
	e.Tick(ctx)
	if cmds := rec.take(); len(cmds) != 1 || cmds[0].Action != alert.PDTrigger {
		t.Errorf("retry: %+v", cmds)
	}
	if len(rec.fallback) != 1 || rec.fallback[0].ID != b.ID || len(rec.fallback[0].Route.Recipients()) != 2 {
		t.Fatalf("backup notification after 2 minutes: %+v", rec.fallback)
	}
	e.Tick(ctx)
	if len(rec.fallback) != 1 {
		t.Error("backup notification is sent once")
	}

	// Acknowledged in Umbrella while PagerDuty was down: after the trigger is taken, the
	// acknowledgement follows.
	if _, err := e.Act(ctx, b.ID, "ack", "eng", ""); err != nil {
		t.Fatal(err)
	}
	rec.take()
	e.PDResult(ctx, b.ID, alert.PDTrigger, "Payments", "", nil)
	if cmds := rec.take(); len(cmds) != 1 || cmds[0].Action != alert.PDAcknowledge {
		t.Errorf("the acknowledgement follows: %+v", cmds)
	}
	e.PDResult(ctx, b.ID, alert.PDAcknowledge, "Payments", "", nil)

	// Resolved in PagerDuty: resolved here.
	keys, err := e.PDKeys(ctx, "umb-"+b.ID, "")
	if err != nil || len(keys) != 1 {
		t.Fatal(keys, err)
	}
	if err := e.PDInbound(ctx, alert.PDUpdate{DedupKey: keys[0], EventType: "incident.resolved", Actor: "Jane", IncidentID: "Q1", IncidentURL: "https://pd/incidents/Q1"}); err != nil {
		t.Fatal(err)
	}
	got, entries, _ := e.Get(ctx, b.ID)
	if got.Status != alert.StatusResolved || got.ResolvedBy != "Jane" || got.PD.IncidentID != "Q1" || got.PD.State != alert.PDAcked {
		t.Errorf("after webhook: %+v", got)
	}
	if keys, _ := e.PDKeys(ctx, "", "Q1"); len(keys) != 1 {
		t.Error("the alert is found by the PagerDuty incident")
	}
	if entries[len(entries)-1].Code != "resolved" || entries[len(entries)-1].Args["why"] != "pagerduty" {
		t.Errorf("timeline end = %+v", entries[len(entries)-1])
	}
	if _, err := e.Act(ctx, b.ID, "comment", "eng", "  "); !errors.Is(err, alert.ErrEmptyComment) {
		t.Error("empty comments are refused")
	}
}

func TestBelowThresholdIsNotRetried(t *testing.T) {
	ctx := context.Background()
	e, _, rec, c := setup(t)
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "app-01", "errors", "warning", "firing")})
	a := active(t, e)[0]
	e.PDResult(ctx, a.ID, alert.PDTrigger, "", "", alert.ErrPDSkipped)
	rec.take()
	c.advance(5 * time.Minute)
	e.Tick(ctx)
	if cmds := rec.take(); len(cmds) != 0 || len(rec.fallback) != 0 {
		t.Errorf("skipped alerts are left alone: %+v %+v", cmds, rec.fallback)
	}
}

func TestMaintenance(t *testing.T) {
	ctx := context.Background()
	e, st, rec, c := setup(t)
	st.Write(func(d *store.Data) {
		d.Maintenance["MW-1"] = &model.Maintenance{ID: "MW-1", Title: "Patching", ServiceIDs: []string{"S-1"},
			Start: c.t.Add(-time.Minute), End: c.t.Add(30 * time.Minute)}
	})
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "app-01", "errors", "critical", "firing")})
	a := active(t, e)[0]
	if !a.Suppressed || a.MaintenanceID != "MW-1" || a.PD.State != alert.PDSkipped {
		t.Fatalf("suppressed: %+v", a)
	}
	c.advance(5 * time.Minute)
	e.Tick(ctx)
	if cmds := rec.take(); len(cmds) != 0 || len(rec.fallback) != 0 {
		t.Errorf("nothing is sent in a window: %+v", cmds)
	}
	c.advance(30 * time.Minute)
	e.Tick(ctx)
	if cmds := rec.take(); len(cmds) != 1 || cmds[0].Action != alert.PDTrigger {
		t.Errorf("sent when the window is over: %+v", cmds)
	}
	got, _, _ := e.Get(ctx, a.ID)
	if got.Suppressed {
		t.Error("no longer suppressed")
	}
	// PagerDuty gets its full time after the window, not the time since the alert opened.
	if len(rec.fallback) != 0 {
		t.Fatalf("no backup notification the moment the window ends: %+v", rec.fallback)
	}
	e.PDResult(ctx, a.ID, alert.PDTrigger, "", "", errors.New("Events API answered 503"))
	c.advance(time.Minute)
	e.Tick(ctx)
	if len(rec.fallback) != 0 {
		t.Fatalf("no backup notification a minute after the window: %+v", rec.fallback)
	}
	c.advance(alert.DefaultFallbackAfter)
	e.Tick(ctx)
	if len(rec.fallback) != 1 {
		t.Fatalf("backup notification once the delay passed after the window: %+v", rec.fallback)
	}
}

func TestRedUseLink(t *testing.T) {
	ctx := context.Background()
	e, _, _, c := setup(t)
	use := ev("CON-1", "u", "db-01", "use.cpu", "warning", "firing")
	use.Method = "use"
	red := ev("CON-2", "r", "app-01", "red.errors", "error", "firing")
	red.Method = "red"
	e.Ingest(ctx, []alert.Incoming{use})
	c.advance(time.Minute)
	e.Ingest(ctx, []alert.Incoming{red})
	p, _ := e.List(ctx, alert.Filter{Status: "active", Method: "red"})
	if len(p.Alerts) != 1 || p.Alerts[0].RelatedID == "" {
		t.Fatalf("red linked to use: %+v", p.Alerts)
	}
	if p.Counts.Active != 1 || p.Counts.BySeverity["error"] != 1 || p.Counts.PDNotTaken != 1 {
		t.Errorf("counts follow the method filter = %+v", p.Counts)
	}
	if p, _ = e.List(ctx, alert.Filter{Status: "active"}); p.Counts.Active != 2 || p.Counts.PDNotTaken != 2 {
		t.Errorf("counts without filters = %+v", p.Counts)
	}
}

func TestLongCommentKeepsCharacters(t *testing.T) {
	ctx := context.Background()
	e, _, _, _ := setup(t)
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "c", "db-01", "disk", "error", "firing")})
	a := active(t, e)[0]
	// 4001 two-byte letters: a byte cut at 4000 would land inside the 2000th one.
	text := "ж" + strings.Repeat("ы", 4000)
	if _, err := e.Act(ctx, a.ID, "comment", "eng", text); err != nil {
		t.Fatal(err)
	}
	_, entries, err := e.Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := entries[len(entries)-1].Args["text"]
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) != 4000 || got != text[:len(text)-len("ы")] {
		t.Errorf("comment kept %d characters, valid UTF-8 %v", utf8.RuneCountInString(got), utf8.ValidString(got))
	}
}

// An incident PagerDuty took is still acknowledged and resolved there when a maintenance
// window covers the alert; the start of a window alone resolves nothing.
func TestWindowDoesNotStrandPagerDutyIncidents(t *testing.T) {
	ctx := context.Background()
	e, st, rec, c := setup(t)
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "app-01", "errors", "critical", "firing")})
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "b", "app-01", "latency", "critical", "firing")})
	list := active(t, e)
	if len(list) != 2 {
		t.Fatalf("two alerts: %+v", list)
	}
	for _, a := range list {
		e.PDResult(ctx, a.ID, alert.PDTrigger, "default", "", nil)
	}
	rec.take()
	st.Write(func(d *store.Data) {
		d.Maintenance["MW-1"] = &model.Maintenance{ID: "MW-1", Title: "Repair", ServiceIDs: []string{"S-1"},
			Start: c.t, End: c.t.Add(time.Hour)}
	})
	c.advance(time.Minute)
	e.Tick(ctx)
	if cmds := rec.take(); len(cmds) != 0 {
		t.Fatalf("a window that starts sends nothing: %+v", cmds)
	}
	for _, a := range active(t, e) {
		if !a.Suppressed || a.PD.State != alert.PDAccepted {
			t.Fatalf("suppressed and still open in PagerDuty: %+v", a)
		}
	}

	// Fixed during the window: the source resolves, the incident is resolved in PagerDuty.
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "app-01", "errors", "critical", "resolved")})
	cmds := rec.take()
	if len(cmds) != 1 || cmds[0].Action != alert.PDResolve {
		t.Fatalf("resolve goes to PagerDuty in a window: %+v", cmds)
	}
	// The resolve fails: it is retried in the window too.
	e.PDResult(ctx, cmds[0].Alert.ID, alert.PDResolve, "default", "", errors.New("Events API answered 503"))
	c.advance(2 * time.Minute)
	e.Tick(ctx)
	if cmds := rec.take(); len(cmds) != 1 || cmds[0].Action != alert.PDResolve {
		t.Fatalf("the resolve is retried in a window: %+v", cmds)
	}
	e.PDResult(ctx, cmds[0].Alert.ID, alert.PDResolve, "default", "", nil)

	// Acknowledged in Umbrella during the window: acknowledged in PagerDuty.
	other := active(t, e)[0]
	if _, err := e.Act(ctx, other.ID, "ack", "eng", ""); err != nil {
		t.Fatal(err)
	}
	if cmds := rec.take(); len(cmds) != 1 || cmds[0].Action != alert.PDAcknowledge {
		t.Fatalf("ack goes to PagerDuty in a window: %+v", cmds)
	}
	if _, err := e.Act(ctx, other.ID, "resolve", "eng", ""); err != nil {
		t.Fatal(err)
	}
	if cmds := rec.take(); len(cmds) != 1 || cmds[0].Action != alert.PDResolve {
		t.Fatalf("manual resolve goes to PagerDuty in a window: %+v", cmds)
	}

	// An alert that opened in the window and never went to PagerDuty: nothing until the window
	// ends, then the trigger while it still fires.
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "c", "app-01", "queue", "critical", "firing")})
	if cmds := rec.take(); len(cmds) != 0 {
		t.Fatalf("no trigger in a window: %+v", cmds)
	}
	c.advance(time.Hour)
	e.Tick(ctx)
	if cmds := rec.take(); len(cmds) != 1 || cmds[0].Action != alert.PDTrigger || cmds[0].Alert.Signal != "queue" {
		t.Fatalf("the trigger after the window: %+v", cmds)
	}
}

// A late webhook about the PagerDuty incident of an earlier opening leaves the reopened alert
// alone.
func TestStaleWebhookAfterReopen(t *testing.T) {
	ctx := context.Background()
	e, _, rec, c := setup(t)
	fire := ev("CON-1", "a", "app-01", "errors", "critical", "firing")
	e.Ingest(ctx, []alert.Incoming{fire})
	a := active(t, e)[0]
	e.PDResult(ctx, a.ID, alert.PDTrigger, "default", "default", nil)
	if err := e.PDInbound(ctx, alert.PDUpdate{DedupKey: a.PD.Key, EventType: "incident.triggered", IncidentID: "Q1", OccurredAt: c.t}); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Minute)
	resolved := fire
	resolved.Status = alert.SourceResolved
	e.Ingest(ctx, []alert.Incoming{resolved})
	c.advance(10 * time.Second)
	e.Ingest(ctx, []alert.Incoming{fire})
	reopenedAt := c.t
	rec.take()
	if got := active(t, e); len(got) != 1 || got[0].ID != a.ID || got[0].PD.IncidentID != "" {
		t.Fatalf("reopened without the old incident: %+v", got)
	}

	// The resolve of the old incident Q1 lands after the reopening.
	c.advance(time.Second)
	if err := e.PDInbound(ctx, alert.PDUpdate{DedupKey: a.PD.Key, EventType: "incident.resolved", Actor: "Jane", IncidentID: "Q1", OccurredAt: c.t}); err != nil {
		t.Fatal(err)
	}
	// An acknowledgement made before the reopening, of an incident never seen here.
	if err := e.PDInbound(ctx, alert.PDUpdate{DedupKey: a.PD.Key, EventType: "incident.acknowledged", Actor: "Jane", IncidentID: "Q0", OccurredAt: reopenedAt.Add(-5 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	got, entries, _ := e.Get(ctx, a.ID)
	if got.Status != alert.StatusOpen || got.PD.IncidentID != "" {
		t.Fatalf("stale webhooks changed the reopened alert: %+v", got)
	}
	stale := 0
	for _, en := range entries {
		if en.Code == "pd_stale" {
			stale++
		}
	}
	if stale != 2 {
		t.Errorf("stale webhooks are on the timeline: %d", stale)
	}

	// The new incident acknowledges the alert.
	if err := e.PDInbound(ctx, alert.PDUpdate{DedupKey: a.PD.Key, EventType: "incident.acknowledged", Actor: "Jane", IncidentID: "Q2", OccurredAt: c.t}); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := e.Get(ctx, a.ID); got.Status != alert.StatusAcknowledged || got.PD.IncidentID != "Q2" {
		t.Fatalf("the current incident applies: %+v", got)
	}
}

// Backup notification is pending until the notifier reports the attempt: when the process
// stops before that (or the queue was full), a later tick, also of a new process, hands it
// over again.
func TestFallbackSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	e, st, lost, c := setup(t)
	e.Ingest(ctx, []alert.Incoming{ev("CON-1", "a", "app-01", "errors", "critical", "firing")})
	a := active(t, e)[0]
	e.PDResult(ctx, a.ID, alert.PDTrigger, "default", "", errors.New("Events API answered 503"))
	c.advance(alert.DefaultFallbackAfter)
	e.Tick(ctx)
	if len(lost.fallback) != 1 {
		t.Fatalf("backup notification is due: %+v", lost.fallback)
	}
	got, _, _ := e.Get(ctx, a.ID)
	if !got.Fallback || got.FallbackState != alert.FallbackPending {
		t.Fatalf("pending until the notifier reports: %+v", got)
	}

	// The process stops before the notifier sent anything; a new one starts on the same database.
	e2 := alert.New(e.DB(), st)
	rec := &recorder{}
	e2.SetSender(rec)
	e2.SetNotifier(rec)
	e2.SetClock(c.now)
	c.advance(time.Minute)
	e2.Tick(ctx)
	if len(rec.fallback) != 0 {
		t.Fatalf("not handed over again too soon: %+v", rec.fallback)
	}
	c.advance(alert.DefaultFallbackRetry)
	e2.Tick(ctx)
	if len(rec.fallback) != 1 || rec.fallback[0].ID != a.ID {
		t.Fatalf("handed over again after the restart: %+v", rec.fallback)
	}
	if err := e2.FallbackDone(ctx, a.ID, nil); err != nil {
		t.Fatal(err)
	}
	c.advance(2 * alert.DefaultFallbackRetry)
	e2.Tick(ctx)
	if len(rec.fallback) != 1 {
		t.Fatalf("sent once it is reported: %+v", rec.fallback)
	}
	if got, _, _ := e2.Get(ctx, a.ID); got.FallbackState != alert.FallbackSent || !got.Fallback {
		t.Fatalf("state = %+v", got)
	}
}

// A reopened alert starts backup notification afresh: a notification still pending from the
// earlier opening is not sent at once.
func TestFallbackResetOnReopen(t *testing.T) {
	ctx := context.Background()
	e, _, rec, c := setup(t)
	fire := ev("CON-1", "a", "app-01", "errors", "critical", "firing")
	e.Ingest(ctx, []alert.Incoming{fire})
	a := active(t, e)[0]
	e.PDResult(ctx, a.ID, alert.PDTrigger, "default", "", errors.New("Events API answered 503"))
	c.advance(alert.DefaultFallbackAfter)
	e.Tick(ctx)
	if len(rec.fallback) != 1 {
		t.Fatalf("due: %+v", rec.fallback)
	}
	resolved := fire
	resolved.Status = alert.SourceResolved
	e.Ingest(ctx, []alert.Incoming{resolved})
	c.advance(alert.DefaultFallbackRetry)
	e.Ingest(ctx, []alert.Incoming{fire})
	e.Tick(ctx)
	if len(rec.fallback) != 1 {
		t.Fatalf("not sent right after the reopening: %d", len(rec.fallback))
	}
	if got, _, _ := e.Get(ctx, a.ID); got.Fallback || got.FallbackState != "" || got.FallbackTry != nil {
		t.Fatalf("reset: %+v", got)
	}
}

// A reopen window set in the alerting policy replaces the default of 10 minutes.
func TestCustomReopenWindow(t *testing.T) {
	ctx := context.Background()
	e, st, _, c := setup(t)
	st.Write(func(d *store.Data) { d.Settings.Alerting.Policy = &model.AlertPolicy{ReopenWindowSeconds: 3600} })
	fire := func(status string) {
		t.Helper()
		if err := e.Ingest(ctx, []alert.Incoming{ev("CON-1", "z1", "db-01.example.com", "cpu", "warning", status)}); err != nil {
			t.Fatal(err)
		}
	}
	fire("firing")
	first := active(t, e)[0].ID
	fire("resolved")
	// Beyond the default window, within the policy's one: the same alert reopens.
	c.advance(30 * time.Minute)
	fire("firing")
	if list := active(t, e); len(list) != 1 || list[0].ID != first {
		t.Fatalf("reopened within the policy window: %+v", list)
	}
	_, entries, err := e.Get(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	reopened := false
	for _, en := range entries {
		reopened = reopened || (en.Code == "reopened" && en.Args["window"] == "1h0m0s")
	}
	if !reopened {
		t.Fatalf("timeline = %+v", entries)
	}
	fire("resolved")
	c.advance(61 * time.Minute)
	fire("firing")
	if list := active(t, e); len(list) != 1 || list[0].ID == first {
		t.Fatalf("a new alert after the policy window: %+v", list)
	}
}
