package alert

import (
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

const (
	day = 24 * time.Hour
	// The longest values the alerting policy may set.
	MaxWindow       = 7 * day
	MaxFallback     = day
	MaxRetention    = 10 * 365 * day
	MaxTestLifetime = day
)

// DefaultPolicy is the alerting policy of an engine with the package defaults, in the units of
// the settings.
func DefaultPolicy() model.AlertPolicy {
	return model.AlertPolicy{ReopenWindowSeconds: int(DefaultWindow / time.Second), FallbackDelaySeconds: int(DefaultFallbackAfter / time.Second),
		FallbackRetrySeconds: int(DefaultFallbackRetry / time.Second), RetentionDays: int(Retention / day),
		TestLifetimeSeconds: int(TestLifetime / time.Second)}
}

// PolicyLimits are the longest values of the alerting policy, in the units of the settings.
func PolicyLimits() model.AlertPolicy {
	return model.AlertPolicy{ReopenWindowSeconds: int(MaxWindow / time.Second), FallbackDelaySeconds: int(MaxFallback / time.Second),
		FallbackRetrySeconds: int(MaxFallback / time.Second), RetentionDays: int(MaxRetention / day),
		TestLifetimeSeconds: int(MaxTestLifetime / time.Second)}
}
