package storetest

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const EnvPostgres = "UMBRELLA_TEST_POSTGRES"

func Server(t *testing.T) store.PGConfig {
	t.Helper()
	v := os.Getenv(EnvPostgres)
	if v == "" {
		t.Skip(EnvPostgres + " is not set (host:port:user:password:database)")
	}
	p := strings.Split(v, ":")
	if len(p) != 5 {
		t.Fatal(EnvPostgres + " must be host:port:user:password:database")
	}
	port, _ := strconv.Atoi(p[1])
	return store.PGConfig{Host: p[0], Port: port, User: p[2], Password: p[3], Database: p[4], SSLMode: "disable"}
}

func TempDatabase(t *testing.T) store.PGConfig {
	t.Helper()
	admin := Server(t)
	name := "umbrella_t1_" + strings.ToLower(auth.RandomToken("", 6))
	name = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, name)
	exec(t, admin, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	t.Cleanup(func() { exec(t, admin, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)") })
	cfg := admin
	cfg.Database = name
	return cfg
}

func exec(t *testing.T, cfg store.PGConfig, sql string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, cfg.DSN())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
}
