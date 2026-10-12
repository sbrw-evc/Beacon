package flow

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Template is a compiled ${…} substitution string.
//
//	${path}                 value at path in the record
//	${a|$b|$c}              first non-empty of a, b, c
//	${a|text}, ${a|"x y"}   literal default when everything before it is empty
//	${a|lower}              filters: lower, upper, trim, json, default:"x", date:"RFC3339"
//	${a|firstline}          text filters: firstline, lastline, errorline, oneline, nohtml, noansi,
//	                        unescape, truncate:N, head:N (lines), tail:N (lines)
//	$${                     a literal "${"
type Template struct {
	src   string
	parts []tplPart
}

type tplPart struct {
	text string
	expr []tplOp
}

type tplOp struct {
	kind  opKind
	path  Path
	value string
}

type opKind int

const (
	opPath opKind = iota
	opLiteral
	opFilter
)

var filters = map[string]bool{"lower": true, "upper": true, "trim": true, "json": true, "default": true, "date": true}

func CompileTemplate(src string) (*Template, error) {
	t := &Template{src: src}
	var text strings.Builder
	i := 0
	for i < len(src) {
		if strings.HasPrefix(src[i:], "$${") {
			text.WriteString("${")
			i += 3
			continue
		}
		if !strings.HasPrefix(src[i:], "${") {
			text.WriteByte(src[i])
			i++
			continue
		}
		end := closing(src, i+2)
		if end < 0 {
			return nil, fmt.Errorf("unclosed ${ at position %d", i)
		}
		ops, err := parseExpr(src[i+2 : end])
		if err != nil {
			return nil, err
		}
		if text.Len() > 0 {
			t.parts = append(t.parts, tplPart{text: text.String()})
			text.Reset()
		}
		t.parts = append(t.parts, tplPart{expr: ops})
		i = end + 1
	}
	if text.Len() > 0 {
		t.parts = append(t.parts, tplPart{text: text.String()})
	}
	return t, nil
}

// closing finds the } that ends a ${…}, skipping quoted strings.
func closing(s string, from int) int {
	var q byte
	for i := from; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == '\\' {
				i++
			} else if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
		case c == '}':
			return i
		}
	}
	return -1
}

func splitPipes(s string) []string {
	var out []string
	var q byte
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == '\\' {
				i++
			} else if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
		case c == '[':
			if j := strings.IndexByte(s[i:], ']'); j > 0 && !strings.ContainsAny(s[i:i+j], "\"'") {
				i += j
			}
		case c == '|':
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func unquote(s string) (string, bool) {
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		if s[0] == '\'' {
			s = `"` + strings.ReplaceAll(strings.ReplaceAll(s[1:len(s)-1], `"`, `\"`), `\'`, `'`) + `"`
		}
		v, err := strconv.Unquote(s)
		if err != nil {
			return s[1 : len(s)-1], true
		}
		return v, true
	}
	return s, false
}

func parseExpr(s string) ([]tplOp, error) {
	segs := splitPipes(s)
	var ops []tplOp
	for i, seg := range segs {
		seg = strings.TrimSpace(seg)
		if i == 0 {
			p, err := ParsePath(strings.TrimPrefix(seg, "$"))
			if err != nil {
				return nil, fmt.Errorf("${%s}: %w", s, err)
			}
			ops = append(ops, tplOp{kind: opPath, path: p})
			continue
		}
		if strings.HasPrefix(seg, "$") {
			p, err := ParsePath(seg[1:])
			if err != nil {
				return nil, fmt.Errorf("${%s}: %w", s, err)
			}
			ops = append(ops, tplOp{kind: opPath, path: p})
			continue
		}
		name, arg, hasArg := strings.Cut(seg, ":")
		if filters[name] && (hasArg || name != "default" && name != "date") {
			arg, _ = unquote(strings.TrimSpace(arg))
			if err := checkTextFilter(name, arg); err != nil {
				return nil, fmt.Errorf("${%s}: %w", s, err)
			}
			if name == "date" && arg == "" {
				arg = "RFC3339"
			}
			ops = append(ops, tplOp{kind: opFilter, path: nil, value: name + "\x00" + arg})
			continue
		}
		if seg == "date" {
			ops = append(ops, tplOp{kind: opFilter, value: "date\x00RFC3339"})
			continue
		}
		v, _ := unquote(seg)
		ops = append(ops, tplOp{kind: opLiteral, value: v})
	}
	return ops, nil
}

func (t *Template) Source() string { return t.src }

func (t *Template) Empty() bool { return t == nil || len(t.parts) == 0 }

// Value renders the template. A template that is a single ${…} keeps the type of the value.
func (t *Template) Value(data map[string]any) (any, error) {
	if t == nil || len(t.parts) == 0 {
		return "", nil
	}
	if len(t.parts) == 1 && t.parts[0].expr != nil {
		return evalExpr(t.parts[0].expr, data)
	}
	var b strings.Builder
	for _, p := range t.parts {
		if p.expr == nil {
			b.WriteString(p.text)
			continue
		}
		v, err := evalExpr(p.expr, data)
		if err != nil {
			return nil, err
		}
		b.WriteString(Stringify(v))
	}
	return b.String(), nil
}

func (t *Template) String(data map[string]any) (string, error) {
	v, err := t.Value(data)
	if err != nil {
		return "", err
	}
	return Stringify(v), nil
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	}
	return false
}

func evalExpr(ops []tplOp, data map[string]any) (any, error) {
	var v any
	for _, op := range ops {
		switch op.kind {
		case opPath:
			if isEmpty(v) {
				if x, ok := op.path.Get(data); ok {
					v = x
				}
			}
		case opLiteral:
			if isEmpty(v) {
				v = op.value
			}
		case opFilter:
			name, arg, _ := strings.Cut(op.value, "\x00")
			var err error
			if v, err = applyFilter(name, arg, v); err != nil {
				return nil, err
			}
		}
	}
	return v, nil
}

func applyFilter(name, arg string, v any) (any, error) {
	if out, ok := applyTextFilter(name, arg, v); ok {
		return out, nil
	}
	switch name {
	case "lower":
		return strings.ToLower(Stringify(v)), nil
	case "upper":
		return strings.ToUpper(Stringify(v)), nil
	case "trim":
		return strings.TrimSpace(Stringify(v)), nil
	case "json":
		b, err := json.Marshal(v)
		return string(b), err
	case "default":
		if isEmpty(v) {
			return arg, nil
		}
		return v, nil
	case "date":
		if isEmpty(v) {
			return v, nil
		}
		ts, err := toTime(v)
		if err != nil {
			return nil, err
		}
		return formatTime(ts, arg), nil
	}
	return v, nil
}

var layouts = map[string]string{
	"RFC3339": time.RFC3339, "RFC3339Nano": time.RFC3339Nano, "RFC1123": time.RFC1123, "RFC1123Z": time.RFC1123Z,
	"DateTime": time.DateTime, "DateOnly": time.DateOnly, "TimeOnly": time.TimeOnly,
}

func formatTime(t time.Time, layout string) string {
	switch layout {
	case "unix":
		return strconv.FormatInt(t.Unix(), 10)
	case "unixms":
		return strconv.FormatInt(t.UnixMilli(), 10)
	}
	if l, ok := layouts[layout]; ok {
		layout = l
	}
	return t.UTC().Format(layout)
}

func toTime(v any) (time.Time, error) {
	switch x := v.(type) {
	case time.Time:
		return x, nil
	case float64:
		return fromEpoch(x), nil
	case int:
		return fromEpoch(float64(x)), nil
	case int64:
		return fromEpoch(float64(x)), nil
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return time.Time{}, err
		}
		return fromEpoch(f), nil
	case string:
		s := strings.TrimSpace(x)
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return fromEpoch(f), nil
		}
		for _, l := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", time.DateTime, time.RFC1123Z, time.RFC1123, "2006.01.02 15:04:05", time.DateOnly} {
			if t, err := time.Parse(l, s); err == nil {
				return t, nil
			}
		}
		return time.Time{}, fmt.Errorf("%q is not a date", s)
	}
	return time.Time{}, fmt.Errorf("%v is not a date", v)
}

func fromEpoch(f float64) time.Time {
	// Values above year 2286 in seconds are taken as milliseconds.
	if math.Abs(f) > 1e10 {
		return time.UnixMilli(int64(f)).UTC()
	}
	sec, frac := math.Modf(f)
	return time.Unix(int64(sec), int64(frac*1e9)).UTC()
}

// Stringify renders a value for templates and event fields.
func Stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case json.Number:
		return x.String()
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// TemplatePaths lists the paths a template reads, for the editor.
func (t *Template) Paths() []string {
	var out []string
	if t == nil {
		return out
	}
	for _, p := range t.parts {
		for _, op := range p.expr {
			if op.kind == opPath {
				out = append(out, op.path.String())
			}
		}
	}
	return out
}
