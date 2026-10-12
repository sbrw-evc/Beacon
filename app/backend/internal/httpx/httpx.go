package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const maxBody = 1 << 20

type Problem struct {
	Code       string   `json:"error"`
	Detail     string   `json:"detail,omitempty"`
	Field      string   `json:"field,omitempty"`
	Violations []string `json:"violations,omitempty"`
}

func JSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func Error(w http.ResponseWriter, code int, errCode string, detail error) {
	p := Problem{Code: errCode}
	if detail != nil {
		p.Detail = detail.Error()
	}
	JSON(w, code, p)
}

func Decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", errors.New("invalid JSON body"))
		return false
	}
	return true
}

func Secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https://gravatar.com https://www.gravatar.com https://secure.gravatar.com; style-src 'self' 'unsafe-inline'; "+
			"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

var imageExt = map[string]bool{".svg": true, ".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".ico": true, ".avif": true}

func NotModified(r *http.Request, etag string, modified time.Time) bool {
	if match := r.Header.Get("If-None-Match"); match != "" {
		for _, v := range strings.Split(match, ",") {
			if v = strings.TrimSpace(v); v == etag || v == "W/"+etag || v == "*" {
				return true
			}
		}
		return false
	}
	if since, err := http.ParseTime(r.Header.Get("If-Modified-Since")); err == nil {
		return !modified.Truncate(time.Second).After(since)
	}
	return false
}

func Web(dir string) http.Handler {
	if dir == "" {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		p := path.Clean("/" + r.URL.Path)
		if strings.HasPrefix(p, "/api/") {
			Error(w, http.StatusNotFound, "not_found", nil)
			return
		}
		if p != "/" {
			if fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err == nil && !fi.IsDir() {
				switch {
				case strings.HasPrefix(p, "/assets/"):
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				case imageExt[strings.ToLower(path.Ext(p))]:
					w.Header().Set("Cache-Control", "public, max-age=604800, stale-while-revalidate=86400")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}
