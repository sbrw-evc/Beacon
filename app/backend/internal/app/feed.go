package app

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// feed hands "incidents changed" to the open incident streams. A subscriber has room for one
// signal: changes that arrive while it is busy fold into the one waiting.
type feed struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func (f *feed) publish() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ch := range f.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (f *feed) subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	f.mu.Lock()
	if f.subs == nil {
		f.subs = map[chan struct{}]struct{}{}
	}
	f.subs[ch] = struct{}{}
	f.mu.Unlock()
	return ch, func() {
		f.mu.Lock()
		delete(f.subs, ch)
		f.mu.Unlock()
	}
}

const (
	// streamGap: a stream sends at most one change per gap, so a burst of events reloads a page once.
	streamGap = time.Second
	// streamPing keeps proxies from closing a quiet stream.
	streamPing = 25 * time.Second
	// streamLife: the browser reconnects after it, so the session is checked again.
	streamLife = 5 * time.Minute
)

// incidentStream is GET /api/incidents/stream: Server-Sent Events with a "change" event whenever
// an incident or its timeline changes. Events carry no data; pages load what they show
// themselves, within the user's scope.
func (a *App) incidentStream(w http.ResponseWriter, r *http.Request) {
	if !a.alertsReady(w) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)
		return
	}
	ch, stop := a.feed.subscribe()
	defer stop()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 3000\nevent: ready\ndata: {}\n\n")
	flusher.Flush()
	ping := time.NewTicker(streamPing)
	defer ping.Stop()
	life := time.NewTimer(streamLife)
	defer life.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-life.C:
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-ch:
			fmt.Fprint(w, "event: change\ndata: {}\n\n")
			flusher.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(streamGap):
			}
		}
	}
}
