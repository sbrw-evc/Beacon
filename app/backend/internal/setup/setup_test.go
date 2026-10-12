package setup_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/config"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory/directorytest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets/secretstest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/setup"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const setupCode = "setup-code-for-tests"

func TestMain(m *testing.M) {
	auth.Iterations = 1000
	os.Exit(m.Run())
}

type swap struct{ h atomic.Value }

func (s *swap) set(h http.Handler) { s.h.Store(&h) }
func (s *swap) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	(*s.h.Load().(*http.Handler)).ServeHTTP(w, r)
}

type env struct {
	t      *testing.T
	srv    *httptest.Server
	dir    string
	mod    *setup.Module
	result *setup.Result
	client *http.Client
	csrf   string
}

func newEnv(t *testing.T) *env {
	e := &env{t: t, dir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(e.dir, config.SetupTokenFile), []byte(setupCode), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &swap{}
	e.mod = setup.New(setup.Options{DataDir: e.dir, Token: setupCode, Version: "test"}, func(r setup.Result) {
		e.result = &r
		a := app.New(app.Options{Version: "test"}, app.Deps{Config: r.Config, Vault: r.Vault, Backend: r.Backend,
			Store: r.Store, Sessions: auth.NewSessions()})
		h.set(a.Handler())
	})
	h.set(e.mod.Handler())
	e.srv = httptest.NewServer(h)
	t.Cleanup(func() {
		e.srv.Close()
		if e.result != nil {
			e.result.Backend.Close()
		}
	})
	e.newClient()
	return e
}

func (e *env) newClient() {
	jar, _ := cookiejar.New(nil)
	e.client = &http.Client{Jar: jar}
	e.csrf = ""
}

func (e *env) call(method, path string, body any, headers map[string]string, out any) int {
	e.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func (e *env) raw(method, path string, body []byte, headers map[string]string, out any) int {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func (e *env) image(path string) image.Image {
	e.t.Helper()
	resp, err := e.client.Get(e.srv.URL + path)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" {
		e.t.Fatalf("GET %s = %d %s", path, resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	img, err := jpeg.Decode(resp.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return img
}

func testImage(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 200, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (e *env) setup(path string, body, out any) int {
	return e.call(http.MethodPost, "/api/setup/"+path, body, map[string]string{setup.TokenHeader: setupCode}, out)
}

func (e *env) login(user, pass string) (int, map[string]any) {
	var out map[string]any
	code := e.call(http.MethodPost, "/api/auth/login", map[string]string{"username": user, "password": pass}, nil, &out)
	if s, ok := out["csrf"].(string); ok {
		e.csrf = s
	}
	return code, out
}

func TestSetupCodeIsRequired(t *testing.T) {
	e := newEnv(t)
	var meta map[string]any
	if code := e.call(http.MethodGet, "/api/meta", nil, nil, &meta); code != 200 || meta["mode"] != "setup" {
		t.Fatalf("meta = %d %v", code, meta)
	}
	if code := e.call(http.MethodGet, "/api/auth/me", nil, nil, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("other API during setup = %d", code)
	}
	for i := 0; i < 5; i++ {
		if code := e.call(http.MethodPost, "/api/setup/token", nil, map[string]string{setup.TokenHeader: "wrong"}, nil); code != 401 {
			t.Fatalf("wrong code #%d = %d", i, code)
		}
	}
	if code := e.setup("token", nil, nil); code != http.StatusTooManyRequests {
		t.Fatalf("after 5 failures = %d, want 429", code)
	}
}

type pgEnv struct {
	host     string
	port     int
	user     string
	password string
	database string
}

func postgres(t *testing.T) pgEnv {
	dsn := os.Getenv("UMBRELLA_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("UMBRELLA_TEST_POSTGRES is not set (host:port:user:password:database)")
	}
	p := strings.Split(dsn, ":")
	if len(p) != 5 {
		t.Fatal("UMBRELLA_TEST_POSTGRES must be host:port:user:password:database")
	}
	port, _ := strconv.Atoi(p[1])
	pg := pgEnv{host: p[0], port: port, user: p[2], password: p[3], database: p[4]}
	drop := func() {
		cfg := store.PGConfig{Host: pg.host, Port: pg.port, User: pg.user, Password: pg.password, Database: pg.database, SSLMode: "disable"}
		conn, err := pgx.Connect(context.Background(), cfg.DSN())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close(context.Background())
		if _, err := conn.Exec(context.Background(), "DROP TABLE IF EXISTS umbrella_state"); err != nil {
			t.Fatal(err)
		}
	}
	drop()
	t.Cleanup(drop)
	return pg
}

func (p pgEnv) body() map[string]any {
	return map[string]any{"host": p.host, "port": p.port, "user": p.user, "password": p.password, "database": p.database, "sslmode": "disable"}
}

func directoryServer(t *testing.T) *directorytest.Server {
	return directorytest.Start(t,
		directorytest.Entry{DN: "dc=example,dc=org"},
		directorytest.Entry{DN: "cn=umbrella,dc=example,dc=org", Password: "ldap-bind-secret"},
		directorytest.Entry{DN: "uid=anna,dc=example,dc=org", Password: "anna-pass-1", Attrs: map[string][]string{
			"objectClass": {"inetOrgPerson"}, "uid": {"anna"}, "cn": {"Anna Ivanova"}, "givenName": {"Anna"}, "sn": {"Ivanova"},
			"title": {"Lead engineer"}, "departmentNumber": {"Monitoring"}, "manager": {"uid=boris,dc=example,dc=org"},
			"mail": {"Anna@Example.org"}, "jpegPhoto": {string(testImage(t, 120, 90))}}},
		directorytest.Entry{DN: "uid=boris,dc=example,dc=org", Password: "boris-pass-1", Attrs: map[string][]string{
			"objectClass": {"inetOrgPerson"}, "uid": {"boris"}, "cn": {"Boris Petrov"}}},
		directorytest.Entry{DN: "cn=admins,dc=example,dc=org", Attrs: map[string][]string{"member": {"uid=anna,dc=example,dc=org"}}},
	)
}

func TestWizardEndToEnd(t *testing.T) {
	pg := postgres(t)
	bao, _ := secretstest.New(t)
	ldapSrv := directoryServer(t)
	e := newEnv(t)

	openbao := map[string]any{"addr": bao.Server.URL, "mount": "umbrella", "auth": "approle",
		"role_id": secretstest.RoleID, "secret_id": secretstest.SecretID}
	ldapCfg := map[string]any{"enabled": true, "kind": "openldap", "url": ldapSrv.URL, "bind_dn": "cn=umbrella,dc=example,dc=org",
		"base_dn": "dc=example,dc=org", "admin_group_dn": "cn=admins,dc=example,dc=org", "first_name_attr": "givenName",
		"last_name_attr": "sn", "title_attr": "title", "department_attr": "departmentNumber", "manager_attr": "manager", "photo_attr": "jpegPhoto"}

	var ob map[string]any
	if e.setup("openbao/test", openbao, &ob); ob["ok"] != true || ob["write_ok"] != true {
		t.Fatalf("openbao test = %v", ob)
	}
	for _, p := range bao.Paths() {
		if strings.Contains(p, "setup-probe") {
			t.Fatalf("probe secret left in OpenBao: %s", p)
		}
	}
	var bad map[string]any
	if e.setup("openbao/test", map[string]any{"addr": bao.Server.URL, "auth": "approle", "role_id": "x", "secret_id": "y"}, &bad); bad["ok"] != false {
		t.Fatalf("wrong AppRole must fail: %v", bad)
	}
	var pgr map[string]any
	if e.setup("postgres/test", pg.body(), &pgr); pgr["ok"] != true || pgr["probe"].(map[string]any)["has_state"] != false {
		t.Fatalf("postgres test = %v", pgr)
	}
	var lr map[string]any
	if e.setup("ldap/test", map[string]any{"config": ldapCfg, "bind_password": "ldap-bind-secret", "test_username": "anna", "test_password": "anna-pass-1"}, &lr); lr["ok"] != true {
		t.Fatalf("ldap test = %v", lr)
	}
	if u := lr["probe"].(map[string]any)["user"].(map[string]any); u["manager"] != "Boris Petrov" || u["title"] != "Lead engineer" || u["has_photo"] != true {
		t.Fatalf("ldap probe user = %v", u)
	}

	policy := map[string]any{"min_length": 14, "require_digits": true, "min_digits": 2, "require_special": true, "min_special": 1,
		"require_mixed_case": true, "letters": "latin_cyrillic"}
	complete := map[string]any{"locale": "ru", "theme": "dark", "timezone": "Mars/Olympus", "openbao": openbao, "postgres": pg.body(),
		"ldap":            map[string]any{"config": ldapCfg, "bind_password": "ldap-bind-secret"},
		"password_policy": map[string]any{"min_length": 4},
		"admin":           map[string]any{"username": "admin", "last_name": "Main", "first_name": "Admin", "password": "short"}}
	var res map[string]any
	if code := e.setup("complete", complete, &res); code != 400 || res["error"] != "invalid_timezone" {
		t.Fatalf("unknown timezone = %d %v", code, res)
	}
	complete["timezone"] = "Europe/Moscow"
	complete["public_url"] = "ftp://umbrella.example.org"
	if code := e.setup("complete", complete, &res); code != 400 || res["error"] != "public_url_invalid" {
		t.Fatalf("bad public address = %d %v", code, res)
	}
	complete["public_url"] = " https://umbrella.example.org/ "
	if code := e.setup("complete", complete, &res); code != 400 || res["error"] != "invalid_password_policy" {
		t.Fatalf("inconsistent policy = %d %v", code, res)
	}
	complete["password_policy"] = policy
	if code := e.setup("complete", complete, &res); code != 400 || res["error"] != "weak_password" {
		t.Fatalf("weak password = %d %v", code, res)
	}
	if v := fmt.Sprint(res["violations"]); !strings.Contains(v, "length") || !strings.Contains(v, "digits") || !strings.Contains(v, "special") {
		t.Fatalf("violations = %v", res["violations"])
	}
	complete["admin"] = map[string]any{"username": "admin", "last_name": "Main", "first_name": "Admin", "title": "CTO", "department": "IT",
		"email": "admin@example.org", "password": "Admin-pass-2026"}
	if code := e.setup("complete", complete, &res); code != 200 {
		t.Fatalf("complete = %d %v", code, res)
	}
	if e.result == nil {
		t.Fatal("main application was not started")
	}

	if h, _ := bao.Get("umbrella/users/USR-1")["password_hash"].(string); !strings.HasPrefix(h, "pbkdf2-sha256$") {
		t.Fatalf("admin password hash must be in OpenBao, got %v", bao.Get("umbrella/users/USR-1"))
	}
	e.result.Store.Read(func(d *store.Data) {
		if u := d.Users["USR-1"]; u == nil || u.PasswordRef != "openbao://umbrella/users/USR-1#password_hash" || u.Name != "Main Admin" || u.Title != "CTO" {
			t.Fatalf("admin record = %+v", u)
		}
		if got := d.Settings.Alerting.PagerDuty.MinSeverity; got != "error" {
			t.Errorf("a new install sends errors and critical alerts to PagerDuty, not everything: %q", got)
		}
	})
	e.result.Store.Read(func(d *store.Data) {
		if got := d.Settings.Alerting.PublicURL; got != "https://umbrella.example.org" {
			t.Fatalf("public address saved by the wizard = %q", got)
		}
	})
	if v := bao.Get("umbrella/postgres"); v["password"] != pg.password {
		t.Fatalf("postgres password in OpenBao = %v", v)
	}
	if v := bao.Get("umbrella/ldap"); v["bind_password"] != "ldap-bind-secret" {
		t.Fatalf("ldap password in OpenBao = %v", v)
	}
	raw, err := os.ReadFile(filepath.Join(e.dir, config.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), pg.password) || strings.Contains(string(raw), "ldap-bind-secret") {
		t.Fatal("plain passwords leaked into umbrella.json")
	}
	if fi, _ := os.Stat(filepath.Join(e.dir, config.FileName)); fi.Mode().Perm() != 0o600 {
		t.Fatalf("umbrella.json mode = %v", fi.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(e.dir, config.SetupTokenFile)); !os.IsNotExist(err) {
		t.Fatal("setup code file must be removed")
	}

	var meta map[string]any
	if e.call(http.MethodGet, "/api/meta", nil, nil, &meta); meta["mode"] != "ready" || meta["default_theme"] != "dark" ||
		meta["default_locale"] != "ru" || meta["default_timezone"] != "Europe/Moscow" || meta["ldap_enabled"] != true {
		t.Fatalf("meta after setup = %v", meta)
	}
	if code := e.setup("complete", complete, nil); code != 404 {
		t.Fatalf("setup API after completion = %d, want 404", code)
	}

	if code, _ := e.login("admin", "wrong-password-1"); code != 401 {
		t.Fatalf("wrong password = %d", code)
	}
	code, me := e.login("admin", "Admin-pass-2026")
	if code != 200 || me["role"] != "admin" || me["source"] != "local" {
		t.Fatalf("admin login = %d %v", code, me)
	}
	if g, _ := me["gravatar"].(string); len(g) != 64 || me["has_avatar"] != false {
		t.Fatalf("avatar fields = %v", me)
	}
	h := map[string]string{app.CSRFHeader: e.csrf}
	var prof map[string]any
	if code := e.call(http.MethodPut, "/api/auth/me/profile", map[string]string{"last_name": "Иванов", "first_name": "Пётр", "middle_name": "Сергеевич",
		"title": "Руководитель мониторинга", "department": "ИТ", "manager": "Директор", "email": "petr@example.org"}, h, &prof); code != 200 ||
		prof["name"] != "Иванов Пётр Сергеевич" || prof["department"] != "ИТ" {
		t.Fatalf("profile = %d %v", code, prof)
	}
	if code := e.call(http.MethodPut, "/api/auth/me/profile", map[string]string{"email": "not an email"}, h, nil); code != 400 {
		t.Fatalf("bad email = %d", code)
	}
	if code := e.call(http.MethodPut, "/api/auth/me/password", map[string]string{"current_password": "nope", "new_password": "Новый-пароль-2026"}, h, nil); code != 403 {
		t.Fatalf("wrong current password = %d", code)
	}
	var weak map[string]any
	if code := e.call(http.MethodPut, "/api/auth/me/password", map[string]string{"current_password": "Admin-pass-2026", "new_password": "simple"}, h, &weak); code != 400 || weak["violations"] == nil {
		t.Fatalf("weak new password = %d %v", code, weak)
	}
	if code := e.call(http.MethodPut, "/api/auth/me/password", map[string]string{"current_password": "Admin-pass-2026", "new_password": "Новый-пароль-2026"}, h, nil); code != 204 {
		t.Fatalf("password change = %d", code)
	}
	if code := e.raw(http.MethodPut, "/api/auth/me/avatar", []byte("not an image"), h, nil); code != 400 {
		t.Fatalf("bad avatar = %d", code)
	}
	var withAvatar map[string]any
	if code := e.raw(http.MethodPut, "/api/auth/me/avatar", testImage(t, 400, 300), h, &withAvatar); code != 200 || withAvatar["has_avatar"] != true {
		t.Fatalf("avatar upload = %d %v", code, withAvatar)
	}
	if img := e.image("/api/users/USR-1/avatar"); img.Bounds().Dx() != 256 || img.Bounds().Dy() != 256 {
		t.Fatalf("avatar size = %v", img.Bounds())
	}
	ver, _ := withAvatar["avatar_version"].(string)
	resp, err := e.client.Get(e.srv.URL + "/api/users/USR-1/avatar?v=" + ver)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if cc := resp.Header.Get("Cache-Control"); ver == "" || !strings.Contains(cc, "immutable") {
		t.Fatalf("versioned avatar cache = %q (version %q)", cc, ver)
	}
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/api/users/USR-1/avatar", nil)
	req.Header.Set("If-None-Match", resp.Header.Get("ETag"))
	revalidated, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	revalidated.Body.Close()
	if revalidated.StatusCode != http.StatusNotModified || revalidated.Header.Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("revalidation = %d %q", revalidated.StatusCode, revalidated.Header.Get("Cache-Control"))
	}
	if code, _ := e.login("admin", "Admin-pass-2026"); code != 401 {
		t.Fatalf("old password still works: %d", code)
	}
	if code, _ := e.login("admin", "Новый-пароль-2026"); code != 200 {
		t.Fatalf("new password = %d", code)
	}
	var sys map[string]any
	if code := e.call(http.MethodGet, "/api/system", nil, nil, &sys); code != 200 {
		t.Fatalf("system = %d", code)
	}
	if sys["openbao"].(map[string]any)["token_ok"] != true || sys["postgres"].(map[string]any)["ok"] != true ||
		sys["ldap"].(map[string]any)["ok"] != true {
		t.Fatalf("system = %v", sys)
	}
	csrf := map[string]string{app.CSRFHeader: e.csrf}
	if code := e.call(http.MethodPut, "/api/settings", map[string]string{"default_timezone": "Asia/Novosibirsk"}, nil, nil); code != 403 {
		t.Fatalf("settings without CSRF = %d", code)
	}
	var settings map[string]any
	if code := e.call(http.MethodPut, "/api/settings", map[string]string{"default_timezone": "Asia/Novosibirsk", "default_theme": "light"}, csrf, &settings); code != 200 ||
		settings["default_timezone"] != "Asia/Novosibirsk" || settings["default_theme"] != "light" || settings["default_locale"] != "ru" {
		t.Fatalf("settings = %d %v", code, settings)
	}
	if code := e.call(http.MethodPut, "/api/settings", map[string]string{"default_timezone": "Moscow"}, csrf, nil); code != 400 {
		t.Fatalf("bad default timezone = %d", code)
	}
	var prefs map[string]any
	if code := e.call(http.MethodPut, "/api/auth/me/preferences", map[string]string{"timezone": "America/New_York"}, csrf, &prefs); code != 200 || prefs["timezone"] != "America/New_York" {
		t.Fatalf("preferences = %d %v", code, prefs)
	}
	if code := e.call(http.MethodPut, "/api/auth/me/preferences", map[string]string{"timezone": "Local"}, csrf, nil); code != 400 {
		t.Fatalf("Local timezone must be rejected, got %d", code)
	}
	var me2 map[string]any
	if e.call(http.MethodGet, "/api/auth/me", nil, nil, &me2); me2["timezone"] != "America/New_York" {
		t.Fatalf("me after preferences = %v", me2)
	}
	if code := e.call(http.MethodPut, "/api/auth/me/preferences", map[string]string{"timezone": ""}, csrf, &prefs); code != 200 || prefs["timezone"] != "" {
		t.Fatalf("reset timezone = %d %v", code, prefs)
	}
	if code := e.call(http.MethodPost, "/api/auth/logout", nil, nil, nil); code != 403 {
		t.Fatalf("logout without CSRF = %d", code)
	}
	if code := e.call(http.MethodPost, "/api/auth/logout", nil, map[string]string{app.CSRFHeader: e.csrf}, nil); code != 204 {
		t.Fatalf("logout = %d", code)
	}
	if code := e.call(http.MethodGet, "/api/auth/me", nil, nil, nil); code != 401 {
		t.Fatalf("me after logout = %d", code)
	}

	e.newClient()
	code, anna := e.login("anna", "anna-pass-1")
	if code != 200 || anna["role"] != "admin" || anna["source"] != "ldap" || anna["name"] != "Anna Ivanova" || anna["first_name"] != "Anna" ||
		anna["last_name"] != "Ivanova" || anna["title"] != "Lead engineer" || anna["department"] != "Monitoring" || anna["manager"] != "Boris Petrov" ||
		anna["has_avatar"] != true || anna["avatar_source"] != "ldap" {
		t.Fatalf("ldap admin = %d %v", code, anna)
	}
	if code := e.call(http.MethodPut, "/api/auth/me/profile", map[string]string{"title": "x"}, map[string]string{app.CSRFHeader: e.csrf}, nil); code != 409 {
		t.Fatalf("directory user editing profile = %d", code)
	}
	e.newClient()
	code, boris := e.login("boris", "boris-pass-1")
	// A new directory user gets the role for new users: the viewer preset made by the wizard.
	if code != 200 || boris["role"] != model.RoleViewer || boris["role_name"] != "Наблюдатель" {
		t.Fatalf("ldap user = %d %v", code, boris)
	}
	if code := e.call(http.MethodGet, "/api/system", nil, nil, nil); code != 403 {
		t.Fatalf("user on system page = %d", code)
	}
	if code := e.call(http.MethodPut, "/api/settings", map[string]string{"default_timezone": "UTC"}, map[string]string{app.CSRFHeader: e.csrf}, nil); code != 403 {
		t.Fatalf("user changing defaults = %d", code)
	}
	if code := e.call(http.MethodPut, "/api/auth/me/preferences", map[string]string{"timezone": "Asia/Yekaterinburg"}, map[string]string{app.CSRFHeader: e.csrf}, nil); code != 200 {
		t.Fatalf("ldap user preferences = %d", code)
	}
	if code, _ := e.login("boris", "wrong-password"); code != 401 {
		t.Fatalf("ldap wrong password = %d", code)
	}

	if err := e.result.Store.Flush(); err != nil {
		t.Fatal(err)
	}
	again := newEnv(t)
	var conflict map[string]any
	if code := again.setup("complete", complete, &conflict); code != 409 || conflict["error"] != "postgres_has_state" {
		t.Fatalf("reinstall over existing state = %d %v", code, conflict)
	}
	reuse := pg.body()
	reuse["reuse_existing"] = true
	complete["postgres"] = reuse
	complete["admin"] = map[string]any{"username": "admin", "password": "Second-pass-2026!"}
	complete["public_url"] = ""
	if code := again.setup("complete", complete, nil); code != 200 {
		t.Fatalf("reinstall with reuse = %d", code)
	}
	again.result.Store.Read(func(d *store.Data) {
		if got := d.Settings.Alerting.PublicURL; got != "https://umbrella.example.org" {
			t.Fatalf("an empty public address must keep the stored one, got %q", got)
		}
	})
	if code, _ := again.login("admin", "Second-pass-2026!"); code != 200 {
		t.Fatal("admin password must be reset by the reinstall")
	}
	var users int
	again.result.Store.Read(func(d *store.Data) { users = len(d.Users) })
	if users != 3 {
		t.Fatalf("existing users must be kept, got %d", users)
	}
}
