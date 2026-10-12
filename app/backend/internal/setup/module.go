package setup

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/config"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	TokenHeader = "X-Setup-Token"

	maxFailures = 5
	lockout     = time.Minute

	secretPostgres = "postgres"
)

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{1,63}$`)

type Options struct {
	DataDir string
	Token   string
	Version string
	Web     http.Handler
}

type Result struct {
	Config  config.File
	Vault   *secrets.Client
	Backend *store.PGBackend
	Store   *store.Store
}

type Module struct {
	opt  Options
	done func(Result)

	mu       sync.Mutex
	finished bool

	lmu      sync.Mutex
	failures int
	until    time.Time
}

func New(opt Options, done func(Result)) *Module {
	if opt.Web == nil {
		opt.Web = http.NotFoundHandler()
	}
	return &Module{opt: opt, done: done}
}

func (m *Module) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "setup"})
	})
	mux.HandleFunc("GET /api/meta", m.meta)
	mux.HandleFunc("GET /api/setup/status", m.meta)
	mux.HandleFunc("POST /api/setup/token", m.guard(func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("POST /api/setup/openbao/test", m.guard(m.testOpenBao))
	mux.HandleFunc("POST /api/setup/postgres/test", m.guard(m.testPostgres))
	mux.HandleFunc("POST /api/setup/ldap/test", m.guard(m.testLDAP))
	mux.HandleFunc("POST /api/setup/complete", m.guard(m.complete))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		httpx.Error(w, http.StatusServiceUnavailable, "setup_required", nil)
	})
	mux.Handle("/", m.opt.Web)
	return httpx.Secure(mux)
}

func (m *Module) meta(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{"mode": "setup", "setup_required": true, "version": m.opt.Version,
		"default_theme": model.ThemeLight, "default_locale": model.LocaleEN, "default_timezone": "UTC", "container": inContainer()})
}

func inContainer() bool {
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func (m *Module) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		finished := m.finished
		m.mu.Unlock()
		if finished {
			httpx.Error(w, http.StatusConflict, "setup_completed", nil)
			return
		}
		m.lmu.Lock()
		locked := time.Now().Before(m.until)
		m.lmu.Unlock()
		if locked {
			httpx.Error(w, http.StatusTooManyRequests, "too_many_attempts", nil)
			return
		}
		given := r.Header.Get(TokenHeader)
		if subtle.ConstantTimeCompare([]byte(given), []byte(m.opt.Token)) != 1 {
			m.lmu.Lock()
			m.failures++
			if m.failures >= maxFailures {
				m.failures, m.until = 0, time.Now().Add(lockout)
			}
			m.lmu.Unlock()
			slog.Warn("setup: wrong setup code", "remote", r.RemoteAddr, "forwarded_for", r.Header.Get("X-Forwarded-For"))
			httpx.Error(w, http.StatusUnauthorized, "invalid_setup_token", nil)
			return
		}
		m.lmu.Lock()
		m.failures = 0
		m.lmu.Unlock()
		next(w, r)
	}
}

func (m *Module) complete(w http.ResponseWriter, r *http.Request) {
	var in completeInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.finished {
		httpx.Error(w, http.StatusConflict, "setup_completed", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	res, err := m.apply(ctx, in)
	if err != nil {
		var se *stepError
		if !errors.As(err, &se) {
			se = fail(http.StatusInternalServerError, "internal", err)
		}
		slog.Warn("setup: not completed", "step", se.code, "err", se.err)
		p := httpx.Problem{Code: se.code, Violations: se.violations}
		if se.err != nil {
			p.Detail = se.err.Error()
		}
		httpx.JSON(w, se.status, p)
		return
	}
	m.finished = true
	_ = os.Remove(filepath.Join(m.opt.DataDir, config.SetupTokenFile))
	slog.Info("setup completed", "admin", in.Admin.Username, "storage", res.Backend.Where(), "ldap", in.LDAP.Config.Enabled)
	if m.done != nil {
		m.done(res)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}
