package secrets_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets/secretstest"
)

func TestPutResolveDelete(t *testing.T) {
	f, c := secretstest.New(t)
	ctx := context.Background()
	ref, err := c.PutRef(ctx, "notify/oncall", "routing_key", "R0UT1NG")
	if err != nil {
		t.Fatal(err)
	}
	if ref != "openbao://umbrella/notify/oncall#routing_key" {
		t.Fatalf("ref = %s", ref)
	}
	if _, err := c.PutRef(ctx, "notify/oncall", "api_token", "tok"); err != nil {
		t.Fatal(err)
	}
	if got := f.Get("umbrella/notify/oncall"); got["routing_key"] != "R0UT1NG" || got["api_token"] != "tok" {
		t.Fatalf("stored = %v", got)
	}
	if v, err := c.Resolve(ref); err != nil || v != "R0UT1NG" {
		t.Fatalf("resolve = %q %v", v, err)
	}
	if _, err := c.Resolve("openbao://umbrella/notify/oncall#missing"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("missing key err = %v", err)
	}
	if err := c.Delete(ctx, "notify/oncall"); err != nil {
		t.Fatal(err)
	}
	if f.Get("umbrella/notify/oncall") != nil {
		t.Fatal("secret not deleted")
	}
	if _, err := c.Resolve(ref); err == nil {
		t.Fatal("deleted secret still resolves")
	}
}

func TestStatusAndSeal(t *testing.T) {
	f, c := secretstest.New(t)
	st := c.Status(context.Background())
	if !st.TokenOK || !st.MountOK || st.Auth != "approle" || st.Version != "2.7.1" {
		t.Fatalf("status = %+v", st)
	}
	f.Sealed = true
	if st := c.Status(context.Background()); !st.Sealed || st.Error == "" {
		t.Fatalf("sealed status = %+v", st)
	}
}

func TestParseRef(t *testing.T) {
	m, p, k, err := secrets.ParseRef("openbao://kv/team/zabbix")
	if err != nil || m != "kv" || p != "team/zabbix" || k != "value" {
		t.Fatalf("%s %s %s %v", m, p, k, err)
	}
	for _, bad := range []string{"vault://x/y", "openbao://only", "openbao://m/../x"} {
		if _, _, _, err := secrets.ParseRef(bad); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
}

func TestDisabled(t *testing.T) {
	c, _ := secrets.New(secrets.Config{})
	if _, err := c.Resolve("openbao://umbrella/x#y"); !errors.Is(err, secrets.ErrNotConfigured) {
		t.Fatalf("err = %v", err)
	}
}
