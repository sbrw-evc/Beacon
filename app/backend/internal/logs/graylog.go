package logs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

// Graylog is read through its search API: the messages API of Graylog 5.1 and later
// (POST /api/search/messages) and, when the server does not have it, the universal search of
// older versions (GET /api/search/universal/absolute). The lines of a machine are those whose
// host field (source unless the source names another) is any of its names; the query of the
// source narrows them further and may hold $host itself.

// Defaults of a Graylog log source.
const (
	GraylogHostField    = "source"
	GraylogMessageField = "message"
	GraylogLevelField   = "level"
	graylogTimeField    = "timestamp"
	graylogTime         = "2006-01-02T15:04:05.000Z"
)

// syslogLevels name the numeric levels Graylog keeps for syslog and GELF messages.
var syslogLevels = []string{"emergency", "alert", "critical", "error", "warning", "notice", "info", "debug"}

// GraylogLevel names a level that Graylog keeps as a syslog number; any other value is kept.
func GraylogLevel(v any) string {
	s := strings.TrimSpace(text2(v))
	if n, err := strconv.ParseFloat(s, 64); err == nil && n >= 0 && int(n) < len(syslogLevels) && n == float64(int(n)) {
		return syslogLevels[int(n)]
	}
	return strings.ToLower(s)
}

// graylogPhrase is a value in the Graylog (Lucene) query language: in double quotes with quotes
// and backslashes escaped.
func graylogPhrase(v string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

// GraylogQuery is the search query of the lines of a machine.
func GraylogQuery(src model.LogSource, q Query) string {
	names := make([]string, 0, len(q.Hosts))
	for _, h := range q.Hosts {
		names = append(names, graylogPhrase(h))
	}
	hosts := "(" + strings.Join(names, " OR ") + ")"
	query := strings.TrimSpace(src.Query)
	var parts []string
	switch {
	case strings.Contains(query, model.HostName):
		parts = append(parts, "("+strings.ReplaceAll(query, model.HostName, hosts)+")")
	default:
		parts = append(parts, or(src.HostField, GraylogHostField)+":"+hosts)
		if query != "" && query != "*" {
			parts = append(parts, "("+query+")")
		}
	}
	if t := strings.TrimSpace(q.Text); t != "" {
		parts = append(parts, or(src.MessageField, GraylogMessageField)+":"+graylogPhrase(t))
	}
	return strings.Join(parts, " AND ")
}

// GraylogStreams are the stream IDs the source is limited to, written comma-separated in the
// index of the source.
func GraylogStreams(src model.LogSource) []string {
	out := []string{}
	for s := range strings.SplitSeq(src.Index, ",") {
		if s = strings.TrimSpace(s); s != "" && s != "*" {
			out = append(out, s)
		}
	}
	return out
}

func graylogRequest(ctx context.Context, method, rawURL string, body []byte, auth *Auth) (*http.Request, error) {
	// Graylog takes an access token as the user name with the password "token".
	if auth != nil && auth.Type == "bearer" {
		auth = &Auth{Type: "basic", Fields: map[string]string{"username": auth.Secrets["token"]}, Secrets: map[string]string{"password": "token"}}
	}
	req, err := request(ctx, method, rawURL, body, auth)
	if err != nil {
		return nil, err
	}
	// Graylog refuses changing requests without it (CSRF protection).
	req.Header.Set("X-Requested-By", "umbrella")
	return req, nil
}

func fetchGraylog(ctx context.Context, src model.LogSource, auth *Auth, q Query) (Result, error) {
	root, err := base(src.URL)
	if err != nil {
		return Result{}, err
	}
	query := GraylogQuery(src, q)
	hostField := or(src.HostField, GraylogHostField)
	msgField := or(src.MessageField, GraylogMessageField)
	levelField := or(src.LevelField, GraylogLevelField)
	fields := []string{graylogTimeField, hostField, msgField, levelField}
	spec := map[string]any{
		"query":      query,
		"streams":    GraylogStreams(src),
		"timerange":  map[string]any{"type": "absolute", "from": q.From.UTC().Format(graylogTime), "to": q.To.UTC().Format(graylogTime)},
		"fields":     fields,
		"size":       q.Limit + 1,
		"sort":       graylogTimeField,
		"sort_order": "desc",
	}
	b, _ := json.Marshal(spec)
	req, err := graylogRequest(ctx, http.MethodPost, root+"/api/search/messages", b, auth)
	if err != nil {
		return Result{}, err
	}
	status, body, err := doStatus(src, req)
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		return fetchGraylogLegacy(ctx, src, auth, q, root, query)
	}
	if err != nil {
		return Result{}, graylogError(err)
	}
	var ans struct {
		Schema []struct {
			Field string `json:"field"`
		} `json:"schema"`
		Datarows [][]any `json:"datarows"`
	}
	if err := json.Unmarshal(body, &ans); err != nil || ans.Schema == nil {
		return Result{}, errors.New("the source did not answer with the Graylog search API")
	}
	col := map[string]int{}
	for i, s := range ans.Schema {
		col[s.Field] = i
	}
	cell := func(row []any, name string) any {
		if i, ok := col[name]; ok && i < len(row) {
			return row[i]
		}
		return nil
	}
	out := Result{Query: query}
	for _, row := range ans.Datarows {
		out.Lines = append(out.Lines, graylogLine(func(name string) any { return cell(row, name) }, hostField, msgField, levelField, ""))
	}
	return out, nil
}

// fetchGraylogLegacy reads the lines through the universal search of Graylog before 5.1.
func fetchGraylogLegacy(ctx context.Context, src model.LogSource, auth *Auth, q Query, root, query string) (Result, error) {
	full := query
	if streams := GraylogStreams(src); len(streams) > 0 {
		ids := make([]string, 0, len(streams))
		for _, s := range streams {
			ids = append(ids, graylogPhrase(s))
		}
		full += " AND streams:(" + strings.Join(ids, " OR ") + ")"
	}
	v := url.Values{"query": {full}, "from": {q.From.UTC().Format(graylogTime)}, "to": {q.To.UTC().Format(graylogTime)},
		"limit": {strconv.Itoa(q.Limit + 1)}, "sort": {graylogTimeField + ":desc"}, "decorate": {"false"}}
	req, err := graylogRequest(ctx, http.MethodGet, root+"/api/search/universal/absolute?"+v.Encode(), nil, auth)
	if err != nil {
		return Result{}, err
	}
	body, err := do(src, req)
	if err != nil {
		return Result{}, graylogError(err)
	}
	var ans struct {
		Messages []struct {
			Message map[string]any `json:"message"`
			Index   string         `json:"index"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &ans); err != nil || ans.Messages == nil {
		return Result{}, errors.New("the source did not answer with the Graylog search API")
	}
	hostField := or(src.HostField, GraylogHostField)
	msgField := or(src.MessageField, GraylogMessageField)
	levelField := or(src.LevelField, GraylogLevelField)
	out := Result{Query: full}
	for _, m := range ans.Messages {
		out.Lines = append(out.Lines, graylogLine(func(name string) any { return field(m.Message, name) }, hostField, msgField, levelField, m.Index))
	}
	return out, nil
}

func graylogLine(get func(string) any, hostField, msgField, levelField, index string) Line {
	at, _ := parseTime(get(graylogTimeField))
	labels := map[string]string{}
	if v := text2(get(hostField)); v != "" {
		labels["host"] = v
	}
	if index != "" {
		labels["index"] = index
	}
	return Line{At: at, Level: GraylogLevel(get(levelField)), Text: clip(text(get(msgField))), Labels: labels}
}

func graylogError(err error) error {
	if strings.Contains(err.Error(), "answered 401") || strings.Contains(err.Error(), "answered 403") {
		return fmt.Errorf("%w (Graylog needs a user or an access token that may read the streams; a token goes as a bearer credential)", err)
	}
	return err
}
