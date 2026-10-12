package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// The web interface reads every list of a policy as an array: null breaks the page.
func TestPoliciesHaveNoNullLists(t *testing.T) {
	for name, ps := range map[string][]ResponsePolicy{
		"defaults":  DefaultResponsePolicies(),
		"effective": Response{Policies: []ResponsePolicy{{Priority: SeverityCritical, Steps: []EscalationStep{{Targets: []string{TargetRoute}}}}}}.Effective().Policies,
	} {
		b, err := json.Marshal(ps)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "null") {
			t.Fatalf("%s: %s", name, b)
		}
	}
}
