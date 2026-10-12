package app

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/logs"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/monitoring"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/rules"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// The machine of an incident: graphs from the monitoring systems whose hosts are the
// configuration item of the incident, log lines from the log sources and the events Umbrella
// received about the item, over a window around the start of the incident.

const (
	maxLogSources   = 20
	maxHostPanels   = 20
	maxHostWindow   = 7 * 24 * 60
	maxMachineRange = 31 * 24 * time.Hour
	machineTimeout  = 25 * time.Second
	machineParallel = 8
	machineEvents   = 200
	machineAlerts   = 50
	logTestLines    = 20
)

type metricsFetcher func(ctx context.Context, src model.MonitoringSource, auth *monitoring.Auth, host model.MonitoringHost, q monitoring.MetricQuery) ([]monitoring.Series, error)

type logsFetcher func(ctx context.Context, src model.LogSource, auth *logs.Auth, q logs.Query) (logs.Result, error)

type hostEventsFetcher func(ctx context.Context, src model.MonitoringSource, auth *monitoring.Auth, host model.MonitoringHost, from, to time.Time, limit int) ([]monitoring.HostEvent, bool, error)

type hostLogsFetcher func(ctx context.Context, src model.MonitoringSource, auth *monitoring.Auth, host model.MonitoringHost, from, to time.Time, limit int, text string) (monitoring.HostLogs, error)

// HostContextService keeps how the machine of an incident is shown and the log sources, and
// reads the graphs and lines of a machine.
type HostContextService struct {
	st      *store.Store
	creds   *CredentialsService
	metrics metricsFetcher
	logs    logsFetcher
	// hostEvents and hostLogs read what a monitoring system (Zabbix) keeps about the host.
	hostEvents hostEventsFetcher
	hostLogs   hostLogsFetcher
	now        func() time.Time
}

func NewHostContextService(st *store.Store, creds *CredentialsService) *HostContextService {
	return &HostContextService{st: st, creds: creds, metrics: monitoring.Metrics, logs: logs.Fetch, hostEvents: monitoring.HostEvents, hostLogs: monitoring.HostLogLines,
		now: func() time.Time { return time.Now().UTC() }}
}

type LogSourceView struct {
	model.LogSource
	CredentialName string `json:"credential_name,omitempty"`
}

type hostContextDefaults struct {
	WindowMinutes int               `json:"window_minutes"`
	LogLimit      int               `json:"log_limit"`
	Panels        []model.HostPanel `json:"panels"`
	LokiQuery     string            `json:"loki_query"`
	Index         string            `json:"index"`
	HostField     string            `json:"host_field"`
	MessageField  string            `json:"message_field"`
	TimeField     string            `json:"time_field"`
	LevelField    string            `json:"level_field"`
	// Graylog are the fields a Graylog source reads when it names none.
	Graylog struct {
		HostField    string `json:"host_field"`
		MessageField string `json:"message_field"`
		LevelField   string `json:"level_field"`
	} `json:"graylog"`
}

type HostContextView struct {
	Settings   model.HostContext   `json:"settings"`
	Defaults   hostContextDefaults `json:"defaults"`
	LogSources []LogSourceView     `json:"log_sources"`
}

func hostContextDefaultsView() hostContextDefaults {
	d := hostContextDefaults{WindowMinutes: model.DefaultHostWindow, LogLimit: model.DefaultHostLogLimit, Panels: model.DefaultHostPanels(),
		LokiQuery: logs.DefaultLokiQuery, Index: logs.DefaultIndex, HostField: logs.DefaultHostField, MessageField: logs.DefaultMessageField,
		TimeField: logs.DefaultTimeField, LevelField: logs.DefaultLevelField}
	d.Graylog.HostField, d.Graylog.MessageField, d.Graylog.LevelField = logs.GraylogHostField, logs.GraylogMessageField, logs.GraylogLevelField
	return d
}

func logSourceView(d *store.Data, src *model.LogSource) LogSourceView {
	v := LogSourceView{LogSource: *src}
	if c := d.Credentials[src.CredentialID]; c != nil {
		v.CredentialName = c.Name
	}
	return v
}

func sortedLogSources(d *store.Data) []*model.LogSource {
	out := make([]*model.LogSource, 0, len(d.LogSources))
	for _, s := range d.LogSources {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b *model.LogSource) int {
		if c := byName(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func (s *HostContextService) View() HostContextView {
	out := HostContextView{Defaults: hostContextDefaultsView(), LogSources: []LogSourceView{}}
	s.st.Read(func(d *store.Data) {
		out.Settings = d.Settings.HostContext.Effective()
		for _, src := range sortedLogSources(d) {
			out.LogSources = append(out.LogSources, logSourceView(d, src))
		}
	})
	return out
}

type HostContextInput struct {
	WindowMinutes int               `json:"window_minutes"`
	LogLimit      int               `json:"log_limit"`
	Panels        []model.HostPanel `json:"panels"`
}

func cleanText(v string, maxLen int) (string, bool) {
	v = strings.Join(strings.Fields(v), " ")
	return v, utf8.RuneCountInString(v) <= maxLen && !strings.ContainsFunc(v, unicode.IsControl)
}

func (in *HostContextInput) check() error {
	if in.WindowMinutes < 1 || in.WindowMinutes > maxHostWindow {
		return invalid("window_invalid", nil)
	}
	if in.LogLimit < 10 || in.LogLimit > logs.MaxLimit {
		return invalid("log_limit_invalid", nil)
	}
	if len(in.Panels) > maxHostPanels {
		return invalid("too_many_panels", nil)
	}
	seen := map[string]bool{}
	for i := range in.Panels {
		p := &in.Panels[i]
		var ok, ok2 bool
		p.Title, ok = cleanText(p.Title, 100)
		p.Unit, ok2 = cleanText(p.Unit, 20)
		p.PromQL, p.ZabbixKey = strings.TrimSpace(p.PromQL), strings.TrimSpace(p.ZabbixKey)
		if !ok || !ok2 || p.Title == "" {
			return invalid("panel_title", nil)
		}
		if (p.PromQL == "" && p.ZabbixKey == "") || len(p.PromQL) > 4000 || len(p.ZabbixKey) > 255 || strings.ContainsFunc(p.ZabbixKey, unicode.IsControl) {
			return invalid("panel_query", nil)
		}
		p.ID = strings.TrimSpace(p.ID)
		if p.ID == "" || len(p.ID) > 40 || seen[p.ID] {
			p.ID = "p" + strconv.Itoa(i+1)
			for seen[p.ID] {
				p.ID += "x"
			}
		}
		seen[p.ID] = true
	}
	return nil
}

func (s *HostContextService) Save(actor string, in HostContextInput) (model.HostContext, error) {
	if err := in.check(); err != nil {
		return model.HostContext{}, err
	}
	now := s.now()
	h := model.HostContext{WindowMinutes: in.WindowMinutes, LogLimit: in.LogLimit, Panels: in.Panels, PanelsSet: true, UpdatedBy: actor, UpdatedAt: &now}
	if h.Panels == nil {
		h.Panels = []model.HostPanel{}
	}
	s.st.Write(func(d *store.Data) {
		d.Settings.HostContext = h
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.host_context", Detail: fmt.Sprintf("window %d min, %d graphs", h.WindowMinutes, len(h.Panels))})
	})
	return h.Effective(), nil
}

type LogSourceInput struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	URL          string `json:"url"`
	CredentialID string `json:"credential_id"`
	SkipVerify   bool   `json:"skip_verify"`
	Enabled      bool   `json:"enabled"`
	Query        string `json:"query"`
	Index        string `json:"index"`
	HostField    string `json:"host_field"`
	MessageField string `json:"message_field"`
	TimeField    string `json:"time_field"`
	LevelField   string `json:"level_field"`
}

func (in *LogSourceInput) check(d *store.Data) error {
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	if n := utf8.RuneCountInString(in.Name); n == 0 || n > 200 || strings.ContainsFunc(in.Name, unicode.IsControl) {
		return invalid("name_invalid", nil)
	}
	if !slices.Contains(model.LogKinds, in.Kind) {
		return invalid("log_kind", nil)
	}
	u, err := optionalURL(in.URL)
	if err != nil || u == "" {
		return invalid("url_invalid", err)
	}
	in.URL = strings.TrimRight(u, "/")
	fields := []*string{&in.Query, &in.Index, &in.HostField, &in.MessageField, &in.TimeField, &in.LevelField}
	for _, f := range fields {
		*f = strings.TrimSpace(*f)
	}
	if in.Kind == model.LogLoki {
		in.Index, in.HostField, in.MessageField, in.TimeField, in.LevelField = "", "", "", "", ""
		if len(in.Query) > 4000 {
			return invalid("log_query", nil)
		}
	} else {
		if in.Kind == model.LogGraylog {
			// Graylog: the query narrows the lines, the index lists stream IDs and the time is
			// always its timestamp.
			in.TimeField = ""
			if len(in.Query) > 4000 || strings.ContainsFunc(in.Query, unicode.IsControl) {
				return invalid("log_query", nil)
			}
			in.Index = strings.Join(logs.GraylogStreams(model.LogSource{Index: in.Index}), ",")
		} else {
			in.Query = ""
		}
		for _, f := range fields[1:] {
			if len(*f) > 200 || strings.ContainsFunc(*f, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
				return invalid("log_field", nil)
			}
		}
		if strings.ContainsAny(in.Index, "/?#") {
			return invalid("log_field", nil)
		}
	}
	if in.CredentialID == "" {
		return nil
	}
	c := d.Credentials[in.CredentialID]
	if c == nil {
		return invalid("credential_not_found", nil)
	}
	if c.Type != "bearer" && c.Type != "basic" && c.Type != "header" {
		return invalid("credential_type", nil)
	}
	return nil
}

func (in LogSourceInput) apply(src *model.LogSource) {
	src.Name, src.Kind, src.URL, src.CredentialID, src.SkipVerify, src.Enabled = in.Name, in.Kind, in.URL, in.CredentialID, in.SkipVerify, in.Enabled
	src.Query, src.Index, src.HostField, src.MessageField, src.TimeField, src.LevelField = in.Query, in.Index, in.HostField, in.MessageField, in.TimeField, in.LevelField
}

func (s *HostContextService) CreateLog(actor string, in LogSourceInput) (LogSourceView, error) {
	var out LogSourceView
	var err error
	s.st.Write(func(d *store.Data) {
		if err = in.check(d); err != nil {
			return
		}
		if len(d.LogSources) >= maxLogSources {
			err = invalid("too_many_sources", nil)
			return
		}
		now := s.now()
		src := &model.LogSource{ID: d.NextID("LOG"), CreatedBy: actor, CreatedAt: now, UpdatedBy: actor, UpdatedAt: now}
		in.apply(src)
		d.LogSources[src.ID] = src
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "logs.create", Object: src.ID, Detail: src.Name + " (" + src.Kind + ") " + src.URL})
		out = logSourceView(d, src)
	})
	return out, err
}

func (s *HostContextService) UpdateLog(actor, id string, in LogSourceInput) (LogSourceView, error) {
	var out LogSourceView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		src := d.LogSources[id]
		if src == nil {
			return
		}
		if err = in.check(d); err != nil {
			return
		}
		in.apply(src)
		src.UpdatedBy, src.UpdatedAt = actor, s.now()
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "logs.update", Object: id, Detail: src.Name + " (" + src.Kind + ") " + src.URL})
		out = logSourceView(d, src)
	})
	return out, err
}

func (s *HostContextService) DeleteLog(actor, id string) error {
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		src := d.LogSources[id]
		if src == nil {
			return
		}
		delete(d.LogSources, id)
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "logs.delete", Object: id, Detail: src.Name})
		err = nil
	})
	return err
}

func (s *HostContextService) auth(id string) (*logs.Auth, error) {
	if id == "" {
		return nil, nil
	}
	c, err := s.creds.Resolve(id)
	if err != nil {
		return nil, err
	}
	return &logs.Auth{Type: c.Type, Fields: c.Fields, Secrets: c.Secrets}, nil
}

type LogTestInput struct {
	LogSourceInput
	Host    string `json:"host"`
	Minutes int    `json:"minutes"`
}

type LogTestReport struct {
	OK    bool        `json:"ok"`
	Error string      `json:"error,omitempty"`
	Query string      `json:"query,omitempty"`
	Lines []logs.Line `json:"lines"`
}

// TestLog reads the last lines of a machine with the given settings without keeping them.
func (s *HostContextService) TestLog(ctx context.Context, in LogTestInput) (LogTestReport, error) {
	var err error
	s.st.Read(func(d *store.Data) { err = in.check(d) })
	if err != nil {
		return LogTestReport{}, err
	}
	host := strings.TrimSpace(in.Host)
	if host == "" || len(host) > 255 {
		return LogTestReport{}, invalid("log_test_host", nil)
	}
	minutes := in.Minutes
	if minutes <= 0 || minutes > maxHostWindow {
		minutes = 60
	}
	out := LogTestReport{Lines: []logs.Line{}}
	auth, err := s.auth(in.CredentialID)
	if err != nil {
		out.Error = err.Error()
		return out, nil
	}
	src := model.LogSource{}
	in.apply(&src)
	ctx, cancel := context.WithTimeout(ctx, machineTimeout)
	defer cancel()
	now := s.now()
	res, err := s.logs(ctx, src, auth, logs.Query{Hosts: []string{host}, From: now.Add(-time.Duration(minutes) * time.Minute), To: now, Limit: logTestLines})
	if err != nil {
		out.Error = err.Error()
		return out, nil
	}
	out.OK, out.Query, out.Lines = true, res.Query, res.Lines
	return out, nil
}

// machineWindow is the time range of an incident: the window before its start and after it
// (span: after its end, or now while it lasts), never past now.
func machineWindow(al alert.Alert, minutes int, span bool, now time.Time) (from, to time.Time) {
	start := al.OpenedAt
	if start.IsZero() {
		start = al.FirstSeen
	}
	win := time.Duration(minutes) * time.Minute
	from, to = start.Add(-win), start.Add(win)
	if span {
		end := now
		if al.ResolvedAt != nil {
			end = *al.ResolvedAt
		}
		to = end.Add(win)
	}
	if to.After(now) {
		to = now
	}
	if to.Sub(from) > maxMachineRange {
		from = to.Add(-maxMachineRange)
	}
	if !to.After(from) {
		from = to.Add(-time.Minute)
	}
	// Whole seconds, the end rounded up so that what happened in the last second is in.
	return from.Truncate(time.Second), to.Add(time.Second - 1).Truncate(time.Second)
}

// machineHost is a host of a monitoring system that is the machine of the incident.
type machineHost struct {
	CIMonitor
	src  model.MonitoringSource
	host model.MonitoringHost
}

// machineOf finds the hosts that are the machine of an incident and every name it is known by.
// An incident without an item looks its machine up by the name in its events.
func machineOf(d *store.Data, al alert.Alert) (hosts []machineHost, names []string) {
	add := func(v ...string) {
		for _, x := range v {
			if x = strings.ToLower(strings.TrimSpace(x)); x != "" && !slices.Contains(names, x) {
				names = append(names, x)
			}
		}
	}
	ci := d.ConfigItems[al.CIID]
	var mons []CIMonitor
	if al.CIID != "" && ci != nil {
		add(alert.CIKeys(ci)...)
		mons = monitorsByCI(d)[ci.ID]
	} else if al.CIName != "" {
		add(alert.EventKeys(al.CIName)...)
		for _, src := range sortedSources(d) {
			if !src.Enabled {
				continue
			}
			for _, h := range src.Hosts {
				if slices.ContainsFunc(append([]string{h.Host, h.Name}, h.DNS...), func(v string) bool {
					return slices.Contains(names, strings.ToLower(strings.TrimSpace(v)))
				}) {
					mons = append(mons, CIMonitor{SourceID: src.ID, SourceName: src.Name, Kind: src.Kind, Key: h.Key, Host: h.Host, Name: h.Name,
						State: h.State, URL: h.URL, Match: MatchName})
				}
			}
		}
	}
	for _, m := range mons {
		src := d.MonitoringSources[m.SourceID]
		if src == nil {
			continue
		}
		i := slices.IndexFunc(src.Hosts, func(h model.MonitoringHost) bool { return h.Key == m.Key })
		if i < 0 {
			continue
		}
		h := src.Hosts[i]
		h.Endpoints = slices.Clone(h.Endpoints)
		cp := *src
		cp.Hosts, cp.Links = nil, nil
		hosts = append(hosts, machineHost{CIMonitor: m, src: cp, host: h})
		add(h.Host, h.Name)
		add(h.DNS...)
		add(h.IPs...)
	}
	return hosts, names
}

type sourceError struct {
	Source string `json:"source"`
	Error  string `json:"error"`
}

type machinePanel struct {
	ID     string              `json:"id"`
	Title  string              `json:"title"`
	Unit   string              `json:"unit"`
	Series []monitoring.Series `json:"series"`
	Errors []sourceError       `json:"errors"`
	// Alert: the metric the alert fired on, with its query, source and threshold.
	Alert     bool     `json:"alert,omitempty"`
	Query     string   `json:"query,omitempty"`
	Source    string   `json:"source,omitempty"`
	Op        string   `json:"op,omitempty"`
	Threshold *float64 `json:"threshold,omitempty"`
	// Focus: a graph of the same kind as the alert (CPU for a CPU alert).
	Focus bool `json:"focus,omitempty"`
}

type machineEvent struct {
	ConnectorID string    `json:"connector_id"`
	Connector   string    `json:"connector"`
	Title       string    `json:"title"`
	Signal      string    `json:"signal"`
	Severity    string    `json:"severity"`
	Status      string    `json:"status"`
	Value       string    `json:"value,omitempty"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
	Count       int       `json:"count"`
}

// machineSourceEvent is a problem the monitoring system of the machine raised on it.
type machineSourceEvent struct {
	monitoring.HostEvent
	Source string `json:"source"`
}

// hasOwnEvents: the systems that keep events and log lines of their hosts.
func hasOwnEvents(kind string) bool { return kind == model.MonitoringZabbix }

type machineIncident struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Severity   string     `json:"severity"`
	Status     string     `json:"status"`
	OpenedAt   time.Time  `json:"opened_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

type machineView struct {
	From          time.Time         `json:"from"`
	To            time.Time         `json:"to"`
	OpenedAt      time.Time         `json:"opened_at"`
	ResolvedAt    *time.Time        `json:"resolved_at,omitempty"`
	WindowMinutes int               `json:"window_minutes"`
	Span          bool              `json:"span"`
	DefaultWindow int               `json:"default_window"`
	Names         []string          `json:"names"`
	Hosts         []CIMonitor       `json:"hosts"`
	Panels        []machinePanel    `json:"panels"`
	LogSources    int               `json:"log_sources"`
	Events        []machineEvent    `json:"events"`
	Incidents     []machineIncident `json:"incidents"`
	EventsError   string            `json:"events_error,omitempty"`
	// SourceEvents: the problems the monitoring systems of the machine raised in the window.
	SourceEvents          []machineSourceEvent `json:"source_events"`
	SourceEventsErrors    []sourceError        `json:"source_events_errors"`
	SourceEventsTruncated bool                 `json:"source_events_truncated,omitempty"`
	// SourceEventHosts is how many hosts of the machine keep their own events.
	SourceEventHosts int `json:"source_event_hosts"`
}

type machineLogLine struct {
	logs.Line
	Source string `json:"source"`
}

type machineLogs struct {
	From      time.Time        `json:"from"`
	To        time.Time        `json:"to"`
	Names     []string         `json:"names"`
	Sources   int              `json:"sources"`
	Lines     []machineLogLine `json:"lines"`
	Truncated bool             `json:"truncated"`
	Errors    []sourceError    `json:"errors"`
}

// windowOf reads the window of a request: minutes (the default when absent) and span.
func windowOf(r *http.Request, def int) (int, bool, error) {
	minutes := def
	if v := r.URL.Query().Get("minutes"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxHostWindow {
			return 0, false, invalid("window_invalid", nil)
		}
		minutes = n
	}
	return minutes, r.URL.Query().Get("span") == "true", nil
}

// Machine reads the graphs of the machine of an incident.
func (s *HostContextService) Machine(ctx context.Context, al alert.Alert, minutes int, span bool) (v machineView) {
	var (
		set   model.HostContext
		hosts []machineHost
		rule  *ruleMetric
	)
	s.st.Read(func(d *store.Data) {
		set = d.Settings.HostContext.Effective()
		hosts, v.Names = machineOf(d, al)
		rule = ruleOf(d, al)
		for _, src := range d.LogSources {
			if src.Enabled {
				v.LogSources++
			}
		}
	})
	if minutes <= 0 {
		minutes = set.WindowMinutes
	}
	v.From, v.To = machineWindow(al, minutes, span, s.now())
	v.OpenedAt, v.ResolvedAt, v.WindowMinutes, v.Span, v.DefaultWindow = al.OpenedAt, al.ResolvedAt, minutes, span, set.WindowMinutes
	if v.OpenedAt.IsZero() {
		v.OpenedAt = al.FirstSeen
	}
	v.Hosts = make([]CIMonitor, 0, len(hosts))
	for _, h := range hosts {
		v.Hosts = append(v.Hosts, h.CIMonitor)
		if hasOwnEvents(h.Kind) {
			v.LogSources++
		}
	}
	if v.Names == nil {
		v.Names = []string{}
	}
	v.Panels = make([]machinePanel, len(set.Panels))
	for i, p := range set.Panels {
		v.Panels[i] = machinePanel{ID: p.ID, Title: p.Title, Unit: p.Unit, Series: []monitoring.Series{}, Errors: []sourceError{}}
	}
	ctx, cancel := context.WithTimeout(ctx, machineTimeout)
	defer cancel()
	// The metric of the alert and the events of the monitoring systems are read alongside the
	// graphs; the metric is shown first.
	var alertPanel *machinePanel
	alertDone := make(chan struct{})
	go func() {
		defer close(alertDone)
		alertPanel = s.alertPanel(ctx, al, rule, hosts, v.Names, v.From, v.To)
	}()
	eventsDone := make(chan struct{})
	go func() {
		defer close(eventsDone)
		s.sourceEvents(ctx, hosts, &v)
	}()
	defer func() {
		<-alertDone
		<-eventsDone
		v.Panels = orderPanels(v.Panels, alertPanel, focusPanels(set.Panels, al))
	}()
	if len(hosts) == 0 || len(set.Panels) == 0 {
		return v
	}
	auths, authErr := s.hostAuths(hosts)
	prefix := len(hosts) > 1
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, machineParallel)
	for i, p := range set.Panels {
		for _, h := range hosts {
			label := h.SourceName
			if prefix {
				label += " · " + firstSet(h.Name, h.Host)
			}
			// Grafana and Graylog systems give alerts and hosts, not graphs.
			if h.Kind != model.MonitoringPrometheus && h.Kind != model.MonitoringZabbix {
				continue
			}
			if err := authErr[h.src.ID]; err != nil {
				v.Panels[i].Errors = append(v.Panels[i].Errors, sourceError{Source: label, Error: err.Error()})
				continue
			}
			if (h.Kind == model.MonitoringPrometheus && strings.TrimSpace(p.PromQL) == "") || (h.Kind == model.MonitoringZabbix && strings.TrimSpace(p.ZabbixKey) == "") {
				continue
			}
			wg.Go(func() {
				sem <- struct{}{}
				defer func() { <-sem }()
				series, err := s.metrics(ctx, h.src, auths[h.src.ID], h.host, monitoring.MetricQuery{Panel: p, From: v.From, To: v.To})
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					v.Panels[i].Errors = append(v.Panels[i].Errors, sourceError{Source: label, Error: err.Error()})
					return
				}
				for _, se := range series {
					switch {
					case se.Name == "value":
						se.Name = label
					case prefix:
						se.Name = label + " · " + se.Name
					}
					v.Panels[i].Series = append(v.Panels[i].Series, se)
				}
			})
		}
	}
	wg.Wait()
	for i := range v.Panels {
		slices.SortFunc(v.Panels[i].Series, func(a, b monitoring.Series) int { return strings.Compare(a.Name, b.Name) })
		slices.SortFunc(v.Panels[i].Errors, func(a, b sourceError) int { return strings.Compare(a.Source, b.Source) })
	}
	return v
}

// hostAuths resolves the credentials of the monitoring systems of the hosts.
func (s *HostContextService) hostAuths(hosts []machineHost) (map[string]*monitoring.Auth, map[string]error) {
	auths := map[string]*monitoring.Auth{}
	authErr := map[string]error{}
	for _, h := range hosts {
		if _, done := auths[h.src.ID]; done || authErr[h.src.ID] != nil || h.src.CredentialID == "" {
			continue
		}
		c, err := s.creds.Resolve(h.src.CredentialID)
		if err != nil {
			authErr[h.src.ID] = err
			continue
		}
		auths[h.src.ID] = &monitoring.Auth{Type: c.Type, Fields: c.Fields, Secrets: c.Secrets}
	}
	return auths, authErr
}

// hostLabel names a host of the machine: its system, and the host when there are several.
func hostLabel(h machineHost, several bool) string {
	if several {
		return h.SourceName + " · " + firstSet(h.Name, h.Host)
	}
	return h.SourceName
}

// sourceEvents reads the problems the monitoring systems of the machine raised in the window.
func (s *HostContextService) sourceEvents(ctx context.Context, hosts []machineHost, v *machineView) {
	v.SourceEvents, v.SourceEventsErrors = []machineSourceEvent{}, []sourceError{}
	var own []machineHost
	for _, h := range hosts {
		if hasOwnEvents(h.Kind) {
			own = append(own, h)
		}
	}
	v.SourceEventHosts = len(own)
	if len(own) == 0 {
		return
	}
	auths, authErr := s.hostAuths(own)
	several := len(own) > 1
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, h := range own {
		label := hostLabel(h, several)
		if err := authErr[h.src.ID]; err != nil {
			v.SourceEventsErrors = append(v.SourceEventsErrors, sourceError{Source: label, Error: err.Error()})
			continue
		}
		wg.Go(func() {
			events, truncated, err := s.hostEvents(ctx, h.src, auths[h.src.ID], h.host, v.From, v.To, machineEvents)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				v.SourceEventsErrors = append(v.SourceEventsErrors, sourceError{Source: label, Error: err.Error()})
				return
			}
			v.SourceEventsTruncated = v.SourceEventsTruncated || truncated
			for _, e := range events {
				v.SourceEvents = append(v.SourceEvents, machineSourceEvent{HostEvent: e, Source: label})
			}
		})
	}
	wg.Wait()
	slices.SortStableFunc(v.SourceEvents, func(a, b machineSourceEvent) int { return b.At.Compare(a.At) })
	if len(v.SourceEvents) > machineEvents {
		v.SourceEvents, v.SourceEventsTruncated = v.SourceEvents[:machineEvents], true
	}
	slices.SortFunc(v.SourceEventsErrors, func(a, b sourceError) int { return strings.Compare(a.Source, b.Source) })
}

// Logs reads the lines of the machine of an incident from every turned-on log source and from
// the log items of the monitoring systems that keep them (Zabbix).
func (s *HostContextService) Logs(ctx context.Context, al alert.Alert, minutes int, span bool, text string) machineLogs {
	var (
		set     model.HostContext
		names   []string
		sources []model.LogSource
		hosts   []machineHost
	)
	s.st.Read(func(d *store.Data) {
		set = d.Settings.HostContext.Effective()
		var all []machineHost
		all, names = machineOf(d, al)
		for _, h := range all {
			if hasOwnEvents(h.Kind) {
				hosts = append(hosts, h)
			}
		}
		for _, src := range sortedLogSources(d) {
			if src.Enabled {
				sources = append(sources, *src)
			}
		}
	})
	if minutes <= 0 {
		minutes = set.WindowMinutes
	}
	out := machineLogs{Names: names, Sources: len(sources) + len(hosts), Lines: []machineLogLine{}, Errors: []sourceError{}}
	if out.Names == nil {
		out.Names = []string{}
	}
	out.From, out.To = machineWindow(al, minutes, span, s.now())
	if len(sources)+len(hosts) == 0 || (len(names) == 0 && len(hosts) == 0) {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, machineTimeout)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	auths, authErr := s.hostAuths(hosts)
	for _, h := range hosts {
		label := hostLabel(h, len(hosts) > 1)
		wg.Go(func() {
			err := authErr[h.src.ID]
			var res monitoring.HostLogs
			if err == nil {
				res, err = s.hostLogs(ctx, h.src, auths[h.src.ID], h.host, out.From, out.To, set.LogLimit, text)
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out.Errors = append(out.Errors, sourceError{Source: label, Error: err.Error()})
				return
			}
			out.Truncated = out.Truncated || res.Truncated
			for _, l := range res.Lines {
				out.Lines = append(out.Lines, machineLogLine{Line: logs.Line{At: l.At, Level: l.Level, Text: l.Text, Labels: l.Labels}, Source: label})
			}
		})
	}
	for _, src := range sources {
		if len(names) == 0 {
			break
		}
		wg.Go(func() {
			auth, err := s.auth(src.CredentialID)
			var res logs.Result
			if err == nil {
				res, err = s.logs(ctx, src, auth, logs.Query{Hosts: names, From: out.From, To: out.To, Limit: set.LogLimit, Text: text})
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out.Errors = append(out.Errors, sourceError{Source: src.Name, Error: err.Error()})
				return
			}
			out.Truncated = out.Truncated || res.Truncated
			for _, l := range res.Lines {
				out.Lines = append(out.Lines, machineLogLine{Line: l, Source: src.Name})
			}
		})
	}
	wg.Wait()
	slices.SortStableFunc(out.Lines, func(a, b machineLogLine) int { return b.At.Compare(a.At) })
	if len(out.Lines) > set.LogLimit {
		out.Lines, out.Truncated = out.Lines[:set.LogLimit], true
	}
	slices.SortFunc(out.Errors, func(a, b sourceError) int { return strings.Compare(a.Source, b.Source) })
	return out
}

const machineEventsSQL = `
SELECT connector_id, title, signal, severity, status, value, first_seen, last_seen, seen
FROM connector_events
WHERE lower(ci) = ANY($1) AND last_seen >= $2 AND first_seen <= $3
ORDER BY last_seen DESC LIMIT $4`

const machineAlertsSQL = `
SELECT id, doc->>'title', severity, status, COALESCE((doc->>'opened_at')::timestamptz, first_seen), resolved_at
FROM alerts
WHERE (($1 <> '' AND ci_id = $1) OR ($1 = '' AND lower(doc->>'ci_name') = ANY($2)))
	AND first_seen <= $4 AND (resolved_at IS NULL OR resolved_at >= $3)
	AND (cardinality($5::text[]) = 0 OR service_ids && $5)
ORDER BY first_seen DESC LIMIT $6`

// machineHistory adds the events Umbrella received about the machine and its incidents in
// the window.
func (a *App) machineHistory(ctx context.Context, al alert.Alert, scope []string, v *machineView) {
	v.Events, v.Incidents = []machineEvent{}, []machineIncident{}
	if len(v.Names) == 0 && al.CIID == "" {
		return
	}
	if scope == nil {
		scope = []string{}
	}
	db := a.alerts.DB()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := db.Query(ctx, machineEventsSQL, v.Names, v.From, v.To, machineEvents)
	if err == nil {
		for rows.Next() {
			var e machineEvent
			if err = rows.Scan(&e.ConnectorID, &e.Title, &e.Signal, &e.Severity, &e.Status, &e.Value, &e.FirstSeen, &e.LastSeen, &e.Count); err != nil {
				break
			}
			v.Events = append(v.Events, e)
		}
		rows.Close()
		if err == nil {
			err = rows.Err()
		}
	}
	if err == nil {
		rows, err = db.Query(ctx, machineAlertsSQL, al.CIID, v.Names, v.From, v.To, scope, machineAlerts)
		if err == nil {
			for rows.Next() {
				var m machineIncident
				if err = rows.Scan(&m.ID, &m.Title, &m.Severity, &m.Status, &m.OpenedAt, &m.ResolvedAt); err != nil {
					break
				}
				v.Incidents = append(v.Incidents, m)
			}
			rows.Close()
			if err == nil {
				err = rows.Err()
			}
		}
	}
	if err != nil {
		v.EventsError = err.Error()
		return
	}
	a.deps.Store.Read(func(d *store.Data) {
		for i := range v.Events {
			if c := d.Connectors[v.Events[i].ConnectorID]; c != nil {
				v.Events[i].Connector = c.Name
			} else if r := d.Rules[strings.TrimPrefix(v.Events[i].ConnectorID, rules.ConnectorPrefix)]; r != nil {
				v.Events[i].Connector = r.Name
			}
		}
	})
}

func (a *App) registerHostContext(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/host-context", a.authed(a.can("monitoring:view", a.hostContextView)))
	mux.HandleFunc("PUT /api/host-context", a.authed(a.can("monitoring:edit", a.hostContextSave)))
	mux.HandleFunc("POST /api/host-context/logs", a.authed(a.can("monitoring:edit", a.createLogSource)))
	mux.HandleFunc("PUT /api/host-context/logs/{id}", a.authed(a.can("monitoring:edit", a.updateLogSource)))
	mux.HandleFunc("DELETE /api/host-context/logs/{id}", a.authed(a.can("monitoring:edit", a.deleteLogSource)))
	mux.HandleFunc("POST /api/host-context/logs/test", a.authed(a.can("monitoring:test", a.testLogSource)))
	mux.HandleFunc("GET /api/incidents/{id}/machine", a.authed(a.can("incidents:view", a.incidentMachine)))
	mux.HandleFunc("GET /api/incidents/{id}/machine/logs", a.authed(a.can("incidents:view", a.incidentMachineLogs)))
}

func (a *App) hostContextView(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, a.hostContext.View())
}

func (a *App) hostContextSave(w http.ResponseWriter, r *http.Request) {
	var in HostContextInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.hostContext.Save(current(r).user.Username, in)
	reply(w, http.StatusOK, out, err)
}

func (a *App) createLogSource(w http.ResponseWriter, r *http.Request) {
	var in LogSourceInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.hostContext.CreateLog(current(r).user.Username, in)
	reply(w, http.StatusCreated, out, err)
}

func (a *App) updateLogSource(w http.ResponseWriter, r *http.Request) {
	var in LogSourceInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.hostContext.UpdateLog(current(r).user.Username, r.PathValue("id"), in)
	reply(w, http.StatusOK, out, err)
}

func (a *App) deleteLogSource(w http.ResponseWriter, r *http.Request) {
	if err := a.hostContext.DeleteLog(current(r).user.Username, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) testLogSource(w http.ResponseWriter, r *http.Request) {
	var in LogTestInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.hostContext.TestLog(r.Context(), in)
	reply(w, http.StatusOK, out, err)
}

// visibleIncident is the incident of the request when the user sees it.
func (a *App) visibleIncident(w http.ResponseWriter, r *http.Request) (alert.Alert, bool) {
	if !a.alertsReady(w) {
		return alert.Alert{}, false
	}
	al, _, err := a.alerts.Get(r.Context(), r.PathValue("id"))
	if err == nil && !al.InScope(a.incidentScope(current(r).user)) {
		err = alert.ErrNotFound
	}
	if err != nil {
		writeError(w, err)
		return al, false
	}
	return al, true
}

func (a *App) incidentMachine(w http.ResponseWriter, r *http.Request) {
	al, ok := a.visibleIncident(w, r)
	if !ok {
		return
	}
	minutes, span, err := windowOf(r, 0)
	if err != nil {
		writeError(w, err)
		return
	}
	v := a.hostContext.Machine(r.Context(), al, minutes, span)
	a.machineHistory(r.Context(), al, a.incidentScope(current(r).user), &v)
	httpx.JSON(w, http.StatusOK, v)
}

func (a *App) incidentMachineLogs(w http.ResponseWriter, r *http.Request) {
	al, ok := a.visibleIncident(w, r)
	if !ok {
		return
	}
	minutes, span, err := windowOf(r, 0)
	if err != nil {
		writeError(w, err)
		return
	}
	text := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(text) > 500 {
		writeError(w, invalid("log_text", nil))
		return
	}
	httpx.JSON(w, http.StatusOK, a.hostContext.Logs(r.Context(), al, minutes, span, text))
}
