package app

import (
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/entra"
)

// A full table of started sign-ins gives way to a new start instead of refusing it.
func TestEntraPendingBounded(t *testing.T) {
	s := NewEntraService(nil, nil, nil, nil)
	now := time.Now()
	first := entra.NewRequest()
	s.remember(first, now.Add(-time.Minute))
	for i := 1; i < entraPendingMax; i++ {
		s.remember(entra.NewRequest(), now)
	}
	fresh := entra.NewRequest()
	s.remember(fresh, now)
	if len(s.pending) != entraPendingMax {
		t.Fatalf("pending = %d, want the cap %d", len(s.pending), entraPendingMax)
	}
	if _, ok := s.take(fresh.State); !ok {
		t.Error("the new sign-in is kept")
	}
	if _, ok := s.take(first.State); ok {
		t.Error("the oldest sign-in gave way")
	}
	s.remember(entra.NewRequest(), now.Add(entraPendingTTL+time.Second))
	if len(s.pending) != 1 {
		t.Errorf("expired sign-ins are dropped: %d left", len(s.pending))
	}
}
