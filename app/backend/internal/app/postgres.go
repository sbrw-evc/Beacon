package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/config"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

var errSwitchUnavailable = invalid("switch_unavailable", nil)

type PostgresDatabase interface {
	Database
	Config() store.PGConfig
	Stats(ctx context.Context) (store.PGStats, error)
	Ping(ctx context.Context) (time.Duration, error)
	Pool() *pgxpool.Pool
}

type PostgresSecrets interface {
	Resolve(ref string) (string, error)
	Put(ctx context.Context, path string, values map[string]string) error
	Ref(path, key string) string
}

type PostgresService struct {
	st      *store.Store
	db      PostgresDatabase
	vault   PostgresSecrets
	runtime Runtime
	cfg     config.File
}

func NewPostgresService(st *store.Store, db PostgresDatabase, vault PostgresSecrets, runtime Runtime, cfg config.File) *PostgresService {
	return &PostgresService{st: st, db: db, vault: vault, runtime: runtime, cfg: cfg}
}

type PostgresOverview struct {
	Connection store.PGConfig `json:"connection"`
	PostgresStatus
}

func (s *PostgresService) Overview(ctx context.Context) PostgresOverview {
	out := PostgresOverview{Connection: s.db.Config(), PostgresStatus: PostgresStatus{Where: s.db.Where(), Persist: s.st.PersistStatus()}}
	info, err := s.db.Info(ctx)
	out.Info, out.OK = info, err == nil
	if err != nil {
		out.Error = err.Error()
	}
	return out
}

type PostgresCheck struct {
	OK        bool   `json:"ok"`
	Version   string `json:"version,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

func (s *PostgresService) Test(ctx context.Context) PostgresCheck {
	latency, err := s.db.Ping(ctx)
	if err != nil {
		return PostgresCheck{Error: err.Error()}
	}
	info, err := s.db.Info(ctx)
	if err != nil {
		return PostgresCheck{Error: err.Error()}
	}
	return PostgresCheck{OK: true, Version: info.Version, LatencyMS: latency.Milliseconds()}
}

func (s *PostgresService) Stats(ctx context.Context) (store.PGStats, error) {
	return s.db.Stats(ctx)
}

type PostgresProbe struct {
	OK    bool          `json:"ok"`
	Probe store.PGProbe `json:"probe"`
	Error string        `json:"error,omitempty"`
}

func (s *PostgresService) Probe(ctx context.Context, target config.PostgresTarget) PostgresProbe {
	probe, err := probeTarget(ctx, target.Config())
	rep := PostgresProbe{OK: err == nil, Probe: probe}
	if err != nil {
		rep.Error = err.Error()
	}
	return rep
}

// probeTarget is store.ProbePostgres where alerts or received requests left without a snapshot
// (a database Umbrella ran on before) also count as Umbrella data that a switch must not merge into.
func probeTarget(ctx context.Context, cfg store.PGConfig) (store.PGProbe, error) {
	probe, err := store.ProbePostgres(ctx, cfg)
	if err != nil || probe.HasState {
		return probe, err
	}
	cfg, _ = cfg.Normalize()
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := pgx.Connect(cctx, cfg.DSN())
	if err != nil {
		return probe, fmt.Errorf("PostgreSQL %s: %w", cfg.Where(), err)
	}
	defer conn.Close(context.Background())
	if probe.HasState, err = umbrellaData(cctx, conn); err != nil {
		return probe, fmt.Errorf("PostgreSQL %s: %w", cfg.Where(), err)
	}
	return probe, nil
}

type PostgresMigration struct {
	Target    config.PostgresTarget `json:"target"`
	Overwrite bool                  `json:"overwrite"`
}

type PostgresMigrated struct {
	Where string `json:"where"`
}

func (s *PostgresService) Migrate(ctx context.Context, actor string, in PostgresMigration) (PostgresMigrated, error) {
	if s.runtime == nil {
		return PostgresMigrated{}, errSwitchUnavailable
	}
	target, err := in.Target.Config().Normalize()
	if err != nil {
		return PostgresMigrated{}, invalid("postgres_invalid", err)
	}
	if sameDatabase(s.db.Config(), target) {
		return PostgresMigrated{}, invalid("postgres_same_database", nil)
	}
	probe, err := probeTarget(ctx, target)
	if err != nil {
		return PostgresMigrated{}, invalid("postgres_unavailable", err)
	}
	if probe.HasState && !in.Overwrite {
		return PostgresMigrated{}, invalid("postgres_has_state", nil)
	}
	if !probe.CanCreate && !probe.HasState {
		return PostgresMigrated{}, invalid("postgres_no_create", nil)
	}
	from := s.db.Where()
	restoreSecret := func() {}
	err = s.runtime.Switch(ctx, func(ctx context.Context) (config.File, error) {
		next, restore, err := s.transfer(ctx, actor, target, in.Overwrite)
		restoreSecret = restore
		return next, err
	})
	if err != nil {
		restoreSecret()
		s.st.Write(func(d *store.Data) {
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.postgres.migrate_failed", Detail: target.Where() + ": " + err.Error()})
		})
		slog.Error("postgres migration failed", "from", from, "to", target.Where(), "err", err)
		return PostgresMigrated{}, invalid("postgres_migration_failed", err)
	}
	slog.Info("postgres migrated", "actor", actor, "from", from, "to", target.Where())
	return PostgresMigrated{Where: target.Where()}, nil
}

func (s *PostgresService) transfer(ctx context.Context, actor string, target store.PGConfig, overwrite bool) (config.File, func(), error) {
	noop := func() {}
	backend, err := store.OpenPostgres(ctx, target)
	if err != nil {
		return config.File{}, noop, err
	}
	defer backend.Close()
	info, err := backend.Info(ctx)
	if err != nil {
		return config.File{}, noop, err
	}
	if info.HasState && !overwrite {
		return config.File{}, noop, errTargetHasData
	}
	s.st.Write(func(d *store.Data) {
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.postgres.migrate", Detail: s.db.Where() + " -> " + target.Where()})
	})
	data, err := s.st.Export()
	if err != nil {
		return config.File{}, noop, err
	}
	// The workers of the source are stopped (Runtime.Switch), so alerts, their timelines, the
	// link signing key and the intake queue are copied as they are, together with the snapshot.
	err = copyUmbrella(ctx, s.db.Pool(), backend.Pool(), overwrite, func(tx pgx.Tx) error { return store.SaveIn(ctx, tx, data) })
	if err != nil {
		return config.File{}, noop, fmt.Errorf("write the data to %s: %w", target.Where(), err)
	}
	ref, restore, err := s.storePassword(ctx, target.Password)
	if err != nil {
		return config.File{}, noop, fmt.Errorf("save the password to OpenBao: %w", err)
	}
	next := s.cfg
	next.Postgres = target
	next.Postgres.Password = ""
	next.PostgresPasswordRef = ref
	return next, restore, nil
}

func (s *PostgresService) storePassword(ctx context.Context, password string) (string, func(), error) {
	if password == "" {
		return "", func() {}, nil
	}
	ref := s.vault.Ref(config.PostgresSecretPath, config.PostgresSecretKey)
	previous, err := s.vault.Resolve(ref)
	if err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return "", nil, err
	}
	if err := s.vault.Put(ctx, config.PostgresSecretPath, map[string]string{config.PostgresSecretKey: password}); err != nil {
		return "", nil, err
	}
	restore := func() {
		if err := s.vault.Put(context.Background(), config.PostgresSecretPath, map[string]string{config.PostgresSecretKey: previous}); err != nil {
			slog.Error("postgres password in OpenBao not restored", "ref", ref, "err", err)
		}
	}
	return ref, restore, nil
}

func sameDatabase(a, b store.PGConfig) bool {
	return strings.EqualFold(a.Host, b.Host) && a.Port == b.Port && a.Database == b.Database
}
