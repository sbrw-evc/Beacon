package flow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/ext"
	"google.golang.org/protobuf/types/known/structpb"
)

// CostLimit bounds the work one evaluation may do, so a condition cannot stall the pipeline.
const CostLimit = 100_000

const maxExprLen = 4000

// Expr is a compiled CEL expression over event, request and connector.
//
// Variables: event (the record), request (headers, query, remote_ip, method of the webhook
// request), connector (id, slug). Extra functions: in_cidr(ip, cidr), sha256(string).
type Expr struct {
	src string
	prg cel.Program
}

var (
	envOnce sync.Once
	celEnv  *cel.Env
	envErr  error
)

func env() (*cel.Env, error) {
	envOnce.Do(func() {
		celEnv, envErr = cel.NewEnv(
			cel.Variable("event", cel.MapType(cel.StringType, cel.DynType)),
			cel.Variable("request", cel.MapType(cel.StringType, cel.DynType)),
			cel.Variable("connector", cel.MapType(cel.StringType, cel.DynType)),
			ext.Strings(),
			ext.Encoders(),
			cel.OptionalTypes(),
			cel.Function("in_cidr",
				cel.Overload("in_cidr_string_string", []*cel.Type{cel.StringType, cel.StringType}, cel.BoolType,
					cel.BinaryBinding(func(a, b ref.Val) ref.Val {
						ip, err := netip.ParseAddr(strings.TrimSpace(fmt.Sprint(a.Value())))
						if err != nil {
							return types.False
						}
						pfx, err := netip.ParsePrefix(strings.TrimSpace(fmt.Sprint(b.Value())))
						if err != nil {
							return types.NewErr("in_cidr: %v", err)
						}
						return types.Bool(pfx.Contains(ip.Unmap()))
					}))),
			cel.Function("sha256",
				cel.Overload("sha256_string", []*cel.Type{cel.StringType}, cel.StringType,
					cel.UnaryBinding(func(a ref.Val) ref.Val {
						sum := sha256.Sum256([]byte(fmt.Sprint(a.Value())))
						return types.String(hex.EncodeToString(sum[:]))
					}))),
		)
	})
	return celEnv, envErr
}

func CompileExpr(src string) (*Expr, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return nil, errors.New("the expression is empty")
	}
	if len(src) > maxExprLen {
		return nil, fmt.Errorf("the expression is longer than %d characters", maxExprLen)
	}
	e, err := env()
	if err != nil {
		return nil, err
	}
	ast, iss := e.Compile(src)
	if iss != nil && iss.Err() != nil {
		return nil, errors.New(strings.TrimSpace(iss.Err().Error()))
	}
	prg, err := e.Program(ast, cel.CostLimit(CostLimit), cel.InterruptCheckFrequency(64))
	if err != nil {
		return nil, err
	}
	return &Expr{src: src, prg: prg}, nil
}

// CompileCondition compiles an expression that must return a bool.
func CompileCondition(src string) (*Expr, error) {
	x, err := CompileExpr(src)
	if err != nil {
		return nil, err
	}
	e, _ := env()
	ast, _ := e.Compile(x.src)
	if t := ast.OutputType(); t != cel.BoolType && t != cel.DynType {
		return nil, fmt.Errorf("the condition returns %s, not bool", t)
	}
	return x, nil
}

func (x *Expr) Source() string { return x.src }

type Scope struct {
	Event     map[string]any
	Request   map[string]any
	Connector map[string]any
}

func (s Scope) vars() map[string]any {
	ev, rq, cn := s.Event, s.Request, s.Connector
	if ev == nil {
		ev = map[string]any{}
	}
	if rq == nil {
		rq = map[string]any{}
	}
	if cn == nil {
		cn = map[string]any{}
	}
	return map[string]any{"event": ev, "request": rq, "connector": cn}
}

func (x *Expr) Eval(ctx context.Context, s Scope) (any, error) {
	out, _, err := x.prg.ContextEval(ctx, s.vars())
	if err != nil {
		return nil, err
	}
	return native(out), nil
}

func (x *Expr) Bool(ctx context.Context, s Scope) (bool, error) {
	v, err := x.Eval(ctx, s)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("the condition returned %s, not bool", Stringify(v))
	}
	return b, nil
}

func native(v ref.Val) any {
	switch x := v.(type) {
	case types.Bool:
		return bool(x)
	case types.String:
		return string(x)
	case types.Int:
		return float64(x)
	case types.Uint:
		return float64(x)
	case types.Double:
		return float64(x)
	case types.Null:
		return nil
	}
	if j, err := v.ConvertToNative(types.JSONValueType); err == nil {
		if pb, ok := j.(*structpb.Value); ok {
			return pb.AsInterface()
		}
	}
	return v.Value()
}
