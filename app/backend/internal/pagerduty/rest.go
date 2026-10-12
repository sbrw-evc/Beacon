package pagerduty

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

const (
	eventsIntegrationType = "events_api_v2_inbound_integration"
	pageLimit             = 100
	maxPages              = 20
)

// WebhookEvents are the incident events Umbrella subscribes to.
var WebhookEvents = []string{
	"incident.triggered", "incident.acknowledged", "incident.unacknowledged", "incident.resolved",
	"incident.reopened", "incident.reassigned", "incident.escalated", "incident.annotated", "incident.priority_updated",
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("PagerDuty REST API answered %d: %s", e.Status, e.Message)
}

// rest calls the REST API with the saved token, or with the given one before it is saved.
func (g *Gateway) rest(ctx context.Context, token, method, path string, q url.Values, body, out any) error {
	set, _ := g.settings()
	return g.restWith(ctx, set, token, method, path, q, body, out)
}

func (g *Gateway) restWith(ctx context.Context, set model.PagerDuty, token, method, path string, q url.Values, body, out any) error {
	return g.restAs(ctx, set, token, "", method, path, q, body, out)
}

// restAs is restWith on behalf of a PagerDuty user (the From header writes need).
func (g *Gateway) restAs(ctx context.Context, set model.PagerDuty, token, from, method, path string, q url.Values, body, out any) error {
	if token == "" {
		if set.APITokenRef == "" {
			return ErrNoAPIToken
		}
		var err error
		if token, err = g.sec.Resolve(set.APITokenRef); err != nil {
			return fmt.Errorf("PagerDuty API token: %w", err)
		}
	}
	u := strings.TrimRight(set.API(), "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token token="+token)
	req.Header.Set("Accept", "application/vnd.pagerduty+json;version=2")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if from != "" {
		req.Header.Set("From", from)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("PagerDuty REST API is unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Message string   `json:"message"`
				Errors  []string `json:"errors"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := strings.TrimSpace(e.Error.Message + " " + strings.Join(e.Error.Errors, "; "))
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return &APIError{Status: resp.StatusCode, Message: msg}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type CheckResult struct {
	OK        bool `json:"ok"`
	Abilities int  `json:"abilities"`
}

// CheckAPI checks the REST API token.
func (g *Gateway) CheckAPI(ctx context.Context, token string) (CheckResult, error) {
	var out struct {
		Abilities []string `json:"abilities"`
	}
	if err := g.rest(ctx, token, http.MethodGet, "/abilities", nil, nil, &out); err != nil {
		return CheckResult{}, err
	}
	return CheckResult{OK: true, Abilities: len(out.Abilities)}, nil
}

type Service struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	HTMLURL   string `json:"html_url"`
	Status    string `json:"status"`
	EventsKey bool   `json:"events_key"`
}

type integrationRef struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	Name           string `json:"name"`
	IntegrationKey string `json:"integration_key"`
}

type serviceObj struct {
	ID               string           `json:"id"`
	Summary          string           `json:"summary"`
	Name             string           `json:"name"`
	HTMLURL          string           `json:"html_url"`
	Status           string           `json:"status"`
	Integrations     []integrationRef `json:"integrations"`
	EscalationPolicy summary          `json:"escalation_policy"`
	Teams            []summary        `json:"teams"`
}

func (s serviceObj) name() string {
	if s.Name != "" {
		return s.Name
	}
	return s.Summary
}

// eventsKey tells a service with an Events API v2 integration.
func (s serviceObj) eventsKey() bool {
	return slices.ContainsFunc(s.Integrations, func(in integrationRef) bool { return strings.HasPrefix(in.Type, eventsIntegrationType) })
}

// listServices reads every PagerDuty service with the given includes, by name.
func (g *Gateway) listServices(ctx context.Context, include ...string) ([]serviceObj, error) {
	var out []serviceObj
	for page := 0; page < maxPages; page++ {
		q := url.Values{"limit": {strconv.Itoa(pageLimit)}, "offset": {strconv.Itoa(page * pageLimit)}, "include[]": include, "sort_by": {"name"}}
		var resp struct {
			Services []serviceObj `json:"services"`
			More     bool         `json:"more"`
		}
		if err := g.rest(ctx, "", http.MethodGet, "/services", q, nil, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Services...)
		if !resp.More {
			break
		}
	}
	return out, nil
}

// Services lists the PagerDuty services and whether they have an Events API v2 integration.
func (g *Gateway) Services(ctx context.Context) ([]Service, error) {
	list, err := g.listServices(ctx, "integrations")
	if err != nil {
		return nil, err
	}
	out := make([]Service, 0, len(list))
	for _, s := range list {
		out = append(out, Service{ID: s.ID, Name: s.name(), HTMLURL: s.HTMLURL, Status: s.Status, EventsKey: s.eventsKey()})
	}
	return out, nil
}

// ServiceKey returns the Events API v2 integration key of a service, creating the integration
// when the service has none and create is set. set and token (empty: the saved one) let
// settings be used before they are saved.
func (g *Gateway) ServiceKey(ctx context.Context, set model.PagerDuty, token, serviceID string, create bool) (key, name string, err error) {
	var resp struct {
		Service serviceObj `json:"service"`
	}
	if err := g.restWith(ctx, set, token, http.MethodGet, "/services/"+url.PathEscape(serviceID), url.Values{"include[]": {"integrations"}}, nil, &resp); err != nil {
		return "", "", err
	}
	name = resp.Service.name()
	for _, in := range resp.Service.Integrations {
		if !strings.HasPrefix(in.Type, eventsIntegrationType) {
			continue
		}
		if in.IntegrationKey != "" {
			return in.IntegrationKey, name, nil
		}
		var full struct {
			Integration integrationRef `json:"integration"`
		}
		if err := g.restWith(ctx, set, token, http.MethodGet, "/services/"+url.PathEscape(serviceID)+"/integrations/"+url.PathEscape(in.ID), nil, nil, &full); err == nil && full.Integration.IntegrationKey != "" {
			return full.Integration.IntegrationKey, name, nil
		}
	}
	if !create {
		return "", name, fmt.Errorf("service %s has no Events API v2 integration", name)
	}
	var created struct {
		Integration integrationRef `json:"integration"`
	}
	body := map[string]any{"integration": map[string]string{"type": eventsIntegrationType, "name": "Umbrella"}}
	if err := g.restWith(ctx, set, token, http.MethodPost, "/services/"+url.PathEscape(serviceID)+"/integrations", nil, body, &created); err != nil {
		return "", name, fmt.Errorf("create an Events API v2 integration in %s: %w", name, err)
	}
	if created.Integration.IntegrationKey == "" {
		return "", name, errors.New("PagerDuty returned no key for the new integration")
	}
	return created.Integration.IntegrationKey, name, nil
}

// IncidentAlertKeys returns the dedup keys of the alerts grouped into a PagerDuty incident.
func (g *Gateway) IncidentAlertKeys(ctx context.Context, incidentID string) ([]string, error) {
	var resp struct {
		Alerts []struct {
			AlertKey string `json:"alert_key"`
		} `json:"alerts"`
	}
	if err := g.rest(ctx, "", http.MethodGet, "/incidents/"+url.PathEscape(incidentID)+"/alerts", url.Values{"limit": {"100"}}, nil, &resp); err != nil {
		return nil, err
	}
	var keys []string
	for _, a := range resp.Alerts {
		if a.AlertKey != "" {
			keys = append(keys, a.AlertKey)
		}
	}
	return keys, nil
}

// CreateWebhookSubscription subscribes target to the incident events of the account and
// returns the subscription ID and its signing secret.
func (g *Gateway) CreateWebhookSubscription(ctx context.Context, target string) (string, string, error) {
	body := map[string]any{"webhook_subscription": map[string]any{
		"type":            "webhook_subscription",
		"description":     "Umbrella: incident status",
		"events":          WebhookEvents,
		"filter":          map[string]string{"type": "account_reference"},
		"delivery_method": map[string]any{"type": "http_delivery_method", "url": target},
	}}
	var resp struct {
		Sub struct {
			ID             string `json:"id"`
			DeliveryMethod struct {
				Secret string `json:"secret"`
			} `json:"delivery_method"`
		} `json:"webhook_subscription"`
	}
	if err := g.rest(ctx, "", http.MethodPost, "/webhook_subscriptions", nil, body, &resp); err != nil {
		return "", "", err
	}
	if resp.Sub.DeliveryMethod.Secret == "" {
		return resp.Sub.ID, "", errors.New("PagerDuty returned no signing secret for the subscription")
	}
	return resp.Sub.ID, resp.Sub.DeliveryMethod.Secret, nil
}

func (g *Gateway) DeleteWebhookSubscription(ctx context.Context, id string) error {
	err := g.rest(ctx, "", http.MethodDelete, "/webhook_subscriptions/"+url.PathEscape(id), nil, nil, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return nil
	}
	return err
}
