// Package presets holds the built-in connector templates. They use the same document format
// as connector export, so a preset is just an import that ships with Umbrella.
package presets

import (
	"bytes"
	"cmp"
	"embed"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
)

//go:embed *.json *.tmpl
var files embed.FS

type Preset struct {
	ID          string        `json:"id"`
	Title       flow.Text     `json:"title"`
	Description flow.Text     `json:"description"`
	Document    flow.Document `json:"document"`
	// Order places the preset in the catalog: lower first, then by the English title.
	Order int `json:"order,omitempty"`
	// Quick makes the preset quick-connectable: one dialog creates the token and the published
	// connector and shows what to set up in the source.
	Quick *Quick `json:"quick,omitempty"`
	// Starter is the graph a connector made without a template starts from; one preset has it.
	Starter *flow.Graph `json:"starter,omitempty"`
}

// Quick is what quick connect needs of a preset. The templates are Go text/template with Vars.
type Quick struct {
	// Order places the preset in the quick connect dialog.
	Order int `json:"order"`
	// Name is the default connector name.
	Name flow.Text `json:"name"`
	// MonitoringKinds: the kinds of monitoring systems whose alert intake the preset can be.
	MonitoringKinds []string `json:"monitoring_kinds,omitempty"`
	// Steps: how many steps the source needs (their text is in the web strings).
	Steps int `json:"steps,omitempty"`
	// Snippet is the configuration to paste into the source; Attachment is a file to import.
	Snippet    *File `json:"snippet,omitempty"`
	Attachment *File `json:"attachment,omitempty"`
	// TestBody is a delivery in the preset's format that the test event sends.
	TestBody string `json:"test_body,omitempty"`

	testBody *template.Template
}

// QuickView is what the web app needs to offer a quick preset; the templates stay here.
type QuickView struct {
	Order           int       `json:"order"`
	Name            flow.Text `json:"name"`
	MonitoringKinds []string  `json:"monitoring_kinds"`
	Steps           int       `json:"steps"`
}

func (q *Quick) View() *QuickView {
	if q == nil {
		return nil
	}
	return &QuickView{Order: q.Order, Name: q.Name, MonitoringKinds: append([]string{}, q.MonitoringKinds...), Steps: q.Steps}
}

// File is a template rendered for a connected source.
type File struct {
	// Kind: yaml, text or shell.
	Kind string `json:"kind,omitempty"`
	// Name is the file name offered for download; empty means copy only.
	Name string `json:"name,omitempty"`
	// Template is the text itself, or TemplateFile names an embedded *.tmpl file with it.
	Template     string `json:"template,omitempty"`
	TemplateFile string `json:"template_file,omitempty"`

	tmpl *template.Template
}

// Vars are what the quick connect templates can use.
type Vars struct {
	URL   string // the ingest address of the connector
	Token string // its bearer token
	Nonce string // the test event ID
	CI    string // the configuration item of the test event
	Title string // the title of the test event
}

var funcs = template.FuncMap{
	// json is a JSON string literal.
	"json": func(v string) string {
		b, _ := json.Marshal(v)
		return string(b)
	},
	// quote is a Go (and YAML double-quoted) string literal.
	"quote": strconv.Quote,
	// yaml is a YAML single-quoted string.
	"yaml": func(v string) string { return "'" + strings.ReplaceAll(v, "'", "''") + "'" },
}

func parse(name, text string) (*template.Template, error) {
	return template.New(name).Funcs(funcs).Option("missingkey=error").Parse(text)
}

func (f *File) compile(name string) error {
	text := f.Template
	if f.TemplateFile != "" {
		b, err := files.ReadFile(f.TemplateFile)
		if err != nil {
			return err
		}
		text = string(b)
	}
	if text == "" {
		return fmt.Errorf("%s: the template is empty", name)
	}
	var err error
	f.tmpl, err = parse(name, text)
	return err
}

// Render fills the template in.
func (f *File) Render(v Vars) (string, error) {
	var b strings.Builder
	if err := f.tmpl.Execute(&b, v); err != nil {
		return "", err
	}
	return b.String(), nil
}

// RenderTestBody is the test delivery; ok is false when the preset has none.
func (q *Quick) RenderTestBody(v Vars) (body []byte, ok bool, err error) {
	if q == nil || q.testBody == nil {
		return nil, false, nil
	}
	var b bytes.Buffer
	if err := q.testBody.Execute(&b, v); err != nil {
		return nil, true, err
	}
	return b.Bytes(), true, nil
}

func (q *Quick) compile(id string) error {
	if q.Snippet != nil {
		if err := q.Snippet.compile(id + ".snippet"); err != nil {
			return err
		}
	}
	if q.Attachment != nil {
		if err := q.Attachment.compile(id + ".attachment"); err != nil {
			return err
		}
	}
	if q.TestBody != "" {
		var err error
		if q.testBody, err = parse(id+".test_body", q.TestBody); err != nil {
			return err
		}
	}
	return nil
}

var all []Preset

func init() {
	entries, err := files.ReadDir(".")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := files.ReadFile(e.Name())
		if err != nil {
			panic(err)
		}
		var p Preset
		if err := json.Unmarshal(b, &p); err != nil {
			panic(fmt.Sprintf("preset %s: %v", e.Name(), err))
		}
		doc, _ := json.Marshal(p.Document)
		if p.Document, err = flow.ParseDocument(doc); err != nil {
			panic(fmt.Sprintf("preset %s: %v", e.Name(), err))
		}
		if p.Quick != nil {
			if err := p.Quick.compile(p.ID); err != nil {
				panic(fmt.Sprintf("preset %s: %v", e.Name(), err))
			}
		}
		all = append(all, p)
	}
	slices.SortFunc(all, func(a, b Preset) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), cmp.Compare(a.Title.EN, b.Title.EN))
	})
}

func All() []Preset { return slices.Clone(all) }

func Get(id string) (Preset, bool) {
	for _, p := range all {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// QuickAll are the quick-connectable presets in their dialog order.
func QuickAll() []Preset {
	var out []Preset
	for _, p := range all {
		if p.Quick != nil {
			out = append(out, p)
		}
	}
	slices.SortStableFunc(out, func(a, b Preset) int { return cmp.Compare(a.Quick.Order, b.Quick.Order) })
	return out
}

// StarterGraph is the graph a connector made without a template starts from.
func StarterGraph() flow.Graph {
	for _, p := range all {
		if p.Starter != nil {
			return cloneGraph(*p.Starter)
		}
	}
	return flow.Graph{}
}

// cloneGraph copies the graph so a caller can change it without touching the preset.
func cloneGraph(g flow.Graph) flow.Graph {
	var out flow.Graph
	b, _ := json.Marshal(g)
	_ = json.Unmarshal(b, &out)
	return out
}
