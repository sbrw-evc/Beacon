package flow

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/textx"
)

// The severities and methods of an event are those of the model; the names stay here so the
// nodes read as before.
const (
	SeverityCritical = model.SeverityCritical
	SeverityError    = model.SeverityError
	SeverityWarning  = model.SeverityWarning
	SeverityLow      = model.SeverityLow
	SeverityInfo     = model.SeverityInfo

	StatusFiring   = "firing"
	StatusResolved = "resolved"

	MethodRED   = model.MethodRED
	MethodUSE   = model.MethodUSE
	MethodOther = model.MethodOther

	maxEventField = 1000
	maxLabels     = 64
)

var Severities = model.SeverityNames()

// Event is the normalized output of a connector: the fixed target schema of map.event.
type Event struct {
	Title      string `json:"title"`
	CI         string `json:"ci"`
	Signal     string `json:"signal"`
	Method     string `json:"method"`
	Severity   string `json:"severity"`
	Status     string `json:"status"`
	ExternalID string `json:"external_id"`
	Value      string `json:"value"`
	// Description is the full text of the alert (a stack trace, the rule's description): the
	// incident shows its start and expands to the whole of it.
	Description string `json:"description,omitempty"`
	// Fields are named values the connector shows in the incident, in its order.
	Fields []Field           `json:"fields,omitempty"`
	Labels map[string]string `json:"labels"`
	// Key identifies a delivery of the same alert: repeated deliveries update one event.
	Key string `json:"key"`
}

// severityWords maps the words sources use for a level (lower case) to the level; the
// priorities P1..P5 and sev1..sev5 are levels too.
var severityWords = map[string]string{
	"critical": SeverityCritical, "crit": SeverityCritical, "disaster": SeverityCritical, "fatal": SeverityCritical,
	"emergency": SeverityCritical, "p1": SeverityCritical, "sev1": SeverityCritical,
	"error": SeverityError, "err": SeverityError, "high": SeverityError, "major": SeverityError, "p2": SeverityError, "sev2": SeverityError,
	"warning": SeverityWarning, "warn": SeverityWarning, "average": SeverityWarning, "medium": SeverityWarning, "minor": SeverityWarning,
	"p3": SeverityWarning, "sev3": SeverityWarning,
	"low": SeverityLow, "p4": SeverityLow, "sev4": SeverityLow,
	"info": SeverityInfo, "information": SeverityInfo, "informational": SeverityInfo, "notice": SeverityInfo,
	"not classified": SeverityInfo, "none": SeverityInfo, "debug": SeverityInfo, "p5": SeverityInfo, "sev5": SeverityInfo,
}

// Field is a named value an incident shows.
type Field = model.Field

func NormalizeSeverity(v string) (string, bool) {
	s, ok := severityWords[strings.ToLower(strings.TrimSpace(v))]
	return s, ok
}

var statusWords = map[string]string{
	"": StatusFiring, "firing": StatusFiring, "fire": StatusFiring, "problem": StatusFiring, "alerting": StatusFiring, "alert": StatusFiring,
	"active": StatusFiring, "triggered": StatusFiring, "trigger": StatusFiring, "open": StatusFiring, "critical": StatusFiring, "1": StatusFiring,
	"resolved": StatusResolved, "resolve": StatusResolved, "ok": StatusResolved, "closed": StatusResolved, "recovery": StatusResolved,
	"recovered": StatusResolved, "inactive": StatusResolved, "normal": StatusResolved, "cleared": StatusResolved, "0": StatusResolved,
}

func NormalizeStatus(v string) (string, bool) {
	s, ok := statusWords[strings.ToLower(strings.TrimSpace(v))]
	return s, ok
}

// EventFromData reads an event from a record produced by map.event (and possibly changed by
// later nodes) and checks it against the target schema.
func EventFromData(d map[string]any) (Event, error) {
	str := func(k string) string { return strings.TrimSpace(Stringify(d[k])) }
	e := Event{
		Title: str("title"), CI: str("ci"), Signal: str("signal"), Method: str("method"),
		Severity: str("severity"), Status: str("status"), ExternalID: str("external_id"), Value: str("value"), Key: str("key"),
	}
	if e.Key == "" {
		return e, errors.New("the record did not pass an “Event mapping” node")
	}
	if e.Title == "" {
		return e, errors.New("title is empty")
	}
	if e.CI == "" {
		return e, errors.New("ci is empty")
	}
	sev, ok := NormalizeSeverity(e.Severity)
	if !ok {
		return e, fmt.Errorf("severity %q is not critical, error, warning, low or info", e.Severity)
	}
	e.Severity = sev
	st, ok := NormalizeStatus(e.Status)
	if !ok {
		return e, fmt.Errorf("status %q is neither firing nor resolved", e.Status)
	}
	e.Status = st
	if e.Method == "" {
		e.Method = MethodOther
	} else if !model.ValidMethod(e.Method) {
		return e, fmt.Errorf("method %q is not red, use or other", e.Method)
	}
	e.Description = strings.TrimSpace(Stringify(d["description"]))
	e.shorten()
	for _, f := range []*string{&e.Title, &e.CI, &e.Signal, &e.ExternalID, &e.Value} {
		*f = textx.Runes(*f, maxEventField)
	}
	e.Description = truncateBytes(e.Description, MaxDescription)
	e.Fields = fieldsOf(d["fields"])
	e.Labels = labelsOf(d["labels"])
	return e, nil
}

// shorten keeps the title and the value to one short line. A source that puts a whole stack
// trace into the title or the value loses none of it: the full text becomes the description
// when the connector set none, or is added after it.
func (e *Event) shorten() {
	var spill []string
	for _, f := range []struct {
		v *string
		n int
	}{{&e.Title, maxTitle}, {&e.Value, maxValue}} {
		full := strings.TrimSpace(*f.v)
		short := truncateText(oneLine(firstLine(full)), f.n)
		if short != oneLine(full) {
			spill = append(spill, full)
		}
		*f.v = short
	}
	for _, s := range spill {
		if strings.Contains(e.Description, s) {
			continue
		}
		if e.Description != "" {
			e.Description += "\n\n"
		}
		e.Description += s
	}
}

func fieldsOf(v any) []Field {
	list, ok := v.([]any)
	if !ok {
		if fs, ok := v.([]Field); ok {
			return fs
		}
		return nil
	}
	var out []Field
	for _, it := range list {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		name := truncateText(oneLine(Stringify(m["name"])), maxFieldName)
		value := truncateText(strings.TrimSpace(Stringify(m["value"])), maxFieldValue)
		if name == "" || value == "" {
			continue
		}
		out = append(out, Field{Name: name, Value: value})
		if len(out) == maxFields {
			break
		}
	}
	return out
}

func labelsOf(v any) map[string]string {
	out := map[string]string{}
	m, ok := v.(map[string]any)
	if !ok {
		return out
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if len(out) == maxLabels {
			break
		}
		if s := Stringify(m[k]); s != "" {
			out[k] = truncate(s, maxEventField)
		}
	}
	return out
}

func eventKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}
