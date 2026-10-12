package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/ingest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/textx"
)

const (
	maxHeaderValue = 2000
	IdempotencyHdr = "Idempotency-Key"
	maxIdempotency = 200
	tokenHeader    = "X-Umbrella-Token"
)

// sensitiveHeaders never reach the request archive, samples or traces.
var sensitiveHeaders = map[string]bool{
	"authorization": true, "proxy-authorization": true, "cookie": true, "set-cookie": true, "x-umbrella-token": true,
}

func writeProblem(w http.ResponseWriter, status int, code string, detail error) {
	httpx.Error(w, status, code, detail)
}

// rates is a token bucket per connector: rate requests per second with a burst of one second.
type rates struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	at     time.Time
}

func (r *rates) allow(id string, rate float64, now time.Time) bool {
	if rate <= 0 {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.buckets == nil {
		r.buckets = map[string]*bucket{}
	}
	b := r.buckets[id]
	if b == nil {
		b = &bucket{tokens: math.Max(rate, 1), at: now}
		r.buckets[id] = b
	}
	b.tokens = math.Min(math.Max(rate, 1), b.tokens+now.Sub(b.at).Seconds()*rate)
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func flatten(h map[string][]string, drop map[string]bool) map[string]string {
	out := map[string]string{}
	for k, vs := range h {
		lk := strings.ToLower(k)
		if len(vs) == 0 || sensitiveHeaders[lk] || drop[lk] {
			continue
		}
		v := strings.Join(vs, ", ")
		v = textx.Bytes(v, maxHeaderValue)
		out[lk] = v
	}
	return out
}

func (a *App) rejectIngest(w http.ResponseWriter, connectorID string, status int, code string) {
	if a.queue != nil && connectorID != "" {
		a.queue.Reject(connectorID)
	}
	httpx.Error(w, status, code, nil)
}

// ingest accepts a webhook delivery: checks the network, size, rate and authentication from
// the published trigger, stores the request durably and answers 202. Processing happens right
// after, in the workers.
func (a *App) ingest(w http.ResponseWriter, r *http.Request) {
	t, err := a.connectors.Target(r.PathValue("slug"))
	if err != nil {
		var pe *PublishError
		if errors.As(err, &pe) {
			httpx.Error(w, http.StatusServiceUnavailable, "connector_invalid", nil)
			return
		}
		if !errors.Is(err, ErrNotFound) {
			slog.Error("connector target", "err", err)
		}
		httpx.Error(w, http.StatusNotFound, "not_found", nil)
		return
	}
	if t.Pipeline != nil && !a.ingestReady() {
		w.Header().Set("Retry-After", "5")
		httpx.Error(w, http.StatusServiceUnavailable, "ingest_unavailable", nil)
		return
	}
	hook := t.Webhook
	ip := a.clientIP(r)
	if !hook.Allowed(ip) {
		a.rejectIngest(w, t.ConnectorID, http.StatusForbidden, "network_not_allowed")
		return
	}
	if !a.rates.allow(t.ConnectorID, hook.Rate, time.Now()) {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(1/math.Max(hook.Rate, 0.001)))))
		a.rejectIngest(w, t.ConnectorID, http.StatusTooManyRequests, "rate_limited")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, hook.MaxBody))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			a.rejectIngest(w, t.ConnectorID, http.StatusRequestEntityTooLarge, "body_too_large")
			return
		}
		httpx.Error(w, http.StatusBadRequest, "bad_request", nil)
		return
	}
	drop, err := a.authenticate(hook, r, body)
	if err != nil {
		if errors.Is(err, ErrSecretsDown) {
			w.Header().Set("Retry-After", "10")
			httpx.Error(w, http.StatusServiceUnavailable, "secrets_unavailable", nil)
			return
		}
		if strings.HasPrefix(err.Error(), "basic") {
			w.Header().Set("WWW-Authenticate", `Basic realm="umbrella"`)
		}
		a.rejectIngest(w, t.ConnectorID, http.StatusUnauthorized, "unauthorized")
		return
	}
	query := map[string]string{}
	for k, vs := range r.URL.Query() {
		if len(vs) > 0 {
			query[k] = vs[0]
		}
	}
	headers := flatten(r.Header, drop)
	in := flow.Input{Body: body, Headers: headers, Query: query, RemoteIP: ip, Method: r.Method}
	if hook.AcceptIf != nil {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		ok, cerr := hook.AcceptIf.Bool(ctx, flow.Scope{Request: in.RequestScope(), Connector: a.connectors.connectorScope(t.ConnectorID)})
		cancel()
		if cerr != nil || !ok {
			if a.queue != nil {
				a.queue.Reject(t.ConnectorID)
			}
			httpx.JSON(w, http.StatusAccepted, map[string]any{"status": "ignored"})
			return
		}
	}
	if t.Capturing {
		a.connectors.captured(t.ConnectorID, model.Sample{Body: body, Headers: headers, Query: query, RemoteIP: ip, Method: r.Method})
	}
	if t.Pipeline == nil {
		httpx.JSON(w, http.StatusAccepted, map[string]any{"status": "captured"})
		return
	}
	key := strings.TrimSpace(r.Header.Get(IdempotencyHdr))
	key = textx.Bytes(key, maxIdempotency)
	id, dup, err := a.queue.Enqueue(r.Context(), ingest.Request{ConnectorID: t.ConnectorID, Version: t.Version, RemoteIP: ip,
		Method: r.Method, Headers: headers, Query: query, Body: body}, key)
	if err != nil {
		slog.Error("ingest request not stored", "connector", t.ConnectorID, "err", err)
		w.Header().Set("Retry-After", "5")
		httpx.Error(w, http.StatusServiceUnavailable, "ingest_unavailable", nil)
		return
	}
	if ack := t.Pipeline.Ack(); ack != nil {
		w.Header().Set("Content-Type", ack.ContentType)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(ack.Status)
		_, _ = io.WriteString(w, ack.Body)
		return
	}
	if dup {
		httpx.JSON(w, http.StatusAccepted, map[string]any{"status": "duplicate"})
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]any{"status": "queued", "id": strconv.FormatInt(id, 10)})
}

// authenticate checks the request against the trigger's credential and returns the header
// names that carry the secret, so they are not archived.
func (a *App) authenticate(hook *flow.Webhook, r *http.Request, body []byte) (map[string]bool, error) {
	drop := map[string]bool{}
	if hook.Credential == "" {
		if hook.Anonymous {
			return drop, nil
		}
		return drop, errors.New("no credential")
	}
	c, err := a.creds.Resolve(hook.Credential)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return drop, errors.New("credential missing")
		}
		return drop, err
	}
	equal := func(got, want string) bool {
		return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
	}
	switch c.Type {
	case flow.CredBearer:
		got := r.Header.Get(tokenHeader)
		if v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			got = v
		}
		if !equal(strings.TrimSpace(got), c.Secrets["token"]) {
			return drop, errors.New("bearer token mismatch")
		}
	case flow.CredBasic:
		u, p, ok := r.BasicAuth()
		if !ok || !equal(u, c.Fields["username"]) || !equal(p, c.Secrets["password"]) {
			return drop, errors.New("basic credentials mismatch")
		}
	case flow.CredHeader:
		name := c.Fields["header"]
		drop[strings.ToLower(name)] = true
		if !equal(r.Header.Get(name), c.Secrets["value"]) {
			return drop, errors.New("header mismatch")
		}
	case flow.CredHMAC:
		drop[strings.ToLower(hook.HMACHeader)] = true
		sig := strings.TrimSpace(r.Header.Get(hook.HMACHeader))
		if len(sig) >= len(hook.HMACPrefix) && strings.EqualFold(sig[:len(hook.HMACPrefix)], hook.HMACPrefix) {
			sig = sig[len(hook.HMACPrefix):]
		}
		got, derr := hex.DecodeString(sig)
		mac := hmac.New(sha256.New, []byte(c.Secrets["secret"]))
		mac.Write(body)
		if derr != nil || c.Secrets["secret"] == "" || !hmac.Equal(got, mac.Sum(nil)) {
			return drop, errors.New("signature mismatch")
		}
	default:
		return drop, fmt.Errorf("credential type %s cannot check requests", c.Type)
	}
	return drop, nil
}

// process runs one stored request through the connector version it was received for.
func (a *App) process(ctx context.Context, r ingest.Request) (out ingest.Outcome, err error) {
	defer func() {
		if p := recover(); p != nil {
			slog.Error("connector panicked", "connector", r.ConnectorID, "version", r.Version, "panic", p)
			err = fmt.Errorf("internal error while processing: %v", p)
		}
	}()
	out.Version = r.Version
	p, err := a.connectors.Pipeline(r.ConnectorID, r.Version)
	if err != nil {
		return out, err
	}
	timeout := a.opt.Ingest.ProcessTimeout
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := p.Run(cctx, flow.Input{RequestID: strconv.FormatInt(r.ID, 10), Body: r.Body, Headers: r.Headers, Query: r.Query,
		RemoteIP: r.RemoteIP, Method: r.Method, Connector: a.connectors.connectorScope(r.ConnectorID)}, flow.RunOptions{})
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, fmt.Errorf("processing took longer than %s", timeout)
	}
	markTestEvents(r, res)
	out.Result = res
	return out, nil
}
