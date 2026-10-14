package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
)

const (
	hashKey    = "password_hash"
	historyKey = "hashes"
)

var ErrUnavailable = errors.New("password store is unavailable")

type Vault interface {
	Resolve(ref string) (string, error)
	PutRef(ctx context.Context, path, key, value string) (string, error)
	Ref(path, key string) string
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

// HistoryPath keeps the hashes of the previous passwords of a user, newest first.
func HistoryPath(userID string) string { return "users/" + userID + "/history" }

// ErrReused: the new password is the current one or one of the remembered previous ones.
var ErrReused = errors.New("the password was used before")

// Replace sets a new password and remembers the previous hash, keeping keep of them. With check
// the new password is refused (ErrReused) when it matches the current one or a remembered one.
func (p *Passwords) Replace(ctx context.Context, userID, currentRef, password string, keep int, check bool) (string, error) {
	var current string
	if currentRef != "" {
		h, err := p.vault.Resolve(currentRef)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		current = h
	}
	history := p.history(userID)
	if check && keep > 0 {
		for _, h := range append([]string{current}, history...)[:min(keep, len(history)+1)] {
			if h != "" && auth.CheckPassword(h, password) {
				return "", ErrReused
			}
		}
	}
	ref, err := p.Set(ctx, userID, password)
	if err != nil {
		return "", err
	}
	if keep > 0 && current != "" {
		history = append([]string{current}, history...)
		if len(history) > keep {
			history = history[:keep]
		}
		b, _ := json.Marshal(history)
		if _, err := p.vault.PutRef(ctx, HistoryPath(userID), historyKey, string(b)); err != nil {
			return ref, fmt.Errorf("%w: password history: %v", ErrUnavailable, err)
		}
	}
	return ref, nil
}

func (p *Passwords) history(userID string) []string {
	raw, err := p.vault.Resolve(p.vault.Ref(HistoryPath(userID), historyKey))
	if err != nil || raw == "" {
		return nil
	}
	var out []string
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}
