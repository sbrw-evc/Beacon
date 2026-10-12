package pagerduty_test

import (
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/pagerduty"
)

// A route matches the team and the primary service of the alert route, the first row from the
// top wins, and the order of rows decides between rows that both match.
func TestRouteMatchesPrimaryServiceInOrder(t *testing.T) {
	// db-01 is in Payments (primary: its team SRE gets the alert) and Reports.
	a := alert.Alert{Route: alert.Route{
		Services: []alert.Ref{{ID: "S-1", Name: "Payments"}, {ID: "S-2", Name: "Reports"}},
		Service:  &alert.Ref{ID: "S-1", Name: "Payments"},
		Team:     &alert.Ref{ID: "T-1", Name: "SRE"},
	}}
	set := model.PagerDuty{RoutingKeyRef: "default", Routes: []model.PDRoute{
		{ID: "R-reports", Name: "Reports", ServiceID: "S-2", RoutingKeyRef: "k1"},
		{ID: "R-sre", Name: "SRE", TeamID: "T-1", RoutingKeyRef: "k2"},
		{ID: "R-pay", Name: "Payments", ServiceID: "S-1", RoutingKeyRef: "k3"},
	}}
	if id, _ := pagerduty.RouteOf(set, a); id != "R-sre" {
		t.Fatalf("a secondary service must not pick the route; got %s", id)
	}
	set.Routes[1], set.Routes[2] = set.Routes[2], set.Routes[1]
	if id, _ := pagerduty.RouteOf(set, a); id != "R-pay" {
		t.Fatalf("the first matching row from the top wins; got %s", id)
	}
	set.Routes = set.Routes[:1]
	if id, _ := pagerduty.RouteOf(set, a); id != pagerduty.DefaultRoute {
		t.Fatalf("no match: default; got %s", id)
	}

	// An alert routed before the primary service was recorded matches any of its services.
	old := a
	old.Route.Service = nil
	if id, _ := pagerduty.RouteOf(set, old); id != "R-reports" {
		t.Fatalf("legacy alert = %s", id)
	}
}
