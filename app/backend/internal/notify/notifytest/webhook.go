package notifytest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Request is a post the fake webhook took.
type Request struct {
	Path, Query, Authorization, ContentType string
	Body                                    map[string]any
}

// Webhook is a fake incoming webhook (Teams, Zoom): every path takes a JSON post and answers
// 202. Paths in Fail answer with that status instead.
type Webhook struct {
	srv      *httptest.Server
	mu       sync.Mutex
	requests []Request
	Fail     map[string]int
}

func NewWebhook(t *testing.T) *Webhook {
	f := &Webhook{Fail: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

// URL is the address of a webhook of the fake.
func (f *Webhook) URL(path string) string { return f.srv.URL + path }

func (f *Webhook) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

func (f *Webhook) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	code := f.Fail[r.URL.Path]
	f.mu.Unlock()
	if code != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]any{"message": "rejected"})
		return
	}
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.requests = append(f.requests, Request{Path: r.URL.Path, Query: r.URL.RawQuery, Authorization: r.Header.Get("Authorization"),
		ContentType: r.Header.Get("Content-Type"), Body: body})
	f.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}
