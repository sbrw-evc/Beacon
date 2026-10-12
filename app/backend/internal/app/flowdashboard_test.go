package app_test

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// flowColumns are the columns of the Umbrella Flow ClickHouse tables (Network repository,
// app/backend/internal/schema/columns.go); rollup marks the ones the flows_1m / flows_5m /
// flows_1h aggregates keep.
var flowColumns = map[string]bool{
	"TimeReceived": true, "SamplingRate": false, "FlowType": true,
	"ExporterAddress": true, "ExporterName": true, "ExporterRef": true, "ExporterSite": true, "ExporterRegion": true,
	"ExporterRole": true, "ExporterGroup": true, "ExporterTenant": true,
	"InIfIndex": true, "InIfName": true, "InIfDescription": true, "InIfSpeed": true, "InIfBoundary": true,
	"InIfConnectivity": true, "InIfProvider": true, "InIfCircuit": true,
	"OutIfIndex": true, "OutIfName": true, "OutIfDescription": true, "OutIfSpeed": true, "OutIfBoundary": true,
	"OutIfConnectivity": true, "OutIfProvider": true, "OutIfCircuit": true,
	"SrcAddr": false, "DstAddr": false, "SrcNetMask": false, "DstNetMask": false, "NextHop": true, "SrcAS": true, "DstAS": true,
	"SrcNetPrefix": true, "SrcNetName": true, "SrcNetRole": true, "SrcNetSite": true, "SrcNetTenant": true, "SrcNetVRF": true,
	"SrcHost": false, "SrcCI": true, "SrcCountry": true, "SrcCity": false,
	"DstNetPrefix": true, "DstNetName": true, "DstNetRole": true, "DstNetSite": true, "DstNetTenant": true, "DstNetVRF": true,
	"DstHost": false, "DstCI": true, "DstCountry": true, "DstCity": false,
	"SrcPort": false, "DstPort": false, "Proto": true, "EType": true, "TCPFlags": false, "IPTos": false,
	"ICMPType": false, "ICMPCode": false, "SrcVlan": true, "DstVlan": true, "ForwardingStatus": true, "App": true,
	"Bytes": true, "Packets": true,
}

// The "CI traffic" dashboard of deploy/grafana (docs/integrations.md §3.5): the Flow ClickHouse
// datasource, the variables Umbrella's incident link fills, and queries over the Flow tables
// that use only their columns (the aggregates only the rollup ones).
func TestFlowCITrafficDashboard(t *testing.T) {
	raw, err := os.ReadFile("../../../../deploy/grafana/flow-ci-traffic.json")
	if err != nil {
		t.Fatal(err)
	}
	var dash struct {
		UID        string `json:"uid"`
		Title      string `json:"title"`
		Templating struct {
			List []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"list"`
		} `json:"templating"`
		Panels []struct {
			Type       string `json:"type"`
			Title      string `json:"title"`
			Datasource *struct {
				Type string `json:"type"`
				UID  string `json:"uid"`
			} `json:"datasource"`
			Targets []struct {
				RawSQL     string `json:"rawSql"`
				Datasource struct {
					Type string `json:"type"`
					UID  string `json:"uid"`
				} `json:"datasource"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(raw, &dash); err != nil {
		t.Fatal(err)
	}
	if dash.UID != "flow-ci-traffic" || dash.Title != "CI traffic" {
		t.Fatalf("uid %q title %q", dash.UID, dash.Title)
	}
	var vars []string
	for _, v := range dash.Templating.List {
		vars = append(vars, v.Name)
	}
	for _, want := range []string{"ci", "ci_id", "ip", "ref"} {
		if !slices.Contains(vars, want) {
			t.Fatalf("variable %s missing: %v", want, vars)
		}
	}
	table := regexp.MustCompile(`FROM \$\{db\}\.(flows|\$\{rollup\})(\s|$)`)
	ident := regexp.MustCompile(`\b[A-Z][A-Za-z]+\b`)
	titles := map[string]bool{}
	for _, p := range dash.Panels {
		titles[p.Title] = true
		if p.Type == "text" {
			continue
		}
		if p.Datasource == nil || p.Datasource.Type != "grafana-clickhouse-datasource" || p.Datasource.UID != "flow-clickhouse" {
			t.Fatalf("panel %q: datasource %+v", p.Title, p.Datasource)
		}
		for _, q := range p.Targets {
			if q.Datasource.UID != "flow-clickhouse" {
				t.Fatalf("panel %q: target datasource %+v", p.Title, q.Datasource)
			}
			m := table.FindStringSubmatch(q.RawSQL)
			if m == nil {
				t.Fatalf("panel %q reads no Flow table: %s", p.Title, q.RawSQL)
			}
			if !strings.Contains(q.RawSQL, "$__timeFilter(TimeReceived)") || !strings.Contains(q.RawSQL, "${ref:sqlstring}") {
				t.Fatalf("panel %q: no time or reference filter", p.Title)
			}
			if m[1] == "flows" && !strings.Contains(q.RawSQL, "${ip:sqlstring}") {
				t.Fatalf("panel %q: no address filter", p.Title)
			}
			// SQL words and functions are upper case or lower camel; the capitalized words
			// left are column names, quoted aliases aside.
			unquoted := regexp.MustCompile(`"[^"]*"|'[^']*'`).ReplaceAllString(q.RawSQL, "")
			for _, w := range ident.FindAllString(unquoted, -1) {
				if w == strings.ToUpper(w) {
					continue
				}
				rollup, ok := flowColumns[w]
				if !ok {
					t.Fatalf("panel %q: unknown column %s", p.Title, w)
				}
				if m[1] != "flows" && !rollup {
					t.Fatalf("panel %q: %s is not in the aggregated tables", p.Title, w)
				}
			}
		}
	}
	for _, want := range []string{"Traffic in / out — ${ci}", "Top talkers", "Top ports and applications", "Top interfaces"} {
		if !titles[want] {
			t.Fatalf("panel %q missing", want)
		}
	}
}
