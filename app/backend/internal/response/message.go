package response

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/notify"
)

// view is what the messages of an incident show: its words and links in the default language.
type view struct {
	locale string
	tz     *time.Location
	a      alert.Alert
	st     *State
	open   string
	gf     string
}

func (v view) t(key string, kv ...string) string { return tr(v.locale, key, kv...) }

func (v view) when(t *time.Time) string {
	if t == nil || t.IsZero() {
		return v.t("none")
	}
	return t.In(v.tz).Format("02.01.2006 15:04 MST")
}

func (v view) priority() string { return v.t(v.st.Priority) }

func (v view) services() []string {
	var out []string
	for _, s := range v.st.Assessment.Services {
		if s.Direct {
			out = append(out, s.Name)
		} else {
			out = append(out, v.t("dep", "name", s.Name, "via", s.Via))
		}
	}
	return out
}

func (v view) statusLine() string {
	switch v.a.Status {
	case alert.StatusAcknowledged:
		return v.t("status.ack", "who", v.a.AckedBy)
	default:
		return v.t(v.a.Status)
	}
}

// facts are the lines of the incident every message carries.
func (v view) facts() [][2]string {
	as := v.st.Assessment
	rows := [][2]string{
		{v.t("priority"), v.t("priority.line", "priority", v.priority(), "impact", v.t(as.Impact), "urgency", v.t(as.Urgency))},
	}
	if s := v.services(); len(s) > 0 {
		rows = append(rows, [2]string{v.t("services"), strings.Join(s, ", ")})
	}
	rows = append(rows, [2]string{v.t("ci"), v.a.CIName})
	if v.a.Route.Team != nil {
		rows = append(rows, [2]string{v.t("team"), v.a.Route.Team.Name})
	}
	opened := v.a.OpenedAt
	rows = append(rows, [2]string{v.t("opened"), v.when(&opened)}, [2]string{v.t("status"), v.statusLine()})
	return rows
}

// links of the incident: the chat, the call, the Jira task, Umbrella and Grafana.
func (v view) links() []notify.DirectLink {
	var out []notify.DirectLink
	if r := v.st.Room; r != nil && r.URL != "" {
		out = append(out, notify.DirectLink{Title: v.t("room"), URL: r.URL})
	}
	if b := v.st.Bridge; b != nil && b.URL != "" {
		out = append(out, notify.DirectLink{Title: v.t("bridge"), URL: b.URL})
	}
	if j := v.st.Task; j != nil && j.URL != "" {
		out = append(out, notify.DirectLink{Title: v.t("jira") + " " + j.Key, URL: j.URL})
	}
	if v.open != "" {
		out = append(out, notify.DirectLink{Title: v.t("umbrella"), URL: v.open})
	}
	if v.gf != "" {
		out = append(out, notify.DirectLink{Title: v.t("grafana"), URL: v.gf})
	}
	return out
}

// stepMessage is the message of an escalation step: level 1 is the first notification.
func (v view) stepMessage(level int) notify.Direct {
	subj, head := v.t("subj.step1", "priority", v.priority(), "id", v.a.ID, "title", v.a.Title), v.t("head.step1")
	if level > 1 {
		lv := fmt.Sprint(level)
		subj, head = v.t("subj.stepN", "level", lv, "priority", v.priority(), "id", v.a.ID, "title", v.a.Title), v.t("head.stepN", "level", lv)
	}
	var text, html strings.Builder
	text.WriteString(head + "\n\n" + v.a.ID + " · " + v.a.Title + "\n\n")
	html.WriteString("🚨 <b>" + escape(head) + "</b>\n\n<b>" + escape(v.a.ID+" · "+v.a.Title) + "</b>\n")
	for _, f := range v.facts() {
		text.WriteString(f[0] + ": " + f[1] + "\n")
		html.WriteString(escape(f[0]) + ": " + escape(f[1]) + "\n")
	}
	links := v.links()
	if len(links) > 0 {
		text.WriteString("\n")
		html.WriteString("\n")
	}
	var hl []string
	for _, l := range links {
		text.WriteString(l.Title + ": " + l.URL + "\n")
		hl = append(hl, `<a href="`+escape(l.URL)+`">`+escape(l.Title)+"</a>")
	}
	html.WriteString(strings.Join(hl, " · "))
	return notify.Direct{Subject: subj, Text: text.String(), HTML: html.String(), Links: links, AckID: v.a.ID, AckTitle: v.t("ack")}
}

// roomDoc is the first message of the war room: everything known about the incident.
func (v view) roomDoc(people []string) Doc {
	as := v.st.Assessment
	d := Doc{P("🚨 " + v.a.ID + " · " + v.a.Title), P(v.t("room.purpose")), H(v.t("room.summary"))}
	var facts []string
	for _, f := range v.facts() {
		facts = append(facts, f[0]+": "+f[1])
	}
	if v.a.Signal != "" && v.a.Signal != v.a.Title {
		facts = append(facts, "Signal: "+v.a.Signal+" ("+strings.ToUpper(v.a.Method)+")")
	}
	d = append(d, UL(facts...), H(v.t("room.impact")))
	var svc []string
	for _, s := range as.Services {
		line := s.Name + " — " + v.t("crit."+s.Criticality)
		if !s.Direct {
			line += " · " + v.t("dep", "name", "", "via", s.Via)
		}
		svc = append(svc, line)
	}
	if len(svc) == 0 {
		svc = []string{v.t("none")}
	}
	d = append(d, UL(svc...), H(v.t("room.reasons")))
	var why []string
	for _, r := range as.Reasons {
		why = append(why, reasonText(v.locale, r))
	}
	d = append(d, UL(why...))
	var src []string
	for _, s := range v.a.Sources {
		line := s.ConnectorID + ": " + s.Title + " · " + v.t(s.Severity)
		if s.Value != "" {
			line += " · " + s.Value
		}
		src = append(src, line)
	}
	if len(src) > 0 {
		d = append(d, H(v.t("room.sources")), UL(src...))
	}
	if len(people) > 0 {
		d = append(d, H(v.t("room.people")), UL(people...))
	}
	if links := v.links(); len(links) > 0 {
		d = append(d, H(v.t("room.links")))
		for _, l := range links {
			if l.Title != v.t("room") {
				d = append(d, Link(l.Title, l.URL))
			}
		}
	}
	d = append(d, H(v.t("room.plan")), UL(v.t("room.plan.1"), v.t("room.plan.2"), v.t("room.plan.3"), v.t("room.plan.4")))
	return d
}

// taskDoc is the description of the Jira task of an incident.
func (v view) taskDoc() Doc {
	d := Doc{P(v.t("task.goal")), H(v.t("room.summary"))}
	var facts []string
	for _, f := range v.facts() {
		facts = append(facts, f[0]+": "+f[1])
	}
	d = append(d, UL(facts...), H(v.t("room.reasons")))
	var why []string
	for _, r := range v.st.Assessment.Reasons {
		why = append(why, reasonText(v.locale, r))
	}
	d = append(d, UL(why...))
	if links := v.links(); len(links) > 0 {
		d = append(d, H(v.t("room.links")))
		for _, l := range links {
			d = append(d, Link(l.Title, l.URL))
		}
	}
	return d
}

func dur(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	d = d.Round(time.Minute)
	h, m := int(d.Hours()), int(d.Minutes())%60
	if h > 0 {
		return fmt.Sprintf("%dh %02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// postmortemDoc is the postmortem template: the facts, the timeline and the sections the team fills in.
func (v view) postmortemDoc(entries []alert.Entry) Doc {
	a := v.a
	d := Doc{P(v.t("pm.goal")), H(v.t("room.summary"))}
	var facts []string
	for _, f := range v.facts() {
		facts = append(facts, f[0]+": "+f[1])
	}
	d = append(d, UL(facts...), H(v.t("room.impact")))
	var why []string
	for _, r := range v.st.Assessment.Reasons {
		why = append(why, reasonText(v.locale, r))
	}
	d = append(d, UL(why...))
	opened := a.OpenedAt
	tta, ttr := time.Duration(0), time.Duration(0)
	if a.AckedAt != nil {
		tta = a.AckedAt.Sub(opened)
	}
	if a.ResolvedAt != nil {
		ttr = a.ResolvedAt.Sub(opened)
	}
	d = append(d, H(v.t("pm.detect")), P(v.t("pm.detect.text", "opened", v.when(&opened), "acked", v.when(a.AckedAt), "resolved", v.when(a.ResolvedAt), "tta", dur(tta), "ttr", dur(ttr))))
	var tl []string
	for _, e := range entries {
		if line := v.entry(e); line != "" {
			tl = append(tl, line)
		}
		if len(tl) >= 60 {
			break
		}
	}
	if len(tl) > 0 {
		d = append(d, H(v.t("pm.timeline")), UL(tl...))
	}
	d = append(d, H(v.t("pm.cause")), P(v.t("pm.cause.todo")), H(v.t("pm.actions")), P(v.t("pm.actions.todo")), H(v.t("pm.lessons")), P(v.t("none")))
	if links := v.links(); len(links) > 0 {
		d = append(d, H(v.t("room.links")))
		for _, l := range links {
			d = append(d, Link(l.Title, l.URL))
		}
	}
	return d
}

// entry is a line of the incident timeline for the postmortem; lines it does not know are left out.
func (v view) entry(e alert.Entry) string {
	at := e.At
	when := at.In(v.tz).Format("15:04:05")
	by := ""
	if e.Author != "" {
		by = " (" + e.Author + ")"
	}
	switch e.Code {
	case "opened", "acknowledged", "resolved", "reopened":
		return when + " — " + v.t(map[string]string{"opened": "open", "acknowledged": "acknowledged", "resolved": "resolved", "reopened": "open"}[e.Code]) + by
	case "severity_raised":
		return when + " — " + v.t(e.Args["from"]) + " → " + v.t(e.Args["to"])
	case "comment":
		return when + " — 💬 " + e.Args["text"] + by
	case "response_priority_raised":
		return when + " — " + v.t("upd.raised", "from", v.t(e.Args["from"]), "to", v.t(e.Args["to"]))
	case "response_step":
		return when + " — " + v.t("upd.step", "level", e.Args["level"], "people", cmp.Or(e.Args["people"], v.t("none")))
	case "event":
		if e.Args["status"] == alert.SourceResolved {
			return when + " — ✓ " + e.Args["title"]
		}
		return when + " — " + e.Args["title"] + " · " + v.t(e.Args["severity"])
	}
	return ""
}
