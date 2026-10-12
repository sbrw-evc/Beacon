package flow

import (
	"context"
	"strings"
	"testing"
)

func TestTextFilters(t *testing.T) {
	data := map[string]any{
		"trace": "\n  java.lang.NullPointerException: boom  \n\tat a.B.c(B.java:1)\n\tat d.E.f(E.java:2)\nCaused by: java.io.IOException: closed\n",
		"html":  "<p>Disk <b>full</b></p><br/>on db-01 &amp; db-02",
		"color": "\x1b[31mred\x1b[0m text",
		"esc":   `line 1\nline 2\ttab`,
		"long":  "abcdefghij",
	}
	cases := map[string]string{
		"${trace|firstline}":            "java.lang.NullPointerException: boom",
		"${trace|lastline}":             "Caused by: java.io.IOException: closed",
		"${trace|errorline}":            "java.lang.NullPointerException: boom",
		"${trace|oneline}":              "java.lang.NullPointerException: boom at a.B.c(B.java:1) at d.E.f(E.java:2) Caused by: java.io.IOException: closed",
		"${html|nohtml}":                "Disk full\n\non db-01 & db-02",
		"${color|noansi}":               "red text",
		"${esc|unescape}":               "line 1\nline 2\ttab",
		"${long|truncate:5}":            "abcd…",
		"${long|truncate:20}":           "abcdefghij",
		"${esc|unescape|head:1}":        "line 1\n…",
		"${esc|unescape|tail:1}":        "line 2\ttab",
		"${missing|$trace|firstline}":   "java.lang.NullPointerException: boom",
		"${trace|firstline|truncate:9}": "java.lan…",
	}
	for src, want := range cases {
		tp, err := CompileTemplate(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if got, err := tp.String(data); err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", src, got, err, want)
		}
	}
	for _, bad := range []string{"${x|truncate}", "${x|truncate:0}", "${x|head:abc}"} {
		if _, err := CompileTemplate(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func run(t *testing.T, nodes []Node, body string) *Result {
	t.Helper()
	g := Graph{Nodes: append([]Node{node("in", "trigger.webhook", map[string]any{"anonymous": true}), node("parse", "parse.json", nil)}, nodes...)}
	ids := []string{"in", "parse"}
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	for i := 0; i+1 < len(ids); i++ {
		g.Edges = append(g.Edges, edge(ids[i], "main", ids[i+1]))
	}
	p, issues := Compile(g, CompileOptions{})
	if p == nil {
		t.Fatalf("compile: %v", issues)
	}
	res, err := p.Run(context.Background(), Input{Body: []byte(body)}, RunOptions{Trace: true})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func eventNode(extra map[string]any) []Node {
	params := map[string]any{"title": "${title}", "ci": "db-01", "severity": "warning", "status": "firing"}
	for k, v := range extra {
		params[k] = v
	}
	return []Node{node("map", "map.event", params), node("out", "out.event", nil)}
}

// transform.text cleans a stack trace, keeps its head and tail and makes a summary of the root cause.
func TestTransformText(t *testing.T) {
	var b strings.Builder
	b.WriteString(`\u001b[31mjava.lang.IllegalStateException: pool exhausted\u001b[0m\n\n\n`)
	for i := range 50 {
		b.WriteString(`\tat org.springframework.X.call(X.java:` + strings.Repeat("1", i%3+1) + `)   \n`)
		b.WriteString(`\tat com.example.Pay.run(Pay.java:7)\n`)
	}
	b.WriteString(`Caused by: java.net.SocketTimeoutException: connect timed out\n\tat java.net.Socket.connect(Socket.java:1)`)
	body := `{"title": "Payments", "trace": "` + b.String() + `"}`
	text := node("text", "transform.text", map[string]any{
		"source": "${trace}", "target": "details", "drop_lines": []any{`^\s+at org\.springframework\.`},
		"head_lines": 3.0, "tail_lines": 2.0, "summary_target": "summary", "summary": "last_error_line",
		"replace": []any{map[string]any{"pattern": `Pay\.java:\d+`, "with": "Pay.java"}},
	})
	res := run(t, append([]Node{text}, eventNode(map[string]any{"title": "${summary}", "description": "${details}"})...), body)
	if len(res.Events) != 1 {
		t.Fatalf("events = %+v, failures = %+v", res.Events, res.Failures)
	}
	e := res.Events[0].Event
	if e.Title != "Caused by: java.net.SocketTimeoutException: connect timed out" {
		t.Errorf("title = %q", e.Title)
	}
	want := "java.lang.IllegalStateException: pool exhausted\n\n\tat com.example.Pay.run(Pay.java)\n… 49 lines skipped …\nCaused by: java.net.SocketTimeoutException: connect timed out\n\tat java.net.Socket.connect(Socket.java:1)"
	if e.Description != want {
		t.Errorf("description =\n%q\nwant\n%q", e.Description, want)
	}

	// extract, keep_lines, single line and a length limit.
	text = node("text", "transform.text", map[string]any{
		"source": "${msg}", "extract": `(?s)ERROR (?P<text>.*)$`, "keep_lines": []any{"disk"}, "single_line": true, "max_chars": 20.0,
	})
	res = run(t, append([]Node{text}, eventNode(map[string]any{"title": "${text}"})...), `{"msg": "INFO start\nERROR disk sda1 is full\nretry\nof the disk again"}`)
	if len(res.Events) != 1 || res.Events[0].Event.Title != "disk sda1 is full o…" {
		t.Errorf("events = %+v, failures = %+v", res.Events, res.Failures)
	}
}

// A title or value that is a whole stack trace is cut to its first line; the full text goes to the description.
func TestEventShortensTitle(t *testing.T) {
	long := strings.Repeat("x", 400)
	res := run(t, eventNode(map[string]any{"value": "${v}", "description": "${d}",
		"fields": []any{map[string]any{"name": "Link", "value": "${link}"}, map[string]any{"name": "Empty", "value": "${none}"}}}),
		`{"title": "NullPointerException: boom\n\tat a.B(B.java:1)", "v": "`+long+`", "d": "context", "link": "http://g/d/1"}`)
	if len(res.Events) != 1 {
		t.Fatalf("failures = %+v", res.Failures)
	}
	e := res.Events[0].Event
	if e.Title != "NullPointerException: boom" || len([]rune(e.Value)) != maxValue {
		t.Errorf("title = %q, value = %d characters", e.Title, len([]rune(e.Value)))
	}
	if e.Description != "context\n\nNullPointerException: boom\n\tat a.B(B.java:1)\n\n"+long {
		t.Errorf("description = %q", e.Description)
	}
	if len(e.Fields) != 1 || e.Fields[0] != (Field{Name: "Link", Value: "http://g/d/1"}) {
		t.Errorf("fields = %+v", e.Fields)
	}
	res = run(t, eventNode(map[string]any{"description": "${d}"}), `{"title": "x", "d": "`+strings.Repeat("y", MaxDescription+100)+`"}`)
	if d := res.Events[0].Event.Description; len(d) > MaxDescription || !strings.HasSuffix(d, "more bytes)") {
		t.Errorf("description of %d bytes ends with %q", len(d), d[len(d)-30:])
	}
}

func TestParseRegexAndKV(t *testing.T) {
	re := node("re", "parse.regex", map[string]any{"source": "${msg}", "pattern": `(?P<host>[\w-]+): (?P<error>.+)`})
	res := run(t, append([]Node{re}, eventNode(map[string]any{"title": "${error}", "ci": "${host}"})...), `{"msg": "db-01: disk full"}`)
	if len(res.Events) != 1 || res.Events[0].Event.CI != "db-01" || res.Events[0].Event.Title != "disk full" {
		t.Errorf("events = %+v, failures = %+v", res.Events, res.Failures)
	}
	re = node("re", "parse.regex", map[string]any{"source": "${msg}", "pattern": `(?P<n>\d+)`, "no_match": "drop"})
	if res := run(t, append([]Node{re}, eventNode(nil)...), `{"title": "t", "msg": "none"}`); len(res.Events) != 0 || res.Filtered != 1 {
		t.Errorf("no match should drop: %+v", res)
	}
	re = node("re", "parse.regex", map[string]any{"source": "${msg}", "pattern": `(\d+)`})
	if _, issues := Compile(Graph{Nodes: []Node{node("in", "trigger.webhook", map[string]any{"anonymous": true}), re}}, CompileOptions{}); !hasIssue(issues, "pattern") {
		t.Errorf("a pattern without named groups is an issue: %v", issues)
	}

	kv := node("kv", "parse.kv", map[string]any{"source": "${v}"})
	res = run(t, append([]Node{kv}, eventNode(map[string]any{"title": "${kv.msg} on ${kv.host}", "value": "${kv.value}"})...),
		`{"v": "[ var='A' host=db-01, msg=\"disk full\"; value=912 ]"}`)
	if len(res.Events) != 1 || res.Events[0].Event.Title != "disk full on db-01" || res.Events[0].Event.Value != "912" {
		t.Errorf("events = %+v, failures = %+v", res.Events, res.Failures)
	}
}
