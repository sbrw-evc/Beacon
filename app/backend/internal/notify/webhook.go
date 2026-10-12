package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

// Microsoft Teams and Zoom Team Chat take messages through incoming webhooks: the address is
// the webhook URL, a secret of its own, so it is redacted wherever it is shown (Show).

// ValidWebhook checks the URL of an incoming webhook: http or https with a host and no user
// part. The API asks for https; plain http is left for tests and proxies inside the network.
func ValidWebhook(v string) bool {
	if v != strings.TrimSpace(v) || len(v) > 2048 || strings.ContainsAny(v, " \r\n") {
		return false
	}
	u, err := url.Parse(v)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && u.Fragment == ""
}

// postJSON posts a JSON body to a webhook; a 2xx answer is success. The URL is a secret: errors
// do not carry it.
func postJSON(ctx context.Context, client *http.Client, service, endpoint string, header http.Header, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return errPermanent{err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return errPermanent{fmt.Errorf("%s: the webhook URL is not valid", service)}
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("%s: %v", service, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode/100 == 2 {
		return nil
	}
	msg := http.StatusText(resp.StatusCode)
	var reply struct {
		Message string `json:"message"`
		Error   any    `json:"error"`
	}
	if json.Unmarshal(data, &reply) == nil && reply.Message != "" {
		msg = reply.Message
	} else if t := strings.TrimSpace(string(data)); t != "" && len(t) <= 200 && !strings.ContainsAny(t, "{<") {
		msg = t
	}
	err = fmt.Errorf("%s: %d %s", service, resp.StatusCode, msg)
	if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusRequestTimeout {
		return errPermanent{err}
	}
	return err
}

type teamsChannel struct{}

func (teamsChannel) Kind() string                        { return ChannelTeams }
func (teamsChannel) Enabled(n model.Notify) bool         { return n.Teams.Enabled }
func (teamsChannel) Ready(n model.Notify) bool           { return n.Teams.Enabled }
func (teamsChannel) Valid(addr string) bool              { return ValidWebhook(addr) }
func (teamsChannel) Recipient(addr string) string        { return TeamsRecipient(addr) }
func (teamsChannel) Show(addr string) string             { return model.RedactURL(addr) }
func (teamsChannel) Person(alert.Person) string          { return "" }
func (teamsChannel) Team(ch alert.Channel) string        { return ch.Teams }
func (teamsChannel) User(model.User) string              { return "" }
func (teamsChannel) Extra(n model.Notify) []string       { return n.ExtraTeams }
func (teamsChannel) Secret(model.Notify) (string, error) { return "", nil }
func (c teamsChannel) Send(ctx context.Context, s *Service, _ model.Notify, _, to string, m composed) (string, error) {
	return "", postJSON(ctx, s.client, "Teams", to, nil, teamsCard(m))
}

// teamsCard is the message as an Adaptive Card: the subject in bold, a text block per line of
// the text and buttons for the links.
func teamsCard(m composed) map[string]any {
	body := []map[string]any{{"type": "TextBlock", "text": m.subject, "weight": "Bolder", "size": "Medium", "wrap": true}}
	gap := false
	for _, line := range strings.Split(strings.TrimRight(m.text, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			gap = true
			continue
		}
		b := map[string]any{"type": "TextBlock", "text": line, "wrap": true, "spacing": "None"}
		if gap {
			b["spacing"] = "Medium"
			gap = false
		}
		body = append(body, b)
	}
	card := map[string]any{
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
		"type":    "AdaptiveCard",
		"version": "1.4",
		"body":    body,
	}
	var actions []map[string]any
	for _, l := range m.links {
		actions = append(actions, map[string]any{"type": "Action.OpenUrl", "title": l.title, "url": l.url})
	}
	if len(actions) > 0 {
		card["actions"] = actions
	}
	return map[string]any{
		"type": "message",
		"attachments": []map[string]any{{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"contentUrl":  nil,
			"content":     card,
		}},
	}
}

type zoomChannel struct{}

func (zoomChannel) Kind() string                 { return ChannelZoom }
func (zoomChannel) Enabled(n model.Notify) bool  { return n.Zoom.Enabled }
func (zoomChannel) Ready(n model.Notify) bool    { return n.Zoom.Enabled && n.Zoom.TokenRef != "" }
func (zoomChannel) Valid(addr string) bool       { return ValidWebhook(addr) }
func (zoomChannel) Recipient(addr string) string { return ZoomRecipient(addr) }
func (zoomChannel) Show(addr string) string      { return model.RedactURL(addr) }
func (zoomChannel) Person(alert.Person) string   { return "" }
func (zoomChannel) Team(ch alert.Channel) string { return ch.Zoom }
func (zoomChannel) User(model.User) string       { return "" }
func (zoomChannel) Extra(n model.Notify) []string {
	return n.ExtraZoom
}

func (zoomChannel) Secret(n model.Notify) (string, error) {
	if n.Zoom.TokenRef == "" {
		return "", errors.New("the Zoom verification token is not set")
	}
	return n.Zoom.TokenRef, nil
}

func (zoomChannel) Send(ctx context.Context, s *Service, _ model.Notify, token, to string, m composed) (string, error) {
	if token == "" {
		return "", errPermanent{errors.New("the Zoom verification token is not set")}
	}
	endpoint, err := zoomEndpoint(to)
	if err != nil {
		return "", errPermanent{err}
	}
	body := map[string]any{
		"head": map[string]any{"text": m.subject},
		"body": []map[string]any{{"type": "message", "text": strings.TrimRight(m.text, "\n")}},
	}
	return "", postJSON(ctx, s.client, "Zoom", endpoint, http.Header{"Authorization": {token}}, body)
}

// zoomEndpoint is the endpoint URL with format=full: the message has a head and a body.
func zoomEndpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("Zoom: the endpoint URL is not valid")
	}
	q := u.Query()
	q.Set("format", "full")
	u.RawQuery = q.Encode()
	return u.String(), nil
}
