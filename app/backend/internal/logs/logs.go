// Package logs reads the log lines of a machine over a time range from a log store: a
// Loki-compatible server through LogQL, an OpenSearch- or Elasticsearch-compatible search API
// or Graylog through its search API.
package logs

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/rules"
)

// Auth is the credential a log store is read with: bearer, basic or header.
type Auth = rules.Auth

// Defaults of a log source.
const (
	DefaultLokiQuery    = `{host=~"$host"}`
	DefaultIndex        = "*"
	DefaultHostField    = "host.name"
	DefaultMessageField = "message"
	DefaultTimeField    = "@timestamp"
	DefaultLevelField   = "log.level"
	MaxLimit            = 2000
	maxLine             = 4000
)

// Query asks for the lines of a machine known by any of Hosts between From and To, newest
// first; Text keeps the lines that contain it (any case).
type Query struct {
	Hosts []string
	From  time.Time
	To    time.Time
	Limit int
	Text  string
}

// Line is one log line.
type Line struct {
	At     time.Time         `json:"at"`
	Level  string            `json:"level,omitempty"`
	Text   string            `json:"text"`
	Labels map[string]string `json:"labels,omitempty"`
}

// Result: the lines, newest first; Truncated says the limit was reached.
type Result struct {
	Lines     []Line `json:"lines"`
	Truncated bool   `json:"truncated"`
	// Query is what was sent, for the person to check.
	Query string `json:"query"`
}

var clients = map[bool]*http.Client{
	false: {Timeout: 30 * time.Second},
	true:  {Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}, //nolint:gosec // the administrator's choice
}

// Fetch reads the lines of a machine.
func Fetch(ctx context.Context, src model.LogSource, auth *Auth, q Query) (Result, error) {
	if q.Limit <= 0 || q.Limit > MaxLimit {
		q.Limit = min(max(q.Limit, 1), MaxLimit)
	}
	if !q.To.After(q.From) {
		return Result{}, errors.New("the time range is empty")
	}
	hosts := cleanHosts(q.Hosts)
	if len(hosts) == 0 {
		return Result{}, errors.New("the machine has no name to look its lines up by")
	}
	q.Hosts = hosts
	var (
		out Result
		err error
	)
	switch src.Kind {
	case model.LogLoki:
		out, err = fetchLoki(ctx, src, auth, q)
	case model.LogOpenSearch:
		out, err = fetchSearch(ctx, src, auth, q)
	case model.LogGraylog:
		out, err = fetchGraylog(ctx, src, auth, q)
	default:
		return Result{}, fmt.Errorf("unknown log store %q", src.Kind)
	}
	if err != nil {
		return Result{}, err
	}
	slices.SortStableFunc(out.Lines, func(a, b Line) int { return b.At.Compare(a.At) })
	if len(out.Lines) > q.Limit {
		out.Lines, out.Truncated = out.Lines[:q.Limit], true
	}
	if out.Lines == nil {
		out.Lines = []Line{}
	}
	return out, nil
}

func cleanHosts(v []string) []string {
	out := []string{}
	for _, h := range v {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" && len(h) <= 255 && !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	slices.Sort(out)
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}

// HostPattern is a case-insensitive regular expression that matches any of the names exactly.
func HostPattern(hosts []string) string {
	alts := make([]string, 0, len(hosts))
	for _, h := range hosts {
		alts = append(alts, regexp.QuoteMeta(h))
	}
	return "(?i)(" + strings.Join(alts, "|") + ")"
}

// LokiQuery puts the names of the machine and the text filter in the LogQL of the source.
func LokiQuery(src model.LogSource, q Query) string {
	query := strings.TrimSpace(src.Query)
	if query == "" {
		query = DefaultLokiQuery
	}
	// $host stands inside a LogQL string: the pattern is quoted the same way without the quotes.
	quoted := strconv.Quote(HostPattern(q.Hosts))
	query = strings.ReplaceAll(query, model.HostName, quoted[1:len(quoted)-1])
	if t := strings.TrimSpace(q.Text); t != "" {
		query += " |~ " + strconv.Quote("(?i)"+regexp.QuoteMeta(t))
	}
	return query
}

func request(ctx context.Context, method, rawURL string, body []byte, auth *Auth) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != nil {
		switch auth.Type {
		case "bearer":
			req.Header.Set("Authorization", "Bearer "+auth.Secrets["token"])
		case "basic":
			req.SetBasicAuth(auth.Fields["username"], auth.Secrets["password"])
		case "header":
			if h := auth.Fields["header"]; h != "" {
				req.Header.Set(h, auth.Secrets["value"])
			}
		default:
			return nil, fmt.Errorf("a %s credential cannot be used to read logs", auth.Type)
		}
	}
	return req, nil
}

func base(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("the source address is not an http or https URL")
	}
	u.RawQuery, u.Fragment = "", ""
	return strings.TrimRight(u.String(), "/"), nil
}

func do(src model.LogSource, req *http.Request) ([]byte, error) {
	_, body, err := doStatus(src, req)
	return body, err
}

// doStatus is do that also tells the status of the answer.
func doStatus(src model.LogSource, req *http.Request) (int, []byte, error) {
	resp, err := clients[src.SkipVerify].Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return resp.StatusCode, body, statusError(resp.StatusCode, body)
}

func statusError(status int, body []byte) error {
	if status/100 != 2 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		if msg == "" {
			return fmt.Errorf("the source answered %d", status)
		}
		return fmt.Errorf("the source answered %d: %s", status, msg)
	}
	return nil
}

func clip(s string) string {
	s = strings.TrimRight(s, "\r\n")
	if len(s) > maxLine {
		cut := maxLine
		for cut > 0 && !utf8Start(s[cut]) {
			cut--
		}
		return s[:cut] + "…"
	}
	return s
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

var lokiLevels = []string{"level", "detected_level", "severity", "lvl"}

func fetchLoki(ctx context.Context, src model.LogSource, auth *Auth, q Query) (Result, error) {
	root, err := base(src.URL)
	if err != nil {
		return Result{}, err
	}
	query := LokiQuery(src, q)
	v := url.Values{"query": {query}, "start": {strconv.FormatInt(q.From.UnixNano(), 10)}, "end": {strconv.FormatInt(q.To.UnixNano(), 10)},
		"limit": {strconv.Itoa(q.Limit + 1)}, "direction": {"backward"}}
	req, err := request(ctx, http.MethodGet, root+"/loki/api/v1/query_range?"+v.Encode(), nil, auth)
	if err != nil {
		return Result{}, err
	}
	body, err := do(src, req)
	if err != nil {
		return Result{}, err
	}
	var ans struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Stream map[string]string `json:"stream"`
				Values [][2]string       `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &ans); err != nil || ans.Status != "success" {
		return Result{}, errors.New("the source did not answer with the Loki API")
	}
	if ans.Data.ResultType != "streams" {
		return Result{}, fmt.Errorf("a %s result is not log lines: the query must select streams", ans.Data.ResultType)
	}
	out := Result{Query: query}
	for _, st := range ans.Data.Result {
		level := ""
		for _, k := range lokiLevels {
			if st.Stream[k] != "" {
				level = st.Stream[k]
				break
			}
		}
		for _, val := range st.Values {
			ns, err := strconv.ParseInt(val[0], 10, 64)
			if err != nil {
				continue
			}
			out.Lines = append(out.Lines, Line{At: time.Unix(0, ns).UTC(), Level: strings.ToLower(level), Text: clip(val[1]), Labels: st.Stream})
		}
	}
	return out, nil
}

// SearchBody is the OpenSearch query of the lines of a machine.
func SearchBody(src model.LogSource, q Query) map[string]any {
	timeField := or(src.TimeField, DefaultTimeField)
	hostField := or(src.HostField, DefaultHostField)
	msgField := or(src.MessageField, DefaultMessageField)
	hosts := make([]any, 0, len(q.Hosts)*2)
	for _, h := range q.Hosts {
		hosts = append(hosts, map[string]any{"match_phrase": map[string]any{hostField: h}})
	}
	filter := []any{
		map[string]any{"range": map[string]any{timeField: map[string]any{"gte": q.From.UTC().Format(time.RFC3339Nano), "lte": q.To.UTC().Format(time.RFC3339Nano),
			"format": "strict_date_optional_time"}}},
		map[string]any{"bool": map[string]any{"should": hosts, "minimum_should_match": 1}},
	}
	if t := strings.TrimSpace(q.Text); t != "" {
		filter = append(filter, map[string]any{"match_phrase": map[string]any{msgField: t}})
	}
	return map[string]any{
		"size":             q.Limit + 1,
		"sort":             []any{map[string]any{timeField: map[string]any{"order": "desc"}}},
		"query":            map[string]any{"bool": map[string]any{"filter": filter}},
		"track_total_hits": false,
	}
}

func or(v, def string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return def
}

func fetchSearch(ctx context.Context, src model.LogSource, auth *Auth, q Query) (Result, error) {
	root, err := base(src.URL)
	if err != nil {
		return Result{}, err
	}
	index := or(src.Index, DefaultIndex)
	b, _ := json.Marshal(SearchBody(src, q))
	req, err := request(ctx, http.MethodPost, root+"/"+url.PathEscape(index)+"/_search?ignore_unavailable=true", b, auth)
	if err != nil {
		return Result{}, err
	}
	body, err := do(src, req)
	if err != nil {
		return Result{}, err
	}
	var ans struct {
		Hits struct {
			Hits []struct {
				Index  string         `json:"_index"`
				Source map[string]any `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &ans); err != nil {
		return Result{}, errors.New("the source did not answer with the search API")
	}
	timeField := or(src.TimeField, DefaultTimeField)
	hostField := or(src.HostField, DefaultHostField)
	msgField := or(src.MessageField, DefaultMessageField)
	levelField := or(src.LevelField, DefaultLevelField)
	out := Result{Query: string(b)}
	for _, h := range ans.Hits.Hits {
		at, _ := parseTime(field(h.Source, timeField))
		text := text(field(h.Source, msgField))
		if text == "" {
			raw, _ := json.Marshal(h.Source)
			text = string(raw)
		}
		labels := map[string]string{"index": h.Index}
		if v := text2(field(h.Source, hostField)); v != "" {
			labels["host"] = v
		}
		out.Lines = append(out.Lines, Line{At: at, Level: strings.ToLower(text2(field(h.Source, levelField))), Text: clip(text), Labels: labels})
	}
	return out, nil
}

// field reads a dotted path from a document, whether the document nests objects or keeps the
// dotted name as one key.
func field(doc map[string]any, path string) any {
	if v, ok := doc[path]; ok {
		return v
	}
	for i := strings.IndexByte(path, '.'); i >= 0; {
		if sub, ok := doc[path[:i]].(map[string]any); ok {
			if v := field(sub, path[i+1:]); v != nil {
				return v
			}
		}
		j := strings.IndexByte(path[i+1:], '.')
		if j < 0 {
			break
		}
		i += 1 + j
	}
	return nil
}

func text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			parts = append(parts, text(e))
		}
		return strings.Join(parts, " ")
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func text2(v any) string {
	if l, ok := v.([]any); ok && len(l) > 0 {
		return text(l[0])
	}
	return text(v)
}

func parseTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999"} {
			if t, err := time.Parse(layout, x); err == nil {
				return t.UTC(), true
			}
		}
		if n, err := strconv.ParseInt(x, 10, 64); err == nil {
			return time.UnixMilli(n).UTC(), true
		}
	case float64:
		return time.UnixMilli(int64(x)).UTC(), true
	}
	return time.Time{}, false
}
