package ingest

import "time"

// Config tunes the intake. A zero field means its default, so the zero Config behaves like
// DefaultConfig.
type Config struct {
	// Workers drain the queue in parallel (batches still fold one at a time, see Drain).
	Workers int
	// BatchSize is how many pending requests one batch claims.
	BatchSize int
	// MaxAttempts bounds the retries of a request whose storing or folding failed for a
	// reason that may pass (a lock timeout, the alert tables not ready yet).
	MaxAttempts int
	// Retention is how long requests, failures, statistics and idempotency keys are kept.
	Retention Retention
	// ProcessTimeout bounds one run of a connector over a request.
	ProcessTimeout time.Duration
	// TestEventWait is how long a test event waits for its incident before it answers.
	TestEventWait time.Duration
}

// DefaultConfig is the intake as it runs unless configured otherwise.
func DefaultConfig() Config {
	return Config{Workers: 2, BatchSize: 50, MaxAttempts: 3, Retention: DefaultRetention, ProcessTimeout: 30 * time.Second,
		TestEventWait: 15 * time.Second}
}

// WithDefaults fills the zero fields with the defaults.
func (c Config) WithDefaults() Config {
	d := DefaultConfig()
	orDefault(&c.Workers, d.Workers)
	orDefault(&c.BatchSize, d.BatchSize)
	orDefault(&c.MaxAttempts, d.MaxAttempts)
	orDefault(&c.Retention.Requests, d.Retention.Requests)
	orDefault(&c.Retention.Failures, d.Retention.Failures)
	orDefault(&c.Retention.Stats, d.Retention.Stats)
	orDefault(&c.Retention.Dedup, d.Retention.Dedup)
	orDefault(&c.ProcessTimeout, d.ProcessTimeout)
	orDefault(&c.TestEventWait, d.TestEventWait)
	return c
}

func orDefault[T int | time.Duration](v *T, def T) {
	if *v <= 0 {
		*v = def
	}
}
