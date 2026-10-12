package credentials

import (
	"context"
	"errors"
	"fmt"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
)

const hashKey = "password_hash"

var ErrUnavailable = errors.New("password store is unavailable")

type Vault interface {
	Resolve(ref string) (string, error)
	PutRef(ctx context.Context, path, key, value string) (string, error)
}

type Passwords struct {
	vault Vault
}

func NewPasswords(v Vault) *Passwords { return &Passwords{vault: v} }

func Path(userID string) string { return "users/" + userID }

func (p *Passwords) Set(ctx context.Context, userID, password string) (string, error) {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return "", err
	}
	return p.Store(ctx, userID, hash)
}

func (p *Passwords) Store(ctx context.Context, userID, hash string) (string, error) {
	ref, err := p.vault.PutRef(ctx, Path(userID), hashKey, hash)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return ref, nil
}

func (p *Passwords) Verify(ref, password string) (bool, error) {
	if ref == "" {
		auth.CheckPassword("", password)
		return false, nil
	}
	hash, err := p.vault.Resolve(ref)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return auth.CheckPassword(hash, password), nil
}
