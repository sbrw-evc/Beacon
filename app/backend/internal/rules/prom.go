package rules

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

// Auth is the credential a metric source is queried with.
type Auth struct {
	Type    string
	Fields  map[string]string
	Secrets map[string]string
}

// Sample is one series of an instant vector.
type Sample struct {
	Labels map[string]string `json:"labels"`
	Value  float64           `json:"value"`
}

const maxSamples = 10000

var clients = map[bool]*http.Client{
	false: {Timeout: 20 * time.Second},
	true:  {Timeout: 20 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}, //nolint:gosec // the administrator's choice
}

// Query runs an instant query against the Prometheus HTTP API of a source.
func Query(ctx context.Context, src model.MetricSource, auth *Auth, promql string) ([]Sample, error) {
	out, err := call(ctx, src, auth, "/api/v1/query", url.Values{"query": {promql}})
	if err != nil {
		return nil, err
	}
	switch out.Data.ResultType {
	case "vector":
		var vec []struct {
			Metric map[string]string `json:"metric"`
			Value  [2]any            `json:"value"`
		}
		if err := json.Unmarshal(out.Data.Result, &vec); err != nil {
			return nil, err
		}
		if len(vec) > maxSamples {
			return nil, fmt.Errorf("the query returned %d series; narrow it to at most %d", len(vec), maxSamples)
		}
		samples := make([]Sample, 0, len(vec))
		for _, v := range vec {
			f, ok := number(v.Value[1])
			if !ok {
				continue
			}
			if v.Metric == nil {
				v.Metric = map[string]string{}
			}
			samples = append(samples, Sample{Labels: v.Metric, Value: f})
		}
		return samples, nil
	case "scalar":
		var sc [2]any
		if err := json.Unmarshal(out.Data.Result, &sc); err != nil {
			return nil, err
		}
		if f, ok := number(sc[1]); ok {
			return []Sample{{Labels: map[string]string{}, Value: f}}, nil
		}
		return nil, nil
	}
	return nil, fmt.Errorf("a %s result cannot be evaluated: use an instant vector", out.Data.ResultType)
}

// Series is one series of a range query: its labels and points, [unix seconds, value].
type Series struct {
	Labels map[string]string
	Points [][2]float64
}

// MaxRangeSeries bounds what a range query may return.
const MaxRangeSeries = 50

// QueryRange runs a range query against the Prometheus HTTP API of a source.
func QueryRange(ctx context.Context, src model.MetricSource, auth *Auth, promql string, start, end time.Time, step time.Duration) ([]Series, error) {
	if step < time.Second {
		step = time.Second
	}
	form := url.Values{"query": {promql}, "start": {strconv.FormatInt(start.Unix(), 10)}, "end": {strconv.FormatInt(end.Unix(), 10)},
		"step": {strconv.FormatFloat(step.Seconds(), 'f', -1, 64)}}
	out, err := call(ctx, src, auth, "/api/v1/query_range", form)
	if err != nil {
		return nil, err
	}
	if out.Data.ResultType != "matrix" {
		return nil, fmt.Errorf("a %s result cannot be drawn: use a query that returns series", out.Data.ResultType)
	}
	var mat []struct {
		Metric map[string]string `json:"metric"`
		Values [][2]any          `json:"values"`
	}
	if err := json.Unmarshal(out.Data.Result, &mat); err != nil {
		return nil, err
	}
	if len(mat) > MaxRangeSeries {
		return nil, fmt.Errorf("the query returned %d series; narrow it to at most %d", len(mat), MaxRangeSeries)
	}
	series := make([]Series, 0, len(mat))
	for _, m := range mat {
		s := Series{Labels: m.Metric, Points: make([][2]float64, 0, len(m.Values))}
		if s.Labels == nil {
			s.Labels = map[string]string{}
		}
		for _, v := range m.Values {
			ts, ok := v[0].(float64)
			f, ok2 := number(v[1])
			if !ok || !ok2 {
				continue
			}
			s.Points = append(s.Points, [2]float64{ts, f})
		}
		series = append(series, s)
	}
	return series, nil
}

type answer struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
	Data      struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	} `json:"data"`
}

// call posts a form to an endpoint of the Prometheus HTTP API and reads a successful answer.
func call(ctx context.Context, src model.MetricSource, auth *Auth, path string, form url.Values) (answer, error) {
	var out answer
	base, err := url.Parse(strings.TrimRight(src.URL, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") {
		return out, errors.New("the source address is not an http or https URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String()+path, strings.NewReader(form.Encode()))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := authorize(req, auth); err != nil {
		return out, err
	}
	resp, err := clients[src.SkipVerify].Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err := json.Unmarshal(body, &out); err != nil {
		if resp.StatusCode/100 != 2 {
			return out, fmt.Errorf("the source answered %d", resp.StatusCode)
		}
		return out, errors.New("the source did not answer with the Prometheus API")
	}
	if out.Status != "success" {
		msg := out.Error
		if msg == "" {
			msg = fmt.Sprintf("the source answered %d", resp.StatusCode)
		}
		return out, errors.New(msg)
	}
	return out, nil
}

// authorize puts the credential of a source on a request.
func authorize(req *http.Request, auth *Auth) error {
	req.Header.Set("Accept", "application/json")
	if auth == nil {
		return nil
	}
	switch auth.Type {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+auth.Secrets["token"])
	case "basic":
		req.SetBasicAuth(auth.Fields["username"], auth.Secrets["password"])
	case "header":
		if h := auth.Fields["header"]; h != "" {
			req.Header.Set(h, auth.Secrets["value"])
		}
	default:
		return fmt.Errorf("a %s credential cannot be used to query metrics", auth.Type)
	}
	return nil
}

// AlertRuleQuery is the expression of the alerting rule of that name on a Prometheus-compatible
// server (the rules API); empty when the server has no such rule.
func AlertRuleQuery(ctx context.Context, src model.MetricSource, auth *Auth, name string) (string, error) {
	base, err := url.Parse(strings.TrimRight(src.URL, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") {
		return "", errors.New("the source address is not an http or https URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String()+"/api/v1/rules?type=alert", nil)
	if err != nil {
		return "", err
	}
	if err := authorize(req, auth); err != nil {
		return "", err
	}
	resp, err := clients[src.SkipVerify].Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	var out struct {
		Status string `json:"status"`
		Data   struct {
			Groups []struct {
				Rules []struct {
					Name  string `json:"name"`
					Query string `json:"query"`
					Type  string `json:"type"`
				} `json:"rules"`
			} `json:"groups"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Status != "success" {
		return "", fmt.Errorf("the source did not list its rules (%d)", resp.StatusCode)
	}
	for _, g := range out.Data.Groups {
		for _, r := range g.Rules {
			if r.Name == name && (r.Type == "" || r.Type == "alerting") && strings.TrimSpace(r.Query) != "" {
				return r.Query, nil
			}
		}
	}
	return "", nil
}

func number(v any) (float64, bool) {
	s, ok := v.(string)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}
