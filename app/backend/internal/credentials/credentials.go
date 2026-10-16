// Package credentials holds what the secret store of Beacon shares with its callers.
// Passwords of people are not kept any more: people sign in through Keystone.
package credentials

import (
	"context"
	"errors"
)

// ErrUnavailable: OpenBao is not configured or refuses the request.
var ErrUnavailable = errors.New("the secret store is unavailable")

// Vault is the part of OpenBao the migrations use.
type Vault interface {
	Resolve(ref string) (string, error)
	PutRef(ctx context.Context, path, key, value string) (string, error)
	Ref(path, key string) string
}

// Path and HistoryPath are where the password hashes of a user and their history were kept
// before Keystone; they are deleted once (app.Migrate).
func Path(userID string) string        { return "users/" + userID }
func HistoryPath(userID string) string { return "users/" + userID + "/history" }
