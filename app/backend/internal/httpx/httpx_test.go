package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
)

func TestWebCaching(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"index.html": "<html></html>", "logo.svg": "<svg/>", "assets/app-1a2b.js": "x"} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h := httpx.Web(dir)
	cases := map[string]string{
		"/logo.svg":           "public, max-age=604800, stale-while-revalidate=86400",
		"/assets/app-1a2b.js": "public, max-age=31536000, immutable",
		"/profile":            "no-cache",
	}
	for path, want := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if got := rec.Header().Get("Cache-Control"); got != want || rec.Code != 200 {
			t.Errorf("%s: %d %q, want %q", path, rec.Code, got, want)
		}
	}
}

func TestNotModified(t *testing.T) {
	at := time.Date(2026, 10, 4, 12, 0, 0, 500, time.UTC)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("If-None-Match", `"a", "b"`)
	if !httpx.NotModified(req, `"b"`, at) {
		t.Error("ETag list must match")
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("If-Modified-Since", at.Format(http.TimeFormat))
	if !httpx.NotModified(req, `"x"`, at) {
		t.Error("same second must be not modified")
	}
	req.Header.Set("If-Modified-Since", at.Add(-time.Hour).Format(http.TimeFormat))
	if httpx.NotModified(req, `"x"`, at) {
		t.Error("older date must be modified")
	}
}
