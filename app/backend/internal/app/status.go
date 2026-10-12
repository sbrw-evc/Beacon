package app

import (
	"context"
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/entra"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/ingest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/logbuf"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// startedAt is when the process started; the setup wizard and later reconfigurations keep it.
var startedAt = time.Now()

const recentLogs = 50

type SessionCounter interface {
	Count() map[string]int
}

type StatusService struct {
	st       *store.Store
	secrets  Secrets
	db       Database
	dir      Directory
	sessions SessionCounter
	queue    *ingest.Queue
	ready    func() bool
	build    BuildInfo
}

func NewStatusService(st *store.Store, secrets Secrets, db Database, dir Directory, sessions SessionCounter, queue *ingest.Queue, ready func() bool, opt Options) *StatusService {
	return &StatusService{st: st, secrets: secrets, db: db, dir: dir, sessions: sessions, queue: queue, ready: ready, build: buildInfo(opt)}
}

type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	BuiltAt   string `json:"built_at,omitempty"`
	Modified  bool   `json:"modified,omitempty"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

// buildInfo prefers what the build passed with -ldflags and falls back to the VCS stamp of go build.
func buildInfo(opt Options) BuildInfo {
	b := BuildInfo{Version: opt.Version, Commit: opt.Commit, BuiltAt: opt.BuiltAt, GoVersion: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if b.Commit == "" {
					b.Commit = s.Value
				}
			case "vcs.time":
				if b.BuiltAt == "" {
					b.BuiltAt = s.Value
				}
			case "vcs.modified":
				b.Modified = s.Value == "true"
			}
		}
	}
	return b
}

type MemoryInfo struct {
	Alloc        uint64     `json:"alloc_bytes"`
	HeapInuse    uint64     `json:"heap_inuse_bytes"`
	Sys          uint64     `json:"sys_bytes"`
	NumGC        uint32     `json:"num_gc"`
	LastGC       *time.Time `json:"last_gc,omitempty"`
	PauseTotalMs int64      `json:"gc_pause_total_ms"`
}

type RuntimeInfo struct {
	StartedAt  time.Time  `json:"started_at"`
	Uptime     int64      `json:"uptime_seconds"`
	Hostname   string     `json:"hostname,omitempty"`
	PID        int        `json:"pid"`
	CPUs       int        `json:"cpus"`
	GOMAXPROCS int        `json:"gomaxprocs"`
	Goroutines int        `json:"goroutines"`
	Memory     MemoryInfo `json:"memory"`
}

func runtimeInfo(now time.Time) RuntimeInfo {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	host, _ := os.Hostname()
	r := RuntimeInfo{
		StartedAt: startedAt, Uptime: int64(now.Sub(startedAt).Seconds()), Hostname: host, PID: os.Getpid(),
		CPUs: runtime.NumCPU(), GOMAXPROCS: runtime.GOMAXPROCS(0), Goroutines: runtime.NumGoroutine(),
		Memory: MemoryInfo{Alloc: m.Alloc, HeapInuse: m.HeapInuse, Sys: m.Sys, NumGC: m.NumGC, PauseTotalMs: int64(m.PauseTotalNs / 1e6)},
	}
	if m.LastGC > 0 {
		t := time.Unix(0, int64(m.LastGC))
		r.Memory.LastGC = &t
	}
	return r
}

type OpenBaoStatus struct {
	secrets.Status
	LatencyMs int64 `json:"latency_ms"`
}

type LDAPStatus struct {
	Enabled    bool   `json:"enabled"`
	Kind       string `json:"kind,omitempty"`
	URL        string `json:"url,omitempty"`
	TLS        string `json:"tls,omitempty"`
	BaseDN     string `json:"base_dn,omitempty"`
	AdminGroup string `json:"admin_group,omitempty"`
	OK         bool   `json:"ok"`
	LatencyMs  int64  `json:"latency_ms"`
	Error      string `json:"error,omitempty"`
}

type EntraStatus struct {
	Enabled      bool   `json:"enabled"`
	Cloud        string `json:"cloud,omitempty"`
	TenantID     string `json:"tenant_id,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	RedirectURL  string `json:"redirect_url,omitempty"`
	AdminGroupID string `json:"admin_group_id,omitempty"`
	UserGroupID  string `json:"user_group_id,omitempty"`
	Issuer       string `json:"issuer,omitempty"`
	Credentials  bool   `json:"credentials"`
	OK           bool   `json:"ok"`
	LatencyMs    int64  `json:"latency_ms"`
	Error        string `json:"error,omitempty"`
}

type PostgresStatus struct {
	Where     string              `json:"where"`
	OK        bool                `json:"ok"`
	LatencyMs int64               `json:"latency_ms"`
	Info      store.PGInfo        `json:"info"`
	Health    *store.PGHealth     `json:"health,omitempty"`
	Persist   store.PersistStatus `json:"persist"`
	Error     string              `json:"error,omitempty"`
}

type IntakeStatus struct {
	Ready      bool             `json:"ready"`
	Connectors int              `json:"connectors"`
	Published  int              `json:"published"`
	Invalid    int              `json:"invalid"`
	Stats      *ingest.Overview `json:"stats,omitempty"`
	Error      string           `json:"error,omitempty"`
}

type SettingsStatus struct {
	defaultsView
	SetupAt time.Time `json:"setup_at"`
	SetupBy string    `json:"setup_by"`
}

type Inventory struct {
	Users       map[string]int `json:"users"`
	Disabled    int            `json:"disabled_users"`
	Sessions    int            `json:"sessions"`
	SignedIn    int            `json:"signed_in_users"`
	Roles       int            `json:"roles"`
	Teams       int            `json:"teams"`
	Services    int            `json:"services"`
	Credentials int            `json:"credentials"`
	AuditKept   int            `json:"audit_entries"`
}

type LogsStatus struct {
	Counts map[string]int `json:"counts"`
	Recent []logbuf.Entry `json:"recent"`
}

type SystemStatus struct {
	CheckedAt time.Time      `json:"checked_at"`
	Version   string         `json:"version"`
	Build     BuildInfo      `json:"build"`
	Runtime   RuntimeInfo    `json:"runtime"`
	OpenBao   OpenBaoStatus  `json:"openbao"`
	Postgres  PostgresStatus `json:"postgres"`
	LDAP      LDAPStatus     `json:"ldap"`
	Entra     EntraStatus    `json:"entra"`
	Intake    IntakeStatus   `json:"intake"`
	Settings  SettingsStatus `json:"settings"`
	Users     map[string]int `json:"users"`
	Inventory Inventory      `json:"inventory"`
	Logs      LogsStatus     `json:"logs"`
}

func since(start time.Time) int64 { return time.Since(start).Milliseconds() }

func (s *StatusService) Collect(ctx context.Context) SystemStatus {
	now := time.Now()
	var settings model.Settings
	out := SystemStatus{CheckedAt: now, Version: s.build.Version, Build: s.build, Runtime: runtimeInfo(now), Users: map[string]int{}}
	s.st.Read(func(d *store.Data) {
		settings = d.Settings
		inv := Inventory{Users: out.Users, Roles: len(d.Roles), Teams: len(d.Teams), Services: len(d.Services), Credentials: len(d.Credentials), AuditKept: len(d.Audit)}
		for _, u := range d.Users {
			inv.Users[u.Source]++
			if u.Disabled {
				inv.Disabled++
			}
		}
		out.Inventory = inv
		out.Intake.Connectors = len(d.Connectors)
		for _, c := range d.Connectors {
			if c.Published > 0 {
				out.Intake.Published++
			}
			if c.PublishError != "" {
				out.Intake.Invalid++
			}
		}
	})
	if s.sessions != nil {
		for _, n := range s.sessions.Count() {
			out.Inventory.Sessions += n
			out.Inventory.SignedIn++
		}
	}
	out.Settings = SettingsStatus{defaultsView: defaultsOf(settings), SetupAt: settings.SetupAt, SetupBy: settings.SetupBy}
	out.Postgres = PostgresStatus{Persist: s.st.PersistStatus()}
	out.LDAP = LDAPStatus{Enabled: settings.LDAP.Enabled}
	out.Logs = LogsStatus{Counts: logbuf.Counts(), Recent: logbuf.Recent(recentLogs)}
	out.Intake.Ready = s.ready != nil && s.ready()

	var wg sync.WaitGroup
	if s.secrets != nil {
		wg.Go(func() {
			start := time.Now()
			out.OpenBao.Status = s.secrets.Status(ctx)
			out.OpenBao.LatencyMs = since(start)
		})
	} else {
		out.OpenBao.Error = secrets.ErrNotConfigured.Error()
	}
	if s.db != nil {
		out.Postgres.Where = s.db.Where()
		wg.Go(func() {
			start := time.Now()
			info, err := s.db.Info(ctx)
			out.Postgres.LatencyMs = since(start)
			out.Postgres.Info, out.Postgres.OK = info, err == nil
			if err != nil {
				out.Postgres.Error = err.Error()
				return
			}
			if h, err := s.db.Health(ctx); err == nil {
				out.Postgres.Health = &h
			}
		})
	} else {
		out.Postgres.Error = "PostgreSQL is not connected"
	}
	if s.queue != nil && out.Intake.Ready {
		wg.Go(func() {
			o, err := s.queue.Overview(ctx)
			if err != nil {
				out.Intake.Error = err.Error()
				return
			}
			out.Intake.Stats = &o
		})
	}
	if settings.LDAP.Enabled {
		l := settings.LDAP
		out.LDAP.Kind, out.LDAP.URL, out.LDAP.TLS, out.LDAP.BaseDN, out.LDAP.AdminGroup = l.Kind, l.URL, l.TLSMode(), l.BaseDN, l.AdminGroupDN
		wg.Go(func() {
			start := time.Now()
			pw, err := s.resolve(l.BindPasswordRef)
			if err == nil {
				_, err = s.dir.Test(l, pw, "", "")
			}
			out.LDAP.LatencyMs = since(start)
			out.LDAP.OK = err == nil
			if err != nil {
				out.LDAP.Error = err.Error()
			}
		})
	}
	if e := settings.Entra; e.Enabled {
		out.Entra = EntraStatus{Enabled: true, Cloud: e.Cloud, TenantID: e.TenantID, ClientID: e.ClientID, RedirectURL: e.RedirectURL, AdminGroupID: e.AdminGroupID, UserGroupID: e.UserGroupID}
		wg.Go(func() {
			start := time.Now()
			secret, err := s.resolve(e.ClientSecretRef)
			var p entra.Probe
			if err == nil {
				p, err = entra.Test(ctx, e, secret)
			}
			out.Entra.LatencyMs = since(start)
			out.Entra.Issuer, out.Entra.Credentials, out.Entra.OK = p.Issuer, p.Credentials, err == nil
			if err != nil {
				out.Entra.Error = err.Error()
			}
		})
	}
	wg.Wait()
	return out
}

func (s *StatusService) resolve(ref string) (string, error) {
	if s.secrets == nil {
		return "", secrets.ErrNotConfigured
	}
	return s.secrets.Resolve(ref)
}
