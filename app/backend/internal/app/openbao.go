package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/config"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type SecretStore interface {
	Status(ctx context.Context) secrets.Status
	Verify(ctx context.Context) secrets.Check
	List(ctx context.Context) ([]string, error)
	CopyTo(ctx context.Context, dst *secrets.Client) ([]string, error)
}

type OpenBaoService struct {
	st      *store.Store
	vault   SecretStore
	runtime Runtime
	cfg     config.File
}

func NewOpenBaoService(st *store.Store, vault SecretStore, runtime Runtime, cfg config.File) *OpenBaoService {
	return &OpenBaoService{st: st, vault: vault, runtime: runtime, cfg: cfg}
}

type OpenBaoConnection struct {
	Addr        string `json:"addr"`
	Mount       string `json:"mount"`
	Namespace   string `json:"namespace,omitempty"`
	Auth        string `json:"auth"`
	AppRolePath string `json:"approle_path,omitempty"`
	CustomCA    bool   `json:"custom_ca"`
	SkipVerify  bool   `json:"skip_verify"`
}

type OpenBaoOverview struct {
	Connection OpenBaoConnection `json:"connection"`
	Status     secrets.Status    `json:"status"`
	Secrets    int               `json:"secrets"`
	ListError  string            `json:"list_error,omitempty"`
}

func (s *OpenBaoService) Overview(ctx context.Context) OpenBaoOverview {
	o := s.cfg.OpenBao
	out := OpenBaoOverview{
		Connection: OpenBaoConnection{Addr: o.Addr, Mount: o.Mount, Namespace: o.Namespace, Auth: o.Auth, AppRolePath: o.AppRolePath,
			CustomCA: o.CACert != "", SkipVerify: o.SkipVerify},
		Status: s.vault.Status(ctx),
	}
	paths, err := s.vault.List(ctx)
	out.Secrets = len(paths)
	if err != nil {
		out.ListError = err.Error()
	}
	return out
}

func (s *OpenBaoService) Test(ctx context.Context) secrets.Check {
	return s.vault.Verify(ctx)
}

type OpenBaoProbe struct {
	secrets.Check
	KV2Error string `json:"kv2_error,omitempty"`
	Secrets  int    `json:"secrets"`
}

func (s *OpenBaoService) Probe(ctx context.Context, target config.OpenBao) OpenBaoProbe {
	_, rep, _ := s.target(ctx, target)
	return rep
}

func (s *OpenBaoService) target(ctx context.Context, in config.OpenBao) (*secrets.Client, OpenBaoProbe, error) {
	c, check := in.Check(ctx)
	rep := OpenBaoProbe{Check: check}
	if !check.OK {
		return nil, rep, invalid("openbao_unavailable", errors.New(check.Error))
	}
	reject := func(code string, err error) (*secrets.Client, OpenBaoProbe, error) {
		rep.OK, rep.Error = false, err.Error()
		return nil, rep, invalid(code, err)
	}
	if err := c.CheckKV2(ctx); err != nil {
		rep.KV2Error = err.Error()
		return reject("openbao_not_kv2", err)
	}
	existing, err := c.List(ctx)
	if err != nil {
		return reject("openbao_list_failed", err)
	}
	rep.Secrets = len(existing)
	return c, rep, nil
}

type OpenBaoMigration struct {
	Target    config.OpenBao `json:"target"`
	Overwrite bool           `json:"overwrite"`
}

type OpenBaoMigrated struct {
	Addr   string `json:"addr"`
	Mount  string `json:"mount"`
	Copied int    `json:"copied"`
	Refs   int    `json:"refs"`
}

func (s *OpenBaoService) Migrate(ctx context.Context, actor string, in OpenBaoMigration) (OpenBaoMigrated, error) {
	if s.runtime == nil {
		return OpenBaoMigrated{}, errSwitchUnavailable
	}
	target, err := in.Target.Normalize()
	if err != nil {
		return OpenBaoMigrated{}, invalid("openbao_invalid", err)
	}
	if target.SameStore(s.cfg.OpenBao) {
		return OpenBaoMigrated{}, invalid("openbao_same_store", nil)
	}
	dst, probe, err := s.target(ctx, target)
	if err != nil {
		return OpenBaoMigrated{}, err
	}
	if probe.Secrets > 0 && !in.Overwrite {
		return OpenBaoMigrated{}, invalid("openbao_target_not_empty", fmt.Errorf("%d secrets in %s", probe.Secrets, target.Mount))
	}
	if _, err := s.vault.List(ctx); err != nil {
		return OpenBaoMigrated{}, invalid("openbao_list_failed", err)
	}
	m := openbaoMove{st: s.st, src: s.vault, dst: dst, from: s.cfg.OpenBao, to: target, cleanup: probe.Secrets == 0}
	err = s.runtime.Switch(ctx, func(ctx context.Context) (config.File, error) { return m.run(ctx, actor, s.cfg) })
	if err != nil {
		m.rollback()
		s.st.Write(func(d *store.Data) {
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.openbao.migrate_failed", Detail: target.Addr + "/" + target.Mount + ": " + err.Error()})
		})
		slog.Error("openbao migration failed", "to", target.Addr, "mount", target.Mount, "err", err)
		return OpenBaoMigrated{}, invalid("openbao_migration_failed", err)
	}
	slog.Info("openbao migrated", "actor", actor, "from", s.cfg.OpenBao.Addr, "to", target.Addr, "mount", target.Mount,
		"copied", len(m.copied), "refs", m.refs)
	return OpenBaoMigrated{Addr: target.Addr, Mount: target.Mount, Copied: len(m.copied), Refs: m.refs}, nil
}

type openbaoMove struct {
	st       *store.Store
	src      SecretStore
	dst      *secrets.Client
	from, to config.OpenBao
	cleanup  bool
	copied   []string
	refs     int
	undoRefs func()
}

func (m *openbaoMove) moveRef(ref string) (string, bool) {
	return secrets.MoveRef(ref, m.from.Mount, m.to.Mount)
}

func (m *openbaoMove) run(ctx context.Context, actor string, cfg config.File) (config.File, error) {
	copied, err := m.src.CopyTo(ctx, m.dst)
	m.copied = copied
	if err != nil {
		return config.File{}, err
	}
	m.refs, m.undoRefs = m.st.MapRefs(m.moveRef)
	next := cfg
	next.OpenBao = m.to
	if ref, ok := m.moveRef(cfg.PostgresPasswordRef); ok {
		next.PostgresPasswordRef = ref
		m.refs++
	}
	m.st.Write(func(d *store.Data) {
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.openbao.migrate",
			Detail: fmt.Sprintf("%s/%s -> %s/%s, %d secrets, %d references", m.from.Addr, m.from.Mount, m.to.Addr, m.to.Mount, len(copied), m.refs)})
	})
	if err := m.st.Flush(); err != nil {
		return config.File{}, fmt.Errorf("save the new secret references: %w", err)
	}
	return next, nil
}

func (m *openbaoMove) rollback() {
	if m.undoRefs != nil {
		m.undoRefs()
		if err := m.st.Flush(); err != nil {
			slog.Error("secret references not restored in PostgreSQL", "err", err)
		}
	}
	if !m.cleanup {
		return
	}
	for _, p := range m.copied {
		if err := m.dst.Delete(context.Background(), p); err != nil {
			slog.Warn("copied secret not removed from the target OpenBao", "path", p, "err", err)
		}
	}
}
