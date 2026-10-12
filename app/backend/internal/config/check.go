package config

import (
	"context"
	"strings"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	PostgresSecretPath = "postgres"
	PostgresSecretKey  = "password"
)

func (o OpenBao) Check(ctx context.Context) (*secrets.Client, secrets.Check) {
	o, err := o.Normalize()
	if err != nil {
		return nil, secrets.Check{Error: err.Error()}
	}
	c, err := o.Client()
	if err != nil {
		return nil, secrets.Check{Error: err.Error()}
	}
	rep := c.Verify(ctx)
	if !rep.OK {
		return nil, rep
	}
	return c, rep
}

func (o OpenBao) SameStore(other OpenBao) bool {
	return strings.EqualFold(o.Addr, other.Addr) && o.Namespace == other.Namespace && o.Mount == other.Mount
}

type PostgresTarget struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password"`
	SSLMode  string `json:"sslmode"`
}

func (p PostgresTarget) Config() store.PGConfig {
	return store.PGConfig{Host: strings.TrimSpace(p.Host), Port: p.Port, Database: strings.TrimSpace(p.Database),
		User: strings.TrimSpace(p.User), SSLMode: p.SSLMode, Password: p.Password}
}
