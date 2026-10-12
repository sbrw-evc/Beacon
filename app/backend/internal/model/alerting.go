package model

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

const (
	PDRegionUS = "us"
	PDRegionEU = "eu"
)

// Alerting holds where alerts go: PagerDuty, backup notification, Grafana and Umbrella Flow.
type Alerting struct {
	// PublicURL is the address people and PagerDuty reach Umbrella at, for links and webhooks.
	PublicURL string    `json:"public_url"`
	PagerDuty PagerDuty `json:"pagerduty"`
	Notify    Notify    `json:"notify"`
	Grafana   Grafana   `json:"grafana"`
	// Flow is where the traffic of an incident or an item is shown (Umbrella Flow).
	Flow NetworkFlow `json:"flow"`
	// Policy tunes the lifecycle of alerts; nil keeps the defaults of the alert engine.
	Policy *AlertPolicy `json:"policy,omitempty"`
}

// NormalizePublicURL checks the address Umbrella is reached at and drops trailing slashes. An
// empty address stays empty (not set).
func NormalizePublicURL(v string) (string, error) {
	v = strings.TrimRight(strings.TrimSpace(v), "/")
	if v == "" {
		return "", nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("the public address must be an http or https URL without a query")
	}
	return v, nil
}

// PagerDuty is the connection to PagerDuty. Keys and tokens are in OpenBao; only references
// are kept here.
type PagerDuty struct {
	Enabled bool   `json:"enabled"`
	Region  string `json:"region"`
	// EventsURL and APIURL replace the regional addresses (a proxy, tests).
	EventsURL             string    `json:"events_url,omitempty"`
	APIURL                string    `json:"api_url,omitempty"`
	RoutingKeyRef         string    `json:"-"`
	ServiceID             string    `json:"service_id,omitempty"`
	ServiceName           string    `json:"service_name,omitempty"`
	APITokenRef           string    `json:"-"`
	WebhookSecretRef      string    `json:"-"`
	WebhookSubscriptionID string    `json:"webhook_subscription_id,omitempty"`
	MinSeverity           string    `json:"min_severity"`
	Routes                []PDRoute `json:"routes"`
	// Mode is the role of PagerDuty next to the notification channels of Umbrella (PDMode*);
	// empty is primary. Modes replaces it for some severities.
	Mode  string            `json:"mode,omitempty"`
	Modes map[string]string `json:"modes,omitempty"`
	// BackupAfterSeconds: in backup mode, how long an incident nobody has taken waits before it
	// goes to PagerDuty; 0 is DefaultPDBackupAfter.
	BackupAfterSeconds int `json:"backup_after_seconds,omitempty"`
	// Sync tunes the two-way synchronization through the REST API (it needs the API token).
	Sync      PDSync     `json:"sync"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	UpdatedBy string     `json:"updated_by,omitempty"`
}

// The role of PagerDuty next to the notification channels of Umbrella.
const (
	// PDModePrimary: incidents go to PagerDuty at once; the channels of Umbrella are the backup
	// when PagerDuty does not take an incident in time.
	PDModePrimary = "primary"
	// PDModeBackup: the channels of Umbrella go first; PagerDuty gets an incident only when
	// nobody has taken it in time or the channels reached nobody.
	PDModeBackup = "backup"
	// PDModeParallel: PagerDuty and the channels of Umbrella get an incident at once.
	PDModeParallel = "parallel"
	// PDModeOff: incidents of the severity do not go to PagerDuty.
	PDModeOff = "off"
)

// DefaultPDBackupAfter is how long backup mode waits before PagerDuty by default.
const DefaultPDBackupAfter = 5 * time.Minute

// ValidPDMode tells whether v names a mode; empty is not one.
func ValidPDMode(v string) bool {
	return v == PDModePrimary || v == PDModeBackup || v == PDModeParallel || v == PDModeOff
}

// ModeFor is the mode for incidents of a severity: the one set for it, else the general one.
func (s PagerDuty) ModeFor(severity string) string {
	if m := s.Modes[severity]; m != "" {
		return m
	}
	if s.Mode != "" {
		return s.Mode
	}
	return PDModePrimary
}

// BackupAfter is how long backup mode waits before PagerDuty.
func (s PagerDuty) BackupAfter() time.Duration {
	if s.BackupAfterSeconds <= 0 {
		return DefaultPDBackupAfter
	}
	return time.Duration(s.BackupAfterSeconds) * time.Second
}

// PDSync is the synchronization with PagerDuty beyond the Events API and the webhooks.
type PDSync struct {
	// IntervalSeconds: how often incident states are read back (covers lost webhooks or
	// an Umbrella PagerDuty cannot reach); 0 is the default, negative turns it off.
	IntervalSeconds int `json:"interval_seconds,omitempty"`
	// FromEmail is the PagerDuty user Umbrella writes notes and priorities as (the From header
	// of the REST API); empty turns writing off.
	FromEmail string `json:"from_email,omitempty"`
	// Notes: comments made in Umbrella become notes of the PagerDuty incident.
	Notes bool `json:"notes"`
	// Priority: the response priority (P1–P5) is set on the PagerDuty incident.
	Priority bool `json:"priority"`
	// OnCall: who is on call in PagerDuty also gets the notifications of Umbrella.
	OnCall bool `json:"on_call"`
	// Queues: a PagerDuty service (queue) owned by a PagerDuty team named like a team of
	// Umbrella gets a route of that team as soon as it is read.
	Queues bool `json:"queues"`
}

// DefaultPDSyncInterval is how often incident states are read back by default.
const DefaultPDSyncInterval = time.Minute

// Interval is how often incident states are read back; 0 is off.
func (s PDSync) Interval() time.Duration {
	switch {
	case s.IntervalSeconds < 0:
		return 0
	case s.IntervalSeconds == 0:
		return DefaultPDSyncInterval
	}
	return max(time.Duration(s.IntervalSeconds)*time.Second, 15*time.Second)
}

func (s PagerDuty) Events() string {
	if s.EventsURL != "" {
		return s.EventsURL
	}
	if s.Region == PDRegionEU {
		return "https://events.eu.pagerduty.com/v2/enqueue"
	}
	return "https://events.pagerduty.com/v2/enqueue"
}

func (s PagerDuty) API() string {
	if s.APIURL != "" {
		return s.APIURL
	}
	if s.Region == PDRegionEU {
		return "https://api.eu.pagerduty.com"
	}
	return "https://api.pagerduty.com"
}

// PDRoute sends the alerts of a team or a business service to a PagerDuty service of their
// own. Empty TeamID or ServiceID match any.
type PDRoute struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	TeamID        string `json:"team_id,omitempty"`
	ServiceID     string `json:"service_id,omitempty"`
	RoutingKeyRef string `json:"-"`
	PDServiceID   string `json:"pd_service_id,omitempty"`
	PDServiceName string `json:"pd_service_name,omitempty"`
}

// Notify is backup notification: when nobody has taken a severe enough alert (PagerDuty is
// off or did not take it in time), the people of its route get it by e-mail and Telegram, team
// channels also in Microsoft Teams and Zoom, and a follow-up once it is acknowledged or resolved.
type Notify struct {
	Email    EmailChannel    `json:"email"`
	Telegram TelegramChannel `json:"telegram"`
	Teams    TeamsChannel    `json:"teams"`
	Zoom     ZoomChannel     `json:"zoom"`
	// DelaySeconds is how long an open alert waits before backup notification; 0 sends it at
	// once. Nil is automatic: 2 minutes while PagerDuty is on (time for it to take the alert),
	// at once while it is off.
	DelaySeconds *int `json:"delay_seconds"`
	// MinSeverity is the lowest severity that goes to backup notification; empty is error.
	MinSeverity string `json:"min_severity"`
	// Extra recipients that always get backup notification (a duty mailbox, a group chat).
	ExtraEmails   []string `json:"extra_emails"`
	ExtraTelegram []string `json:"extra_telegram"`
	// ExtraTeams and ExtraZoom are webhook URLs of Teams channels and Zoom chats.
	ExtraTeams []string   `json:"extra_teams"`
	ExtraZoom  []string   `json:"extra_zoom"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
	UpdatedBy  string     `json:"updated_by,omitempty"`
	// Templates replace built-in message templates by name ("fallback.text", "followup.html"…);
	// nil keeps the built-in ones.
	Templates map[string]string `json:"templates,omitempty"`
}

const (
	SMTPStartTLS = "starttls"
	SMTPTLS      = "tls"
	SMTPNone     = "none"
)

type EmailChannel struct {
	Enabled     bool   `json:"enabled"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Security    string `json:"security"`
	SkipVerify  bool   `json:"skip_verify"`
	Username    string `json:"username"`
	PasswordRef string `json:"-"`
	From        string `json:"from"`
}

type TelegramChannel struct {
	Enabled  bool   `json:"enabled"`
	TokenRef string `json:"-"`
	// Bot turns on the bot built into Umbrella: it reads updates by long polling (no webhook,
	// nothing to deploy), lets people acknowledge, resolve and comment from Telegram, link their
	// account, and keeps the messages it sent in step with the incident.
	Bot bool `json:"bot"`
	// APIURL replaces https://api.telegram.org (a proxy, tests).
	APIURL string `json:"api_url,omitempty"`
}

// TeamsChannel posts to Microsoft Teams channels through their incoming webhooks (a Workflows
// "When a Teams webhook request is received" flow or a legacy Incoming Webhook). The webhook URL
// is the address and carries its own secret, so the channel has no other one.
type TeamsChannel struct {
	Enabled bool `json:"enabled"`
}

// ZoomChannel posts to Zoom Team Chat channels through the Incoming Webhook app: the endpoint
// URL is the address, the verification token (kept in OpenBao) goes in the Authorization header.
type ZoomChannel struct {
	Enabled  bool   `json:"enabled"`
	TokenRef string `json:"-"`
}

// Grafana opens the context of an incident: a dashboard with the time around the incident and
// variables for the item, the service, the team and the incident.
type Grafana struct {
	// DashboardURL is the dashboard address, for example https://grafana/d/abc/incident; its own
	// query (an organization, fixed variables) is kept.
	DashboardURL string `json:"dashboard_url"`
	// WindowMinute is how much time before the incident and after its end is shown.
	WindowMinute int        `json:"window_minutes"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
	UpdatedBy    string     `json:"updated_by,omitempty"`
}

// NetworkFlow is Umbrella Flow, the NetFlow / IPFIX / sFlow console: the incident and item
// cards link to its traffic around the incident through Umbrella (/go/incidents/{id}/flow).
type NetworkFlow struct {
	// URL is the public address of Umbrella Flow, without a trailing slash; empty: no links.
	URL       string     `json:"url"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	UpdatedBy string     `json:"updated_by,omitempty"`
}

// AlertPolicy tunes the lifecycle of alerts. A zero field keeps the default of the alert
// engine: reopen window 10 minutes, fallback delay 2 minutes, fallback retry 5 minutes,
// retention 90 days, test incidents 5 minutes.
type AlertPolicy struct {
	// ReopenWindowSeconds: an alert resolved this long ago opens again on a new firing event
	// instead of a new alert, and RED and USE alerts of one service opened within it are linked.
	ReopenWindowSeconds int `json:"reopen_window_seconds,omitempty"`
	// FallbackDelaySeconds: how long backup notification waits for PagerDuty to take an alert
	// while PagerDuty is on and Notify.DelaySeconds is not set.
	FallbackDelaySeconds int `json:"fallback_delay_seconds,omitempty"`
	// FallbackRetrySeconds: a backup notification or follow-up not reported as attempted this
	// long after it was handed to the notifier is handed over again.
	FallbackRetrySeconds int `json:"fallback_retry_seconds,omitempty"`
	// RetentionDays: resolved alerts older than this are deleted with their timelines.
	RetentionDays int `json:"retention_days,omitempty"`
	// TestLifetimeSeconds: a test incident of a connector resolves itself after this long.
	TestLifetimeSeconds int        `json:"test_lifetime_seconds,omitempty"`
	UpdatedAt           *time.Time `json:"updated_at,omitempty"`
	UpdatedBy           string     `json:"updated_by,omitempty"`
}
