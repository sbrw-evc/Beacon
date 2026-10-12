package flow

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPath(t *testing.T) {
	data := map[string]any{
		"labels": map[string]any{"app.kubernetes.io/name": "api", "severity": "high"},
		"alerts": []any{map[string]any{"id": "a1"}, map[string]any{"id": "a2"}},
	}
	cases := map[string]any{
		`labels.severity`:                  "high",
		`labels["app.kubernetes.io/name"]`: "api",
		`labels['app.kubernetes.io/name']`: "api",
		`alerts[1].id`:                     "a2",
		`alerts.0.id`:                      "a1",
		`alerts[-1].id`:                    "a2",
	}
	for src, want := range cases {
		p, err := ParsePath(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		got, ok := p.Get(data)
		if !ok || got != want {
			t.Errorf("%s = %v, %v; want %v", src, got, ok, want)
		}
	}
	for _, bad := range []string{"", "a..b", "a[", `a["x]`, "a[x]", ".a"} {
		if _, err := ParsePath(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
	p, _ := ParsePath(`labels["app.kubernetes.io/name"]`)
	if p.String() != `labels["app.kubernetes.io/name"]` {
		t.Errorf("String() = %s", p)
	}
}

func TestTemplate(t *testing.T) {
	data := map[string]any{
		"host": "db-01", "empty": "", "n": 5.0, "flag": true,
		"labels": map[string]any{"app.kubernetes.io/name": "api", "env": " Prod "},
		"ts":     "2026-10-05T10:00:00Z",
		"epoch":  1759658400.0,
		"list":   []any{"x", "y"},
	}
	cases := map[string]string{
		"${host}":                             "db-01",
		"host=${host}, n=${n}":                "host=db-01, n=5",
		"${missing|$host}":                    "db-01",
		"${missing|$empty|fallback}":          "fallback",
		`${missing|"two words"}`:              "two words",
		`${labels["app.kubernetes.io/name"]}`: "api",
		"${labels.env|trim|lower}":            "prod",
		"${host|upper}":                       "DB-01",
		`${missing|default:"none"}`:           "none",
		"${ts|date:\"unix\"}":                 "1791194400",
		"${epoch|date}":                       "2025-10-05T10:00:00Z",
		"${list|json}":                        `["x","y"]`,
		"$${literal}":                         "${literal}",
		"${flag}":                             "true",
		"${missing}":                          "",
		"cost: ${n|$host}":                    "cost: 5",
		"${labels|json}":                      `{"app.kubernetes.io/name":"api","env":" Prod "}`,
		`${missing|"a|b"}`:                    "a|b",
		"${empty|$missing|$labels.env|trim}":  "Prod",
	}
	for src, want := range cases {
		tp, err := CompileTemplate(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		got, err := tp.String(data)
		if err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", src, got, err, want)
		}
	}
	tp, _ := CompileTemplate("${n}")
	if v, _ := tp.Value(data); v != 5.0 {
		t.Errorf("a single placeholder keeps the type, got %#v", v)
	}
	for _, bad := range []string{"${", "${a..b}", "x ${}"} {
		if _, err := CompileTemplate(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
	tp, _ = CompileTemplate("${host|date}")
	if _, err := tp.String(data); err == nil {
		t.Error("date of a non-date must fail")
	}
}

func TestExpr(t *testing.T) {
	ctx := context.Background()
	s := Scope{
		Event:   map[string]any{"status": "firing", "labels": map[string]any{"severity": "critical"}, "count": 3.0},
		Request: map[string]any{"headers": map[string]any{"x-env": "prod"}, "remote_ip": "10.1.2.3"},
	}
	cases := map[string]bool{
		`event.status == "firing"`:                      true,
		`event.labels.severity in ["critical", "high"]`: true,
		`request.headers["x-env"] == "prod"`:            true,
		`in_cidr(request.remote_ip, "10.0.0.0/8")`:      true,
		`in_cidr(request.remote_ip, "192.168.0.0/16")`:  false,
		`event.count > 2.0`:                             true,
		`has(event.labels.team)`:                        false,
		`event.status.upperAscii() == "FIRING"`:         true,
		`sha256("a").size() == 64`:                      true,
	}
	for src, want := range cases {
		x, err := CompileCondition(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		got, err := x.Bool(ctx, s)
		if err != nil || got != want {
			t.Errorf("%s = %v, %v; want %v", src, got, err, want)
		}
	}
	if _, err := CompileCondition(`event.status + 1`); err == nil {
		t.Error("type errors are found at compile time")
	}
	if _, err := CompileCondition(`"text"`); err == nil {
		t.Error("a condition must return bool")
	}
	x, err := CompileCondition(`[1,2,3,4,5,6,7,8,9,10].all(a, [1,2,3,4,5,6,7,8,9,10].all(b, [1,2,3,4,5,6,7,8,9,10].all(c, [1,2,3,4,5,6,7,8,9,10].all(d, [1,2,3,4,5,6,7,8,9,10].all(e, a+b+c+d+e > 0)))))`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.Bool(ctx, s); err == nil || !strings.Contains(err.Error(), "cost") {
		t.Errorf("the cost limit stops expensive expressions, got %v", err)
	}
	v, err := mustExpr(t, `{"a": [1, "b"]}`).Eval(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(v); string(b) != `{"a":[1,"b"]}` {
		t.Errorf("maps and lists convert to JSON values, got %s", b)
	}
}

func mustExpr(t *testing.T, src string) *Expr {
	t.Helper()
	x, err := CompileExpr(src)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func node(id, typ string, params map[string]any) Node {
	return Node{ID: id, Type: typ, TypeVersion: 1, Params: params}
}

func edge(src, out, dst string) Edge {
	return Edge{ID: src + "-" + dst, Source: src, SourceOutput: out, Target: dst}
}

const alertmanager = `{
  "status": "firing",
  "commonLabels": {"cluster": "prod-1"},
  "alerts": [
    {"status": "firing", "fingerprint": "f1", "labels": {"alertname": "HighCPU", "instance": "db-01", "severity": "page"}, "annotations": {"summary": "CPU is high"}},
    {"status": "resolved", "fingerprint": "f2", "labels": {"alertname": "DiskFull", "instance": "db-02", "severity": "warning"}, "annotations": {}},
    {"status": "firing", "fingerprint": "f3", "labels": {"alertname": "Watchdog", "instance": "", "severity": "none"}},
    {"status": "firing", "fingerprint": "f4", "labels": {"alertname": "Odd", "instance": "app-01", "severity": "weird"}}
  ]
}`

func alertGraph() Graph {
	return Graph{
		Nodes: []Node{
			node("in", "trigger.webhook", map[string]any{"anonymous": true}),
			node("parse", "parse.json", map[string]any{"items": "alerts", "root_as": "root"}),
			node("drop", "filter", map[string]any{"condition": `event.labels.alertname != "Watchdog"`}),
			node("sev", "map.severity", map[string]any{
				"source":  "${labels.severity}",
				"mapping": []any{map[string]any{"from": "page", "to": "critical"}},
			}),
			node("labels", "enrich.labels", map[string]any{"labels": []any{
				map[string]any{"key": "cluster", "value": "${root.commonLabels.cluster}"},
				map[string]any{"key": "source", "value": "alertmanager"},
			}}),
			node("map", "map.event", map[string]any{
				"title": "${annotations.summary|$labels.alertname}", "ci": "${labels.instance}", "signal": "${labels.alertname}",
				"external_id": "${fingerprint}",
			}),
			node("out", "out.event", nil),
		},
		Edges: []Edge{
			edge("in", "main", "parse"), edge("parse", "main", "drop"), edge("drop", "main", "sev"),
			edge("sev", "main", "labels"), edge("labels", "main", "map"), edge("map", "main", "out"),
		},
	}
}

func TestPipelineAlertmanager(t *testing.T) {
	p, issues := Compile(alertGraph(), CompileOptions{})
	if p == nil {
		t.Fatalf("compile: %v", issues)
	}
	res, err := p.Run(context.Background(), Input{RequestID: "r1", Body: []byte(alertmanager)}, RunOptions{Trace: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 2 {
		t.Fatalf("events = %+v, failures = %+v", res.Events, res.Failures)
	}
	e := res.Events[0].Event
	if e.Title != "CPU is high" || e.CI != "db-01" || e.Severity != SeverityCritical || e.Status != StatusFiring ||
		e.Labels["cluster"] != "prod-1" || e.Labels["source"] != "alertmanager" || e.Labels["alertname"] != "HighCPU" {
		t.Errorf("first event = %+v", e)
	}
	if res.Events[0].Lineage != (Lineage{Request: "r1", Item: 0}) || res.Events[1].Lineage.Item != 1 {
		t.Errorf("lineage = %+v, %+v", res.Events[0].Lineage, res.Events[1].Lineage)
	}
	e2 := res.Events[1].Event
	if e2.Title != "DiskFull" || e2.Status != StatusResolved || e2.Severity != SeverityWarning {
		t.Errorf("second event = %+v", e2)
	}
	if res.Filtered != 1 {
		t.Errorf("filtered = %d", res.Filtered)
	}
	if len(res.Failures) != 1 || res.Failures[0].Node != "sev" || !strings.Contains(res.Failures[0].Error, "weird") || res.Failures[0].Lineage.Item != 3 {
		t.Errorf("failures = %+v", res.Failures)
	}
	if tr := res.Trace["parse"]; tr.In != 1 || tr.Out["main"] != 4 || len(tr.Output["main"]) != 4 {
		t.Errorf("parse trace = %+v", tr)
	}
	if tr := res.Trace["drop"]; tr.Filtered != 1 {
		t.Errorf("filter trace = %+v", tr)
	}
	if !slices.Equal(res.Order, []string{"in", "parse", "drop", "sev", "labels", "map", "out"}) {
		t.Errorf("order = %v", res.Order)
	}
	if e.Key == e2.Key || e.Key == "" {
		t.Error("keys differ per alert")
	}
	again, _ := p.Run(context.Background(), Input{RequestID: "r2", Body: []byte(alertmanager)}, RunOptions{})
	if again.Events[0].Event.Key != e.Key {
		t.Error("the same alert gets the same key on redelivery")
	}
}

func TestPipelineErrorPolicies(t *testing.T) {
	g := alertGraph()
	g.Nodes[3].OnError = OnErrorSkip
	p, issues := Compile(g, CompileOptions{})
	if p == nil {
		t.Fatal(issues)
	}
	res, _ := p.Run(context.Background(), Input{Body: []byte(alertmanager)}, RunOptions{})
	if res.Skipped != 1 || len(res.Failures) != 0 {
		t.Errorf("skip: skipped=%d failures=%v", res.Skipped, res.Failures)
	}

	g = alertGraph()
	g.Nodes[3].OnError = OnErrorRoute
	g.Nodes = append(g.Nodes, node("raw", "map.event", map[string]any{"title": "Unmapped ${labels.alertname}", "ci": "${labels.instance}", "severity": "warning"}))
	g.Edges = append(g.Edges, edge("sev", "error", "raw"), edge("raw", "main", "out"))
	p, issues = Compile(g, CompileOptions{})
	if p == nil {
		t.Fatal(issues)
	}
	res, _ = p.Run(context.Background(), Input{Body: []byte(alertmanager)}, RunOptions{})
	if len(res.Events) != 3 || len(res.Failures) != 0 {
		t.Fatalf("route_error: events=%+v failures=%+v", res.Events, res.Failures)
	}
	if res.Events[2].Event.Title != "Unmapped Odd" || res.Events[2].Event.Severity != SeverityWarning {
		t.Errorf("routed event = %+v", res.Events[2].Event)
	}

	res, _ = p.Run(context.Background(), Input{Body: []byte("not json")}, RunOptions{})
	if len(res.Failures) != 1 || res.Failures[0].Node != "parse" || res.Failures[0].Raw != "not json" {
		t.Errorf("bad body: %+v", res.Failures)
	}
}

func TestPipelineSwitch(t *testing.T) {
	g := Graph{
		Nodes: []Node{
			node("in", "trigger.webhook", map[string]any{"anonymous": true}),
			node("parse", "parse.json", nil),
			node("sw", "route.switch", map[string]any{"rules": []any{
				map[string]any{"output": "db", "condition": `event.host.startsWith("db")`},
				map[string]any{"output": "web", "condition": `event.host.startsWith("web")`},
			}}),
			node("db", "map.event", map[string]any{"title": "DB ${host}", "ci": "${host}", "severity": "critical"}),
			node("other", "map.event", map[string]any{"title": "Host ${host}", "ci": "${host}", "severity": "info"}),
			node("out", "out.event", nil),
		},
		Edges: []Edge{
			edge("in", "main", "parse"), edge("parse", "main", "sw"),
			edge("sw", "db", "db"), edge("sw", "else", "other"), edge("sw", "web", "other"),
			edge("db", "main", "out"), edge("other", "main", "out"),
		},
	}
	p, issues := Compile(g, CompileOptions{})
	if p == nil {
		t.Fatal(issues)
	}
	res, _ := p.Run(context.Background(), Input{Body: []byte(`[{"host":"db-1"},{"host":"web-1"},{"host":"x"}]`)}, RunOptions{})
	var titles []string
	for _, e := range res.Events {
		titles = append(titles, e.Event.Title+"/"+e.Event.Severity)
	}
	if !slices.Equal(titles, []string{"DB db-1/critical", "Host web-1/info", "Host x/info"}) {
		t.Errorf("titles = %v", titles)
	}
}

func TestCompileIssues(t *testing.T) {
	codes := func(g Graph, opt CompileOptions) []string {
		_, issues := Compile(g, opt)
		var out []string
		for _, i := range issues {
			out = append(out, i.Code)
		}
		return out
	}
	has := func(t *testing.T, got []string, want string) {
		t.Helper()
		if !slices.Contains(got, want) {
			t.Errorf("issues %v do not contain %s", got, want)
		}
	}
	g := alertGraph()
	g.Nodes[0].Params = map[string]any{}
	has(t, codes(g, CompileOptions{}), "auth_required")

	g = alertGraph()
	g.Edges = append(g.Edges, edge("map", "main", "parse"))
	has(t, codes(g, CompileOptions{}), "cycle")

	g = alertGraph()
	g.Edges = g.Edges[:5]
	has(t, codes(g, CompileOptions{}), "no_output")
	has(t, codes(g, CompileOptions{}), "unreachable")
	if p, _ := Compile(g, CompileOptions{Partial: true}); p == nil {
		t.Error("partial graphs compile for test runs")
	}

	g = alertGraph()
	g.Edges = append(g.Edges, edge("parse", "nope", "map"))
	has(t, codes(g, CompileOptions{}), "unknown_output")

	g = alertGraph()
	g.Nodes = append(g.Nodes, node("in2", "trigger.webhook", map[string]any{"anonymous": true}))
	has(t, codes(g, CompileOptions{}), "many_triggers")

	g = alertGraph()
	g.Nodes[2].Params["condition"] = "event.status +"
	has(t, codes(g, CompileOptions{}), "cel")

	g = alertGraph()
	g.Nodes[5].Params["title"] = "${a..b}"
	has(t, codes(g, CompileOptions{}), "template")

	g = alertGraph()
	g.Nodes[1].TypeVersion = 9
	has(t, codes(g, CompileOptions{}), "unknown_type")

	g = alertGraph()
	g.Nodes[0].Params = map[string]any{"credential": "CRD-1"}
	has(t, codes(g, CompileOptions{Credential: func(string) (string, bool) { return "", false }}), "credential")
	has(t, codes(g, CompileOptions{Credential: func(string) (string, bool) { return "oauth", true }}), "credential")
	if _, issues := Compile(g, CompileOptions{Credential: func(string) (string, bool) { return CredBearer, true }}); HasErrors(issues) {
		t.Errorf("a bearer credential is accepted: %v", issues)
	}

	g = alertGraph()
	g.Nodes = append(g.Nodes, node("ack", "ack.response", map[string]any{"status": 500.0}))
	has(t, codes(g, CompileOptions{}), "range")
}

func TestStopAtAndPins(t *testing.T) {
	p, issues := Compile(alertGraph(), CompileOptions{})
	if p == nil {
		t.Fatal(issues)
	}
	res, _ := p.Run(context.Background(), Input{Body: []byte(alertmanager)}, RunOptions{StopAt: "sev", Trace: true})
	if len(res.Events) != 0 || res.Trace["labels"] != nil || res.Trace["sev"] == nil {
		t.Errorf("stop at sev ran %v", res.Order)
	}
	pinned := map[string]map[string][]Record{"parse": {"main": {
		{Data: map[string]any{"labels": map[string]any{"alertname": "Pinned", "instance": "h1", "severity": "page"}, "fingerprint": "p1"}},
	}}}
	res, _ = p.Run(context.Background(), Input{Body: []byte("ignored")}, RunOptions{Pinned: pinned, Trace: true})
	if len(res.Events) != 1 || res.Events[0].Event.Title != "Pinned" || !res.Trace["parse"].Pinned {
		t.Errorf("pinned run = %+v", res)
	}
}

func TestDisabledNode(t *testing.T) {
	g := alertGraph()
	g.Nodes[2].Disabled = true
	p, issues := Compile(g, CompileOptions{})
	if p == nil {
		t.Fatal(issues)
	}
	res, _ := p.Run(context.Background(), Input{Body: []byte(alertmanager)}, RunOptions{})
	if res.Filtered != 0 || len(res.Failures) != 2 {
		t.Errorf("a disabled filter passes everything: filtered=%d failures=%+v", res.Filtered, res.Failures)
	}
}

func TestWebhookConfig(t *testing.T) {
	g := alertGraph()
	g.Nodes[0].Params = map[string]any{"anonymous": true, "allowed_networks": []any{"10.0.0.0/8", "192.168.1.5"}, "accept_if": `request.method == "POST"`}
	w, issues := CompileWebhook(g, CompileOptions{})
	if w == nil {
		t.Fatal(issues)
	}
	if !w.Allowed("10.2.3.4") || !w.Allowed("192.168.1.5") || w.Allowed("192.168.1.6") || w.Allowed("::ffff:8.8.8.8") {
		t.Error("networks are checked")
	}
	if w.MaxBody != 1024*1024 {
		t.Errorf("max body = %d", w.MaxBody)
	}
	ok, err := w.AcceptIf.Bool(context.Background(), Scope{Request: Input{Method: "POST"}.RequestScope()})
	if err != nil || !ok {
		t.Errorf("accept_if = %v %v", ok, err)
	}
}

func TestContextCancel(t *testing.T) {
	p, _ := Compile(alertGraph(), CompileOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	if _, err := p.Run(ctx, Input{Body: []byte(alertmanager)}, RunOptions{}); err == nil {
		t.Error("a finished context stops the run")
	}
}

func TestTypesDescribed(t *testing.T) {
	for _, nt := range Types() {
		if nt.Title.EN == "" || nt.Title.RU == "" || nt.Description.EN == "" || nt.Description.RU == "" {
			t.Errorf("%s has no title or description", nt.Type)
		}
		for _, p := range nt.Params {
			if p.Title.EN == "" || p.Title.RU == "" {
				t.Errorf("%s.%s has no title", nt.Type, p.Key)
			}
		}
	}
	if _, err := json.Marshal(Types()); err != nil {
		t.Fatal(err)
	}
}
