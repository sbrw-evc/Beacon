package monitoring

import (
	"cmp"
	"context"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

// The events and log lines a monitoring system keeps about one host, read for the machine of an
// incident. Zabbix answers both; the calls are the same on 5.0 to 8.0.

// HostEvent is a problem a monitoring system raised on a host.
type HostEvent struct {
	ID    string    `json:"id"`
	At    time.Time `json:"at"`
	Title string    `json:"title"`
	// Severity is the Umbrella name of the severity; Level is the system's own word.
	Severity     string     `json:"severity"`
	Level        string     `json:"level"`
	Status       string     `json:"status"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
	Acknowledged bool       `json:"acknowledged,omitempty"`
	Suppressed   bool       `json:"suppressed,omitempty"`
	Tags         []string   `json:"tags,omitempty"`
	URL          string     `json:"url,omitempty"`
}

// HostLogLine is a value of a log item of a host.
type HostLogLine struct {
	At     time.Time         `json:"at"`
	Level  string            `json:"level,omitempty"`
	Text   string            `json:"text"`
	Labels map[string]string `json:"labels,omitempty"`
}

// HostLogs: the lines, newest first; Truncated says the limit was reached.
type HostLogs struct {
	Lines     []HostLogLine
	Truncated bool
	// Items is how many log items the host has.
	Items int
}

// Zabbix severities: names and the Umbrella severity they open, as in the Zabbix preset.
var zabbixLevels = []struct{ name, severity string }{
	{"Not classified", model.SeverityInfo},
	{"Information", model.SeverityInfo},
	{"Warning", model.SeverityLow},
	{"Average", model.SeverityWarning},
	{"High", model.SeverityError},
	{"Disaster", model.SeverityCritical},
}

const (
	maxHostLogItems = 100
	maxLogText      = 4000
)

type zabbixEvent struct {
	EventID      string  `json:"eventid"`
	Clock        flexInt `json:"clock"`
	Name         string  `json:"name"`
	Severity     flexInt `json:"severity"`
	REventID     string  `json:"r_eventid"`
	Acknowledged flexInt `json:"acknowledged"`
	Suppressed   flexInt `json:"suppressed"`
	ObjectID     string  `json:"objectid"`
	Tags         []struct {
		Tag   string `json:"tag"`
		Value string `json:"value"`
	} `json:"tags"`
}

// HostEvents reads the trigger problems of a host that began between from and to, and those
// that began before and were still going on at from, newest first.
func HostEvents(ctx context.Context, src model.MonitoringSource, auth *Auth, host model.MonitoringHost, from, to time.Time, limit int) ([]HostEvent, bool, error) {
	if src.Kind != model.MonitoringZabbix {
		return nil, false, ErrNoQuery
	}
	z, _, closeFn, err := openZabbix(ctx, src, auth)
	if err != nil {
		return nil, false, err
	}
	defer closeFn()
	fields := []string{"eventid", "clock", "name", "severity", "r_eventid", "acknowledged", "suppressed", "objectid"}
	var events []zabbixEvent
	if err := z.call(ctx, "event.get", map[string]any{"output": fields, "selectTags": []string{"tag", "value"},
		"hostids": []string{host.Key}, "source": 0, "object": 0, "value": 1,
		"time_from": from.Unix(), "time_till": to.Unix(),
		"sortfield": []string{"clock", "eventid"}, "sortorder": "DESC", "limit": limit + 1}, &events); err != nil {
		return nil, false, err
	}
	truncated := len(events) > limit
	if truncated {
		events = events[:limit]
	}
	// Problems still open that began before the window.
	var open []zabbixEvent
	if err := z.call(ctx, "problem.get", map[string]any{"output": fields, "selectTags": []string{"tag", "value"},
		"hostids": []string{host.Key}, "source": 0, "object": 0, "time_till": from.Unix(),
		"sortfield": []string{"eventid"}, "sortorder": "DESC", "limit": limit}, &open); err != nil {
		return nil, false, err
	}
	for _, e := range open {
		if !slices.ContainsFunc(events, func(x zabbixEvent) bool { return x.EventID == e.EventID }) {
			events = append(events, e)
		}
	}
	// The time each problem ended.
	var rids []string
	for _, e := range events {
		if e.REventID != "" && e.REventID != "0" {
			rids = append(rids, e.REventID)
		}
	}
	ended := map[string]time.Time{}
	if len(rids) > 0 {
		var rec []zabbixEvent
		if err := z.call(ctx, "event.get", map[string]any{"output": []string{"eventid", "clock"}, "eventids": rids}, &rec); err != nil {
			return nil, false, err
		}
		for _, r := range rec {
			ended[r.EventID] = time.Unix(int64(r.Clock), 0).UTC()
		}
	}
	out := make([]HostEvent, 0, len(events))
	for _, e := range events {
		lv := zabbixLevels[0]
		if int(e.Severity) >= 0 && int(e.Severity) < len(zabbixLevels) {
			lv = zabbixLevels[e.Severity]
		}
		h := HostEvent{ID: e.EventID, At: time.Unix(int64(e.Clock), 0).UTC(), Title: e.Name, Severity: lv.severity, Level: lv.name,
			Status: "firing", Acknowledged: e.Acknowledged == 1, Suppressed: e.Suppressed == 1}
		if e.REventID != "" && e.REventID != "0" {
			h.Status = "resolved"
			if t, ok := ended[e.REventID]; ok {
				h.ResolvedAt = &t
			}
		}
		for _, t := range e.Tags {
			if t.Value != "" {
				h.Tags = append(h.Tags, t.Tag+": "+t.Value)
			} else {
				h.Tags = append(h.Tags, t.Tag)
			}
		}
		if e.ObjectID != "" {
			h.URL = z.web + "/tr_events.php?triggerid=" + url.QueryEscape(e.ObjectID) + "&eventid=" + url.QueryEscape(e.EventID)
		}
		out = append(out, h)
	}
	slices.SortStableFunc(out, func(a, b HostEvent) int {
		return cmp.Or(b.At.Compare(a.At), strings.Compare(b.ID, a.ID))
	})
	return out, truncated, nil
}

type zabbixLogItem struct {
	ItemID    string  `json:"itemid"`
	Name      string  `json:"name"`
	Key       string  `json:"key_"`
	ValueType flexInt `json:"value_type"`
}

type zabbixLogValue struct {
	ItemID     string  `json:"itemid"`
	Clock      flexInt `json:"clock"`
	NS         flexInt `json:"ns"`
	Value      string  `json:"value"`
	Source     string  `json:"source"`
	Severity   flexInt `json:"severity"`
	LogEventID flexInt `json:"logeventid"`
}

// logKey: an item that reads a log file or the Windows event log, whatever its type of
// information.
func logKey(key string) bool {
	for _, p := range []string{"log[", "logrt[", "eventlog[", "log.", "logrt.", "eventlog."} {
		if strings.HasPrefix(key, p) {
			return !strings.Contains(key[:strings.IndexByte(key+"[", '[')], ".count")
		}
	}
	return false
}

// Windows event log levels a log item keeps in severity.
var eventLogLevels = map[int]string{1: "information", 2: "warning", 4: "error", 7: "failure audit", 8: "success audit", 9: "critical", 10: "verbose"}

// textLevel finds the level a line names, such as ERROR or level=warn.
var textLevel = regexp.MustCompile(`\b(EMERG|ALERT|CRIT(?:ICAL)?|FATAL|PANIC|ERR(?:OR)?|WARN(?:ING)?|NOTICE|INFO|DEBUG|TRACE)\b|\blevel=["']?([a-zA-Z]+)`)

func levelOf(text string) string {
	if len(text) > 200 {
		text = text[:200]
	}
	m := textLevel.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return strings.ToLower(m[1] + m[2])
}

// HostLogLines reads the values of the log items of a host between from and to, newest
// first: items of the log type and items that read a log (log[], logrt[], eventlog[]) as text.
// Text keeps the lines that contain it.
func HostLogLines(ctx context.Context, src model.MonitoringSource, auth *Auth, host model.MonitoringHost, from, to time.Time, limit int, text string) (HostLogs, error) {
	if src.Kind != model.MonitoringZabbix {
		return HostLogs{}, ErrNoQuery
	}
	z, _, closeFn, err := openZabbix(ctx, src, auth)
	if err != nil {
		return HostLogs{}, err
	}
	defer closeFn()
	var items []zabbixLogItem
	if err := z.call(ctx, "item.get", map[string]any{"output": []string{"itemid", "name", "key_", "value_type"}, "hostids": []string{host.Key},
		"filter": map[string]any{"value_type": []int{1, 2, 4}}, "sortfield": "name"}, &items); err != nil {
		return HostLogs{}, err
	}
	byID := map[string]zabbixLogItem{}
	ids := map[int][]string{}
	for _, it := range items {
		if it.ValueType != 2 && !logKey(it.Key) {
			continue
		}
		if len(byID) >= maxHostLogItems {
			break
		}
		byID[it.ItemID] = it
		ids[int(it.ValueType)] = append(ids[int(it.ValueType)], it.ItemID)
	}
	out := HostLogs{Lines: []HostLogLine{}, Items: len(byID)}
	if len(byID) == 0 {
		return out, nil
	}
	for _, vt := range []int{2, 4, 1} {
		if len(ids[vt]) == 0 {
			continue
		}
		p := map[string]any{"output": "extend", "history": vt, "itemids": ids[vt], "time_from": from.Unix(), "time_till": to.Unix(),
			"sortfield": "clock", "sortorder": "DESC", "limit": limit + 1}
		if text != "" {
			p["search"] = map[string]any{"value": text}
		}
		var vals []zabbixLogValue
		if err := z.call(ctx, "history.get", p, &vals); err != nil {
			return HostLogs{}, err
		}
		if len(vals) > limit {
			out.Truncated = true
		}
		for _, v := range vals {
			it := byID[v.ItemID]
			line := HostLogLine{At: time.Unix(int64(v.Clock), int64(v.NS)).UTC(), Text: strings.TrimRight(v.Value, "\r\n"),
				Labels: map[string]string{"item": it.Name, "key": it.Key}}
			if len(line.Text) > maxLogText {
				line.Text = line.Text[:maxLogText] + "…"
			}
			if v.Source != "" {
				line.Labels["source"] = v.Source
			}
			if v.LogEventID != 0 {
				line.Labels["event_id"] = strconv.Itoa(int(v.LogEventID))
			}
			if strings.HasPrefix(it.Key, "eventlog") {
				line.Level = eventLogLevels[int(v.Severity)]
			} else {
				line.Level = levelOf(line.Text)
			}
			out.Lines = append(out.Lines, line)
		}
	}
	slices.SortStableFunc(out.Lines, func(a, b HostLogLine) int { return b.At.Compare(a.At) })
	if len(out.Lines) > limit {
		out.Lines, out.Truncated = out.Lines[:limit], true
	}
	return out, nil
}
