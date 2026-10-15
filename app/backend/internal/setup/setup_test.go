package setup_test

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets/secretstest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/setup"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/spa"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/spa/spatest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const setupCode = "setup-code-for-tests"

func TestMain(m *testing.M) {
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

// signIn runs the sign-in through the fake SPA as the SPA person id and returns where Umbrella
// sent the browser at the end; after a sign-in that ends at "/" it reads the CSRF token.
func (e *env) signIn(fake *spatest.SPA, id string) string {
	e.t.Helper()
	fake.SignedIn = id
	var last *http.Request
	hc := *e.client
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		last = req
		return nil
	}
	resp, err := hc.Get(e.srv.URL + "/api/auth/spa/start?return=/")
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	if last == nil {
		return ""
	}
	if last.URL.RequestURI() == "/" {
		var me map[string]any
		e.call(http.MethodGet, "/api/auth/me", nil, nil, &me)
		e.csrf, _ = me["csrf"].(string)
	}
	return last.URL.RequestURI()
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

func TestWizardEndToEnd(t *testing.T) {
	pg := postgres(t)
	bao, _ := secretstest.New(t)
	fake := spatest.New(t)
	fake.Users = []spa.User{
		{ID: "usr_admin", Username: "admin", Name: "Main Admin", Email: "admin@example.org", Roles: []string{"admin"}},
		{ID: "usr_boris", Username: "boris", Name: "Boris Petrov", Roles: []string{model.RoleViewer}},
		{ID: "usr_nobody", Username: "nobody", Name: "No Role"},
	}
	e := newEnv(t)

	openbao := map[string]any{"addr": bao.Server.URL, "mount": "umbrella", "auth": "approle",
		"role_id": secretstest.RoleID, "secret_id": secretstest.SecretID}
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
	var sr map[string]any
	if e.setup("spa/test", map[string]any{"spa_connection_key": fake.ConnectionKey(), "public_url": e.srv.URL}, &sr); sr["ok"] != true || sr["issuer"] != fake.Server.URL {
		t.Fatalf("spa test = %v", sr)
	}
	if e.setup("spa/test", map[string]any{"spa_connection_key": "spa1.broken", "public_url": e.srv.URL}, &sr); sr["ok"] != false || sr["error"] == "" {
		t.Fatalf("broken key must fail: %v", sr)
	}

	complete := map[string]any{"locale": "ru", "theme": "dark", "timezone": "Mars/Olympus", "openbao": openbao, "postgres": pg.body(),
		"spa_connection_key": "spa1.broken"}
	var res map[string]any
	if code := e.setup("complete", complete, &res); code != 400 || res["error"] != "invalid_timezone" {
		t.Fatalf("unknown timezone = %d %v", code, res)
	}
	complete["timezone"] = "Europe/Moscow"
	complete["public_url"] = "ftp://umbrella.example.org"
	if code := e.setup("complete", complete, &res); code != 400 || res["error"] != "public_url_invalid" {
		t.Fatalf("bad public address = %d %v", code, res)
	}
	complete["public_url"] = " " + e.srv.URL + "/ "
	if code := e.setup("complete", complete, &res); code != 400 || res["error"] != "spa_key_invalid" {
		t.Fatalf("bad SPA key = %d %v", code, res)
	}
	complete["spa_connection_key"] = fake.ConnectionKey()
	if code := e.setup("complete", complete, &res); code != 200 {
		t.Fatalf("complete = %d %v", code, res)
	}
	if e.result == nil {
		t.Fatal("main application was not started")
	}

	if v := bao.Get("umbrella/spa"); v["connection_key"] != fake.ConnectionKey() {
		t.Fatalf("SPA key in OpenBao = %v", v)
	}
	e.result.Store.Read(func(d *store.Data) {
		if len(d.Users) != 0 {
			t.Fatalf("the wizard makes no accounts: %d", len(d.Users))
		}
		if got := d.Settings.Alerting.PagerDuty.MinSeverity; got != "error" {
			t.Errorf("a new install sends errors and critical alerts to PagerDuty, not everything: %q", got)
		}
		if got := d.Settings.Alerting.PublicURL; got != e.srv.URL {
			t.Fatalf("public address saved by the wizard = %q", got)
		}
	})
	if v := bao.Get("umbrella/postgres"); v["password"] != pg.password {
		t.Fatalf("postgres password in OpenBao = %v", v)
	}
	raw, err := os.ReadFile(filepath.Join(e.dir, config.FileName))
	if err != nil {
		t.Fatal(err)
	}
	var stored config.File
	if err := json.Unmarshal(raw, &stored); err != nil || stored.Postgres.Password != "" || strings.Contains(string(raw), fake.ConnectionKey()) {
		t.Fatalf("secrets leaked into umbrella.json (%v): %s", err, raw)
	}
	if fi, _ := os.Stat(filepath.Join(e.dir, config.FileName)); fi.Mode().Perm() != 0o600 {
		t.Fatalf("umbrella.json mode = %v", fi.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(e.dir, config.SetupTokenFile)); !os.IsNotExist(err) {
		t.Fatal("setup code file must be removed")
	}

	var meta map[string]any
	if e.call(http.MethodGet, "/api/meta", nil, nil, &meta); meta["mode"] != "ready" || meta["default_theme"] != "dark" ||
		meta["default_locale"] != "ru" || meta["default_timezone"] != "Europe/Moscow" || meta["spa"].(map[string]any)["connected"] != true {
		t.Fatalf("meta after setup = %v", meta)
	}
	if code := e.setup("complete", complete, nil); code != 404 {
		t.Fatalf("setup API after completion = %d, want 404", code)
	}
	if code := e.call(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": "x"}, nil, nil); code != 404 && code != 405 {
		t.Fatalf("password sign-in must be gone: %d", code)
	}

	// The first administrator is whoever SPA gives the role admin.
	if end := e.signIn(fake, "usr_admin"); end != "/" {
		t.Fatalf("admin sign-in ended at %s", end)
	}
	var me map[string]any
	if code := e.call(http.MethodGet, "/api/auth/me", nil, nil, &me); code != 200 || me["role"] != "admin" || me["source"] != "spa" || me["name"] != "Main Admin" {
		t.Fatalf("admin = %d %v", code, me)
	}
	h := map[string]string{app.CSRFHeader: e.csrf}
	var withAvatar map[string]any
	if code := e.raw(http.MethodPut, "/api/auth/me/avatar", []byte("not an image"), h, nil); code != 400 {
		t.Fatalf("bad avatar = %d", code)
	}
	if code := e.raw(http.MethodPut, "/api/auth/me/avatar", testImage(t, 400, 300), h, &withAvatar); code != 200 || withAvatar["has_avatar"] != true {
		t.Fatalf("avatar upload = %d %v", code, withAvatar)
	}
	id, _ := me["id"].(string)
	if img := e.image("/api/users/" + id + "/avatar"); img.Bounds().Dx() != 256 || img.Bounds().Dy() != 256 {
		t.Fatalf("avatar size = %v", img.Bounds())
	}
	var sys map[string]any
	if code := e.call(http.MethodGet, "/api/system", nil, nil, &sys); code != 200 {
		t.Fatalf("system = %d", code)
	}
	if sys["openbao"].(map[string]any)["token_ok"] != true || sys["postgres"].(map[string]any)["ok"] != true {
		t.Fatalf("system = %v", sys)
	}
	if code := e.call(http.MethodPut, "/api/settings", map[string]string{"default_timezone": "Asia/Novosibirsk"}, nil, nil); code != 403 {
		t.Fatalf("settings without CSRF = %d", code)
	}
	var settings map[string]any
	if code := e.call(http.MethodPut, "/api/settings", map[string]string{"default_timezone": "Asia/Novosibirsk", "default_theme": "light"}, h, &settings); code != 200 ||
		settings["default_timezone"] != "Asia/Novosibirsk" || settings["default_theme"] != "light" || settings["default_locale"] != "ru" {
		t.Fatalf("settings = %d %v", code, settings)
	}
	var prefs map[string]any
	if code := e.call(http.MethodPut, "/api/auth/me/preferences", map[string]string{"timezone": "America/New_York"}, h, &prefs); code != 200 || prefs["timezone"] != "America/New_York" {
		t.Fatalf("preferences = %d %v", code, prefs)
	}
	if code := e.call(http.MethodPut, "/api/auth/me/preferences", map[string]string{"timezone": "Local"}, h, nil); code != 400 {
		t.Fatalf("Local timezone must be rejected, got %d", code)
	}
	if code := e.call(http.MethodPost, "/api/auth/logout", nil, nil, nil); code != 403 {
		t.Fatalf("logout without CSRF = %d", code)
	}
	var out map[string]string
	if code := e.call(http.MethodPost, "/api/auth/logout", nil, h, &out); code != 200 || !strings.HasPrefix(out["redirect"], fake.Server.URL) {
		t.Fatalf("logout = %d %v", code, out)
	}
	if code := e.call(http.MethodGet, "/api/auth/me", nil, nil, nil); code != 401 {
		t.Fatalf("me after logout = %d", code)
	}

	e.newClient()
	if end := e.signIn(fake, "usr_boris"); end != "/" {
		t.Fatalf("boris sign-in ended at %s", end)
	}
	var boris map[string]any
	// The viewer preset made by the wizard is in the catalogue SPA gives roles from.
	if code := e.call(http.MethodGet, "/api/auth/me", nil, nil, &boris); code != 200 || boris["role"] != model.RoleViewer || boris["role_name"] != "Наблюдатель" {
		t.Fatalf("viewer = %d %v", code, boris)
	}
	if code := e.call(http.MethodGet, "/api/system", nil, nil, nil); code != 403 {
		t.Fatalf("viewer on system page = %d", code)
	}
	e.newClient()
	if end := e.signIn(fake, "usr_nobody"); !strings.Contains(end, "sso_error=") {
		t.Fatalf("a person without a role in Umbrella ended at %s", end)
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
	complete["public_url"] = ""
	delete(complete, "spa_connection_key")
	if code := again.setup("complete", complete, nil); code != 200 {
		t.Fatalf("reinstall with reuse = %d", code)
	}
	again.result.Store.Read(func(d *store.Data) {
		if got := d.Settings.Alerting.PublicURL; got != e.srv.URL {
			t.Fatalf("an empty public address must keep the stored one, got %q", got)
		}
		if len(d.Users) != 2 {
			t.Fatalf("existing users must be kept, got %d", len(d.Users))
		}
	})
	if v := bao.Get("umbrella/spa"); v["connection_key"] != fake.ConnectionKey() {
		t.Fatalf("an empty SPA key must keep the stored one: %v", v)
	}
}
