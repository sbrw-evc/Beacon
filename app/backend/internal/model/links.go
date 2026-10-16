package model

import (
	"net/url"
	"strings"
)

// IncidentURL is the page of an incident in Beacon under the public address base (a trailing
// slash is dropped); an empty base gives the path alone.
func IncidentURL(base, id string) string {
	return strings.TrimRight(base, "/") + "/incidents?id=" + url.QueryEscape(id)
}

// GrafanaHopURL is the link that opens the Grafana context of an incident through Beacon, so
// the link stays right when the Grafana settings change.
func GrafanaHopURL(base, id string) string {
	return strings.TrimRight(base, "/") + "/go/incidents/" + url.PathEscape(id) + "/grafana"
}

// FlowHopURL is the link that opens the traffic of an incident in Current through
// Beacon, so the link stays right when the Current address changes.
func FlowHopURL(base, id string) string {
	return strings.TrimRight(base, "/") + "/go/incidents/" + url.PathEscape(id) + "/flow"
}

// CIFlowHopURL opens the recent traffic of a configuration item in Current through Beacon.
func CIFlowHopURL(base, id string) string {
	return strings.TrimRight(base, "/") + "/go/cis/" + url.PathEscape(id) + "/flow"
}
