package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	FileName       = "umbrella.json"
	SetupTokenFile = "setup-token"
	version        = 1

	AuthToken   = "token"
	AuthAppRole = "approle"
)

type OpenBao struct {
	Addr        string `json:"addr"`
	Mount       string `json:"mount"`
	Namespace   string `json:"namespace,omitempty"`
	Auth        string `json:"auth"`
	Token       string `json:"token,omitempty"`
	RoleID      string `json:"role_id,omitempty"`
	SecretID    string `json:"secret_id,omitempty"`
	AppRolePath string `json:"approle_path,omitempty"`
	CACert      string `json:"ca_cert,omitempty"`
	SkipVerify  bool   `json:"skip_verify,omitempty"`
}

type File struct {
	Version             int            `json:"version"`
	OpenBao             OpenBao        `json:"openbao"`
	Postgres            store.PGConfig `json:"postgres"`
	PostgresPasswordRef string         `json:"postgres_password_ref"`
	CompletedAt         *time.Time     `json:"completed_at,omitempty"`
}

func (o OpenBao) Normalize() (OpenBao, error) {
	o.Addr = strings.TrimRight(strings.TrimSpace(o.Addr), "/")
	o.Mount = strings.Trim(strings.TrimSpace(o.Mount), "/")
	o.Namespace = strings.Trim(strings.TrimSpace(o.Namespace), "/")
	o.AppRolePath = strings.Trim(strings.TrimSpace(o.AppRolePath), "/")
	o.Token = strings.TrimSpace(o.Token)
	o.RoleID = strings.TrimSpace(o.RoleID)
	o.SecretID = strings.TrimSpace(o.SecretID)
	o.CACert = strings.TrimSpace(o.CACert)
	if o.Mount == "" {
		o.Mount = "umbrella"
	}
	u, err := url.Parse(o.Addr)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return o, errors.New("OpenBao address must look like https://openbao.example:8200")
	}
	switch o.Auth {
	case AuthToken:
		if o.Token == "" {
			return o, errors.New("OpenBao token is required")
		}
		o.RoleID, o.SecretID, o.AppRolePath = "", "", ""
	case AuthAppRole:
		if o.RoleID == "" || o.SecretID == "" {
			return o, errors.New("AppRole role_id and secret_id are required")
		}
		if o.AppRolePath == "" {
			o.AppRolePath = "approle"
		}
		o.Token = ""
	default:
		return o, errors.New("OpenBao sign-in method must be token or approle")
	}
	return o, nil
}

func (o OpenBao) Client() (*secrets.Client, error) {
	return secrets.New(secrets.Config{Addr: o.Addr, Mount: o.Mount, Namespace: o.Namespace, Token: o.Token,
		RoleID: o.RoleID, SecretID: o.SecretID, AppRolePath: o.AppRolePath, CACertPEM: o.CACert,
		InsecureSkipVerify: o.SkipVerify})
}

func Read(dir string) (File, bool, error) {
	var f File
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return f, false, nil
	}
	if err != nil {
		return f, false, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, false, fmt.Errorf("%s: %w", FileName, err)
	}
	if f.Version != version {
		return f, false, fmt.Errorf("%s: unsupported version %d", FileName, f.Version)
	}
	return f, true, nil
}

func Write(dir string, f File) error {
	f.Version = version
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return store.WriteFileAtomic(filepath.Join(dir, FileName), append(b, '\n'))
}
