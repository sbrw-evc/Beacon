package secrets_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets/secretstest"
)

func TestVerifyCleansUp(t *testing.T) {
	f, c := secretstest.New(t)
	rep := c.Verify(context.Background())
	if !rep.OK || !rep.WriteOK || !rep.Status.Renewable {
		t.Fatalf("verify = %+v", rep)
	}
	if paths := f.Paths(); len(paths) != 0 {
		t.Fatalf("probe secret left behind: %v", paths)
	}
}

func TestListAndCopy(t *testing.T) {
	f, src := secretstest.New(t)
	f.Set("umbrella/postgres", map[string]any{"password": "pg"})
	f.Set("umbrella/users/USR-1", map[string]any{"password_hash": "h1", "n": 7})
	f.Set("umbrella/users/deep/USR-2", map[string]any{"password_hash": "h2"})
	f.Set("other/x", map[string]any{"v": "no"})
	ctx := context.Background()
	paths, err := src.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(paths)
	if want := []string{"postgres", "users/USR-1", "users/deep/USR-2"}; !slices.Equal(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	if err := src.CheckKV2(ctx); err != nil {
		t.Fatal(err)
	}
	g, _ := secretstest.New(t)
	dst, err := secrets.New(secrets.Config{Addr: g.Server.URL, Mount: "moved", RoleID: secretstest.RoleID, SecretID: secretstest.SecretID})
	if err != nil {
		t.Fatal(err)
	}
	copied, err := src.CopyTo(ctx, dst)
	if err != nil || len(copied) != 3 {
		t.Fatalf("copied = %v %v", copied, err)
	}
	if v := g.Get("moved/users/USR-1"); v["password_hash"] != "h1" || v["n"] != float64(7) {
		t.Fatalf("copied secret = %v", v)
	}
	if g.Get("moved/x") != nil || g.Get("umbrella/postgres") != nil {
		t.Fatalf("copy wrote outside the target mount: %v", g.Paths())
	}
}

func TestMoveRef(t *testing.T) {
	cases := []struct{ ref, want string }{
		{"openbao://umbrella/users/USR-1#password_hash", "openbao://kv2/users/USR-1#password_hash"},
		{"openbao://umbrella/ldap", "openbao://kv2/ldap#value"},
		{"openbao://other/ldap#bind_password", "openbao://other/ldap#bind_password"},
		{"", ""},
	}
	for _, c := range cases {
		if got, _ := secrets.MoveRef(c.ref, "umbrella", "kv2"); got != c.want {
			t.Fatalf("MoveRef(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
	if _, moved := secrets.MoveRef("openbao://umbrella/a#b", "umbrella", "umbrella"); moved {
		t.Fatal("a move within the same mount must be a no-op")
	}
}

type realBao struct {
	t     *testing.T
	addr  string
	token string
}

func openbaoFromEnv(t *testing.T) realBao {
	v := os.Getenv("UMBRELLA_TEST_OPENBAO")
	if v == "" {
		t.Skip("UMBRELLA_TEST_OPENBAO is not set (addr|root-token)")
	}
	addr, token, ok := strings.Cut(v, "|")
	if !ok {
		t.Fatal("UMBRELLA_TEST_OPENBAO must be addr|root-token")
	}
	return realBao{t: t, addr: addr, token: token}
}

func (b realBao) call(method, path string, body any) int {
	b.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, b.addr+"/v1/"+path, &buf)
	req.Header.Set("X-Vault-Token", b.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func (b realBao) tempMount(kind map[string]any) string {
	name := "umbrella-t1-" + strings.ToLower(auth.RandomToken("", 4))
	if code := b.call(http.MethodPost, "sys/mounts/"+name, kind); code/100 != 2 {
		b.t.Fatalf("enable %s = %d", name, code)
	}
	b.t.Cleanup(func() { b.call(http.MethodDelete, "sys/mounts/"+name, nil) })
	return name
}

func (b realBao) client(mount string) *secrets.Client {
	c, err := secrets.New(secrets.Config{Addr: b.addr, Mount: mount, Token: b.token})
	if err != nil {
		b.t.Fatal(err)
	}
	return c
}

func TestCopyBetweenRealMounts(t *testing.T) {
	b := openbaoFromEnv(t)
	kv2 := map[string]any{"type": "kv", "options": map[string]string{"version": "2"}}
	src, dst := b.client(b.tempMount(kv2)), b.client(b.tempMount(kv2))
	kv1 := b.client(b.tempMount(map[string]any{"type": "kv", "options": map[string]string{"version": "1"}}))
	ctx := context.Background()
	for _, p := range []string{"postgres", "users/USR-1", "users/a b/USR-2"} {
		if err := src.Put(ctx, p, map[string]string{"k": "v-" + p}); err != nil {
			t.Fatal(err)
		}
	}
	if err := dst.CheckKV2(ctx); err != nil {
		t.Fatal(err)
	}
	if err := kv1.CheckKV2(ctx); err == nil || !strings.Contains(err.Error(), "not KV version 2") {
		t.Fatalf("kv v1 mount accepted: %v", err)
	}
	if err := b.client("umbrella-t1-missing").CheckKV2(ctx); err == nil {
		t.Fatal("missing mount accepted")
	}
	copied, err := src.CopyTo(ctx, dst)
	if err != nil || len(copied) != 3 {
		t.Fatalf("copied = %v %v", copied, err)
	}
	got, err := dst.List(ctx)
	if err != nil || len(got) != 3 {
		t.Fatalf("target list = %v %v", got, err)
	}
	ref := dst.Ref("users/a b/USR-2", "k")
	if v, err := dst.Resolve(ref); err != nil || v != "v-users/a b/USR-2" {
		t.Fatalf("resolve %s = %q %v", ref, v, err)
	}
}
