package flow

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

type Text struct {
	EN string `json:"en"`
	RU string `json:"ru"`
}

const (
	CategoryTrigger   = "trigger"
	CategoryParse     = "parse"
	CategoryTransform = "transform"
	CategoryRoute     = "route"
	CategoryOutput    = "output"
	CategoryConfig    = "config"
)

// Param kinds the editor renders as form controls.
const (
	KindString     = "string"
	KindText       = "text"
	KindNumber     = "number"
	KindBool       = "bool"
	KindSelect     = "select"
	KindTemplate   = "template"
	KindCEL        = "cel"
	KindPath       = "path"
	KindCredential = "credential"
	KindList       = "list"
	KindTable      = "table"
)

type Option struct {
	Value string `json:"value"`
	Title Text   `json:"title"`
}

// Param declares one node parameter. The editor builds its form from these declarations and
// the server validates against the same declarations, so both always agree.
type Param struct {
	Key             string   `json:"key"`
	Kind            string   `json:"kind"`
	Title           Text     `json:"title"`
	Help            Text     `json:"help,omitzero"`
	Required        bool     `json:"required,omitempty"`
	Default         any      `json:"default,omitempty"`
	Placeholder     string   `json:"placeholder,omitempty"`
	Options         []Option `json:"options,omitempty"`
	CredentialTypes []string `json:"credential_types,omitempty"`
	Columns         []Param  `json:"columns,omitempty"`
	Min             *float64 `json:"min,omitempty"`
	Max             *float64 `json:"max,omitempty"`
}

type NodeType struct {
	Type        string   `json:"type"`
	Version     int      `json:"version"`
	Category    string   `json:"category"`
	Title       Text     `json:"title"`
	Description Text     `json:"description"`
	Params      []Param  `json:"params"`
	Inputs      int      `json:"inputs"`
	Outputs     []string `json:"outputs"`
	// DynamicOutputs: output names come from the node's parameters (route.switch).
	DynamicOutputs bool `json:"dynamic_outputs,omitempty"`
	// CanFail: the node can reject single records and honours on_error.
	CanFail   bool `json:"can_fail,omitempty"`
	Singleton bool `json:"singleton,omitempty"`

	compile func(c *compiler, n Node) Step
	outputs func(n Node) []string
}

func (t *NodeType) outputsOf(n Node) []string {
	var out []string
	if t.outputs != nil {
		out = t.outputs(n)
	} else {
		out = slices.Clone(t.Outputs)
	}
	if t.CanFail && n.OnError == OnErrorRoute {
		out = append(out, OutError)
	}
	return out
}

var registry = map[string]map[int]*NodeType{}

func register(t *NodeType) {
	// The editor reads these lists as JSON arrays, never null.
	if t.Outputs == nil {
		t.Outputs = []string{}
	}
	if t.Params == nil {
		t.Params = []Param{}
	}
	if registry[t.Type] == nil {
		registry[t.Type] = map[int]*NodeType{}
	}
	registry[t.Type][t.Version] = t
}

func TypeOf(name string, version int) (*NodeType, bool) {
	t, ok := registry[name][version]
	return t, ok
}

// Latest returns the newest version of a node type, used when the editor adds a node.
func Latest(name string) (*NodeType, bool) {
	var best *NodeType
	for _, t := range registry[name] {
		if best == nil || t.Version > best.Version {
			best = t
		}
	}
	return best, best != nil
}

// Types lists every registered node type and version, for the editor palette.
func Types() []NodeType {
	var out []NodeType
	for _, vs := range registry {
		for _, t := range vs {
			out = append(out, *t)
		}
	}
	rank := map[string]int{CategoryTrigger: 0, CategoryParse: 1, CategoryTransform: 2, CategoryRoute: 3, CategoryOutput: 4, CategoryConfig: 5}
	slices.SortFunc(out, func(a, b NodeType) int {
		if a.Category != b.Category {
			return rank[a.Category] - rank[b.Category]
		}
		if a.Type != b.Type {
			return strings.Compare(a.Type, b.Type)
		}
		return a.Version - b.Version
	})
	return out
}

// compiler turns node parameters into typed values and collects issues instead of stopping at
// the first one, so the editor can show every problem at once.
type compiler struct {
	node       Node
	typ        *NodeType
	issues     []Issue
	credential func(id string) (string, bool)
	webhook    *Webhook
	ack        *Ack
}

func (c *compiler) fail(param, code, format string, args ...any) {
	c.issues = append(c.issues, Issue{Level: LevelError, NodeID: c.node.ID, Param: param, Code: code, Message: fmt.Sprintf(format, args...)})
}

func (c *compiler) spec(key string) Param {
	for _, p := range c.typ.Params {
		if p.Key == key {
			return p
		}
	}
	panic("flow: undeclared param " + c.typ.Type + "." + key)
}

func (c *compiler) raw(key string) (any, bool) {
	v, ok := c.node.Params[key]
	if !ok || v == nil {
		p := c.spec(key)
		if p.Default != nil {
			return p.Default, true
		}
		return nil, false
	}
	return v, true
}

func (c *compiler) str(key string) string {
	v, ok := c.raw(key)
	if !ok {
		if c.spec(key).Required {
			c.fail(key, "required", "the parameter is required")
		}
		return ""
	}
	s, isStr := v.(string)
	if !isStr {
		c.fail(key, "type", "must be a string")
		return ""
	}
	s = strings.TrimSpace(s)
	if s == "" && c.spec(key).Required {
		c.fail(key, "required", "the parameter is required")
	}
	return s
}

func (c *compiler) text(key string) string {
	v, ok := c.raw(key)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func (c *compiler) num(key string) float64 {
	p := c.spec(key)
	v, ok := c.raw(key)
	if !ok {
		if p.Required {
			c.fail(key, "required", "the parameter is required")
		}
		return 0
	}
	var f float64
	switch x := v.(type) {
	case float64:
		f = x
	case int:
		f = float64(x)
	case json.Number:
		f, _ = x.Float64()
	case string:
		if strings.TrimSpace(x) == "" {
			return 0
		}
		if _, err := fmt.Sscan(x, &f); err != nil {
			c.fail(key, "type", "must be a number")
			return 0
		}
	default:
		c.fail(key, "type", "must be a number")
		return 0
	}
	if math.IsNaN(f) || p.Min != nil && f < *p.Min || p.Max != nil && f > *p.Max {
		c.fail(key, "range", "must be between %v and %v", deref(p.Min), deref(p.Max))
	}
	return f
}

func deref(f *float64) any {
	if f == nil {
		return "∞"
	}
	return *f
}

func (c *compiler) boolean(key string) bool {
	v, ok := c.raw(key)
	if !ok {
		return false
	}
	b, isBool := v.(bool)
	if !isBool {
		c.fail(key, "type", "must be true or false")
	}
	return b
}

func (c *compiler) choice(key string) string {
	s := c.str(key)
	if s == "" {
		return s
	}
	p := c.spec(key)
	if !slices.ContainsFunc(p.Options, func(o Option) bool { return o.Value == s }) {
		c.fail(key, "option", "%q is not one of the allowed values", s)
	}
	return s
}

func (c *compiler) template(key string) *Template {
	s := c.text(key)
	if strings.TrimSpace(s) == "" {
		if c.spec(key).Required {
			c.fail(key, "required", "the parameter is required")
		}
		return nil
	}
	t, err := CompileTemplate(s)
	if err != nil {
		c.fail(key, "template", "%v", err)
		return nil
	}
	return t
}

func (c *compiler) condition(key string) *Expr {
	s := c.str(key)
	if s == "" {
		return nil
	}
	x, err := CompileCondition(s)
	if err != nil {
		c.fail(key, "cel", "%v", err)
		return nil
	}
	return x
}

func (c *compiler) path(key string) Path {
	s := c.str(key)
	if s == "" {
		return nil
	}
	p, err := ParsePath(s)
	if err != nil {
		c.fail(key, "path", "%v", err)
	}
	return p
}

func (c *compiler) pathOr(key, def string) Path {
	if p := c.path(key); p != nil {
		return p
	}
	p, _ := ParsePath(def)
	return p
}

func (c *compiler) list(key string) []string {
	v, ok := c.raw(key)
	if !ok {
		return nil
	}
	var out []string
	switch x := v.(type) {
	case []any:
		for _, it := range x {
			s, isStr := it.(string)
			if !isStr {
				c.fail(key, "type", "must be a list of strings")
				return nil
			}
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	case []string:
		out = x
	case string:
		for _, s := range strings.FieldsFunc(x, func(r rune) bool { return r == '\n' || r == ',' }) {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	default:
		c.fail(key, "type", "must be a list of strings")
	}
	return out
}

func (c *compiler) table(key string) []map[string]string {
	v, ok := c.raw(key)
	if !ok {
		return nil
	}
	rows, isList := v.([]any)
	if !isList {
		c.fail(key, "type", "must be a list of rows")
		return nil
	}
	cols := c.spec(key).Columns
	var out []map[string]string
	for i, r := range rows {
		m, isMap := r.(map[string]any)
		if !isMap {
			c.fail(key, "type", "row %d is not an object", i+1)
			continue
		}
		row := map[string]string{}
		blank := true
		for _, col := range cols {
			s := ""
			switch x := m[col.Key].(type) {
			case string:
				s = x
			case nil:
			default:
				s = Stringify(x)
			}
			row[col.Key] = s
			if strings.TrimSpace(s) != "" {
				blank = false
			}
		}
		if blank {
			continue
		}
		for _, col := range cols {
			if col.Required && strings.TrimSpace(row[col.Key]) == "" {
				c.fail(key, "required", "row %d: %s is required", i+1, col.Title.EN)
			}
		}
		out = append(out, row)
	}
	return out
}

func (c *compiler) credentialRef(key string) string {
	id := c.str(key)
	if id == "" || c.credential == nil {
		return id
	}
	typ, ok := c.credential(id)
	if !ok {
		c.fail(key, "credential", "the credential %s does not exist", id)
		return id
	}
	if allowed := c.spec(key).CredentialTypes; len(allowed) > 0 && !slices.Contains(allowed, typ) {
		c.fail(key, "credential", "credentials of type %s cannot be used here", typ)
	}
	return id
}

var outputName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

func ptr(f float64) *float64 { return &f }
