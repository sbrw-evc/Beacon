// Package logbuf keeps the latest warnings and errors of the process in memory, so the
// system status page can show them without access to the container logs.
package logbuf

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/textx"
)

const (
	size     = 100
	maxValue = 500
)

type Entry struct {
	At      time.Time         `json:"at"`
	Level   string            `json:"level"`
	Message string            `json:"message"`
	Attrs   map[string]string `json:"attrs,omitempty"`
}

type ring struct {
	mu      sync.Mutex
	entries []Entry
	next    int
	full    bool
	counts  map[string]int
}

var buf = &ring{entries: make([]Entry, size), counts: map[string]int{}}

func (r *ring) add(e Entry) {
	r.mu.Lock()
	r.entries[r.next] = e
	r.next = (r.next + 1) % size
	r.full = r.full || r.next == 0
	r.counts[e.Level]++
	r.mu.Unlock()
}

// Recent returns up to n kept entries, newest first.
func Recent(n int) []Entry {
	buf.mu.Lock()
	defer buf.mu.Unlock()
	kept := buf.next
	if buf.full {
		kept = size
	}
	n = min(n, kept)
	out := make([]Entry, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, buf.entries[(buf.next-i+size)%size])
	}
	return out
}

// Counts returns how many warnings and errors were logged since the start, by level.
func Counts() map[string]int {
	buf.mu.Lock()
	defer buf.mu.Unlock()
	out := make(map[string]int, len(buf.counts))
	for k, v := range buf.counts {
		out[k] = v
	}
	return out
}

func reset() {
	buf.mu.Lock()
	buf.entries, buf.next, buf.full, buf.counts = make([]Entry, size), 0, false, map[string]int{}
	buf.mu.Unlock()
}

// Handler passes every record on and keeps a copy of warnings and errors.
type Handler struct {
	next   slog.Handler
	attrs  []slog.Attr
	groups string
}

func Wrap(next slog.Handler) *Handler { return &Handler{next: next} }

func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= slog.LevelWarn || h.next.Enabled(ctx, l)
}

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		e := Entry{At: r.Time, Level: strings.ToLower(r.Level.String()), Message: r.Message}
		put := func(key string, v slog.Value) {
			if e.Attrs == nil {
				e.Attrs = map[string]string{}
			}
			s := v.Resolve().String()
			if c := textx.Runes(s, maxValue); c != s {
				s = c + "…"
			}
			e.Attrs[key] = s
		}
		for _, a := range h.attrs {
			put(a.Key, a.Value)
		}
		r.Attrs(func(a slog.Attr) bool {
			put(h.groups+a.Key, a.Value)
			return true
		})
		buf.add(e)
	}
	if !h.next.Enabled(ctx, r.Level) {
		return nil
	}
	return h.next.Handle(ctx, r)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.next = h.next.WithAttrs(attrs)
	for _, a := range attrs {
		c.attrs = append(c.attrs[:len(c.attrs):len(c.attrs)], slog.Attr{Key: h.groups + a.Key, Value: a.Value})
	}
	return &c
}

func (h *Handler) WithGroup(name string) slog.Handler {
	c := *h
	c.next = h.next.WithGroup(name)
	c.groups = h.groups + name + "."
	return &c
}
