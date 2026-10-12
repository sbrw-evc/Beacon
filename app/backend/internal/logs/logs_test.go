package logs_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/logs"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

func TestLoki(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/query_range" || r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		got = r.URL.Query().Get("query")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "streams", "result": []any{
			map[string]any{"stream": map[string]string{"host": "web-01", "level": "ERROR"}, "values": [][2]string{{"1700000060000000000", "disk full\n"}, {"1700000000000000000", "retrying"}}},
			map[string]any{"stream": map[string]string{"host": "web-01"}, "values": [][2]string{{"1700000030000000000", "hello"}}},
		}}})
	}))
	defer srv.Close()
	src := model.LogSource{Kind: model.LogLoki, URL: srv.URL}
	from := time.Unix(1_699_999_000, 0)
	res, err := logs.Fetch(context.Background(), src, &logs.Auth{Type: "bearer", Secrets: map[string]string{"token": "tok"}},
		logs.Query{Hosts: []string{"web-01", "10.0.0.1", "WEB-01"}, From: from, To: from.Add(time.Hour), Limit: 2, Text: "disk."})
	if err != nil {
		t.Fatal(err)
	}
	if got != `{host=~"(?i)(10\\.0\\.0\\.1|web-01)"} |~ "(?i)disk\\."` {
		t.Fatalf("query %s", got)
	}
	if len(res.Lines) != 2 || !res.Truncated || res.Lines[0].Text != "disk full" || res.Lines[0].Level != "error" || res.Lines[1].Text != "hello" {
		t.Fatalf("lines %+v", res)
	}
}

func TestOpenSearch(t *testing.T) {
	var body map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"hits": map[string]any{"hits": []any{
			map[string]any{"_index": "logs-1", "_source": map[string]any{"@timestamp": "2023-11-14T22:14:20Z", "message": "oom killer", "host": map[string]any{"name": "db-01"}, "log.level": "WARN"}},
			map[string]any{"_index": "logs-1", "_source": map[string]any{"@timestamp": "2023-11-14T22:15:20.5Z", "message": "started", "host": map[string]any{"name": "db-01"}}},
		}}})
	}))
	defer srv.Close()
	src := model.LogSource{Kind: model.LogOpenSearch, URL: srv.URL, Index: "logs-*"}
	from := time.Unix(1_699_999_000, 0)
	res, err := logs.Fetch(context.Background(), src, nil, logs.Query{Hosts: []string{"db-01"}, From: from, To: from.Add(time.Hour), Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/logs-*/_search" {
		t.Fatalf("path %s", path)
	}
	b, _ := json.Marshal(body)
	if !strings.Contains(string(b), `"host.name":"db-01"`) || !strings.Contains(string(b), `"@timestamp"`) {
		t.Fatalf("body %s", b)
	}
	if len(res.Lines) != 2 || res.Lines[0].Text != "started" || res.Lines[1].Level != "warn" || res.Lines[1].Labels["host"] != "db-01" {
		t.Fatalf("lines %+v", res.Lines)
	}
}

func TestFetchNeedsAName(t *testing.T) {
	from := time.Unix(1_699_999_000, 0)
	if _, err := logs.Fetch(context.Background(), model.LogSource{Kind: model.LogLoki, URL: "http://x"}, nil, logs.Query{From: from, To: from.Add(time.Hour)}); err == nil {
		t.Fatal("no names: want an error")
	}
}

func TestGraylog(t *testing.T) {
	var spec map[string]any
	var user, pass, csrf string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/search/messages" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		user, pass, _ = r.BasicAuth()
		csrf = r.Header.Get("X-Requested-By")
		_ = json.NewDecoder(r.Body).Decode(&spec)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schema": []any{
				map[string]any{"column_type": "field", "field": "timestamp"}, map[string]any{"column_type": "field", "field": "source"},
				map[string]any{"column_type": "field", "field": "message"}, map[string]any{"column_type": "field", "field": "level"},
			},
			"datarows": []any{
				[]any{"2023-11-14T22:14:20.000Z", "web-01", "nginx: upstream timed out", 3},
				[]any{"2023-11-14T22:15:20.000Z", "web-01", "started", nil},
			},
		})
	}))
	defer srv.Close()
	src := model.LogSource{Kind: model.LogGraylog, URL: srv.URL, Index: "abc, def", Query: "facility:nginx"}
	from := time.Unix(1_699_999_000, 0)
	res, err := logs.Fetch(context.Background(), src, &logs.Auth{Type: "bearer", Secrets: map[string]string{"token": "tok"}},
		logs.Query{Hosts: []string{"web-01", "10.0.0.1"}, From: from, To: from.Add(time.Hour), Limit: 50, Text: `say "hi"`})
	if err != nil {
		t.Fatal(err)
	}
	if user != "tok" || pass != "token" || csrf == "" {
		t.Fatalf("auth %q %q %q", user, pass, csrf)
	}
	want := `source:("10.0.0.1" OR "web-01") AND (facility:nginx) AND message:"say \"hi\""`
	if spec["query"] != want || res.Query != want {
		t.Fatalf("query %v", spec["query"])
	}
	if s, _ := json.Marshal(spec["streams"]); string(s) != `["abc","def"]` || spec["sort_order"] != "desc" {
		t.Fatalf("spec %v", spec)
	}
	if len(res.Lines) != 2 || res.Lines[0].Text != "started" || res.Lines[1].Level != "error" || res.Lines[1].Labels["host"] != "web-01" {
		t.Fatalf("lines %+v", res.Lines)
	}
}

// Graylog before 5.1 has no messages API: the universal search is read instead, and $host in the
// query stands for the names of the machine.
func TestGraylogLegacy(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/search/universal/absolute" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		query = r.URL.Query().Get("query")
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": []any{
			map[string]any{"index": "graylog_3", "message": map[string]any{"timestamp": "2023-11-14T22:14:20.000Z", "host": "db-01", "message": "oom", "level": 4}},
		}})
	}))
	defer srv.Close()
	src := model.LogSource{Kind: model.LogGraylog, URL: srv.URL, Index: "s1", Query: "host:$host AND NOT level:7"}
	from := time.Unix(1_699_999_000, 0)
	res, err := logs.Fetch(context.Background(), src, nil, logs.Query{Hosts: []string{"db-01"}, From: from, To: from.Add(time.Hour), Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if query != `(host:("db-01") AND NOT level:7) AND streams:("s1")` {
		t.Fatalf("query %s", query)
	}
	if len(res.Lines) != 1 || res.Lines[0].Level != "warning" || res.Lines[0].Text != "oom" || res.Lines[0].Labels["index"] != "graylog_3" {
		t.Fatalf("lines %+v", res.Lines)
	}
}
