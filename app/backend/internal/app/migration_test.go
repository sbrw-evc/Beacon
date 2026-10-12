package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/config"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets/secretstest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store/storetest"
)

type recordingRuntime struct {
	next  config.File
	calls int
	fail  error
}

func (r *recordingRuntime) Switch(ctx context.Context, transfer func(ctx context.Context) (config.File, error)) error {
	r.calls++
	next, err := transfer(ctx)
	if err != nil {
		return err
	}
	if r.fail != nil {
		return r.fail
	}
	r.next = next
	return nil
}

func inputCode(err error) string {
	var ie *app.InputError
	if errors.As(err, &ie) {
		return ie.Code
	}
	return ""
}

func target(c store.PGConfig) config.PostgresTarget {
	return config.PostgresTarget{Host: c.Host, Port: c.Port, Database: c.Database, User: c.User, Password: c.Password, SSLMode: c.SSLMode}
}

func TestPostgresMigration(t *testing.T) {
	ctx := context.Background()
	src, dst := storetest.TempDatabase(t), storetest.TempDatabase(t)
	bao, vault := secretstest.New(t)
	bao.Set("umbrella/postgres", map[string]any{"password": "source-secret"})

	backend, err := store.OpenPostgres(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	st := store.New()
	if _, err := st.Attach(ctx, backend); err != nil {
		t.Fatal(err)
	}
	st.Write(func(d *store.Data) {
		d.Users["USR-1"] = &model.User{ID: "USR-1", Username: "admin", Role: model.RoleAdmin, PasswordRef: "openbao://umbrella/users/USR-1#password_hash"}
	})
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	rt := &recordingRuntime{}
	cfg := config.File{Postgres: backend.Config(), PostgresPasswordRef: "openbao://umbrella/postgres#password"}
	svc := app.NewPostgresService(st, backend, vault, rt, cfg)

	if probe := svc.Probe(ctx, target(dst)); !probe.OK || probe.Probe.HasState || !probe.Probe.CanCreate {
		t.Fatalf("probe = %+v", probe)
	}
	if _, err := svc.Migrate(ctx, "admin", app.PostgresMigration{Target: target(src)}); inputCode(err) != "postgres_same_database" {
		t.Fatalf("same database = %v", err)
	}
	out, err := svc.Migrate(ctx, "admin", app.PostgresMigration{Target: target(dst)})
	if err != nil {
		t.Fatal(err)
	}
	if out.Where != dst.Where() || rt.next.Postgres.Database != dst.Database || rt.next.Postgres.Password != "" ||
		rt.next.PostgresPasswordRef != "openbao://umbrella/postgres#password" {
		t.Fatalf("migrated = %+v, next config = %+v", out, rt.next)
	}
	if got := bao.Get("umbrella/postgres")["password"]; got != dst.Password {
		t.Fatalf("password in OpenBao = %v", got)
	}

	moved, err := store.OpenPostgres(ctx, dst)
	if err != nil {
		t.Fatal(err)
	}
	defer moved.Close()
	movedStore := store.New()
	if restored, err := movedStore.Attach(ctx, moved); err != nil || !restored {
		t.Fatalf("attach target = %v %v", restored, err)
	}
	movedStore.Read(func(d *store.Data) {
		if u := d.Users["USR-1"]; u == nil || u.PasswordRef != "openbao://umbrella/users/USR-1#password_hash" {
			t.Fatalf("users in target = %+v", d.Users)
		}
		if last := d.Audit[len(d.Audit)-1]; last.Action != "settings.postgres.migrate" {
			t.Fatalf("last audit = %+v", last)
		}
	})

	if _, err := svc.Migrate(ctx, "admin", app.PostgresMigration{Target: target(dst)}); inputCode(err) != "postgres_has_state" {
		t.Fatalf("target with state = %v", err)
	}
	if err := vault.Put(ctx, "postgres", map[string]string{"password": "source-secret"}); err != nil {
		t.Fatal(err)
	}
	rt.fail = errors.New("open failed")
	if _, err := svc.Migrate(ctx, "admin", app.PostgresMigration{Target: target(dst), Overwrite: true}); inputCode(err) != "postgres_migration_failed" {
		t.Fatalf("failed switch = %v", err)
	}
	if got := bao.Get("umbrella/postgres")["password"]; got != "source-secret" {
		t.Fatalf("password must be restored after a failed switch, got %v", got)
	}
}

func TestPostgresMigrationNeedsRuntime(t *testing.T) {
	svc := app.NewPostgresService(store.New(), nil, nil, nil, config.File{})
	if _, err := svc.Migrate(context.Background(), "admin", app.PostgresMigration{}); inputCode(err) != "switch_unavailable" {
		t.Fatalf("err = %v", err)
	}
}

func baoConfig(f *secretstest.Fake, mount string) config.OpenBao {
	return config.OpenBao{Addr: f.Server.URL, Mount: mount, Auth: config.AuthAppRole, RoleID: secretstest.RoleID, SecretID: secretstest.SecretID}
}

func TestOpenBaoMigration(t *testing.T) {
	ctx := context.Background()
	src, _ := secretstest.New(t)
	src.Set("umbrella/users/USR-1", map[string]any{"password_hash": "h1"})
	src.Set("umbrella/ldap", map[string]any{"bind_password": "bind"})
	src.Set("umbrella/postgres", map[string]any{"password": "pg"})
	cfg := config.File{OpenBao: baoConfig(src, "umbrella"), PostgresPasswordRef: "openbao://umbrella/postgres#password"}
	cfg.OpenBao, _ = cfg.OpenBao.Normalize()
	vault, err := cfg.OpenBao.Client()
	if err != nil {
		t.Fatal(err)
	}
	st := store.New()
	st.Write(func(d *store.Data) {
		d.Settings.LDAP.BindPasswordRef = "openbao://umbrella/ldap#bind_password"
		d.Users["USR-1"] = &model.User{ID: "USR-1", Username: "admin", PasswordRef: "openbao://umbrella/users/USR-1#password_hash"}
		d.Users["USR-2"] = &model.User{ID: "USR-2", Username: "anna", Source: model.SourceLDAP}
	})
	rt := &recordingRuntime{}
	svc := app.NewOpenBaoService(st, vault, rt, cfg)

	if ov := svc.Overview(ctx); ov.Secrets != 3 || !ov.Status.TokenOK || ov.Connection.Mount != "umbrella" {
		t.Fatalf("overview = %+v", ov)
	}
	if rep := svc.Test(ctx); !rep.OK {
		t.Fatalf("test = %+v", rep)
	}
	if _, err := svc.Migrate(ctx, "admin", app.OpenBaoMigration{Target: cfg.OpenBao}); inputCode(err) != "openbao_same_store" {
		t.Fatalf("same store = %v", err)
	}
	bad := baoConfig(src, "kv2")
	bad.SecretID = "wrong"
	if probe := svc.Probe(ctx, bad); probe.OK || probe.Error == "" {
		t.Fatalf("bad credentials probe = %+v", probe)
	}

	failing, _ := secretstest.New(t)
	rt.fail = errors.New("open failed")
	if _, err := svc.Migrate(ctx, "admin", app.OpenBaoMigration{Target: baoConfig(failing, "kv2")}); inputCode(err) != "openbao_migration_failed" {
		t.Fatalf("failed switch = %v", err)
	}
	if paths := failing.Paths(); len(paths) != 0 {
		t.Fatalf("copied secrets must be removed after a failed switch: %v", paths)
	}
	st.Read(func(d *store.Data) {
		if d.Users["USR-1"].PasswordRef != "openbao://umbrella/users/USR-1#password_hash" || d.Settings.LDAP.BindPasswordRef != "openbao://umbrella/ldap#bind_password" {
			t.Fatal("references must be restored after a failed switch")
		}
	})

	dst, _ := secretstest.New(t)
	rt.fail = nil
	if probe := svc.Probe(ctx, baoConfig(dst, "kv2")); !probe.OK || probe.Secrets != 0 {
		t.Fatalf("probe = %+v", probe)
	}
	out, err := svc.Migrate(ctx, "admin", app.OpenBaoMigration{Target: baoConfig(dst, "kv2")})
	if err != nil {
		t.Fatal(err)
	}
	if out.Copied != 3 || out.Refs != 3 || out.Mount != "kv2" {
		t.Fatalf("migrated = %+v", out)
	}
	if rt.next.OpenBao.Addr != dst.Server.URL || rt.next.OpenBao.Mount != "kv2" || rt.next.PostgresPasswordRef != "openbao://kv2/postgres#password" {
		t.Fatalf("next config = %+v", rt.next)
	}
	if dst.Get("kv2/users/USR-1")["password_hash"] != "h1" || dst.Get("kv2/ldap")["bind_password"] != "bind" {
		t.Fatalf("target secrets = %v", dst.Paths())
	}
	if src.Get("umbrella/users/USR-1") == nil {
		t.Fatal("the source must stay untouched")
	}
	st.Read(func(d *store.Data) {
		if d.Users["USR-1"].PasswordRef != "openbao://kv2/users/USR-1#password_hash" || d.Settings.LDAP.BindPasswordRef != "openbao://kv2/ldap#bind_password" ||
			d.Users["USR-2"].PasswordRef != "" {
			t.Fatalf("references after migration: %+v %+v", d.Settings.LDAP, d.Users["USR-1"])
		}
	})
	if _, err := svc.Migrate(ctx, "admin", app.OpenBaoMigration{Target: baoConfig(dst, "kv2")}); inputCode(err) != "openbao_target_not_empty" {
		t.Fatalf("non-empty target = %v", err)
	}
}
