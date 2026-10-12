package model

import "time"

const (
	MonitoringZabbix     = "zabbix"
	MonitoringPrometheus = "prometheus"
	MonitoringGrafana    = "grafana"
	MonitoringGraylog    = "graylog"

	HostUp       = "up"
	HostPartial  = "partial"
	HostDown     = "down"
	HostUnknown  = "unknown"
	HostDisabled = "disabled"

	// HostNoCI is the link of a host someone said belongs to no configuration item, so the
	// automatic match is not used for it.
	HostNoCI = "-"
)

// MonitoringSource is a monitoring system (Zabbix, a Prometheus-compatible API, Grafana or Graylog)
// whose host list is read and matched with configuration items. The credential, when set, is one of the
// credential catalog.
type MonitoringSource struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	URL          string `json:"url"`
	CredentialID string `json:"credential_id,omitempty"`
	SkipVerify   bool   `json:"skip_verify"`
	Enabled      bool   `json:"enabled"`
	SyncMinutes  int    `json:"sync_minutes"`
	// Prometheus: the instant query that lists targets and the label that names the host.
	// Grafana: the label of alert instances that names the host (empty: instance, host…).
	// Graylog: the search query that limits the messages hosts are read from (empty: all) and
	// the message field that names the host (empty: source).
	Query     string `json:"query,omitempty"`
	HostLabel string `json:"host_label,omitempty"`
	// Grafana and Graylog: read the firing alerts through the API every PollSeconds and hand
	// them to the connector as if the contact point (HTTP notification) had sent them. Alerts
	// that stop firing are resolved.
	PollAlerts  bool `json:"poll_alerts,omitempty"`
	PollSeconds int  `json:"poll_seconds,omitempty"`
	// QuietMinutes (Graylog): an alert is resolved once its event definition gave no event for
	// its key for this long (0: 15 minutes).
	QuietMinutes int `json:"quiet_minutes,omitempty"`
	// Polled are the alerts the last poll found firing, by fingerprint: an alert missing from
	// the next poll is resolved.
	Polled map[string]PolledAlert `json:"-"`
	// ConnectorID is the connector that receives the alerts of this system («Приём алертов»).
	// A Prometheus system also serves RED and USE rules as a metric source (store.Data.MetricSource).
	ConnectorID string `json:"connector_id,omitempty"`

	Hosts []MonitoringHost `json:"-"`
	// Links are hosts linked by hand: host key to configuration item ID, or HostNoCI.
	Links map[string]string `json:"-"`
	Sync  MonitoringSync    `json:"sync"`

	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MonitoringHost is a host as the monitoring system knows it.
type MonitoringHost struct {
	// Key identifies the host within its source: the Zabbix host ID or the Prometheus host name.
	Key string `json:"key"`
	// Host is the technical name, Name the visible one.
	Host   string   `json:"host"`
	Name   string   `json:"name"`
	IPs    []string `json:"ips"`
	DNS    []string `json:"dns"`
	Groups []string `json:"groups"`
	// Endpoints are the Prometheus instances of the host.
	Endpoints []string `json:"endpoints,omitempty"`
	State     string   `json:"state"`
	URL       string   `json:"url,omitempty"`
}

// Normalize replaces missing lists with empty ones. The snapshot (gob) does not keep empty
// lists, so a host read back after a restart has nil where it had [], and the API must still
// send [] for them.
func (h *MonitoringHost) Normalize() {
	if h.IPs == nil {
		h.IPs = []string{}
	}
	if h.DNS == nil {
		h.DNS = []string{}
	}
	if h.Groups == nil {
		h.Groups = []string{}
	}
}

// NormalizeHosts normalizes every host of the source.
func (s *MonitoringSource) NormalizeHosts() {
	for i := range s.Hosts {
		s.Hosts[i].Normalize()
	}
}

// MonitoringSync is the last reading of the host list of a source.
type MonitoringSync struct {
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
	Actor      string    `json:"actor"`
	Hosts      int       `json:"hosts"`
	Version    string    `json:"version,omitempty"`
}

// MonitoringPoll is the last poll of the alerts of a source; it is kept in memory only.
type MonitoringPoll struct {
	At     time.Time `json:"at"`
	OK     bool      `json:"ok"`
	Error  string    `json:"error,omitempty"`
	Firing int       `json:"firing"`
	// Sent counts the alerts handed to the connector by the last poll: new, changed and resolved.
	Sent int `json:"sent"`
}

// PolledAlert is what a poll remembers of a firing alert to resolve it once it is gone.
type PolledAlert struct {
	Labels      map[string]string
	Annotations map[string]string
	StartsAt    time.Time
	RuleUID     string
	Value       string
}
