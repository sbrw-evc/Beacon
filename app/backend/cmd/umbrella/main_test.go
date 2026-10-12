package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/ingest"
)

// The intake settings default to the built-in values, and a bad value keeps its default.
func TestIngestConfig(t *testing.T) {
	if got := ingestConfig(); got != ingest.DefaultConfig() {
		t.Errorf("defaults = %+v", got)
	}
	t.Setenv("UMBRELLA_INGEST_WORKERS", "4")
	t.Setenv("UMBRELLA_INGEST_BATCH_SIZE", "-1")
	t.Setenv("UMBRELLA_INGEST_TEST_WAIT", "40s")
	t.Setenv("UMBRELLA_INGEST_KEEP_REQUESTS", "soon")
	got := ingestConfig()
	want := ingest.DefaultConfig()
	want.Workers, want.TestEventWait = 4, 40*time.Second
	if got != want {
		t.Errorf("config = %+v", got)
	}
}

// A switch waits for the requests the old application still serves, except the one asking for it.
func TestSwitchHandlerDrain(t *testing.T) {
	h := &switchHandler{}
	release := make(chan struct{})
	started := make(chan struct{})
	finished := make(chan struct{})
	drained := make(chan bool, 1)
	h.Set(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/switch":
			drained <- h.Drain(r.Context(), maintenance{}, 5*time.Second)
		default:
			close(started)
			<-release
			close(finished)
		}
	}))
	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/intake", nil))
	<-started
	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/switch", nil))
	time.Sleep(100 * time.Millisecond)
	select {
	case <-drained:
		t.Fatal("the switch must wait for the running request")
	default:
	}
	// New requests already go to the maintenance handler and are not waited for.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/x", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("during a switch = %d", rec.Code)
	}
	close(release)
	select {
	case ok := <-drained:
		if !ok {
			t.Fatal("drain timed out")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the switch waits for itself")
	}
	<-finished

	// A request that never ends does not block a switch forever.
	stuck := make(chan struct{})
	defer close(stuck)
	running := make(chan struct{})
	h.Set(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(running); <-stuck }))
	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	<-running
	if h.Drain(t.Context(), maintenance{}, 50*time.Millisecond) {
		t.Fatal("drain must report the request it gave up on")
	}
}
