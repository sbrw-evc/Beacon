package ingest

import (
	"strings"
	"unicode/utf8"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
)

// PostgreSQL text and jsonb take neither the NUL character nor invalid UTF-8 (SQLSTATE 22021,
// 22P05), and senders deliver both: a Windows script posting cp1251, a JSON string with \u0000.
// Everything a request turns into text is cleaned before it is written, so such a request is
// stored like any other instead of failing its transaction.

// cleanText drops NUL characters and replaces invalid UTF-8 with U+FFFD.
func cleanText(s string) string {
	if utf8.ValidString(s) && !strings.ContainsRune(s, 0) {
		return s
	}
	return strings.ReplaceAll(strings.ToValidUTF8(s, "�"), "\x00", "")
}

func cleanMap(m map[string]string) map[string]string {
	dirty := false
	for k, v := range m {
		if cleanText(k) != k || cleanText(v) != v {
			dirty = true
			break
		}
	}
	if !dirty {
		return m
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[cleanText(k)] = cleanText(v)
	}
	return out
}

// cleanAny cleans the strings of decoded JSON data (failure records keep the item that failed).
func cleanAny(v any) any {
	switch x := v.(type) {
	case string:
		return cleanText(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[cleanText(k)] = cleanAny(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = cleanAny(e)
		}
		return out
	case map[string]string:
		return cleanMap(x)
	}
	return v
}

func cleanEvent(e *flow.Event) {
	e.Title, e.CI, e.Signal, e.Method = cleanText(e.Title), cleanText(e.CI), cleanText(e.Signal), cleanText(e.Method)
	e.Severity, e.Status, e.ExternalID = cleanText(e.Severity), cleanText(e.Status), cleanText(e.ExternalID)
	e.Value, e.Key, e.Description = cleanText(e.Value), cleanText(e.Key), cleanText(e.Description)
	for i := range e.Fields {
		e.Fields[i].Name, e.Fields[i].Value = cleanText(e.Fields[i].Name), cleanText(e.Fields[i].Value)
	}
	e.Labels = cleanMap(e.Labels)
}

func cleanFailure(f *flow.Failure) {
	f.Node, f.Error, f.Raw = cleanText(f.Node), cleanText(f.Error), cleanText(f.Raw)
	if f.Data != nil {
		f.Data, _ = cleanAny(f.Data).(map[string]any)
	}
}

// rawBody is the body of a request as text for a failure record. A body that is not UTF-8 is
// kept readable (invalid bytes become U+FFFD) and says so in note.
func rawBody(b []byte) (raw, note string) {
	return cleanText(string(b)), bodyNote(b)
}

// bodyNote explains a failure of a request whose body is not UTF-8 (cp1251 and the like).
func bodyNote(b []byte) string {
	if utf8.Valid(b) {
		return ""
	}
	return " (the body is not valid UTF-8; invalid bytes are shown as �)"
}
