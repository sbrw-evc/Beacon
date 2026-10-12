// Package alert turns connector events into alerts: one alert per configuration item and
// signal, folded across sources and connectors, with a lifecycle (open, acknowledged,
// resolved), routing to the owning team, maintenance windows, delivery to PagerDuty and
// backup notification when PagerDuty does not take the alert.
package alert

import (
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

const (
	StatusOpen         = "open"
	StatusAcknowledged = "acknowledged"
	StatusResolved     = "resolved"

	// PagerDuty delivery states of an alert.
	PDPending  = "pending"
	PDAccepted = "accepted"
	PDAcked    = "acked"
	PDFailed   = "failed"
	PDSkipped  = "skipped"
	// PDOff: PagerDuty is turned off, so the alert is not sent there. It is not a failure: backup
	// notification is the main channel then.
	PDOff = "off"
	// PDStandby: PagerDuty is the backup of the notification channels (model.PDModeBackup):
	// the incident goes there only if nobody takes it in time.
	PDStandby = "standby"

	// Backup notification states of an alert.
	FallbackPending = "pending"
	// FallbackSending: the notifier is sending it; acknowledging the alert no longer cancels it.
	FallbackSending = "sending"
	FallbackSent    = "sent"

	// TestLabel marks the test events of a connector: their incidents go nowhere (neither to
	// PagerDuty nor to backup notification).
	TestLabel = "umbrella_test"

	SourceFiring   = "firing"
	SourceResolved = "resolved"

	// Route.Via: where the people of the route come from.
	ViaService  = "service"
	ViaCIOwners = "ci_owners"
	ViaNone     = "none"
)

// SeverityRank orders severities by model.Severities; unknown ones rank 0.
func SeverityRank(s string) int { return model.SeverityRank(s) }

func Active(status string) bool { return status == StatusOpen || status == StatusAcknowledged }

// IsTest reports whether the alert comes from a test event (label umbrella_test=true).
func (a *Alert) IsTest() bool { return a.Labels[TestLabel] == "true" }

// Source is one event of one connector that feeds the alert.
type Source struct {
	ConnectorID string `json:"connector_id"`
	Key         string `json:"key"`
	Status      string `json:"status"`
	Severity    string `json:"severity"`
	Title       string `json:"title"`
	Value       string `json:"value,omitempty"`
	// Description and Fields are what the connector shows of the event: the full text and
	// named values (model.Field).
	Description string        `json:"description,omitempty"`
	Fields      []model.Field `json:"fields,omitempty"`
	FirstSeen   time.Time     `json:"first_seen"`
	LastSeen    time.Time     `json:"last_seen"`
}

type Ref struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Person is someone the alert is routed to, with the contacts backup notification uses.
type Person struct {
	UserID   string `json:"user_id"`
	Name     string `json:"name"`
	Email    string `json:"email,omitempty"`
	Telegram string `json:"telegram,omitempty"`
	// Role: lead or member of the team, or the NetBox role of a CI owner.
	Role string `json:"role,omitempty"`
}

// Route is who the alert belongs to: the business services of its configuration item, the
// owning team of the most critical one and its people. Owners are the people responsible for
// the item in NetBox; they get the alert when the team has nobody.
type Route struct {
	Services []Ref `json:"services"`
	// Service is the primary service: the first of Services whose owning team gets the alert,
	// or the first service when none has a team. PagerDuty routes match it.
	Service *Ref     `json:"service,omitempty"`
	Team    *Ref     `json:"team,omitempty"`
	People  []Person `json:"people"`
	// Channel is the team's own mailbox and chat; then People is only the lead.
	Channel *Channel  `json:"channel,omitempty"`
	Owners  []Person  `json:"owners"`
	Via     string    `json:"via"`
	At      time.Time `json:"at"`
}

// Channel is a team channel backup notification goes to besides the people. Teams and Zoom are
// webhook URLs: they carry a secret and are shown redacted (Redacted).
type Channel struct {
	Email    string `json:"email,omitempty"`
	Telegram string `json:"telegram,omitempty"`
	Teams    string `json:"teams,omitempty"`
	Zoom     string `json:"zoom,omitempty"`
}

// TeamChannel is the own channel of a team; empty when the team has none.
func TeamChannel(t model.Team) Channel {
	return Channel{Email: t.Email, Telegram: t.Telegram, Teams: t.Teams, Zoom: t.Zoom}
}

// Empty: no address is set.
func (c Channel) Empty() bool { return c == Channel{} }

// Redacted is the channel as it may be shown: webhook URLs cut to their host and last characters.
func (c Channel) Redacted() Channel {
	if c.Teams != "" {
		c.Teams = model.RedactURL(c.Teams)
	}
	if c.Zoom != "" {
		c.Zoom = model.RedactURL(c.Zoom)
	}
	return c
}

// secretAddress: channels whose addresses are webhook URLs that carry a secret.
var secretAddress = map[string]bool{"teams": true, "zoom": true}

// Redacted is the alert as the API shows it: webhook URLs of the team channel and of the
// addresses reached are redacted. The stored alert keeps them for the follow-up.
func (a Alert) Redacted() Alert {
	if a.Route.Channel != nil {
		ch := a.Route.Channel.Redacted()
		a.Route.Channel = &ch
	}
	if len(a.Notified) > 0 {
		out := make([]Notified, len(a.Notified))
		for i, n := range a.Notified {
			if secretAddress[n.Channel] {
				n.Address = model.RedactURL(n.Address)
			}
			out[i] = n
		}
		a.Notified = out
	}
	return a
}

// Recipients are the people backup notification goes to; a team channel comes on top.
func (r Route) Recipients() []Person {
	if len(r.People) > 0 {
		return r.People
	}
	return r.Owners
}

func (r Route) ServiceIDs() []string {
	out := make([]string, 0, len(r.Services))
	for _, s := range r.Services {
		out = append(out, s.ID)
	}
	return out
}

// InScope reports whether the alert belongs to one of the business services; an empty scope
// takes every alert, an alert without services is in no other scope.
func (a *Alert) InScope(scope []string) bool {
	if len(scope) == 0 {
		return true
	}
	for _, s := range a.Route.Services {
		if slices.Contains(scope, s.ID) {
			return true
		}
	}
	return false
}

// PD is the PagerDuty side of an alert.
type PD struct {
	State string `json:"state"`
	// Key is the dedup_key of the Events API: umb-<alert id>.
	Key string `json:"key"`
	// Route names the PagerDuty route of the last delivery. RouteID is the route the accepted
	// trigger went by: acknowledge, resolve and severity updates go to that PagerDuty service
	// even after the alert is routed to another team. It is cleared when the alert reopens.
	Route   string `json:"route,omitempty"`
	RouteID string `json:"route_id,omitempty"`
	Error   string `json:"error,omitempty"`
	// ErrorCode is the code of Error the interface translates (see DeliveryError).
	ErrorCode   string     `json:"error_code,omitempty"`
	Retry       string     `json:"retry,omitempty"`
	AttemptAt   *time.Time `json:"attempt_at,omitempty"`
	IncidentID  string     `json:"incident_id,omitempty"`
	IncidentURL string     `json:"incident_url,omitempty"`
	// OldIncidents are the PagerDuty incidents of earlier openings of the alert: their late
	// webhooks must not change the reopened alert.
	OldIncidents []string `json:"old_incidents,omitempty"`
	// Escalated: the incident left standby (or was sent by hand or by an escalation step), so a
	// new trigger goes to PagerDuty whatever the mode says. Cleared when the alert reopens.
	Escalated bool `json:"escalated,omitempty"`
	// Queue is the PagerDuty service the incident is in, as PagerDuty last said; it changes when
	// the incident is moved to another service there.
	Queue     string `json:"queue,omitempty"`
	QueueName string `json:"queue_name,omitempty"`
}

type Alert struct {
	ID       string `json:"id"`
	DedupKey string `json:"dedup_key"`
	Title    string `json:"title"`
	// CIID is empty while the item named by the events is not in the catalog; CIName keeps the
	// name the events use.
	CIID      string             `json:"ci_id,omitempty"`
	CIName    string             `json:"ci_name"`
	CIKind    string             `json:"ci_kind,omitempty"`
	Signal    string             `json:"signal"`
	Method    string             `json:"method"`
	Severity  string             `json:"severity"`
	Status    string             `json:"status"`
	Sources   map[string]*Source `json:"sources"`
	Labels    map[string]string  `json:"labels"`
	Count     int                `json:"count"`
	FirstSeen time.Time          `json:"first_seen"`
	// OpenedAt is when the alert last opened: the first event or a reopening in the window.
	OpenedAt   time.Time  `json:"opened_at"`
	LastSeen   time.Time  `json:"last_seen"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy string     `json:"resolved_by,omitempty"`
	AckedBy    string     `json:"acked_by,omitempty"`
	AckedAt    *time.Time `json:"acked_at,omitempty"`
	// Suppressed: a maintenance window covers the item or its service; no trigger is sent and no
	// backup notification, but an incident PagerDuty already has is still acknowledged and resolved.
	Suppressed    bool   `json:"suppressed"`
	MaintenanceID string `json:"maintenance_id,omitempty"`
	Route         Route  `json:"route"`
	PD            PD     `json:"pd"`
	// UnsuppressedAt is when a maintenance window over the alert last ended: backup
	// notification waits its delay from then, as it does from OpenedAt.
	UnsuppressedAt *time.Time `json:"unsuppressed_at,omitempty"`
	Fallback       bool       `json:"fallback"`
	FallbackAt     *time.Time `json:"fallback_at,omitempty"`
	// FallbackState is pending from the moment backup notification is due until the notifier
	// reports it was attempted (sent); Tick hands a pending one to the notifier again, so it
	// survives a restart or a full queue. FallbackTry is when it was last handed over.
	FallbackState string     `json:"fallback_state,omitempty"`
	FallbackTry   *time.Time `json:"fallback_try,omitempty"`
	// SendingAt is when a notifier claimed the sending (FallbackSending).
	SendingAt *time.Time `json:"sending_at,omitempty"`
	// Notified are the addresses backup notification reached; they get a follow-up when the
	// alert is acknowledged or resolved.
	Notified []Notified `json:"notified,omitempty"`
	// FollowUp is the follow-up still to send to Notified: acknowledged or resolved. It is
	// pending until the notifier reports it, like FallbackState; FollowUpTry is when it was
	// last handed over.
	FollowUp    string     `json:"follow_up,omitempty"`
	FollowUpTry *time.Time `json:"follow_up_try,omitempty"`
	RelatedID   string     `json:"related_id,omitempty"`

	// EventCI is the name the first event gave the item; the item is found by it again when
	// the hand-made links of monitoring hosts change.
	EventCI string `json:"event_ci,omitempty"`
	// Excluded: the host of the events is marked «Не является КЕ» on the monitoring systems
	// page; the alert is suppressed (not sent anywhere) for that reason, not a window.
	Excluded bool `json:"excluded,omitempty"`
}

// Notified is an address backup notification of the alert was sent to.
type Notified struct {
	Channel string `json:"channel"`
	Address string `json:"address"`
	// Recipient is who the address belongs to (see notify.UserRecipient), for the time zone of
	// the follow-up.
	Recipient string `json:"recipient,omitempty"`
	// Ref names the message sent where the channel can answer it (the Telegram message ID).
	Ref string `json:"ref,omitempty"`
}

func (a *Alert) Clone() Alert {
	c := *a
	c.Sources = make(map[string]*Source, len(a.Sources))
	for k, v := range a.Sources {
		s := *v
		c.Sources[k] = &s
	}
	c.Labels = maps.Clone(a.Labels)
	c.Route.Services = slices.Clone(a.Route.Services)
	c.Route.People = slices.Clone(a.Route.People)
	c.Route.Owners = slices.Clone(a.Route.Owners)
	c.Notified = slices.Clone(a.Notified)
	c.PD.OldIncidents = slices.Clone(a.PD.OldIncidents)
	return c
}

// Entry is a line of the alert timeline. Code and Args are translated by the interface.
type Entry struct {
	ID     int64             `json:"id"`
	At     time.Time         `json:"at"`
	Kind   string            `json:"kind"`
	Code   string            `json:"code"`
	Args   map[string]string `json:"args,omitempty"`
	Author string            `json:"author,omitempty"`
}

// Timeline kinds.
const (
	KindStatus      = "status"
	KindEvent       = "event"
	KindPagerDuty   = "pagerduty"
	KindMaintenance = "maintenance"
	KindFallback    = "fallback"
	KindComment     = "comment"
	KindRoute       = "route"
)

// Action is what the PagerDuty Events API is asked to do with an alert.
type Action string

const (
	PDTrigger     Action = "trigger"
	PDAcknowledge Action = "acknowledge"
	PDResolve     Action = "resolve"
	// PDNote adds Command.Text as a note of the PagerDuty incident (REST API); it does not
	// change the delivery state of the alert.
	PDNote Action = "note"
)

// Command asks the PagerDuty gateway to deliver an action for a copy of the alert.
type Command struct {
	Action Action
	Alert  Alert
	// Text and Actor: the note of PDNote and who wrote it.
	Text, Actor string
}

// Sender delivers commands to PagerDuty and reports the outcome back with Engine.PDResult.
type Sender interface {
	Send(cmd Command)
}

// Notifier sends backup notification for an alert nobody has taken (PagerDuty is off or did
// not take it) and reports the attempt back with Engine.FallbackDone. Fallback may be called
// again for the same alert while it is pending. FollowUp tells the addresses that got backup
// notification that the alert was acknowledged or resolved (Alert.FollowUp) and reports back
// with Engine.FollowUpDone.
type Notifier interface {
	Fallback(a Alert)
	FollowUp(a Alert)
	// On tells whether any channel is turned on, so the channels can go before PagerDuty.
	On() bool
}

// DeliveryError is a failed PagerDuty delivery with a code the interface translates: no_key,
// key_unavailable, queue_full, breaker, unreachable, unavailable, rejected or below_threshold.
// Detail is shown next to the translated code (the minimum severity, an HTTP status and the
// answer of PagerDuty).
type DeliveryError struct {
	Code   string
	Msg    string
	Detail string
	// Err is the sentinel the error matches with errors.Is.
	Err error
}

func (e *DeliveryError) Error() string {
	msg := e.Msg
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if e.Detail != "" {
		return msg + ": " + e.Detail
	}
	return msg
}

func (e *DeliveryError) Unwrap() error { return e.Err }

// DeliveryCode is the code of a PagerDuty delivery error, "error" when it has none.
func DeliveryCode(err error) (code, detail string) {
	var d *DeliveryError
	if errors.As(err, &d) {
		return d.Code, d.Detail
	}
	return "error", err.Error()
}

// Incoming is an event handed to the engine.
type Incoming struct {
	ConnectorID string
	Key         string
	Title       string
	CI          string
	Signal      string
	Method      string
	Severity    string
	Status      string
	Value       string
	Description string
	Fields      []model.Field
	Labels      map[string]string
}
