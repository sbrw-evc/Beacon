package credentials_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/credentials"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets/secretstest"
)

func TestPasswords(t *testing.T) {
	auth.Iterations = 1000
	bao, vault := secretstest.New(t)
	p := credentials.NewPasswords(vault)
	ref, err := p.Set(context.Background(), "USR-7", "Secret-pass-1")
	if err != nil || ref != "openbao://umbrella/users/USR-7#password_hash" {
		t.Fatalf("ref = %q, %v", ref, err)
	}
	if ok, err := p.Verify(ref, "Secret-pass-1"); !ok || err != nil {
		t.Fatalf("verify = %v, %v", ok, err)
	}
	if ok, _ := p.Verify(ref, "wrong"); ok {
		t.Fatal("wrong password accepted")
	}
	if ok, _ := p.Verify("", "anything"); ok {
		t.Fatal("empty reference accepted")
	}
	bao.Sealed = true
	if _, err := p.Verify("openbao://umbrella/users/USR-8#password_hash", "x"); !errors.Is(err, credentials.ErrUnavailable) {
		t.Fatalf("sealed store error = %v", err)
	}
}
