package app_test

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

// readEvent returns the name of the next event on a Server-Sent Events stream.
func readEvent(t *testing.T, lines <-chan string, within time.Duration) string {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatal("the stream ended")
			}
			if name, found := strings.CutPrefix(l, "event: "); found {
				return name
			}
		case <-deadline:
			return ""
		}
	}
}

func TestIncidentStream(t *testing.T) {
	f := newConnFixture(t, true)
	auth := f.webhookConnector()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.h.srv.URL+"/api/incidents/stream", nil)
	resp, err := f.admin.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	lines := make(chan string)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	if got := readEvent(t, lines, 5*time.Second); got != "ready" {
		t.Fatalf("first event = %q", got)
	}

	// A new incident reaches the open stream without asking for it.
	f.ingest("hook", `{"id":"s1","title":"Disk full","host":"db-01","signal":"disk","severity":"error","status":"firing"}`, auth)
	if got := readEvent(t, lines, 10*time.Second); got != "change" {
		t.Fatalf("after a new incident: %q", got)
	}

	// So does acknowledging it by hand.
	var page struct {
		Alerts []struct {
			ID string `json:"id"`
		} `json:"alerts"`
	}
	f.expect(f.admin, http.MethodGet, "/api/incidents", nil, http.StatusOK, &page)
	if len(page.Alerts) != 1 {
		t.Fatalf("incidents = %+v", page)
	}
	time.Sleep(1200 * time.Millisecond) // past the gap of the first change and its follow-ups
	for readEvent(t, lines, 300*time.Millisecond) != "" {
	}
	f.expect(f.admin, http.MethodPost, "/api/incidents/"+page.Alerts[0].ID+"/ack", nil, http.StatusOK, nil)
	if got := readEvent(t, lines, 5*time.Second); got != "change" {
		t.Fatalf("after ack: %q", got)
	}

	// Without the permission to see incidents there is no stream.
	f.h.addLocal("nobody", "Nobody-pass-2026", model.RoleUser, time.Now())
	nobody := f.h.client()
	nobody.login("nobody", "Nobody-pass-2026")
	if got := nobody.call(http.MethodGet, "/api/incidents/stream", nil, nil); got != http.StatusForbidden {
		t.Fatalf("stream without incidents:view = %d", got)
	}
}
