package flow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
)

const (
	MaxNodes = 200
	MaxEdges = 400

	OutMain  = "main"
	OutElse  = "else"
	OutError = "error"

	OnErrorFail  = "fail_record"
	OnErrorSkip  = "skip"
	OnErrorRoute = "route_error"
)

// Graph is the stored form of a connector. Node IDs are stable and never derived from names,
// so renaming a node does not break anything that refers to it.
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

type Node struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	TypeVersion int            `json:"type_version"`
	Name        string         `json:"name,omitempty"`
	Params      map[string]any `json:"params"`
	Position    Position       `json:"position"`
	Disabled    bool           `json:"disabled,omitempty"`
	OnError     string         `json:"on_error,omitempty"`
}

type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Edge struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	SourceOutput string `json:"source_output"`
	Target       string `json:"target"`
	TargetInput  string `json:"target_input,omitempty"`
}

const (
	LevelError   = "error"
	LevelWarning = "warning"
)

type Issue struct {
	Level   string `json:"level"`
	NodeID  string `json:"node_id,omitempty"`
	EdgeID  string `json:"edge_id,omitempty"`
	Param   string `json:"param,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (i Issue) Error() string {
	where := ""
	if i.NodeID != "" {
		where = i.NodeID
		if i.Param != "" {
			where += "." + i.Param
		}
		where += ": "
	}
	return where + i.Message
}

func HasErrors(issues []Issue) bool {
	return slices.ContainsFunc(issues, func(i Issue) bool { return i.Level == LevelError })
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func ParseGraph(b []byte) (Graph, error) {
	var g Graph
	if len(bytes.TrimSpace(b)) == 0 {
		return g, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&g); err != nil {
		return g, fmt.Errorf("the graph is not valid JSON: %w", err)
	}
	return g, nil
}

func (g Graph) JSON() []byte {
	if g.Nodes == nil {
		g.Nodes = []Node{}
	}
	if g.Edges == nil {
		g.Edges = []Edge{}
	}
	for i := range g.Nodes {
		if g.Nodes[i].Params == nil {
			g.Nodes[i].Params = map[string]any{}
		}
	}
	b, _ := json.Marshal(g)
	return b
}

func (g Graph) Node(id string) (Node, bool) {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

// order sorts nodes topologically; ties are broken by node ID so the order never depends
// on where nodes sit on the canvas. ok is false when the graph has a cycle.
func order(ids []string, edges []Edge) ([]string, bool) {
	deg := map[string]int{}
	next := map[string][]string{}
	for _, id := range ids {
		deg[id] = 0
	}
	for _, e := range edges {
		if _, ok := deg[e.Target]; !ok {
			continue
		}
		if _, ok := deg[e.Source]; !ok {
			continue
		}
		deg[e.Target]++
		next[e.Source] = append(next[e.Source], e.Target)
	}
	var ready []string
	for _, id := range ids {
		if deg[id] == 0 {
			ready = append(ready, id)
		}
	}
	slices.Sort(ready)
	out := make([]string, 0, len(ids))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		out = append(out, id)
		for _, t := range next[id] {
			deg[t]--
			if deg[t] == 0 {
				i, _ := slices.BinarySearch(ready, t)
				ready = slices.Insert(ready, i, t)
			}
		}
	}
	return out, len(out) == len(ids)
}

func reachable(from string, edges []Edge) map[string]bool {
	next := map[string][]string{}
	for _, e := range edges {
		next[e.Source] = append(next[e.Source], e.Target)
	}
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, t := range next[id] {
			if !seen[t] {
				seen[t] = true
				stack = append(stack, t)
			}
		}
	}
	return seen
}

func ancestors(of string, edges []Edge) map[string]bool {
	prev := map[string][]string{}
	for _, e := range edges {
		prev[e.Target] = append(prev[e.Target], e.Source)
	}
	seen := map[string]bool{of: true}
	stack := []string{of}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, s := range prev[id] {
			if !seen[s] {
				seen[s] = true
				stack = append(stack, s)
			}
		}
	}
	return seen
}
