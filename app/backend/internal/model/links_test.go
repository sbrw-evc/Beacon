package model

import "testing"

func TestIncidentLinks(t *testing.T) {
	cases := []struct {
		base, id, incident, grafana string
	}{
		{"https://umbrella.example.com", "INC-7", "https://umbrella.example.com/incidents?id=INC-7", "https://umbrella.example.com/go/incidents/INC-7/grafana"},
		{"https://umbrella.example.com/", "INC-7", "https://umbrella.example.com/incidents?id=INC-7", "https://umbrella.example.com/go/incidents/INC-7/grafana"},
		{"https://x.example/umb//", "INC-7", "https://x.example/umb/incidents?id=INC-7", "https://x.example/umb/go/incidents/INC-7/grafana"},
		{"", "INC-7", "/incidents?id=INC-7", "/go/incidents/INC-7/grafana"},
		{"http://u", "a b&c=d/e?f#g", "http://u/incidents?id=a+b%26c%3Dd%2Fe%3Ff%23g", "http://u/go/incidents/a%20b&c=d%2Fe%3Ff%23g/grafana"},
		{"http://u", "ИНЦ-1", "http://u/incidents?id=%D0%98%D0%9D%D0%A6-1", "http://u/go/incidents/%D0%98%D0%9D%D0%A6-1/grafana"},
	}
	for _, c := range cases {
		if got := IncidentURL(c.base, c.id); got != c.incident {
			t.Errorf("IncidentURL(%q, %q) = %q, want %q", c.base, c.id, got, c.incident)
		}
		if got := GrafanaHopURL(c.base, c.id); got != c.grafana {
			t.Errorf("GrafanaHopURL(%q, %q) = %q, want %q", c.base, c.id, got, c.grafana)
		}
	}
}

func TestFlowLinks(t *testing.T) {
	if got := FlowHopURL("https://umbrella.example.com/", "INC 7"); got != "https://umbrella.example.com/go/incidents/INC%207/flow" {
		t.Errorf("FlowHopURL = %q", got)
	}
	if got := CIFlowHopURL("", "CI/1"); got != "/go/cis/CI%2F1/flow" {
		t.Errorf("CIFlowHopURL = %q", got)
	}
}
