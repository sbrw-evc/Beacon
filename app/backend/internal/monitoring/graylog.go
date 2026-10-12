package monitoring

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

// Graylog is read through its REST API: /api/system for the version, the scripting API of
// Graylog 5.1 and later (/api/search/aggregate, or the legacy terms search before it) for the
// hosts that sent messages lately, and the events search (/api/events/search) for the alerts.
// A Graylog event is a moment, not a state: an event definition keeps firing as long as its
// condition holds. An alert is therefore a series of events of one definition and one key
// (its group-by values), firing while its events keep coming and resolved once none came for
// the quiet time of the source.

const (
	// DefaultGraylogHostField names the host of a message.
	DefaultGraylogHostField = "source"
	// GraylogHostRange is how far back the messages are counted for the host list.
	GraylogHostRange = 24 * time.Hour
	// DefaultGraylogQuiet: an alert is resolved after this long without events.
	DefaultGraylogQuiet = 15 * time.Minute
	// MaxGraylogEvents bounds the events read in one go.
	MaxGraylogEvents = 20000
	graylogEventPage = 500
)

// GraylogEvent is one alert event of Graylog.
type GraylogEvent struct {
	ID             string            `json:"id"`
	DefinitionID   string            `json:"event_definition_id"`
	DefinitionType string            `json:"event_definition_type"`
	Timestamp      time.Time         `json:"-"`
	Message        string            `json:"message"`
	Source         string            `json:"source"`
	Key            string            `json:"key"`
	Priority       int               `json:"priority"`
	Alert          bool              `json:"alert"`
	Fields         map[string]string `json:"fields"`
	GroupBy        map[string]string `json:"group_by_fields"`
	// Title and Description are those of the event definition.
	Title       string `json:"-"`
	Description string `json:"-"`
	// Raw is the event as Graylog keeps it, sent on to the connector unchanged.
	Raw json.RawMessage `json:"-"`
}

// Series identifies the alert the event belongs to: its definition and key.
func (e GraylogEvent) Series() string {
	h := fnv.New64a()
	h.Write([]byte(e.DefinitionID))
	h.Write([]byte{0xff})
	h.Write([]byte(e.Key))
	return fmt.Sprintf("%016x", h.Sum64())
}

// Host is the host the event names in the field (group-by values first, then the event fields).
func (e GraylogEvent) Host(field string) string {
	if field == "" {
		field = DefaultGraylogHostField
	}
	if v := e.GroupBy[field]; v != "" {
		return v
	}
	return e.Fields[field]
}

// GraylogReading is what one read of the events search returned, newest first.
type GraylogReading struct {
	Version string
	Events  []GraylogEvent
}

func graylogDo(ctx context.Context, src model.MonitoringSource, auth *Auth, method, path string, in, out any) (int, error) {
	base, err := baseURL(src.URL)
	if err != nil {
		return 0, err
	}
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base.String(), "/")+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	// Graylog refuses changing requests without it (CSRF protection).
	req.Header.Set("X-Requested-By", "umbrella")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != nil {
		switch auth.Type {
		case "bearer":
			// An access token goes as the user name with the password "token".
			req.SetBasicAuth(auth.Secrets["token"], "token")
		case "basic":
			req.SetBasicAuth(auth.Fields["username"], auth.Secrets["password"])
		case "header":
			if h := auth.Fields["header"]; h != "" {
				req.Header.Set(h, auth.Secrets["value"])
			}
		default:
			return 0, fmt.Errorf("a %s credential cannot be used with Graylog", auth.Type)
		}
	}
	resp, err := clients[src.SkipVerify].Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return resp.StatusCode, fmt.Errorf("Graylog answered %d: check the access token or the user and its permissions (reading streams and events)", resp.StatusCode)
	case resp.StatusCode/100 != 2:
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return resp.StatusCode, fmt.Errorf("Graylog answered %d on %s: %s", resp.StatusCode, path, msg)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return resp.StatusCode, errors.New("the address does not answer with the Graylog API; give the address of the Graylog web interface")
	}
	return resp.StatusCode, nil
}

func graylogVersion(ctx context.Context, src model.MonitoringSource, auth *Auth) (string, error) {
	var sys struct {
		Version string `json:"version"`
	}
	if _, err := graylogDo(ctx, src, auth, http.MethodGet, "/api/system", nil, &sys); err != nil {
		return "", err
	}
	if sys.Version == "" {
		return "", errors.New("the address does not answer with the Graylog API; give the address of the Graylog web interface")
	}
	v, _, _ := strings.Cut(sys.Version, "+")
	return v, nil
}

// ReadGraylog reads the version and the alert events of the last span, newest first.
func ReadGraylog(ctx context.Context, src model.MonitoringSource, auth *Auth, span time.Duration) (GraylogReading, error) {
	var out GraylogReading
	var err error
	if out.Version, err = graylogVersion(ctx, src, auth); err != nil {
		return out, err
	}
	out.Events = []GraylogEvent{}
	seconds := max(int(span.Seconds()), 60)
	for page := 1; ; page++ {
		var ans struct {
			Events []struct {
				Event json.RawMessage `json:"event"`
			} `json:"events"`
			TotalEvents int `json:"total_events"`
			Context     struct {
				EventDefinitions map[string]struct {
					Title       string `json:"title"`
					Description string `json:"description"`
				} `json:"event_definitions"`
			} `json:"context"`
		}
		in := map[string]any{
			"query": "", "page": page, "per_page": graylogEventPage,
			"filter":    map[string]any{"alerts": "only", "event_definitions": []string{}},
			"timerange": map[string]any{"type": "relative", "range": seconds},
			"sort_by":   "timestamp", "sort_direction": "desc",
		}
		if _, err := graylogDo(ctx, src, auth, http.MethodPost, "/api/events/search", in, &ans); err != nil {
			return out, err
		}
		for _, raw := range ans.Events {
			e, err := parseGraylogEvent(raw.Event)
			if err != nil {
				continue
			}
			if d, ok := ans.Context.EventDefinitions[e.DefinitionID]; ok {
				e.Title, e.Description = d.Title, d.Description
			}
			out.Events = append(out.Events, e)
		}
		if len(ans.Events) < graylogEventPage || page*graylogEventPage >= ans.TotalEvents {
			break
		}
		if len(out.Events) >= MaxGraylogEvents {
			return out, fmt.Errorf("Graylog has more than %d alert events in %s; at most %d are read", MaxGraylogEvents, span, MaxGraylogEvents)
		}
	}
	slices.SortStableFunc(out.Events, func(a, b GraylogEvent) int { return b.Timestamp.Compare(a.Timestamp) })
	return out, nil
}

func parseGraylogEvent(raw json.RawMessage) (GraylogEvent, error) {
	var e GraylogEvent
	var loose struct {
		Timestamp string         `json:"timestamp"`
		Fields    map[string]any `json:"fields"`
		GroupBy   map[string]any `json:"group_by_fields"`
		KeyTuple  []any          `json:"key_tuple"`
	}
	// Fields and group-by values may be numbers; they are read as text.
	var strict struct {
		ID             string `json:"id"`
		DefinitionID   string `json:"event_definition_id"`
		DefinitionType string `json:"event_definition_type"`
		Message        string `json:"message"`
		Source         string `json:"source"`
		Key            string `json:"key"`
		Priority       any    `json:"priority"`
		Alert          bool   `json:"alert"`
	}
	if err := json.Unmarshal(raw, &strict); err != nil {
		return e, err
	}
	if err := json.Unmarshal(raw, &loose); err != nil {
		return e, err
	}
	if strict.ID == "" {
		return e, errors.New("the event has no ID")
	}
	e = GraylogEvent{ID: strict.ID, DefinitionID: strict.DefinitionID, DefinitionType: strict.DefinitionType, Message: strict.Message,
		Source: strict.Source, Key: strict.Key, Alert: strict.Alert, Fields: stringMap(loose.Fields), GroupBy: stringMap(loose.GroupBy), Raw: raw}
	e.Priority, _ = strconv.Atoi(strings.TrimSuffix(fmt.Sprint(strict.Priority), ".0"))
	if t, err := time.Parse(time.RFC3339Nano, loose.Timestamp); err == nil {
		e.Timestamp = t.UTC()
	} else if t, err := time.Parse("2006-01-02 15:04:05.000", loose.Timestamp); err == nil {
		e.Timestamp = t.UTC()
	}
	return e, nil
}

func stringMap(m map[string]any) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		switch x := v.(type) {
		case nil:
		case string:
			out[k] = x
		default:
			b, _ := json.Marshal(x)
			out[k] = string(b)
		}
	}
	return out
}

// graylogHosts counts the messages per value of the host field over the last day: through the
// aggregation API of Graylog 5.1 and later, else through the legacy terms search.
func graylogHosts(ctx context.Context, src model.MonitoringSource, auth *Auth, field string) (map[string]int, error) {
	query := strings.TrimSpace(src.Query)
	var agg struct {
		Datarows [][]any `json:"datarows"`
	}
	in := map[string]any{
		"query": query, "streams": []string{},
		"timerange": map[string]any{"type": "relative", "range": int(GraylogHostRange.Seconds())},
		"group_by":  []any{map[string]any{"field": field, "limit": MaxHosts}},
		"metrics":   []any{map[string]any{"function": "count"}},
	}
	status, err := graylogDo(ctx, src, auth, http.MethodPost, "/api/search/aggregate", in, &agg)
	if err == nil {
		out := map[string]int{}
		for _, row := range agg.Datarows {
			if len(row) < 2 {
				continue
			}
			name, _ := row[0].(string)
			n, _ := row[1].(float64)
			if name != "" && name != "(Empty Value)" {
				out[name] += int(n)
			}
		}
		return out, nil
	}
	if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
		return nil, err
	}
	if query == "" {
		query = "*"
	}
	var terms struct {
		Terms map[string]int `json:"terms"`
	}
	v := url.Values{"field": {field}, "query": {query}, "range": {strconv.Itoa(int(GraylogHostRange.Seconds()))}, "size": {strconv.Itoa(MaxHosts)}}
	if _, err := graylogDo(ctx, src, auth, http.MethodGet, "/api/search/universal/relative/terms?"+v.Encode(), nil, &terms); err != nil {
		return nil, err
	}
	return terms.Terms, nil
}

// fetchGraylog makes the host list of the hosts that sent messages over the last day: down
// while an alert of the host fires (its events came within the quiet time), up otherwise.
func fetchGraylog(ctx context.Context, src model.MonitoringSource, auth *Auth) (Result, error) {
	field := strings.TrimSpace(src.HostLabel)
	if field == "" {
		field = DefaultGraylogHostField
	}
	reading, err := ReadGraylog(ctx, src, auth, GraylogQuiet(src))
	if err != nil {
		return Result{}, err
	}
	counts, err := graylogHosts(ctx, src, auth, field)
	if err != nil {
		return Result{}, err
	}
	firing := map[string]bool{}
	for _, e := range reading.Events {
		if h := hostOf(e.Host(field)); h != "" {
			firing[h] = true
		}
	}
	type acc struct {
		raw []string
	}
	byKey := map[string]*acc{}
	for raw := range counts {
		key := hostOf(raw)
		if key == "" {
			continue
		}
		if byKey[key] == nil {
			byKey[key] = &acc{}
		}
		byKey[key].raw = append(byKey[key].raw, raw)
	}
	base := strings.TrimRight(src.URL, "/")
	out := Result{Version: reading.Version, Hosts: make([]model.MonitoringHost, 0, len(byKey))}
	for key, a := range byKey {
		h := model.MonitoringHost{Key: key, Host: key, Name: key, Groups: []string{}, Endpoints: sortedSet(a.raw), State: model.HostUp}
		h.IPs, h.DNS = addresses(key)
		h.IPs, h.DNS = orEmpty(h.IPs), orEmpty(h.DNS)
		if firing[key] {
			h.State = model.HostDown
		}
		h.URL = base + "/search?q=" + url.QueryEscape(field+":"+strconv.Quote(a.raw[0])) + "&rangetype=relative&relative=86400"
		out.Hosts = append(out.Hosts, h)
	}
	return out, nil
}

// GraylogQuiet is how long an alert of the source fires after its last event.
func GraylogQuiet(src model.MonitoringSource) time.Duration {
	if src.QuietMinutes > 0 {
		return time.Duration(src.QuietMinutes) * time.Minute
	}
	return DefaultGraylogQuiet
}
