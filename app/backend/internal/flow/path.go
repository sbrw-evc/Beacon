package flow

import (
	"fmt"
	"strconv"
	"strings"
)

// Path addresses a value inside a record: a.b[0]["app.kubernetes.io/name"].c
type Path []step

type step struct {
	key   string
	index int
	isIdx bool
}

func ParsePath(s string) (Path, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty path")
	}
	var p Path
	i := 0
	for i < len(s) {
		switch c := s[i]; {
		case c == '.':
			if i == 0 || i == len(s)-1 || s[i+1] == '.' || s[i+1] == '[' {
				return nil, fmt.Errorf("unexpected '.' at %d in %q", i, s)
			}
			i++
		case c == '[':
			end, st, err := parseBracket(s, i)
			if err != nil {
				return nil, err
			}
			p = append(p, st)
			i = end
		default:
			j := i
			for j < len(s) && s[j] != '.' && s[j] != '[' {
				if s[j] == ']' || s[j] == '"' || s[j] == ' ' {
					return nil, fmt.Errorf("unexpected %q at %d in %q", s[j], j, s)
				}
				j++
			}
			p = append(p, step{key: s[i:j]})
			i = j
		}
	}
	if len(p) == 0 {
		return nil, fmt.Errorf("empty path")
	}
	return p, nil
}

func parseBracket(s string, i int) (int, step, error) {
	if i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\'') {
		q := s[i+1]
		var b strings.Builder
		j := i + 2
		for ; j < len(s) && s[j] != q; j++ {
			if s[j] == '\\' && j+1 < len(s) {
				j++
			}
			b.WriteByte(s[j])
		}
		if j+1 >= len(s) || s[j+1] != ']' {
			return 0, step{}, fmt.Errorf("unterminated [\"…\"] in %q", s)
		}
		return j + 2, step{key: b.String()}, nil
	}
	end := strings.IndexByte(s[i:], ']')
	if end < 0 {
		return 0, step{}, fmt.Errorf("unterminated [ in %q", s)
	}
	n, err := strconv.Atoi(strings.TrimSpace(s[i+1 : i+end]))
	if err != nil {
		return 0, step{}, fmt.Errorf("index %q in %q is not a number", s[i+1:i+end], s)
	}
	return i + end + 1, step{index: n, isIdx: true}, nil
}

func (p Path) String() string {
	var b strings.Builder
	for i, st := range p {
		switch {
		case st.isIdx:
			fmt.Fprintf(&b, "[%d]", st.index)
		case plainKey(st.key):
			if i > 0 {
				b.WriteByte('.')
			}
			b.WriteString(st.key)
		default:
			b.WriteString("[" + strconv.Quote(st.key) + "]")
		}
	}
	return b.String()
}

func plainKey(k string) bool {
	if k == "" {
		return false
	}
	for _, r := range k {
		if !(r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r > 127) {
			return false
		}
	}
	return true
}

// Get walks v along the path. Numeric keys also index arrays, so a.0 works like a[0].
func (p Path) Get(v any) (any, bool) {
	cur := v
	for _, st := range p {
		switch c := cur.(type) {
		case map[string]any:
			if st.isIdx {
				x, ok := c[strconv.Itoa(st.index)]
				if !ok {
					return nil, false
				}
				cur = x
				continue
			}
			x, ok := c[st.key]
			if !ok {
				return nil, false
			}
			cur = x
		case []any:
			idx := st.index
			if !st.isIdx {
				n, err := strconv.Atoi(st.key)
				if err != nil {
					return nil, false
				}
				idx = n
			}
			if idx < 0 {
				idx += len(c)
			}
			if idx < 0 || idx >= len(c) {
				return nil, false
			}
			cur = c[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

// Set writes value at the path, creating maps on the way. Arrays are not created.
func (p Path) Set(root map[string]any, value any) error {
	cur := root
	for i, st := range p {
		if st.isIdx {
			return fmt.Errorf("cannot set an array element in %s", p)
		}
		if i == len(p)-1 {
			cur[st.key] = value
			return nil
		}
		next, ok := cur[st.key].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[st.key] = next
		}
		cur = next
	}
	return nil
}

func Lookup(v any, path string) (any, bool) {
	p, err := ParsePath(path)
	if err != nil {
		return nil, false
	}
	return p.Get(v)
}
