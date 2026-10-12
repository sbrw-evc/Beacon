package notify

import "time"

// Delivery defaults of backup notification, in one place.
const (
	// DefaultQueueSize is how many notifications wait for delivery; more are handed over again
	// later by the engine.
	DefaultQueueSize = 1000
	// DefaultHTTPTimeout bounds one call to the Telegram Bot API.
	DefaultHTTPTimeout = 15 * time.Second
	// DefaultRetries is how many times a message is tried; DefaultBackoff is the first pause
	// between tries, doubled after each.
	DefaultRetries = 3
	DefaultBackoff = 2 * time.Second
	// DefaultSMTPTimeout bounds the delivery of one e-mail.
	DefaultSMTPTimeout = 30 * time.Second
	// LinkTTL is how long an acknowledgement link in a notification works.
	LinkTTL = 24 * time.Hour
)
