package store_test

import (
	"context"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store/storetest"
)

func TestPostgresHealth(t *testing.T) {
	cfg := storetest.TempDatabase(t)
	ctx := context.Background()
	backend, err := store.OpenPostgres(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	h, err := backend.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h.Database != cfg.Database || h.User != cfg.User || h.Size <= 0 || h.StartedAt == nil || h.Connections < 1 || h.MaxConnections < h.Connections {
		t.Errorf("health = %+v", h)
	}
	if h.Pool.Max < 1 || h.Pool.Total < 1 || h.Pool.Acquires < 1 {
		t.Errorf("pool = %+v", h.Pool)
	}
}
