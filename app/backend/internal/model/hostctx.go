package model

import "time"

// The incident card shows the machine of the incident around the time it happened: graphs from
// the monitoring systems that know the machine, log lines from log sources and the events of
// Umbrella itself.

// Kinds of log sources.
const (
	LogLoki       = "loki"
	LogOpenSearch = "opensearch"
	LogGraylog    = "graylog"
)

var LogKinds = []string{LogLoki, LogOpenSearch, LogGraylog}

// LogSource is a log store the lines of a machine are read from: a Loki-compatible server
// (LogQL), an OpenSearch- or Elasticsearch-compatible search API or Graylog.
type LogSource struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	URL          string `json:"url"`
	CredentialID string `json:"credential_id,omitempty"`
	SkipVerify   bool   `json:"skip_verify"`
	Enabled      bool   `json:"enabled"`
	// Query (LogQL) selects the lines of a machine; $host is a regular expression that matches
	// every name of the machine. Graylog: a search query that narrows the lines; $host in it
	// stands for the names of the machine, otherwise the host field must be one of them.
	Query string `json:"query,omitempty"`
	// Index, HostField, MessageField, TimeField and LevelField: where an OpenSearch document keeps
	// the machine name, the line, its time and level. Graylog: Index is the stream IDs, comma
	// separated (empty: every stream), and the time is always its timestamp.
	Index        string `json:"index,omitempty"`
	HostField    string `json:"host_field,omitempty"`
	MessageField string `json:"message_field,omitempty"`
	TimeField    string `json:"time_field,omitempty"`
	LevelField   string `json:"level_field,omitempty"`

	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

// HostPanel is one graph of the machine: a PromQL query for Prometheus-compatible systems
// ($selector selects the series of the machine, $host is its name) and an item key for Zabbix
// (* is any text). A panel without a query for a kind of system is not drawn from it.
type HostPanel struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Unit      string `json:"unit"`
	PromQL    string `json:"promql"`
	ZabbixKey string `json:"zabbix_key"`
}

// HostContext is how the machine of an incident is shown.
type HostContext struct {
	// WindowMinutes is the default time before and after the start of the incident.
	WindowMinutes int `json:"window_minutes"`
	// Panels are the graphs; PanelsSet tells an empty list chosen by somebody from no choice
	// (the snapshot does not keep empty lists).
	Panels    []HostPanel `json:"panels"`
	PanelsSet bool        `json:"-"`
	// LogLimit is how many log lines are read at most from each source.
	LogLimit  int        `json:"log_limit"`
	UpdatedBy string     `json:"updated_by,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

const (
	DefaultHostWindow   = 60
	DefaultHostLogLimit = 300
)

// Selector placeholders of panel queries.
const (
	HostSelector = "$selector"
	HostName     = "$host"
)

// DefaultHostPanels are the graphs of a machine watched by node_exporter or the Zabbix agent
// templates.
func DefaultHostPanels() []HostPanel {
	const fs = `fstype!~"tmpfs|overlay|squashfs|ramfs|devtmpfs",`
	const nic = `device!~"lo|veth.*|docker.*|br-.*|cali.*|flannel.*|cni.*|virbr.*",`
	return []HostPanel{
		{ID: "cpu", Title: "CPU", Unit: "%",
			PromQL:    `100 * (1 - avg by (instance) (rate(node_cpu_seconds_total{mode="idle",` + HostSelector + `}[5m])))`,
			ZabbixKey: "system.cpu.util"},
		{ID: "memory", Title: "Memory", Unit: "%",
			PromQL:    `100 * (1 - node_memory_MemAvailable_bytes{` + HostSelector + `} / node_memory_MemTotal_bytes{` + HostSelector + `})`,
			ZabbixKey: "vm.memory.utilization"},
		{ID: "load", Title: "Load average (1 min)", Unit: "",
			PromQL:    `node_load1{` + HostSelector + `}`,
			ZabbixKey: "system.cpu.load[all,avg1]"},
		{ID: "disk", Title: "Disk space used", Unit: "%",
			PromQL:    `100 * (1 - node_filesystem_avail_bytes{` + fs + HostSelector + `} / node_filesystem_size_bytes{` + fs + HostSelector + `})`,
			ZabbixKey: "vfs.fs.*size[*,pused]"},
		{ID: "net_in", Title: "Network in", Unit: "bps",
			PromQL:    `8 * rate(node_network_receive_bytes_total{` + nic + HostSelector + `}[5m])`,
			ZabbixKey: "net.if.in[*]"},
		{ID: "net_out", Title: "Network out", Unit: "bps",
			PromQL:    `8 * rate(node_network_transmit_bytes_total{` + nic + HostSelector + `}[5m])`,
			ZabbixKey: "net.if.out[*]"},
	}
}

// Effective fills in what was never chosen.
func (h HostContext) Effective() HostContext {
	if h.WindowMinutes <= 0 {
		h.WindowMinutes = DefaultHostWindow
	}
	if h.LogLimit <= 0 {
		h.LogLimit = DefaultHostLogLimit
	}
	if !h.PanelsSet && len(h.Panels) == 0 {
		h.Panels = DefaultHostPanels()
	}
	if h.Panels == nil {
		h.Panels = []HostPanel{}
	}
	return h
}
