package ingest_test

import (
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/ingest"
)

// The zero config is the default one, and set fields are kept.
func TestConfigDefaults(t *testing.T) {
	if got := (ingest.Config{}).WithDefaults(); got != ingest.DefaultConfig() {
		t.Errorf("zero config = %+v", got)
	}
	d := ingest.DefaultConfig()
	if d.Workers != 2 || d.BatchSize != 50 || d.MaxAttempts != 3 || d.Retention != ingest.DefaultRetention ||
		d.ProcessTimeout != 30*time.Second || d.TestEventWait != 15*time.Second {
		t.Errorf("defaults changed: %+v", d)
	}
	got := ingest.Config{Workers: 5, Retention: ingest.Retention{Dedup: time.Hour}}.WithDefaults()
	if got.Workers != 5 || got.Retention.Dedup != time.Hour || got.Retention.Requests != ingest.DefaultRetention.Requests || got.BatchSize != 50 {
		t.Errorf("partial config = %+v", got)
	}
}
