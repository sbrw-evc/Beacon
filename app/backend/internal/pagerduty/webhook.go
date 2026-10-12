package pagerduty

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
)

var (
	ErrNoWebhookSecret = errors.New("no PagerDuty webhook signing secret is set: the webhook is refused")
	ErrBadSignature    = errors.New("the X-PagerDuty-Signature check failed")
)

// VerifySignature checks X-PagerDuty-Signature: one or more v1=<hex HMAC-SHA256 of the body>.
func VerifySignature(secret string, body []byte, header string) bool {
	if secret == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	for _, part := range strings.Split(header, ",") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(part), "v1="); ok && hmac.Equal([]byte(v), []byte(want)) {
			return true
		}
	}
	return false
}

type summary struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

type WebhookEvent struct {
	Event struct {
		ID           string    `json:"id"`
		EventType    string    `json:"event_type"`
		ResourceType string    `json:"resource_type"`
		OccurredAt   time.Time `json:"occurred_at"`
		Agent        *summary  `json:"agent"`
		Data         struct {
			ID          string    `json:"id"`
			Type        string    `json:"type"`
			HTMLURL     string    `json:"html_url"`
			IncidentKey string    `json:"incident_key"`
			Status      string    `json:"status"`
			Content     string    `json:"content"`
			Assignees   []summary `json:"assignees"`
			Priority    *summary  `json:"priority"`
			// Service is the queue the incident is in.
			Service  *summary `json:"service"`
			Incident *struct {
				ID      string `json:"id"`
				HTMLURL string `json:"html_url"`
			} `json:"incident"`
		} `json:"data"`
	} `json:"event"`
}

func (w WebhookEvent) incident() (string, string) {
	d := w.Event.Data
	if d.Incident != nil && d.Incident.ID != "" {
		return d.Incident.ID, d.Incident.HTMLURL
	}
	if d.Type == "incident" || strings.HasPrefix(w.Event.EventType, "incident.") {
		return d.ID, d.HTMLURL
	}
	return "", ""
}

func (w WebhookEvent) detail() string {
	d := w.Event.Data
	switch w.Event.EventType {
	case "incident.reassigned", "incident.escalated":
		var names []string
		for _, a := range d.Assignees {
			names = append(names, a.Summary)
		}
		return strings.Join(names, ", ")
	case "incident.priority_updated":
		if d.Priority != nil {
			return d.Priority.Summary
		}
	case "incident.annotated":
		return truncate(d.Content, 300)
	}
	return ""
}

// HandleWebhook verifies and applies a Webhooks v3 delivery. It returns how many alerts it
// changed.
func (g *Gateway) HandleWebhook(ctx context.Context, body []byte, signature string) (int, error) {
	set, _ := g.settings()
	if set.WebhookSecretRef == "" {
		return 0, ErrNoWebhookSecret
	}
	secret, err := g.sec.Resolve(set.WebhookSecretRef)
	if err != nil {
		return 0, err
	}
	if !VerifySignature(secret, body, signature) {
		return 0, ErrBadSignature
	}
	var ev WebhookEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	g.mu.Lock()
	g.stat.LastWebhookAt = &now
	g.mu.Unlock()
	if ev.Event.EventType == "pagey.ping" || g.results == nil {
		return 0, nil
	}
	if ev.Event.EventType == "incident.annotated" && isOwnNote(ev.Event.Data.Content) {
		// A note Umbrella wrote itself: the comment is already on the timeline.
		return 0, nil
	}
	incID, incURL := ev.incident()
	actor := ""
	if ev.Event.Agent != nil {
		actor = ev.Event.Agent.Summary
	}
	keys, err := g.keysFor(ctx, ev.Event.Data.IncidentKey, incID)
	if err != nil {
		return 0, err
	}
	applied := 0
	for _, k := range keys {
		u := alert.PDUpdate{DedupKey: k, EventType: ev.Event.EventType, Actor: actor,
			IncidentID: incID, IncidentURL: incURL, Detail: ev.detail(), OccurredAt: ev.Event.OccurredAt}
		if sv := ev.Event.Data.Service; sv != nil && ev.Event.Data.Incident == nil {
			// The service of the incident itself (an event about a note names the incident apart).
			u.Queue, u.QueueName = sv.ID, sv.Summary
		}
		err := g.results.PDInbound(ctx, u)
		switch {
		case err == nil:
			applied++
		case !errors.Is(err, alert.ErrNotFound):
			// PagerDuty delivers the webhook again when it is not taken.
			return applied, err
		}
	}
	return applied, nil
}

// keysFor finds the alerts of an incident: by its incident_key (the dedup_key Umbrella sent),
// by the incident ID seen before, or by asking PagerDuty which alerts the incident groups.
func (g *Gateway) keysFor(ctx context.Context, incidentKey, incidentID string) ([]string, error) {
	keys, err := g.results.PDKeys(ctx, incidentKey, incidentID)
	if err != nil || len(keys) > 0 || incidentID == "" {
		return keys, err
	}
	if strings.HasPrefix(incidentKey, "umb-") {
		return []string{incidentKey}, nil
	}
	cctx, cancel := context.WithTimeout(ctx, DefaultLookupTimeout)
	defer cancel()
	found, err := g.IncidentAlertKeys(cctx, incidentID)
	if err != nil {
		return nil, nil
	}
	for _, k := range found {
		if strings.HasPrefix(k, "umb-") {
			keys = append(keys, k)
		}
	}
	return keys, nil
}
