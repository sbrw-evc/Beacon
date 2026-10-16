package presets

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/sbrw-evc/beacon/app/backend/internal/flow"
)

// pipeline compiles a preset once a credential is chosen.
func pipeline(t *testing.T, p Preset) *flow.Pipeline {
	t.Helper()
	mapping := map[string]string{}
	for _, s := range p.Document.Credentials {
		mapping[s.Slot] = "CRD-1"
	}
	g, err := p.Document.Apply(mapping, func(string) (string, bool) { return flow.CredBearer, true })
	if err != nil {
		t.Fatalf("%s: %v", p.ID, err)
	}
	pl, issues := flow.Compile(g, flow.CompileOptions{Credential: func(string) (string, bool) { return flow.CredBearer, true }})
	if pl == nil {
		t.Fatalf("%s: %v", p.ID, issues)
	}
	return pl
}

// Every preset compiles once a credential is chosen, and its samples produce events without failures.
func TestPresets(t *testing.T) {
	if len(All()) != 6 {
		t.Fatalf("presets = %d", len(All()))
	}
	for _, p := range All() {
		if p.Title.RU == "" || p.Description.RU == "" || len(p.Document.Samples) == 0 {
			t.Errorf("%s: titles and samples are required", p.ID)
		}
		pl := pipeline(t, p)
		for _, s := range p.Document.Samples {
			res, err := pl.Run(context.Background(), flow.Input{Body: []byte(s.Body)}, flow.RunOptions{})
			if err != nil || len(res.Events) == 0 || len(res.Failures) > 0 {
				t.Errorf("%s / %s: events=%+v failures=%+v err=%v", p.ID, s.Name, res.Events, res.Failures, err)
			}
		}
	}
}

// The catalog keeps the generic webhook last, and quick connect offers the presets in its order.
func TestOrder(t *testing.T) {
	var ids []string
	for _, p := range All() {
		ids = append(ids, p.ID)
	}
	if strings.Join(ids, ",") != "network-flow,grafana,graylog,alertmanager,zabbix,webhook" {
		t.Errorf("catalog = %v", ids)
	}
	ids = nil
	for _, p := range QuickAll() {
		ids = append(ids, p.ID)
	}
	if strings.Join(ids, ",") != "zabbix,alertmanager,grafana,graylog,network-flow,webhook" {
		t.Errorf("quick = %v", ids)
	}
}

// The test delivery of every quick preset goes through its own pipeline and makes an event
// with the chosen item, also when the item needs escaping.
func TestQuickTestBody(t *testing.T) {
	for _, p := range QuickAll() {
		body, ok, err := p.Quick.RenderTestBody(Vars{Nonce: "abc123", CI: `db-01 "x"<&>`, Title: "Test"})
		if !ok || err != nil {
			t.Fatalf("%s: ok=%v err=%v", p.ID, ok, err)
		}
		res, err := pipeline(t, p).Run(context.Background(), flow.Input{Body: body}, flow.RunOptions{})
		if err != nil || len(res.Events) != 1 || len(res.Failures) > 0 {
			t.Fatalf("%s: events=%+v failures=%+v err=%v", p.ID, res.Events, res.Failures, err)
		}
		if e := res.Events[0].Event; e.CI != `db-01 "x"<&>` || e.Title != "Test" || e.ExternalID != "umbrella-test-abc123" {
			t.Errorf("%s: event %+v", p.ID, e)
		}
	}
}

// A blank connector starts from webhook → JSON → event mapping → event, a copy of its own.
func TestStarterGraph(t *testing.T) {
	g := StarterGraph()
	if len(g.Nodes) != 4 || len(g.Edges) != 3 || g.Nodes[2].Params["ci"] != "${host}" {
		t.Fatalf("starter = %+v", g)
	}
	g.Nodes[0].Params["x"] = 1
	if _, ok := StarterGraph().Nodes[0].Params["x"]; ok {
		t.Error("the starter graph is shared between callers")
	}
}

// The media type template filled in with its placeholders is the file in deploy/zabbix.
func TestZabbixMediaTypeMatchesDeploy(t *testing.T) {
	b, err := os.ReadFile("../../../../deploy/zabbix/beacon-mediatype.yaml")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := Get("zabbix")
	got, err := p.Quick.Attachment.Render(Vars{URL: "http://beacon:8080/api/ingest/zabbix", Token: "<beacon ingest token>"})
	if err != nil || got != string(b) {
		t.Errorf("presets/zabbix-mediatype.yaml.tmpl differs from deploy/zabbix/beacon-mediatype.yaml: copy it again (%v)", err)
	}
	got, _ = p.Quick.Attachment.Render(Vars{URL: "https://umb.example.com/api/ingest/zabbix-2", Token: "tok'en"})
	if !strings.Contains(got, "value: 'https://umb.example.com/api/ingest/zabbix-2'") || !strings.Contains(got, "value: 'tok''en'") {
		t.Errorf("filled media type:\n%s", got)
	}
}
