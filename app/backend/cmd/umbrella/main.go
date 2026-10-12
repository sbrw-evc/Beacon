package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/config"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/ingest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/logbuf"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/setup"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// Set at build time with -ldflags "-X main.version=... -X main.commit=... -X main.builtAt=...".
var (
	version = "dev"
	commit  string
	builtAt string
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ingestConfig reads the intake settings; an unset or invalid value keeps the default.
func ingestConfig() ingest.Config {
	c := ingest.DefaultConfig()
	num := func(key string, v *int) {
		if s := env(key, ""); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				*v = n
			} else {
				slog.Warn("ignored: not a positive number", "env", key, "value", s)
			}
		}
	}
	dur := func(key string, v *time.Duration) {
		if s := env(key, ""); s != "" {
			if d, err := time.ParseDuration(s); err == nil && d > 0 {
				*v = d
			} else {
				slog.Warn("ignored: not a positive duration", "env", key, "value", s)
			}
		}
	}
	num("UMBRELLA_INGEST_WORKERS", &c.Workers)
	num("UMBRELLA_INGEST_BATCH_SIZE", &c.BatchSize)
	num("UMBRELLA_INGEST_MAX_ATTEMPTS", &c.MaxAttempts)
	dur("UMBRELLA_INGEST_PROCESS_TIMEOUT", &c.ProcessTimeout)
	dur("UMBRELLA_INGEST_TEST_WAIT", &c.TestEventWait)
	dur("UMBRELLA_INGEST_KEEP_REQUESTS", &c.Retention.Requests)
	dur("UMBRELLA_INGEST_KEEP_FAILURES", &c.Retention.Failures)
	dur("UMBRELLA_INGEST_KEEP_STATS", &c.Retention.Stats)
	dur("UMBRELLA_INGEST_KEEP_IDEMPOTENCY", &c.Retention.Dedup)
	return c
}

func fail(msg string, err error) {
	slog.Error(msg, "err", err)
	os.Exit(1)
}

// switchHandler serves the current handler and counts the requests each handler still serves,
// so a switch can wait for requests of the old application before it copies its data.
type switchHandler struct{ cur atomic.Pointer[served] }

type served struct {
	h        http.Handler
	inflight atomic.Int64
}

func (s *switchHandler) Set(h http.Handler) { s.swap(h) }

func (s *switchHandler) swap(h http.Handler) *served {
	return s.cur.Swap(&served{h: h})
}

func (s *switchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for {
		cur := s.cur.Load()
		cur.inflight.Add(1)
		// A request that raced with a swap goes to the new handler: the one waiting for the
		// old handler to drain must never miss it.
		if s.cur.Load() != cur {
			cur.inflight.Add(-1)
			continue
		}
		defer cur.inflight.Add(-1)
		cur.h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), servedKey{}, cur)))
		return
	}
}

type servedKey struct{}

// Drain puts h in place and waits up to timeout for requests of the previous handler to end,
// except the request of ctx itself (the one asking for the switch).
func (s *switchHandler) Drain(ctx context.Context, h http.Handler, timeout time.Duration) bool {
	old := s.swap(h)
	if old == nil {
		return true
	}
	var self int64
	if ctx.Value(servedKey{}) == old {
		self = 1
	}
	deadline := time.Now().Add(timeout)
	for old.inflight.Load() > self {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	return true
}

type runtime struct {
	mu       sync.Mutex
	switchMu sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
	cfg      config.File
	vault    *secrets.Client
	backend  *store.PGBackend
	st       *store.Store
	dataDir  string
	handler  *switchHandler
	launch   func(config.File, *secrets.Client, *store.PGBackend, *store.Store)
}

func (rt *runtime) halt() (config.File, *secrets.Client, *store.PGBackend, *store.Store, bool) {
	rt.mu.Lock()
	cancel, done := rt.cancel, rt.done
	cfg, vault, backend, st := rt.cfg, rt.vault, rt.backend, rt.st
	rt.cancel, rt.done = nil, nil
	rt.mu.Unlock()
	if cancel == nil {
		return cfg, vault, backend, st, false
	}
	cancel()
	<-done
	return cfg, vault, backend, st, true
}

func (rt *runtime) stop() {
	if _, _, backend, _, ok := rt.halt(); ok {
		backend.Close()
	}
}

func (rt *runtime) Switch(ctx context.Context, transfer func(ctx context.Context) (config.File, error)) error {
	rt.switchMu.Lock()
	defer rt.switchMu.Unlock()
	if !rt.handler.Drain(ctx, maintenance{}, 15*time.Second) {
		slog.Warn("requests still running when switching connections")
	}
	oldCfg, oldVault, oldBackend, oldSt, ok := rt.halt()
	if !ok {
		return errors.New("umbrella is not running")
	}
	restore := func(err error) error {
		rt.launch(oldCfg, oldVault, oldBackend, oldSt)
		return err
	}
	cfg, err := transfer(ctx)
	if err != nil {
		return restore(err)
	}
	vault, backend, st, err := open(ctx, cfg, 15*time.Second)
	if err != nil {
		return restore(err)
	}
	if err := config.Write(rt.dataDir, cfg); err != nil {
		backend.Close()
		return restore(err)
	}
	oldBackend.Close()
	rt.launch(cfg, vault, backend, st)
	slog.Info("umbrella reconfigured", "openbao", cfg.OpenBao.Addr, "storage", backend.Where())
	return nil
}

type maintenance struct{}

func (maintenance) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", "2")
	httpx.Error(w, http.StatusServiceUnavailable, "switching", errors.New("Umbrella is switching its connections, try again in a moment"))
}

func main() {
	slog.SetDefault(slog.New(logbuf.Wrap(slog.NewTextHandler(os.Stderr, nil))))
	addr := flag.String("addr", env("UMBRELLA_ADDR", ":8080"), "listen address")
	web := flag.String("web", env("UMBRELLA_WEB_DIR", "../web/dist"), "built web UI directory")
	dataDir := flag.String("data", env("UMBRELLA_DATA_DIR", "data"), "directory for umbrella.json and sessions")
	showVersion := flag.Bool("version", false, "print the version and exit")
	healthcheck := flag.Bool("healthcheck", false, "probe /healthz of the running server and exit 0 when it answers")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	if *healthcheck {
		os.Exit(probe(*addr))
	}
	if *dataDir == "" {
		fail("data directory", errors.New("UMBRELLA_DATA_DIR must not be empty"))
	}
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		fail("data directory", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	webDir := *web
	if fi, err := os.Stat(webDir); err != nil || !fi.IsDir() {
		slog.Warn("web UI not found, serving API only", "dir", webDir)
		webDir = ""
	}
	webHandler := httpx.Web(webDir)
	opts := app.Options{Version: version, Commit: commit, BuiltAt: builtAt, SecureCookies: env("UMBRELLA_SECURE_COOKIES", "false") == "true", Web: webHandler,
		Ingest: ingestConfig()}
	sessions := auth.NewSessions()
	if err := sessions.Load(*dataDir); err != nil {
		slog.Warn("sessions not restored", "err", err)
	}

	handler := &switchHandler{}
	rt := &runtime{dataDir: *dataDir, handler: handler}
	rt.launch = func(cfg config.File, vault *secrets.Client, backend *store.PGBackend, st *store.Store) {
		bg, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		a := app.New(opts, app.Deps{Config: cfg, Vault: vault, Backend: backend, Store: st, Sessions: sessions, Runtime: rt})
		var wg sync.WaitGroup
		wg.Add(2)
		go vault.Run(bg)
		go func() {
			defer wg.Done()
			st.Run(bg, 2*time.Second, func() error { return sessions.Save(*dataDir) })
		}()
		go func() {
			defer wg.Done()
			a.Run(bg)
		}()
		go func() {
			wg.Wait()
			close(done)
		}()
		rt.mu.Lock()
		rt.cancel, rt.done = cancel, done
		rt.cfg, rt.vault, rt.backend, rt.st = cfg, vault, backend, st
		rt.mu.Unlock()
		handler.Set(a.Handler())
	}
	start := rt.launch

	cfg, found, err := config.Read(*dataDir)
	if err != nil {
		fail("configuration", err)
	}
	if found && cfg.CompletedAt != nil {
		wait, err := time.ParseDuration(env("UMBRELLA_STARTUP_WAIT", "2m"))
		if err != nil {
			wait = 2 * time.Minute
		}
		vault, backend, st, err := open(ctx, cfg, wait)
		if err != nil {
			fail("startup", err)
		}
		start(cfg, vault, backend, st)
		slog.Info("umbrella ready", "openbao", cfg.OpenBao.Addr, "storage", backend.Where())
	} else {
		token, err := setupToken(*dataDir)
		if err != nil {
			fail("setup code", err)
		}
		mod := setup.New(setup.Options{DataDir: *dataDir, Token: token, Version: version, Web: webHandler},
			func(r setup.Result) { start(r.Config, r.Vault, r.Backend, r.Store) })
		handler.Set(mod.Handler())
		slog.Warn("first start: open the web UI and finish the setup wizard", "setup_code", token,
			"file", filepath.Join(*dataDir, config.SetupTokenFile))
	}

	srv := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	slog.Info("umbrella started", "version", version, "addr", *addr, "data_dir", *dataDir)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		rt.stop()
		fail("server failed", err)
	}
	rt.stop()
	if err := sessions.Save(*dataDir); err != nil {
		slog.Warn("sessions not saved", "err", err)
	}
	slog.Info("umbrella stopped")
}

func open(ctx context.Context, cfg config.File, wait time.Duration) (*secrets.Client, *store.PGBackend, *store.Store, error) {
	vault, err := cfg.OpenBao.Client()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := vault.Ready(ctx, wait); err != nil {
		return nil, nil, nil, fmt.Errorf("OpenBao %s: %w", cfg.OpenBao.Addr, err)
	}
	pg := cfg.Postgres
	if cfg.PostgresPasswordRef != "" {
		if pg.Password, err = vault.Resolve(cfg.PostgresPasswordRef); err != nil {
			return nil, nil, nil, fmt.Errorf("PostgreSQL password: %w", err)
		}
	}
	deadline := time.Now().Add(wait)
	var backend *store.PGBackend
	for {
		backend, err = store.OpenPostgres(ctx, pg)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return nil, nil, nil, err
		}
		slog.Info("waiting for PostgreSQL", "err", err)
		select {
		case <-ctx.Done():
			return nil, nil, nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	st := store.New()
	restored, err := st.Attach(ctx, backend)
	if err != nil {
		backend.Close()
		return nil, nil, nil, err
	}
	if !restored {
		backend.Close()
		return nil, nil, nil, errors.New("the database has no Umbrella state: restore it or remove umbrella.json to run the setup wizard again")
	}
	if err := app.Migrate(ctx, st, vault); err != nil {
		backend.Close()
		return nil, nil, nil, fmt.Errorf("move password hashes to OpenBao: %w", err)
	}
	return vault, backend, st, nil
}

func setupToken(dir string) (string, error) {
	if t := strings.TrimSpace(os.Getenv("UMBRELLA_SETUP_TOKEN")); t != "" {
		return t, nil
	}
	path := filepath.Join(dir, config.SetupTokenFile)
	if b, err := os.ReadFile(path); err == nil {
		if t := strings.TrimSpace(string(b)); t != "" {
			return t, nil
		}
	}
	t := auth.RandomToken("", 12)
	if err := store.WriteFileAtomic(path, []byte(t+"\n")); err != nil {
		return "", err
	}
	return t, nil
}

func probe(addr string) int {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
