package pagerduty

import "time"

// Delivery defaults of the PagerDuty gateway, in one place.
const (
	// DefaultQueueSize is how many commands wait for delivery; more fail as queue_full.
	DefaultQueueSize = 10000
	// DefaultHTTPTimeout bounds one call to PagerDuty.
	DefaultHTTPTimeout = 15 * time.Second
	// DefaultRetries is how many times an event is tried; DefaultBackoff is the first pause
	// between tries, doubled after each.
	DefaultRetries = 3
	DefaultBackoff = 500 * time.Millisecond
	// After DefaultBreakerThreshold failures in a row delivery pauses for DefaultBreakerPause.
	DefaultBreakerThreshold = 5
	DefaultBreakerPause     = 60 * time.Second
	// DefaultLookupTimeout bounds asking PagerDuty which alerts an incident groups.
	DefaultLookupTimeout = 10 * time.Second
)
