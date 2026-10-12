package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/monitoring"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	maxMonitoringSources = 50
	monitoringTimeout    = 5 * time.Minute
	monitoringActor      = "monitoring"

	// How a host was matched with a configuration item.
	MatchManual    = "manual"
	MatchExcluded  = "excluded"
	MatchName      = "name"
	MatchShort     = "short"
	MatchDNS       = "dns"
	MatchIP        = "ip"
	MatchAmbiguous = "ambiguous"
)

var (
	ErrMonitoringRunning = errors.New("the host list of this source is already being read")
	ErrHostNotFound      = errors.New("the source has no such host; read the host list again")
)

func init() {
	statuses = append(statuses,
		orgStatus{ErrMonitoringRunning, http.StatusConflict, "monitoring_running"},
		orgStatus{ErrHostNotFound, http.StatusNotFound, "host_not_found"},
		orgStatus{ErrSourceInUse, http.StatusConflict, "source_in_use"},
	)
}

type hostFetcher func(ctx context.Context, src model.MonitoringSource, auth *monitoring.Auth) (monitoring.Result, error)

// MonitoringService keeps the monitoring systems hosts are read from, reads their host lists on
// a schedule and matches the hosts with configuration items.
type MonitoringService struct {
	st      *store.Store
	creds   *CredentialsService
	cis     *CIService
	fetch   hostFetcher
	now     func() time.Time
	mu      sync.Mutex
	running map[string]bool
	// polls tells the last poll of the alerts of a source (alertPoller.status).
	polls func(id string) *model.MonitoringPoll
}

const (
	defaultPollSeconds = 60
	minPollSeconds     = 15
	maxPollSeconds     = 3600
)

func NewMonitoringService(st *store.Store, creds *CredentialsService, cis *CIService) *MonitoringService {
	return &MonitoringService{st: st, creds: creds, cis: cis, fetch: monitoring.Fetch, now: func() time.Time { return time.Now().UTC() },
		running: map[string]bool{}}
}

type MonitoringSourceInput struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	URL          string `json:"url"`
	CredentialID string `json:"credential_id"`
	SkipVerify   bool   `json:"skip_verify"`
	Enabled      bool   `json:"enabled"`
	SyncMinutes  int    `json:"sync_minutes"`
	Query        string `json:"query"`
	HostLabel    string `json:"host_label"`
	ConnectorID  string `json:"connector_id"`
	PollAlerts   bool   `json:"poll_alerts"`
	PollSeconds  int    `json:"poll_seconds"`
	QuietMinutes int    `json:"quiet_minutes"`
}

type MonitoringSourceView struct {
	model.MonitoringSource
	CredentialName string     `json:"credential_name,omitempty"`
	Hosts          int        `json:"hosts"`
	Matched        int        `json:"matched"`
	Unmatched      int        `json:"unmatched"`
	Running        bool       `json:"running"`
	NextSyncAt     *time.Time `json:"next_sync_at,omitempty"`
	// Connector receives the alerts of the system; Rules counts the RED and USE rules that
	// query a Prometheus system.
	Connector *SystemConnector `json:"connector,omitempty"`
	Rules     int              `json:"rules"`
	// Poll is the last poll of the alerts of a Grafana or Graylog system since Umbrella started.
	Poll *model.MonitoringPoll `json:"poll,omitempty"`
}

// SystemConnector is the alert intake of a monitoring system. LastReceived is filled in by
// the handler from the intake statistics.
type SystemConnector struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Slug         string     `json:"slug"`
	Status       string     `json:"status"`
	IngestPath   string     `json:"ingest_path"`
	LastReceived *time.Time `json:"last_received,omitempty"`
	Received     int        `json:"received"`
}

type MonitoringView struct {
	Sources  []MonitoringSourceView `json:"sources"`
	Defaults struct {
		Query     string `json:"query"`
		HostLabel string `json:"host_label"`
		// GrafanaHostLabels name the host of a Grafana alert when the system sets no label.
		GrafanaHostLabels []string `json:"grafana_host_labels"`
		PollSeconds       int      `json:"poll_seconds"`
		// Graylog: the host field and the quiet time when the system sets none.
		GraylogHostField    string `json:"graylog_host_field"`
		GraylogQuietMinutes int    `json:"graylog_quiet_minutes"`
	} `json:"defaults"`
}

type MonitoringTestReport struct {
	OK      bool                   `json:"ok"`
	Error   string                 `json:"error,omitempty"`
	Version string                 `json:"version,omitempty"`
	Hosts   int                    `json:"hosts"`
	Sample  []model.MonitoringHost `json:"sample"`
}

// hostMatch is the configuration item a host belongs to and how that was found.
type hostMatch struct {
	CIID       string
	How        string
	Candidates []string
}

// hostMatcher matches hosts with configuration items by the same names events use: the
// technical and visible name, DNS names and IP addresses of the host against the name, short
// name, IP addresses and domain DNS name of the item. A name shared by several items matches
// none of them.
type hostMatcher struct {
	d     *store.Data
	index map[string][]string
}

func newHostMatcher(d *store.Data) *hostMatcher {
	m := &hostMatcher{d: d, index: map[string][]string{}}
	for id, ci := range d.ConfigItems {
		for _, k := range alert.CIKeys(ci) {
			if k = strings.ToLower(strings.TrimSpace(k)); k != "" && !slices.Contains(m.index[k], id) {
				m.index[k] = append(m.index[k], id)
			}
		}
	}
	for _, ids := range m.index {
		slices.Sort(ids)
	}
	return m
}

func (m *hostMatcher) match(src *model.MonitoringSource, h model.MonitoringHost) hostMatch {
	if id, ok := src.Links[h.Key]; ok {
		if id == model.HostNoCI {
			return hostMatch{How: MatchExcluded}
		}
		if m.d.ConfigItems[id] != nil {
			return hostMatch{CIID: id, How: MatchManual}
		}
	}
	type cand struct{ v, how string }
	cands := []cand{{h.Host, MatchName}, {h.Name, MatchName}}
	for _, v := range h.DNS {
		cands = append(cands, cand{v, MatchDNS})
	}
	for _, v := range h.IPs {
		cands = append(cands, cand{v, MatchIP})
	}
	var ambiguous []string
	for _, c := range cands {
		full := strings.ToLower(strings.TrimSpace(c.v))
		if full == "" {
			continue
		}
		short, _, _ := strings.Cut(full, ".")
		for _, k := range alert.EventKeys(full) {
			ids := m.index[k]
			if len(ids) == 1 {
				how := c.how
				if net.ParseIP(full) != nil {
					how = MatchIP
				}
				ciName := strings.ToLower(m.d.ConfigItems[ids[0]].Name)
				ciShort, _, _ := strings.Cut(ciName, ".")
				if how != MatchIP && net.ParseIP(full) == nil && ((k == short && k != full) || (k == ciShort && k != ciName)) {
					how = MatchShort
				}
				return hostMatch{CIID: ids[0], How: how}
			}
			if len(ids) > 1 {
				for _, id := range ids {
					if !slices.Contains(ambiguous, id) {
						ambiguous = append(ambiguous, id)
					}
				}
				break
			}
		}
	}
	if len(ambiguous) > 0 {
		slices.Sort(ambiguous)
		return hostMatch{How: MatchAmbiguous, Candidates: ambiguous}
	}
	return hostMatch{}
}

// CIMonitor is a host of a monitoring system that covers a configuration item.
type CIMonitor struct {
	SourceID   string `json:"source_id"`
	SourceName string `json:"source_name"`
	Kind       string `json:"kind"`
	Key        string `json:"key"`
	Host       string `json:"host"`
	Name       string `json:"name"`
	State      string `json:"state"`
	URL        string `json:"url,omitempty"`
	Match      string `json:"match"`
}

func sortedSources(d *store.Data) []*model.MonitoringSource {
	out := make([]*model.MonitoringSource, 0, len(d.MonitoringSources))
	for _, s := range d.MonitoringSources {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b *model.MonitoringSource) int {
		if c := byName(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

// monitorsByCI lists the hosts of the turned-on sources that cover every configuration item.
func monitorsByCI(d *store.Data) map[string][]CIMonitor {
	out := map[string][]CIMonitor{}
	if len(d.MonitoringSources) == 0 {
		return out
	}
	m := newHostMatcher(d)
	for _, src := range sortedSources(d) {
		if !src.Enabled {
			continue
		}
		for _, h := range src.Hosts {
			hm := m.match(src, h)
			if hm.CIID == "" {
				continue
			}
			out[hm.CIID] = append(out[hm.CIID], CIMonitor{SourceID: src.ID, SourceName: src.Name, Kind: src.Kind, Key: h.Key, Host: h.Host,
				Name: h.Name, State: h.State, URL: h.URL, Match: hm.How})
		}
	}
	return out
}

// monitorable: devices and virtual machines that are expected to run.
func monitorable(ci *model.ConfigItem) bool {
	return (ci.Kind == model.CIKindDevice || ci.Kind == model.CIKindVM) && ci.Status != model.CIStatusPlanned && ci.Status != model.CIStatusDecommissioning
}

// notMonitored: a monitorable item no turned-on host of any source covers.
func notMonitored(ci *model.ConfigItem, mons []CIMonitor) bool {
	return monitorable(ci) && !slices.ContainsFunc(mons, func(m CIMonitor) bool { return m.State != model.HostDisabled })
}

func (s *MonitoringService) isRunning(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[id]
}

func (s *MonitoringService) sourceView(d *store.Data, m *hostMatcher, src *model.MonitoringSource) MonitoringSourceView {
	v := MonitoringSourceView{MonitoringSource: *src, Hosts: len(src.Hosts), Running: s.isRunning(src.ID)}
	if c := d.Credentials[src.CredentialID]; c != nil {
		v.CredentialName = c.Name
	}
	if cn := d.Connectors[src.ConnectorID]; cn != nil {
		v.Connector = &SystemConnector{ID: cn.ID, Name: cn.Name, Slug: cn.Slug, Status: statusOf(cn), IngestPath: "/api/ingest/" + cn.Slug}
	} else {
		v.ConnectorID = ""
	}
	for _, r := range d.Rules {
		if r.SourceID == src.ID {
			v.Rules++
		}
	}
	for _, h := range src.Hosts {
		// The same groups as the host list: a host said to be no item is neither matched nor
		// unmatched.
		switch hm := m.match(src, h); {
		case hm.CIID != "":
			v.Matched++
		case hm.How != MatchExcluded:
			v.Unmatched++
		}
	}
	if s.polls != nil && pollable(src.Kind) {
		v.Poll = s.polls(src.ID)
	}
	if src.Enabled && src.SyncMinutes > 0 {
		next := src.Sync.StartedAt.Add(time.Duration(src.SyncMinutes) * time.Minute)
		if src.Sync.StartedAt.IsZero() {
			next = s.now()
		}
		v.NextSyncAt = &next
	}
	return v
}

func (s *MonitoringService) View() MonitoringView {
	out := MonitoringView{Sources: []MonitoringSourceView{}}
	out.Defaults.Query, out.Defaults.HostLabel = monitoring.DefaultQuery, monitoring.DefaultHostLabel
	out.Defaults.GrafanaHostLabels, out.Defaults.PollSeconds = monitoring.GrafanaHostLabels, defaultPollSeconds
	out.Defaults.GraylogHostField, out.Defaults.GraylogQuietMinutes = monitoring.DefaultGraylogHostField, int(monitoring.DefaultGraylogQuiet/time.Minute)
	s.st.Read(func(d *store.Data) {
		m := newHostMatcher(d)
		for _, src := range sortedSources(d) {
			out.Sources = append(out.Sources, s.sourceView(d, m, src))
		}
	})
	return out
}

func (s *MonitoringService) check(d *store.Data, in *MonitoringSourceInput) error {
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	if n := utf8.RuneCountInString(in.Name); n == 0 || n > 200 || strings.ContainsFunc(in.Name, unicode.IsControl) {
		return invalid("name_invalid", nil)
	}
	if in.Kind != model.MonitoringZabbix && in.Kind != model.MonitoringPrometheus && in.Kind != model.MonitoringGrafana && in.Kind != model.MonitoringGraylog {
		return invalid("monitoring_kind", nil)
	}
	u, err := optionalURL(in.URL)
	if err != nil || u == "" {
		return invalid("url_invalid", err)
	}
	in.URL = strings.TrimRight(u, "/")
	if in.SyncMinutes < 0 || in.SyncMinutes > 10080 {
		return invalid("monitoring_interval", nil)
	}
	switch in.Kind {
	case model.MonitoringPrometheus:
		in.Query, in.HostLabel = strings.TrimSpace(in.Query), strings.TrimSpace(in.HostLabel)
		if len(in.Query) > 4000 || len(in.HostLabel) > 200 {
			return invalid("monitoring_query", nil)
		}
	case model.MonitoringGrafana:
		in.Query, in.HostLabel = "", strings.TrimSpace(in.HostLabel)
		if len(in.HostLabel) > 200 {
			return invalid("monitoring_query", nil)
		}
	case model.MonitoringGraylog:
		in.Query, in.HostLabel = strings.TrimSpace(in.Query), strings.TrimSpace(in.HostLabel)
		if len(in.Query) > 4000 || len(in.HostLabel) > 200 || strings.ContainsFunc(in.HostLabel, unicode.IsSpace) {
			return invalid("monitoring_query", nil)
		}
	default:
		in.Query, in.HostLabel = "", ""
	}
	if in.Kind == model.MonitoringGraylog {
		if in.QuietMinutes == 0 {
			in.QuietMinutes = int(monitoring.DefaultGraylogQuiet / time.Minute)
		}
		if in.QuietMinutes < 1 || in.QuietMinutes > 1440 {
			return invalid("monitoring_quiet", nil)
		}
	} else {
		in.QuietMinutes = 0
	}
	if pollable(in.Kind) {
		if in.PollSeconds == 0 {
			in.PollSeconds = defaultPollSeconds
		}
		if in.PollSeconds < minPollSeconds || in.PollSeconds > maxPollSeconds {
			return invalid("monitoring_poll_interval", nil)
		}
	} else {
		in.PollAlerts, in.PollSeconds = false, 0
	}
	if in.ConnectorID != "" && d.Connectors[in.ConnectorID] == nil {
		return invalid("connector_not_found", nil)
	}
	if in.CredentialID == "" {
		if in.Kind == model.MonitoringZabbix {
			return invalid("monitoring_credential_required", nil)
		}
		return nil
	}
	c := d.Credentials[in.CredentialID]
	if c == nil {
		return invalid("credential_not_found", nil)
	}
	ok := c.Type == "bearer" || c.Type == "basic"
	if in.Kind != model.MonitoringZabbix {
		ok = ok || c.Type == "header"
	}
	if !ok {
		return invalid("credential_type", nil)
	}
	return nil
}

func (in MonitoringSourceInput) apply(src *model.MonitoringSource) {
	src.Name, src.Kind, src.URL, src.CredentialID, src.SkipVerify = in.Name, in.Kind, in.URL, in.CredentialID, in.SkipVerify
	src.Enabled, src.SyncMinutes, src.Query, src.HostLabel = in.Enabled, in.SyncMinutes, in.Query, in.HostLabel
	src.ConnectorID, src.PollAlerts, src.PollSeconds, src.QuietMinutes = in.ConnectorID, in.PollAlerts, in.PollSeconds, in.QuietMinutes
}

func (s *MonitoringService) Create(actor string, in MonitoringSourceInput) (MonitoringSourceView, error) {
	var out MonitoringSourceView
	var err error
	s.st.Write(func(d *store.Data) {
		if err = s.check(d, &in); err != nil {
			return
		}
		if len(d.MonitoringSources) >= maxMonitoringSources {
			err = invalid("too_many_sources", nil)
			return
		}
		now := s.now()
		src := &model.MonitoringSource{ID: d.NextID("MON"), Links: map[string]string{}, CreatedBy: actor, CreatedAt: now, UpdatedBy: actor, UpdatedAt: now}
		in.apply(src)
		d.MonitoringSources[src.ID] = src
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "monitoring.create", Object: src.ID, Detail: src.Name + " (" + src.Kind + ") " + src.URL})
		out = s.sourceView(d, newHostMatcher(d), src)
	})
	return out, err
}

func (s *MonitoringService) Update(actor, id string, in MonitoringSourceInput) (MonitoringSourceView, error) {
	var out MonitoringSourceView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		src := d.MonitoringSources[id]
		if src == nil {
			return
		}
		if err = s.check(d, &in); err != nil {
			return
		}
		if in.Kind != model.MonitoringPrometheus && rulesUse(d, id) {
			err = ErrSourceInUse
			return
		}
		// Another system means other hosts: what was read and linked before no longer applies.
		if src.Kind != in.Kind || !strings.EqualFold(src.URL, in.URL) {
			src.Hosts, src.Links, src.Sync, src.Polled = nil, map[string]string{}, model.MonitoringSync{}, nil
		}
		in.apply(src)
		src.UpdatedBy, src.UpdatedAt = actor, s.now()
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "monitoring.update", Object: id, Detail: src.Name + " (" + src.Kind + ") " + src.URL})
		out = s.sourceView(d, newHostMatcher(d), src)
	})
	return out, err
}

func (s *MonitoringService) Delete(actor, id string) error {
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		src := d.MonitoringSources[id]
		if src == nil {
			return
		}
		if rulesUse(d, id) {
			err = ErrSourceInUse
			return
		}
		delete(d.MonitoringSources, id)
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "monitoring.delete", Object: id, Detail: src.Name})
		err = nil
	})
	return err
}

// rulesUse: RED or USE rules query the source.
func rulesUse(d *store.Data, id string) bool {
	for _, r := range d.Rules {
		if r.SourceID == id {
			return true
		}
	}
	return false
}

// LinkConnector makes a connector the alert intake of a monitoring system; an empty ID unlinks.
func (s *MonitoringService) LinkConnector(actor, id, connectorID string) error {
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		src := d.MonitoringSources[id]
		if src == nil {
			return
		}
		if connectorID != "" && d.Connectors[connectorID] == nil {
			err = invalid("connector_not_found", nil)
			return
		}
		err = nil
		src.ConnectorID = connectorID
		src.UpdatedBy, src.UpdatedAt = actor, s.now()
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "monitoring.connector", Object: id, Detail: src.Name + ": connector " + firstSet(connectorID, "none")})
	})
	return err
}

func (s *MonitoringService) auth(id string) (*monitoring.Auth, error) {
	if id == "" {
		return nil, nil
	}
	c, err := s.creds.Resolve(id)
	if err != nil {
		return nil, err
	}
	return &monitoring.Auth{Type: c.Type, Fields: c.Fields, Secrets: c.Secrets}, nil
}

// Test reads the host list with the given settings without keeping it.
func (s *MonitoringService) Test(ctx context.Context, in MonitoringSourceInput) (MonitoringTestReport, error) {
	var err error
	s.st.Read(func(d *store.Data) { err = s.check(d, &in) })
	if err != nil {
		return MonitoringTestReport{}, err
	}
	auth, err := s.auth(in.CredentialID)
	if err != nil {
		return MonitoringTestReport{Error: err.Error(), Sample: []model.MonitoringHost{}}, nil
	}
	src := model.MonitoringSource{}
	in.apply(&src)
	ctx, cancel := context.WithTimeout(ctx, monitoringTimeout)
	defer cancel()
	res, err := s.fetch(ctx, src, auth)
	if err != nil {
		return MonitoringTestReport{Error: err.Error(), Sample: []model.MonitoringHost{}}, nil
	}
	out := MonitoringTestReport{OK: true, Version: res.Version, Hosts: len(res.Hosts), Sample: slices.Clone(res.Hosts[:min(len(res.Hosts), 5)])}
	if out.Sample == nil {
		out.Sample = []model.MonitoringHost{}
	}
	for i := range out.Sample {
		out.Sample[i].Normalize()
	}
	return out, nil
}

// Sync reads the host list of a source. A failure to reach the system is recorded in the
// returned state; the error is only for a reading that cannot start.
func (s *MonitoringService) Sync(ctx context.Context, actor, id string) (model.MonitoringSync, error) {
	var src model.MonitoringSource
	found := false
	s.st.Read(func(d *store.Data) {
		if p := d.MonitoringSources[id]; p != nil {
			src, found = *p, true
		}
	})
	if !found {
		return model.MonitoringSync{}, ErrNotFound
	}
	s.mu.Lock()
	if s.running[id] {
		s.mu.Unlock()
		return model.MonitoringSync{}, ErrMonitoringRunning
	}
	s.running[id] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, id)
		s.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, monitoringTimeout)
	defer cancel()
	state := model.MonitoringSync{StartedAt: s.now(), Actor: actor}
	auth, err := s.auth(src.CredentialID)
	var res monitoring.Result
	if err == nil {
		res, err = s.fetch(ctx, src, auth)
	}
	if err != nil {
		state.Error = err.Error()
	} else {
		state.OK, state.Hosts, state.Version = true, len(res.Hosts), res.Version
	}
	state.FinishedAt = s.now()
	s.st.Write(func(d *store.Data) {
		p := d.MonitoringSources[id]
		if p == nil {
			return
		}
		before := len(p.Hosts)
		if state.OK {
			p.Hosts = res.Hosts
			keys := map[string]bool{}
			for _, h := range res.Hosts {
				keys[h.Key] = true
			}
			// Links of hosts that are gone go with them.
			for k := range p.Links {
				if !keys[k] {
					delete(p.Links, k)
				}
			}
		}
		p.Sync = state
		if actor != monitoringActor || !state.OK || before != len(p.Hosts) {
			detail := fmt.Sprintf("%s: %d hosts", p.Name, state.Hosts)
			if !state.OK {
				detail = p.Name + ": failed: " + state.Error
			}
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "monitoring.sync", Object: id, Detail: detail})
		}
	})
	return state, nil
}

// Run reads the host lists of the sources on their intervals until ctx ends.
func (s *MonitoringService) Run(ctx context.Context) {
	tk := time.NewTicker(time.Minute)
	defer tk.Stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			for _, id := range s.due() {
				wg.Go(func() {
					if _, err := s.Sync(ctx, monitoringActor, id); err != nil && !errors.Is(err, ErrMonitoringRunning) {
						slog.Error("monitoring: reading hosts failed", "source", id, "err", err)
					}
				})
			}
		}
	}
}

func (s *MonitoringService) due() []string {
	var out []string
	now := s.now()
	s.st.Read(func(d *store.Data) {
		for id, src := range d.MonitoringSources {
			if src.Enabled && src.SyncMinutes > 0 && !now.Before(src.Sync.StartedAt.Add(time.Duration(src.SyncMinutes)*time.Minute)) {
				out = append(out, id)
			}
		}
	})
	slices.Sort(out)
	return out
}

// HostView is a host of a monitoring system with the configuration item it belongs to.
type HostView struct {
	model.MonitoringHost
	SourceID   string       `json:"source_id"`
	SourceName string       `json:"source_name"`
	Kind       string       `json:"kind"`
	CI         *ServiceRef  `json:"ci,omitempty"`
	Match      string       `json:"match"`
	Candidates []ServiceRef `json:"candidates"`
	// AlsoIn: unmatched hosts of other sources that are the same machine.
	AlsoIn []HostRef `json:"also_in"`
}

type HostSummary struct {
	Total     int `json:"total"`
	Matched   int `json:"matched"`
	Unmatched int `json:"unmatched"`
	Ambiguous int `json:"ambiguous"`
	Excluded  int `json:"excluded"`
}

type HostList struct {
	Items   []HostView  `json:"items"`
	Summary HostSummary `json:"summary"`
	// Limited: more hosts match the filter than are returned.
	Limited bool `json:"limited"`
}

type HostFilter struct {
	Source string
	// Match: matched, unmatched, ambiguous or excluded.
	Match string
	Query string
}

const maxHostRows = 2000

func (s *MonitoringService) Hosts(f HostFilter) HostList {
	out := HostList{Items: []HostView{}}
	q := strings.ToLower(strings.TrimSpace(f.Query))
	s.st.Read(func(d *store.Data) {
		m := newHostMatcher(d)
		ref := func(id string) ServiceRef {
			if ci := d.ConfigItems[id]; ci != nil {
				return ServiceRef{ID: id, Name: ci.Name}
			}
			return ServiceRef{ID: id, Name: id}
		}
		var all []HostView
		for _, src := range sortedSources(d) {
			for _, h := range src.Hosts {
				h.Normalize()
				hm := m.match(src, h)
				v := HostView{MonitoringHost: h, SourceID: src.ID, SourceName: src.Name, Kind: src.Kind, Match: hm.How,
					Candidates: []ServiceRef{}, AlsoIn: []HostRef{}}
				if hm.CIID != "" {
					r := ref(hm.CIID)
					v.CI = &r
				}
				for _, id := range hm.Candidates {
					v.Candidates = append(v.Candidates, ref(id))
				}
				all = append(all, v)
			}
		}
		linkSameMachine(all)
		for _, v := range all {
			if f.Source != "" && v.SourceID != f.Source {
				continue
			}
			sum := &out.Summary
			sum.Total++
			group := "unmatched"
			switch {
			case v.CI != nil:
				sum.Matched++
				group = "matched"
			case v.Match == MatchExcluded:
				sum.Excluded++
				group = "excluded"
			default:
				sum.Unmatched++
				if v.Match == MatchAmbiguous {
					sum.Ambiguous++
				}
			}
			if f.Match != "" && f.Match != group && !(f.Match == MatchAmbiguous && v.Match == MatchAmbiguous) {
				continue
			}
			if q != "" && !v.contains(q) {
				continue
			}
			if len(out.Items) >= maxHostRows {
				out.Limited = true
				continue
			}
			out.Items = append(out.Items, v)
		}
	})
	return out
}

// HostRef names a host of another source that is the same machine.
type HostRef struct {
	SourceID   string `json:"source_id"`
	SourceName string `json:"source_name"`
	Key        string `json:"key"`
	Host       string `json:"host"`
}

// hostKeys are the names a host is known by, in the forms the matcher compares.
func hostKeys(h model.MonitoringHost) []string {
	var out []string
	for _, v := range append(append([]string{h.Host, h.Name}, h.DNS...), h.IPs...) {
		for _, k := range alert.EventKeys(v) {
			if k != "" && !slices.Contains(out, k) {
				out = append(out, k)
			}
		}
	}
	return out
}

// linkSameMachine notes, for every host without a configuration item, the hosts of other
// sources that share a name or an address with it: one machine seen by several systems needs
// one item, not one per system.
func linkSameMachine(all []HostView) {
	index := map[string][]int{}
	for i, v := range all {
		if v.CI != nil || v.Match == MatchExcluded {
			continue
		}
		for _, k := range hostKeys(v.MonitoringHost) {
			index[k] = append(index[k], i)
		}
	}
	for i := range all {
		v := &all[i]
		if v.CI != nil || v.Match == MatchExcluded {
			continue
		}
		seen := map[int]bool{}
		for _, k := range hostKeys(v.MonitoringHost) {
			for _, j := range index[k] {
				o := all[j]
				if j == i || seen[j] || o.SourceID == v.SourceID {
					continue
				}
				seen[j] = true
				v.AlsoIn = append(v.AlsoIn, HostRef{SourceID: o.SourceID, SourceName: o.SourceName, Key: o.Key, Host: firstSet(o.Name, o.Host)})
			}
		}
	}
}

func (v HostView) contains(q string) bool {
	fields := []string{v.Host, v.Name, v.SourceName}
	fields = append(fields, v.IPs...)
	fields = append(fields, v.DNS...)
	fields = append(fields, v.Groups...)
	if v.CI != nil {
		fields = append(fields, v.CI.Name, v.CI.ID)
	}
	return slices.ContainsFunc(fields, func(x string) bool { return strings.Contains(strings.ToLower(x), q) })
}

// LinkInput links a host by hand. Mode ci links it to CIID, none says it belongs to no item,
// auto goes back to the automatic match.
type LinkInput struct {
	SourceID string `json:"source_id"`
	Key      string `json:"key"`
	Mode     string `json:"mode"`
	CIID     string `json:"ci_id"`
}

func (s *MonitoringService) Link(actor string, in LinkInput) (HostView, error) {
	var err error
	s.st.Write(func(d *store.Data) {
		src := d.MonitoringSources[in.SourceID]
		if src == nil {
			err = ErrNotFound
			return
		}
		i := slices.IndexFunc(src.Hosts, func(h model.MonitoringHost) bool { return h.Key == in.Key })
		if i < 0 {
			err = ErrHostNotFound
			return
		}
		if src.Links == nil {
			src.Links = map[string]string{}
		}
		h := src.Hosts[i]
		detail := src.Name + " / " + h.Host
		switch in.Mode {
		case "ci":
			ci := d.ConfigItems[in.CIID]
			if ci == nil {
				err = invalid("ci_not_found", nil)
				return
			}
			src.Links[h.Key] = ci.ID
			detail += " linked to " + ci.ID + " " + ci.Name
		case "none":
			src.Links[h.Key] = model.HostNoCI
			detail += " belongs to no configuration item"
		case "auto":
			delete(src.Links, h.Key)
			detail += " matched automatically"
		default:
			err = invalid("link_mode", nil)
			return
		}
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "monitoring.link", Object: src.ID, Detail: detail})
	})
	if err != nil {
		return HostView{}, err
	}
	return s.host(in.SourceID, in.Key)
}

func (s *MonitoringService) host(sourceID, key string) (HostView, error) {
	for _, v := range s.Hosts(HostFilter{Source: sourceID}).Items {
		if v.Key == key {
			return v, nil
		}
	}
	return HostView{}, ErrHostNotFound
}

// CreateCIInput makes a configuration item of a host; Register also creates it in NetBox.
type CreateCIInput struct {
	SourceID string `json:"source_id"`
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Register bool   `json:"register"`
}

// CreateCI makes a configuration item named after the technical name of the host with its IP
// addresses and links the host to it.
func (s *MonitoringService) CreateCI(ctx context.Context, actor string, in CreateCIInput) (CIView, error) {
	same, err := s.host(in.SourceID, in.Key)
	if err != nil {
		return CIView{}, err
	}
	return s.createCI(ctx, actor, same, in.Kind, in.Register)
}

// createCI makes the item of a host as read by host, links the host to it and the same
// machine in the other systems too.
func (s *MonitoringService) createCI(ctx context.Context, actor string, same HostView, kind string, register bool) (CIView, error) {
	h := same.MonitoringHost
	if kind == "" {
		kind = model.CIKindDevice
	}
	name := h.Host
	if name == "" {
		name = h.Name
	}
	desc := ""
	if h.Name != "" && h.Name != name {
		desc = h.Name
	}
	ci, err := s.cis.Create(ctx, actor, CIInput{Name: name, Kind: kind, Status: model.CIStatusActive, Description: desc,
		IPs: slices.Clone(h.IPs[:min(len(h.IPs), maxCIIPs)]), Register: register})
	if err != nil {
		return CIView{}, err
	}
	s.st.Write(func(d *store.Data) {
		if src := d.MonitoringSources[same.SourceID]; src != nil && d.ConfigItems[ci.ID] != nil {
			if src.Links == nil {
				src.Links = map[string]string{}
			}
			src.Links[h.Key] = ci.ID
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "monitoring.link", Object: src.ID, Detail: src.Name + " / " + h.Host + " linked to new " + ci.ID})
		}
		// The same machine in the other systems belongs to the same item.
		for _, o := range same.AlsoIn {
			if src := d.MonitoringSources[o.SourceID]; src != nil && d.ConfigItems[ci.ID] != nil {
				if src.Links == nil {
					src.Links = map[string]string{}
				}
				src.Links[o.Key] = ci.ID
				d.AddAudit(store.AuditEntry{Actor: actor, Action: "monitoring.link", Object: src.ID, Detail: src.Name + " / " + o.Host + " linked to new " + ci.ID})
			}
		}
	})
	return s.cis.Get(ci.ID)
}

// dropHostLinks forgets the hand-made links to a deleted configuration item.
func dropHostLinks(d *store.Data, id string) {
	for _, src := range d.MonitoringSources {
		for k, v := range src.Links {
			if v == id {
				delete(src.Links, k)
			}
		}
	}
}

const (
	PresenceNetBox    = "netbox"
	PresenceInventory = "inventory-db"
	PresenceDirectory = "directory"

	Present = "present"
	Missing = "missing"
)

// CIPresence says whether a system knows the configuration item: NetBox, the domain
// controller and every turned-on monitoring system.
type CIPresence struct {
	Kind      string `json:"kind"`
	SourceID  string `json:"source_id,omitempty"`
	Name      string `json:"name"`
	State     string `json:"state"`
	Detail    string `json:"detail,omitempty"`
	HostState string `json:"host_state,omitempty"`
	URL       string `json:"url,omitempty"`
}

var hostStateRank = map[string]int{model.HostDisabled: 0, model.HostUnknown: 1, model.HostUp: 2, model.HostPartial: 3, model.HostDown: 4}

// presence lists the systems the item is in and those it is missing from. NetBox counts when it
// is connected or the item is linked there; the domain controller when the item was checked.
func presence(d *store.Data, ci *model.ConfigItem, mons []CIMonitor, sources []*model.MonitoringSource) []CIPresence {
	out := []CIPresence{}
	if ci.NetBox != nil {
		out = append(out, CIPresence{Kind: PresenceNetBox, Name: "NetBox", State: Present, Detail: ci.NetBox.Kind + " #" + strconv.Itoa(ci.NetBox.ID), URL: ci.NetBox.URL})
	} else if d.Settings.NetBox.Enabled {
		out = append(out, CIPresence{Kind: PresenceNetBox, Name: "NetBox", State: Missing})
	}
	if ref := ci.InventoryDB; ref != nil {
		out = append(out, CIPresence{Kind: PresenceInventory, Name: "Inventory DB", State: Present, Detail: "device #" + strconv.Itoa(ref.ID), URL: ref.URL})
	} else if d.Settings.InventoryDB.Enabled && d.Settings.InventoryDB.ImportDevices && (ci.Kind == model.CIKindDevice) {
		out = append(out, CIPresence{Kind: PresenceInventory, Name: "Inventory DB", State: Missing})
	}
	if dir := ci.Directory; dir != nil {
		p := CIPresence{Kind: PresenceDirectory, Name: "Active Directory", State: Missing}
		if dir.Status == model.DirectoryMatched {
			p.State, p.Detail = Present, firstSet(dir.DNSName, dir.DN)
			if dir.Disabled {
				p.HostState = model.HostDisabled
			}
		}
		out = append(out, p)
	}
	for _, src := range sources {
		p := CIPresence{Kind: src.Kind, SourceID: src.ID, Name: src.Name, State: Missing}
		var names []string
		for _, m := range mons {
			if m.SourceID != src.ID {
				continue
			}
			p.State = Present
			names = append(names, firstSet(m.Name, m.Host))
			if p.URL == "" {
				p.URL = m.URL
			}
			if p.HostState == "" || hostStateRank[m.State] > hostStateRank[p.HostState] {
				p.HostState = m.State
			}
		}
		p.Detail = strings.Join(names, ", ")
		out = append(out, p)
	}
	return out
}

func enabledSourceList(d *store.Data) []*model.MonitoringSource {
	var out []*model.MonitoringSource
	for _, s := range sortedSources(d) {
		if s.Enabled {
			out = append(out, s)
		}
	}
	return out
}
