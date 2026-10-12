package notify

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
)

// The messages are text/template files: <message>.<part>.tmpl, where the part is the e-mail
// subject, the plain text of the e-mail or the HTML of Telegram. The words of each language are
// in words.<locale>.json; templates take them with t. A template file ends with one newline that
// is not part of the template.
//
//go:embed templates
var templateFiles embed.FS

// Messages and their parts an administrator can replace (model.Notify.Templates is keyed by
// "<message>.<part>").
var (
	templateMessages = []string{"fallback", "followup", "test"}
	templateParts    = []string{"subject", "text", "html"}
)

// TemplateNames are the templates an administrator can replace, in order.
func TemplateNames() []string {
	var out []string
	for _, m := range templateMessages {
		for _, p := range templateParts {
			out = append(out, m+"."+p)
		}
	}
	return out
}

var (
	words        = map[string]map[string]string{}
	builtinFiles = map[string]string{} // template name -> source
	builtinSets  = map[string]*template.Template{}
)

func init() {
	entries, err := fs.ReadDir(templateFiles, "templates")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		raw, err := fs.ReadFile(templateFiles, "templates/"+e.Name())
		if err != nil {
			panic(err)
		}
		switch name := e.Name(); {
		case strings.HasPrefix(name, "words.") && strings.HasSuffix(name, ".json"):
			w := map[string]string{}
			if err := json.Unmarshal(raw, &w); err != nil {
				panic(fmt.Sprintf("notify: %s: %v", name, err))
			}
			words[strings.TrimSuffix(strings.TrimPrefix(name, "words."), ".json")] = w
		case strings.HasSuffix(name, ".tmpl"):
			builtinFiles[strings.TrimSuffix(name, ".tmpl")] = strings.TrimSuffix(string(raw), "\n")
		}
	}
	for loc := range words {
		builtinSets[loc] = template.Must(parseSet(loc, nil))
	}
}

// lang is the words of a language: English or Russian (the default).
func lang(locale string) map[string]string {
	if locale == "en" {
		return words["en"]
	}
	return words["ru"]
}

func langKey(locale string) string {
	if locale == "en" {
		return "en"
	}
	return "ru"
}

// Word is a word of the interface outside messages (the acknowledgement page) in a language.
func Word(locale, key string) string { return lang(locale)[key] }

// DefaultTemplates are the built-in templates by name.
func DefaultTemplates() map[string]string {
	out := map[string]string{}
	for _, n := range TemplateNames() {
		out[n] = builtinFiles[n]
	}
	return out
}

// parseSet parses the built-in templates of a language with the overrides on top.
func parseSet(locale string, overrides map[string]string) (*template.Template, error) {
	w := lang(locale)
	var root *template.Template
	root = template.New("").Funcs(template.FuncMap{
		// t is a word; pairs of arguments fill its {placeholders}.
		"t": wordFunc(w, false),
		// include is replaced for each run by execute, which bounds how deep it nests.
		"include": func(string, any) (string, error) { return "", errors.New("include outside execute") },
		"join":    strings.Join,
		"upper":   strings.ToUpper,
		"lower":   strings.ToLower,
		// html escapes as the messages always did (html.EscapeString).
		"html": func(v any) string { return html.EscapeString(fmt.Sprint(v)) },
	})
	if _, err := root.Parse(builtinFiles["partials"]); err != nil {
		return nil, err
	}
	for _, n := range TemplateNames() {
		if _, err := root.New(n).Parse(builtinFiles[n]); err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
	}
	names := make([]string, 0, len(overrides))
	for n := range overrides {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		if !slices.Contains(TemplateNames(), n) {
			return nil, &TemplateError{Name: n, Err: errors.New("unknown template")}
		}
		if _, err := root.New(n).Parse(overrides[n]); err != nil {
			return nil, &TemplateError{Name: n, Err: err}
		}
	}
	return root, nil
}

// TemplateError tells which template is wrong.
type TemplateError struct {
	Name string
	Err  error
}

func (e *TemplateError) Error() string { return e.Name + ": " + e.Err.Error() }
func (e *TemplateError) Unwrap() error { return e.Err }

// Message is what the templates of a message see.
type Message struct {
	ID, Title string
	// Severity: a name of model.Severities; t turns it into its priority and word ("P1 · critical").
	Severity string
	CI       string
	Signal   string
	Services []string
	Team     *alert.Ref
	// Opened and the other times are formatted in the time zone of the recipient.
	Opened string
	// PDState: pending, failed, skipped, off…; PDErrorCode is translated with t "err.<code>".
	PDState, PDErrorCode, PDError string
	// PDMode is the role of PagerDuty for the severity (model.PDMode*); empty while it is off.
	PDMode string
	// Links (empty when the public address is not set, Ack also when links are off).
	Ack, Open, Grafana string
	// Flow is the traffic of the incident in Umbrella Flow (empty while its address is not set).
	Flow string
	// Follow-up: Event is acknowledged or resolved.
	Event                  string
	AckedBy, AckedAt       string
	ResolvedBy, ResolvedAt string
}

// messages renders the messages of a language with the administrator's templates; a template
// that fails falls back to the built-in one.
type messages struct {
	locale  string
	builtin *template.Template
	custom  *template.Template
}

func newMessages(locale string, overrides map[string]string) messages {
	m := messages{locale: langKey(locale), builtin: builtinSets[langKey(locale)]}
	if len(overrides) > 0 {
		set, err := parseSet(locale, overrides)
		if err != nil {
			slog.Warn("notification templates not used", "err", err)
		}
		m.custom = set
	}
	return m
}

// wordFunc is the t of the templates: a word of w whose {placeholders} the pairs of arguments
// fill. With escape the word is HTML-escaped first; the arguments are escaped already then.
func wordFunc(w map[string]string, escape bool) func(key string, kv ...string) string {
	return func(key string, kv ...string) string {
		v := w[key]
		if escape {
			v = html.EscapeString(v)
		}
		for i := 0; i+1 < len(kv); i += 2 {
			v = strings.ReplaceAll(v, "{"+kv[i]+"}", kv[i+1])
		}
		return v
	}
}

// isHTML tells the parts sent as Telegram HTML: <message>.html.
func isHTML(name string) bool { return strings.HasSuffix(name, ".html") }

// escaped is a copy of data whose strings (in structs, pointers, slices and maps) are
// HTML-escaped, so a template sees only text that is safe in Telegram HTML.
func escaped(data any) any {
	if data == nil {
		return nil
	}
	return escapeValue(reflect.ValueOf(data)).Interface()
}

func escapeValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.String:
		return reflect.ValueOf(html.EscapeString(v.String())).Convert(v.Type())
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		p := reflect.New(v.Type().Elem())
		p.Elem().Set(escapeValue(v.Elem()))
		return p
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(escapeValue(v.Elem()))
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := range v.NumField() {
			if out.Field(i).CanSet() {
				out.Field(i).Set(escapeValue(v.Field(i)))
			}
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			out.Index(i).Set(escapeValue(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		for it := v.MapRange(); it.Next(); {
			out.SetMapIndex(it.Key(), escapeValue(it.Value()))
		}
		return out
	}
	return v
}

// maxInclude bounds how deep include nests: a template that includes itself fails instead of
// overflowing the stack.
const maxInclude = 16

// execute runs a template of a set in a language. Each run works on its own clone, so the
// include depth is counted per run even while messages render concurrently.
//
// A Telegram HTML part (<message>.html) sees escaped data and escaped words, and html returns
// its argument as is: a template, an administrator's one too, cannot put unescaped text into
// the message whether it calls html or not, and the built-in ones read as before.
func execute(set *template.Template, locale string, w io.Writer, name string, data any) error {
	run, err := set.Clone()
	if err != nil {
		return err
	}
	if isHTML(name) {
		data = escaped(data)
		run.Funcs(template.FuncMap{
			"t":    wordFunc(lang(locale), true),
			"html": func(v any) string { return fmt.Sprint(v) },
		})
	}
	depth := 0
	run.Funcs(template.FuncMap{"include": func(name string, data any) (string, error) {
		if depth >= maxInclude {
			return "", fmt.Errorf("include %q: nested more than %d deep", name, maxInclude)
		}
		depth++
		defer func() { depth-- }()
		var b strings.Builder
		err := run.ExecuteTemplate(&b, name, data)
		return b.String(), err
	}})
	return run.ExecuteTemplate(w, name, data)
}

func (m messages) exec(name string, data any) string {
	if m.custom != nil {
		var b strings.Builder
		err := execute(m.custom, m.locale, &b, name, data)
		if err == nil {
			return b.String()
		}
		slog.Warn("notification template failed: the built-in one is used", "template", name, "err", err)
	}
	var b strings.Builder
	if err := execute(m.builtin, m.locale, &b, name, data); err != nil {
		slog.Error("built-in notification template failed", "template", name, "err", err)
	}
	return b.String()
}

func (m messages) render(message string, data any) composed {
	return composed{subject: strings.TrimSpace(m.exec(message+".subject", data)), text: m.exec(message+".text", data), html: m.exec(message+".html", data)}
}

const timeLayout = "02.01.2006 15:04 MST"

func formatTime(t *time.Time, tz *time.Location) string {
	if t == nil {
		return ""
	}
	return t.In(tz).Format(timeLayout)
}

// CleanTemplates normalizes overrides as they come from a form: line ends become \n, and empty
// ones and those equal to the built-in template are dropped; nil when none is left. The rest
// are checked with CheckTemplates.
func CleanTemplates(in map[string]string) (map[string]string, error) {
	var out map[string]string
	for n, v := range in {
		v = strings.ReplaceAll(v, "\r\n", "\n")
		if strings.TrimSpace(v) == "" || v == builtinFiles[n] || v == builtinFiles[n]+"\n" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[n] = v
	}
	if err := CheckTemplates(out); err != nil {
		return nil, err
	}
	return out, nil
}

// CheckTemplates parses the overrides and renders them with a sample incident in every
// language, so a mistake is found when they are saved rather than when an incident comes.
func CheckTemplates(overrides map[string]string) error {
	for loc := range words {
		set, err := parseSet(loc, overrides)
		if err != nil {
			return err
		}
		for _, sample := range samples() {
			for _, n := range TemplateNames() {
				if _, ok := overrides[n]; !ok || !strings.HasPrefix(n, sample.message+".") {
					continue
				}
				if err := execute(set, loc, &strings.Builder{}, n, sample.data); err != nil {
					return &TemplateError{Name: n, Err: err}
				}
			}
		}
	}
	return nil
}

type sample struct {
	message string
	data    Message
}

// samples are the messages of a sample incident: every line of backup notification, both
// follow-ups and the test message.
func samples() []sample {
	at := time.Date(2026, 10, 5, 9, 5, 0, 0, time.UTC)
	base := Message{ID: "INC-1042", Title: "HTTP 5xx on app-01", Severity: "critical", CI: "app-01.corp.local", Signal: "http.errors",
		Services: []string{"Payments"}, Team: &alert.Ref{ID: "team", Name: "Payments SRE"}, Opened: formatTime(&at, time.UTC),
		PDState: alert.PDFailed, PDErrorCode: "unreachable", PDError: "PagerDuty is not reachable",
		Ack: "https://umbrella.example.com/ack/sample", Open: "https://umbrella.example.com/incidents?id=INC-1042",
		Grafana: "https://umbrella.example.com/go/incidents/INC-1042/grafana"}
	ack, res := base, base
	ack.Event, ack.AckedBy, ack.AckedAt = alert.StatusAcknowledged, "ivanov", formatTime(&at, time.UTC)
	res.Event, res.ResolvedAt = alert.StatusResolved, formatTime(&at, time.UTC)
	return []sample{{"fallback", base}, {"followup", ack}, {"followup", res}, {"test", base}}
}

// Preview is a message rendered for the settings page.
type Preview struct {
	Name    string `json:"name"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html"`
	// Error: an override that fails; the built-in template is shown instead.
	Error string `json:"error,omitempty"`
}

// PreviewTemplates renders the sample incident with the overrides in a language.
func PreviewTemplates(locale string, overrides map[string]string) ([]Preview, error) {
	if _, err := parseSet(locale, overrides); err != nil {
		return nil, err
	}
	m := newMessages(locale, overrides)
	var out []Preview
	for _, s := range samples() {
		name := s.message
		if s.data.Event != "" {
			name += "." + s.data.Event
		}
		c := m.render(s.message, s.data)
		p := Preview{Name: name, Subject: c.subject, Text: c.text, HTML: c.html}
		for _, part := range templateParts {
			n := s.message + "." + part
			if _, ok := overrides[n]; ok {
				if err := execute(m.custom, m.locale, &strings.Builder{}, n, s.data); err != nil && p.Error == "" {
					p.Error = (&TemplateError{Name: n, Err: err}).Error()
				}
			}
		}
		out = append(out, p)
	}
	return out, nil
}
