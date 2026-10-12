package response

import (
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

func testCatalog() Catalog {
	return Catalog{
		Services: map[string]model.Service{
			"S-1": {ID: "S-1", Name: "Payments", Criticality: model.CriticalityHigh, Status: model.ServiceActive, OwnerTeamID: "T-1"},
			"S-2": {ID: "S-2", Name: "Online banking", Criticality: model.CriticalityCritical, Status: model.ServiceActive, DependsOn: []string{"S-1"}},
			"S-3": {ID: "S-3", Name: "Mobile app", Criticality: model.CriticalityMedium, Status: model.ServiceActive, DependsOn: []string{"S-2"}},
			"S-4": {ID: "S-4", Name: "Old", Criticality: model.CriticalityCritical, Status: model.ServiceRetired, DependsOn: []string{"S-1"}},
			"S-5": {ID: "S-5", Name: "Wiki", Criticality: model.CriticalityLow, Status: model.ServiceActive},
		},
	}
}

func incident(sev, method string, services ...string) alert.Alert {
	a := alert.Alert{ID: "INC-1", Severity: sev, Method: method, Status: alert.StatusOpen}
	for _, s := range services {
		a.Route.Services = append(a.Route.Services, alert.Ref{ID: s, Name: s})
	}
	return a
}

func TestAssessDependentServicesRaisePriority(t *testing.T) {
	p := model.DefaultImpactPolicy()
	as := Assess(p, testCatalog(), incident(model.SeverityWarning, model.MethodUSE, "S-1"), nil, time.Now())
	// Payments is high, but Online banking (critical) depends on it, and the mobile app on that:
	// the retired service is left out.
	if len(as.Services) != 3 || !as.Services[0].Direct || as.Services[1].Name != "Online banking" || as.Services[1].Via != "Payments" || as.Services[2].Via != "Online banking" {
		t.Fatalf("services = %+v", as.Services)
	}
	if as.TopCriticality != model.CriticalityCritical || as.Impact != model.ImpactExtensive {
		t.Fatalf("impact = %s from %s, reasons %+v", as.Impact, as.TopCriticality, as.Reasons)
	}
	// extensive × medium = P2.
	if as.Priority != model.SeverityError {
		t.Fatalf("priority = %s", as.Priority)
	}
}

func TestAssessRaisesAndMatrix(t *testing.T) {
	p := model.DefaultImpactPolicy()
	c := testCatalog()
	// A low service with a USE signal: minor × high = P3.
	as := Assess(p, c, incident(model.SeverityError, model.MethodUSE, "S-5"), nil, time.Now())
	if as.Impact != model.ImpactMinor || as.Priority != model.SeverityWarning {
		t.Fatalf("use: %s %s", as.Impact, as.Priority)
	}
	// The same as RED widens to moderate: moderate × high = P2.
	as = Assess(p, c, incident(model.SeverityError, model.MethodRED, "S-5"), nil, time.Now())
	if as.Impact != model.ImpactModerate || as.Priority != model.SeverityError {
		t.Fatalf("red: %s %s %+v", as.Impact, as.Priority, as.Reasons)
	}
	// Five active incidents on the service widen once more: significant × high = P2.
	as = Assess(p, c, incident(model.SeverityError, model.MethodRED, "S-5"), map[string]int{"S-5": 5}, time.Now())
	if as.Impact != model.ImpactSignificant || as.Incidents != 5 {
		t.Fatalf("mass: %s %d", as.Impact, as.Incidents)
	}
	// No service: the policy's impact.
	as = Assess(p, c, incident(model.SeverityCritical, model.MethodOther), nil, time.Now())
	if as.Impact != model.ImpactModerate || as.Priority != model.SeverityError || as.Reasons[0].Code != "no_service" {
		t.Fatalf("no service: %+v", as)
	}
	// Never lower keeps the severity of the events.
	p.NeverLower = true
	as = Assess(p, c, incident(model.SeverityCritical, model.MethodOther), nil, time.Now())
	if as.Priority != model.SeverityCritical {
		t.Fatalf("never lower: %s", as.Priority)
	}
}

func TestPoliciesNormalize(t *testing.T) {
	pols, err := model.NormalizePolicies([]model.ResponsePolicy{{Priority: model.SeverityCritical, Enabled: true, Steps: []model.EscalationStep{
		{AfterMinutes: 30, Targets: []string{"lead", "lead"}, Methods: []string{"email"}},
		{AfterMinutes: 0, Targets: []string{"route"}, Methods: []string{"telegram", "war_room"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(pols) != 5 || pols[0].Steps[0].AfterMinutes != 0 || len(pols[0].Steps[1].Targets) != 1 || pols[1].Enabled || pols[0].Jira.PostmortemDays != 10 {
		t.Fatalf("policies = %+v", pols)
	}
	for _, bad := range []model.ResponsePolicy{
		{Priority: "p0"},
		{Priority: model.SeverityError, Bridge: "skype"},
		{Priority: model.SeverityError, Steps: []model.EscalationStep{{Methods: []string{"pigeon"}}}},
		{Priority: model.SeverityError, Steps: []model.EscalationStep{{Targets: []string{"boss"}}}},
		{Priority: model.SeverityError, Steps: []model.EscalationStep{{AfterMinutes: -1}}},
	} {
		if _, err := model.NormalizePolicies([]model.ResponsePolicy{bad}); err == nil {
			t.Errorf("%+v is accepted", bad)
		}
	}
	var ip model.ImpactPolicy
	if err := ip.Validate(); err != nil || ip.Matrix[model.ImpactMinor][model.SeverityInfo] != model.SeverityInfo || ip.Criticality[model.CriticalityHigh] != model.ImpactSignificant {
		t.Fatalf("an empty impact policy gets the defaults: %v %+v", err, ip)
	}
}

func TestDocFormats(t *testing.T) {
	d := Doc{H("Summary"), P("a <b> & c"), UL("one", "two"), Link("Open", "https://x.example/?a=1&b=2")}
	adf := d.ADF()
	if adf["type"] != "doc" || len(adf["content"].([]any)) != 4 {
		t.Fatalf("adf = %+v", adf)
	}
	if h := d.HTML(); h != `<p><b>Summary</b></p><p>a &lt;b&gt; &amp; c</p><ul><li>one</li><li>two</li></ul><p><a href="https://x.example/?a=1&amp;b=2">Open</a></p>` {
		t.Fatalf("html = %s", h)
	}
}
