package flow

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/textx"
)

type Lineage struct {
	Request string `json:"request,omitempty"`
	Item    int    `json:"item"`
}

// Record is one item flowing between nodes. Raw keeps the source text of the item when there is
// one (the request body, or the JSON of one array element) so failures can show what came in.
type Record struct {
	Data    map[string]any `json:"data"`
	Raw     string         `json:"raw,omitempty"`
	Lineage Lineage        `json:"lineage"`
}

func (r Record) clone() Record {
	r.Data, _ = deepCopy(r.Data).(map[string]any)
	if r.Data == nil {
		r.Data = map[string]any{}
	}
	return r
}

func deepCopy(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, val := range x {
			m[k] = deepCopy(val)
		}
		return m
	case []any:
		s := make([]any, len(x))
		for i, val := range x {
			s[i] = deepCopy(val)
		}
		return s
	}
	return v
}

type Step func(x *Exec, r Record, emit func(output string, r Record)) error

// Exec carries what a step may read besides its record.
type Exec struct {
	ctx       context.Context
	request   map[string]any
	connector map[string]any
	res       *Result
}

func (x *Exec) Context() context.Context { return x.ctx }

func (x *Exec) scope(r Record) Scope {
	return Scope{Event: r.Data, Request: x.request, Connector: x.connector}
}

type cnode struct {
	node    Node
	typ     *NodeType
	step    Step
	outputs []string
	in      []Edge
	rank    int
}

type Pipeline struct {
	graph   Graph
	order   []*cnode
	byID    map[string]*cnode
	trigger *cnode
	webhook *Webhook
	ack     *Ack
}

type CompileOptions struct {
	// Partial allows a graph without a reachable out.event: test runs up to a node.
	Partial bool
	// Credential reports the type of a credential, or false when it does not exist.
	Credential func(id string) (string, bool)
}

// Compile checks the graph and prepares every node once: templates are parsed, CEL is compiled,
// credentials are checked. The returned pipeline is safe for concurrent use.
func Compile(g Graph, opt CompileOptions) (*Pipeline, []Issue) {
	var issues []Issue
	add := func(i Issue) {
		if i.Level == "" {
			i.Level = LevelError
		}
		issues = append(issues, i)
	}
	if len(g.Nodes) > MaxNodes {
		add(Issue{Code: "too_many_nodes", Message: fmt.Sprintf("a connector can have at most %d nodes", MaxNodes)})
	}
	if len(g.Edges) > MaxEdges {
		add(Issue{Code: "too_many_edges", Message: fmt.Sprintf("a connector can have at most %d connections", MaxEdges)})
	}
	if HasErrors(issues) {
		return nil, issues
	}

	p := &Pipeline{graph: g, byID: map[string]*cnode{}}
	var triggers []*cnode
	singles := map[string]int{}
	for _, n := range g.Nodes {
		if !idPattern.MatchString(n.ID) {
			add(Issue{NodeID: n.ID, Code: "node_id", Message: "node IDs are 1–64 letters, digits, - and _"})
			continue
		}
		if p.byID[n.ID] != nil {
			add(Issue{NodeID: n.ID, Code: "duplicate_node", Message: "two nodes have the same ID"})
			continue
		}
		t, ok := TypeOf(n.Type, n.TypeVersion)
		if !ok {
			add(Issue{NodeID: n.ID, Code: "unknown_type", Message: fmt.Sprintf("unknown node type %s v%d", n.Type, n.TypeVersion)})
			continue
		}
		switch n.OnError {
		case "", OnErrorFail, OnErrorSkip:
		case OnErrorRoute:
			if !t.CanFail {
				add(Issue{NodeID: n.ID, Code: "on_error", Message: "this node cannot route errors"})
			}
		default:
			add(Issue{NodeID: n.ID, Code: "on_error", Message: "on_error must be fail_record, skip or route_error"})
		}
		c := &compiler{node: n, typ: t, credential: opt.Credential}
		cn := &cnode{node: n, typ: t}
		cn.step = t.compile(c, n)
		cn.outputs = t.outputsOf(n)
		issues = append(issues, c.issues...)
		if c.webhook != nil {
			p.webhook = c.webhook
		}
		if c.ack != nil {
			p.ack = c.ack
		}
		p.byID[n.ID] = cn
		if t.Category == CategoryTrigger {
			triggers = append(triggers, cn)
		}
		if t.Singleton {
			singles[t.Type]++
			if singles[t.Type] == 2 {
				add(Issue{NodeID: n.ID, Code: "singleton", Message: fmt.Sprintf("a connector can have only one %s node", t.Type)})
			}
		}
	}
	switch len(triggers) {
	case 0:
		add(Issue{Code: "no_trigger", Message: "the connector needs a trigger node"})
	case 1:
		p.trigger = triggers[0]
	default:
		for _, t := range triggers[1:] {
			add(Issue{NodeID: t.node.ID, Code: "many_triggers", Message: "a connector has exactly one trigger"})
		}
	}

	seen := map[string]bool{}
	var edges []Edge
	for _, e := range g.Edges {
		src, dst := p.byID[e.Source], p.byID[e.Target]
		switch {
		case src == nil || dst == nil:
			add(Issue{EdgeID: e.ID, Code: "dangling_edge", Message: "a connection points to a missing node"})
			continue
		case e.Source == e.Target:
			add(Issue{EdgeID: e.ID, NodeID: e.Source, Code: "self_loop", Message: "a node cannot be connected to itself"})
			continue
		case dst.typ.Inputs == 0:
			add(Issue{EdgeID: e.ID, NodeID: e.Target, Code: "no_input", Message: "this node has no input"})
			continue
		}
		if e.SourceOutput == "" {
			e.SourceOutput = OutMain
		}
		if !slices.Contains(src.outputs, e.SourceOutput) {
			add(Issue{EdgeID: e.ID, NodeID: e.Source, Code: "unknown_output", Message: fmt.Sprintf("the node has no output %q", e.SourceOutput)})
			continue
		}
		key := e.Source + "\x00" + e.SourceOutput + "\x00" + e.Target
		if seen[key] {
			add(Issue{Level: LevelWarning, EdgeID: e.ID, Code: "duplicate_edge", Message: "the same connection is drawn twice"})
			continue
		}
		seen[key] = true
		edges = append(edges, e)
		dst.in = append(dst.in, e)
	}

	ids := make([]string, 0, len(p.byID))
	for id := range p.byID {
		ids = append(ids, id)
	}
	sorted, acyclic := order(ids, edges)
	if !acyclic {
		add(Issue{Code: "cycle", Message: "the connections form a loop; a connector runs from the trigger forward only"})
	}
	for i, id := range sorted {
		cn := p.byID[id]
		cn.rank = i
		p.order = append(p.order, cn)
	}
	for _, cn := range p.order {
		slices.SortFunc(cn.in, func(a, b Edge) int {
			if d := p.byID[a.Source].rank - p.byID[b.Source].rank; d != 0 {
				return d
			}
			outs := p.byID[a.Source].outputs
			return slices.Index(outs, a.SourceOutput) - slices.Index(outs, b.SourceOutput)
		})
	}

	if p.trigger != nil && acyclic {
		reach := reachable(p.trigger.node.ID, edges)
		outReached := false
		for _, cn := range p.order {
			if cn.typ.Category == CategoryConfig {
				continue
			}
			if !reach[cn.node.ID] {
				add(Issue{Level: LevelWarning, NodeID: cn.node.ID, Code: "unreachable", Message: "the node is not connected to the trigger and never runs"})
				continue
			}
			if cn.typ.Category == CategoryOutput && !cn.node.Disabled {
				outReached = true
			}
		}
		if !outReached && !opt.Partial {
			add(Issue{Code: "no_output", Message: "no enabled “Event” node is connected to the trigger: the connector would produce nothing"})
		}
	}
	slices.SortStableFunc(issues, func(a, b Issue) int {
		if a.Level != b.Level {
			if a.Level == LevelError {
				return -1
			}
			return 1
		}
		return 0
	})
	if HasErrors(issues) {
		return nil, issues
	}
	return p, issues
}

func (p *Pipeline) Graph() Graph       { return p.graph }
func (p *Pipeline) Webhook() *Webhook  { return p.webhook }
func (p *Pipeline) Ack() *Ack          { return p.ack }
func (p *Pipeline) Has(id string) bool { return p.byID[id] != nil }

// Input is one delivery from a source: a webhook request or a captured sample.
type Input struct {
	RequestID string
	Body      []byte
	Headers   map[string]string
	Query     map[string]string
	RemoteIP  string
	Method    string
	Connector map[string]any
}

func (in Input) RequestScope() map[string]any {
	h := map[string]any{}
	for k, v := range in.Headers {
		h[strings.ToLower(k)] = v
	}
	q := map[string]any{}
	for k, v := range in.Query {
		q[k] = v
	}
	return map[string]any{"headers": h, "query": q, "remote_ip": in.RemoteIP, "method": in.Method, "id": in.RequestID}
}

type RunOptions struct {
	// StopAt runs only the node and the nodes it depends on.
	StopAt string
	// Pinned replaces the outputs of nodes: the node is not run, its pinned records are used.
	Pinned map[string]map[string][]Record
	// Trace records the input and output of every node.
	Trace      bool
	TraceLimit int
}

type Failure struct {
	Node    string         `json:"node"`
	Error   string         `json:"error"`
	Lineage Lineage        `json:"lineage"`
	Data    map[string]any `json:"data,omitempty"`
	Raw     string         `json:"raw,omitempty"`
}

type Emitted struct {
	Event   Event   `json:"event"`
	Lineage Lineage `json:"lineage"`
	Node    string  `json:"node"`
}

type NodeTrace struct {
	In       int                 `json:"in"`
	Out      map[string]int      `json:"out"`
	Input    []Record            `json:"input,omitempty"`
	Output   map[string][]Record `json:"output,omitempty"`
	Errors   []Failure           `json:"errors,omitempty"`
	Filtered int                 `json:"filtered,omitempty"`
	Skipped  int                 `json:"skipped,omitempty"`
	Micros   int64               `json:"us"`
	Pinned   bool                `json:"pinned,omitempty"`
	Disabled bool                `json:"disabled,omitempty"`
}

type Result struct {
	Events   []Emitted             `json:"events"`
	Failures []Failure             `json:"failures"`
	Filtered int                   `json:"filtered"`
	Skipped  int                   `json:"skipped"`
	Trace    map[string]*NodeTrace `json:"trace,omitempty"`
	Order    []string              `json:"order,omitempty"`
}

func (r *Result) Failed() bool { return len(r.Failures) > 0 }

// Run passes one input through the pipeline. Records that fail a node follow that node's
// on_error policy; one bad record never stops the others. The error is returned only when the
// context ends.
func (p *Pipeline) Run(ctx context.Context, in Input, opt RunOptions) (*Result, error) {
	res := &Result{Events: []Emitted{}, Failures: []Failure{}}
	if opt.Trace {
		res.Trace = map[string]*NodeTrace{}
		if opt.TraceLimit <= 0 {
			opt.TraceLimit = 50
		}
	}
	x := &Exec{ctx: ctx, request: in.RequestScope(), connector: in.Connector, res: res}
	run := reachable(p.trigger.node.ID, p.edges())
	if opt.StopAt != "" {
		anc := ancestors(opt.StopAt, p.edges())
		for id := range run {
			if !anc[id] {
				delete(run, id)
			}
		}
	}
	outputs := map[string]map[string][]Record{}
	for _, cn := range p.order {
		id := cn.node.ID
		if !run[id] || cn.typ.Category == CategoryConfig {
			continue
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
		var tr *NodeTrace
		if opt.Trace {
			tr = &NodeTrace{Out: map[string]int{}}
			res.Trace[id] = tr
			res.Order = append(res.Order, id)
		}
		if pin, ok := opt.Pinned[id]; ok {
			outputs[id] = pin
			if tr != nil {
				tr.Pinned = true
				tr.Output = map[string][]Record{}
				for name, recs := range pin {
					tr.Out[name] = len(recs)
					tr.Output[name] = head(recs, opt.TraceLimit)
				}
			}
			continue
		}
		var input []Record
		if cn == p.trigger {
			input = []Record{{Data: map[string]any{}, Raw: string(in.Body), Lineage: Lineage{Request: in.RequestID}}}
		} else {
			for _, e := range cn.in {
				input = append(input, outputs[e.Source][e.SourceOutput]...)
			}
		}
		out := map[string][]Record{}
		start := time.Now()
		if cn.node.Disabled {
			if len(cn.outputs) > 0 {
				out[cn.outputs[0]] = input
			}
		} else {
			emit := func(name string, r Record) { out[name] = append(out[name], r) }
			filteredBefore, skippedBefore := res.Filtered, res.Skipped
			for i, r := range input {
				if i%64 == 63 {
					if err := ctx.Err(); err != nil {
						return res, err
					}
				}
				err := cn.step(x, r, emit)
				if err == nil {
					continue
				}
				if ctx.Err() != nil {
					return res, ctx.Err()
				}
				f := Failure{Node: id, Error: err.Error(), Lineage: r.Lineage, Data: r.Data, Raw: truncate(r.Raw, 4000)}
				if tr != nil && len(tr.Errors) < opt.TraceLimit {
					tr.Errors = append(tr.Errors, f)
				}
				switch cn.node.OnError {
				case OnErrorSkip:
					res.Skipped++
				case OnErrorRoute:
					rr := r.clone()
					rr.Data["error"] = map[string]any{"node": id, "message": err.Error()}
					emit(OutError, rr)
				default:
					res.Failures = append(res.Failures, f)
				}
			}
			if tr != nil {
				tr.Filtered, tr.Skipped = res.Filtered-filteredBefore, res.Skipped-skippedBefore
			}
		}
		outputs[id] = out
		if tr != nil {
			tr.Micros = time.Since(start).Microseconds()
			tr.Disabled = cn.node.Disabled
			tr.In = len(input)
			tr.Input = head(input, opt.TraceLimit)
			tr.Output = map[string][]Record{}
			for _, name := range cn.outputs {
				tr.Out[name] = len(out[name])
				tr.Output[name] = head(out[name], opt.TraceLimit)
			}
		}
	}
	return res, nil
}

func (p *Pipeline) edges() []Edge {
	var out []Edge
	for _, cn := range p.order {
		out = append(out, cn.in...)
	}
	return out
}

func head(r []Record, n int) []Record {
	if len(r) > n {
		r = r[:n]
	}
	out := make([]Record, len(r))
	for i := range r {
		out[i] = r[i].clone()
	}
	return out
}

func truncate(s string, n int) string {
	if c := textx.Runes(s, n); c != s {
		return c + "…"
	}
	return s
}

// ParsePins reads pinned node outputs stored with a draft.
func ParsePins(b []byte) (map[string]map[string][]Record, error) {
	out := map[string]map[string][]Record{}
	if len(strings.TrimSpace(string(b))) == 0 {
		return out, nil
	}
	err := json.Unmarshal(b, &out)
	return out, err
}
