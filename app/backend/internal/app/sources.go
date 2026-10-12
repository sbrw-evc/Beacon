package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/ingest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/presets"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// Quick connect and the test event: one dialog sets up a source end to end (a generated
// bearer token, a connector from a template, published), and one button checks the path from
// the connector to the incident and its route.

const (
	// testHeader marks a test request the intake made itself; the intake drops these headers
	// from requests that come from outside, so a source cannot pass its alerts off as tests.
	testHeader   = "x-umbrella-test"
	testCIHeader = "x-umbrella-test-ci"
	// TestEventTitle is the title of the incident a test event opens.
	TestEventTitle = "Тестовое событие Umbrella"
	testEventCI    = "umbrella-test"
)

var (
	ErrNotPublished    = errors.New("the connector is not published")
	ErrIngestDown      = errors.New("the intake is not ready")
	ErrTestEventFailed = errors.New("the connector failed to process the test event")
)

func init() {
	sensitiveHeaders[testHeader] = true
	sensitiveHeaders[testCIHeader] = true
	statuses = append(statuses,
		orgStatus{ErrNotPublished, http.StatusConflict, "connector_not_published"},
		orgStatus{ErrIngestDown, http.StatusServiceUnavailable, "ingest_unavailable"},
	)
}

// Texts quick connect writes into what it creates, in the language of the installation.
var (
	quickCredentialName = flow.Text{EN: "Intake token: ", RU: "Токен приёма: "}
	quickCredentialDesc = flow.Text{EN: "Created by quick connect of a source", RU: "Создан быстрым подключением источника"}
	quickPublishComment = flow.Text{EN: "Quick connect", RU: "Быстрое подключение"}
)

// localize picks the text in the language of the installation: Russian unless it is English.
func localize(t flow.Text, locale string) string {
	if locale == model.LocaleEN && t.EN != "" || t.RU == "" {
		return t.EN
	}
	return t.RU
}

type QuickConnectInput struct {
	Preset string `json:"preset"`
	Name   string `json:"name"`
	// MonitoringID: the monitoring system whose alerts the connector receives («Приём
	// алертов»); its name is the default connector name.
	MonitoringID string `json:"monitoring_id"`
}

// QuickInstructions is what the source needs: the address, the header with the token and a
// ready piece of configuration (Alertmanager receiver, Grafana contact point, curl), all
// rendered from the preset's quick connect templates.
type QuickInstructions struct {
	Preset      string `json:"preset"`
	IngestURL   string `json:"ingest_url"`
	AuthHeader  string `json:"auth_header"`
	Snippet     string `json:"snippet,omitempty"`
	SnippetKind string `json:"snippet_kind,omitempty"`
	// SnippetFile is the file name to download the snippet as; empty means copy only.
	SnippetFile string `json:"snippet_file,omitempty"`
}

// QuickAttachment is a file for the source to import (the Zabbix media type).
type QuickAttachment struct {
	Name    string `json:"name"`
	Kind    string `json:"kind,omitempty"`
	Content string `json:"content"`
}

type QuickConnectResult struct {
	Connector    ConnectorView `json:"connector"`
	CredentialID string        `json:"credential_id"`
	IngestURL    string        `json:"ingest_url"`
	// Token is shown once: only OpenBao keeps it.
	Token        string            `json:"token"`
	Instructions QuickInstructions `json:"instructions"`
	Attachment   *QuickAttachment  `json:"attachment,omitempty"`
	// MediaTypeYAML is the attachment's content, kept for clients that predate Attachment.
	MediaTypeYAML string `json:"mediatype_yaml,omitempty"`
	MonitoringID  string `json:"monitoring_id,omitempty"`
}

// slugify makes an ingest path out of a name: ASCII letters and digits, the rest dashes.
func slugify(name, fallback string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 50 {
		s = strings.Trim(s[:50], "-")
	}
	if len(s) < 2 {
		s = fallback
	}
	return s
}

// freeSlug is the slug, or the slug with the first free number after it.
func freeSlug(d *store.Data, slug string) string {
	if !slugTaken(d, slug, "") {
		return slug
	}
	for i := 2; ; i++ {
		if s := slug + "-" + strconv.Itoa(i); !slugTaken(d, s, "") {
			return s
		}
	}
}

// baseURL is the address sources reach Umbrella at: the public address from the notification
// settings, or the one the browser used.
func (a *App) baseURL(r *http.Request) string {
	var pub string
	a.deps.Store.Read(func(d *store.Data) { pub = d.Settings.Alerting.PublicURL })
	if pub = strings.TrimRight(strings.TrimSpace(pub), "/"); pub != "" {
		return pub
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// QuickConnect creates a bearer token, a connector from the template with that token and
// publishes it. Whatever was created is removed again when a later step fails.
func (a *App) QuickConnect(ctx context.Context, actor model.User, base string, in QuickConnectInput) (QuickConnectResult, error) {
	var out QuickConnectResult
	p, ok := presets.Get(in.Preset)
	if !ok || p.Quick == nil {
		return out, invalid("unknown_preset", nil)
	}
	locale := a.settings.Get().DefaultLocale
	name := strings.Join(strings.Fields(in.Name), " ")
	if in.MonitoringID != "" {
		var sys string
		a.deps.Store.Read(func(d *store.Data) {
			if m := d.MonitoringSources[in.MonitoringID]; m != nil {
				sys = m.Name
			}
		})
		if sys == "" {
			return out, ErrNotFound
		}
		if name == "" {
			name = sys
		}
	}
	if name == "" {
		name = localize(p.Quick.Name, locale)
	}
	cred, token, err := a.creds.GenerateBearer(ctx, actor.Username, localize(quickCredentialName, locale)+name, localize(quickCredentialDesc, locale))
	if err != nil {
		return out, err
	}
	cleanup := func(connectorID string) {
		if connectorID != "" {
			if err := a.connectors.Delete(actor, connectorID); err != nil {
				slog.Warn("quick connect: connector not removed", "connector", connectorID, "err", err)
			}
		}
		if err := a.creds.Delete(ctx, actor.Username, cred.ID); err != nil {
			slog.Warn("quick connect: credential not removed", "credential", cred.ID, "err", err)
		}
	}
	mapping := map[string]string{}
	for _, slot := range p.Document.Credentials {
		mapping[slot.Slot] = cred.ID
	}
	var view ConnectorView
	for range 5 {
		var slug string
		a.deps.Store.Read(func(d *store.Data) { slug = freeSlug(d, slugify(name, in.Preset)) })
		view, err = a.connectors.Create(actor, ConnectorInput{Name: name, Slug: slug, Preset: in.Preset, Tags: p.Document.Tags,
			Description: localize(p.Description, locale)}, &p.Document, mapping)
		if !errors.Is(err, ErrSlugTaken) {
			break
		}
	}
	if err != nil {
		cleanup("")
		return out, err
	}
	view, err = a.connectors.Publish(actor, view.ID, PublishInput{Comment: localize(quickPublishComment, locale)})
	if err != nil {
		cleanup(view.ID)
		return out, err
	}
	if in.MonitoringID != "" {
		if err := a.monitoring.LinkConnector(actor.Username, in.MonitoringID, view.ID); err != nil {
			cleanup(view.ID)
			return out, err
		}
		out.MonitoringID = in.MonitoringID
	}
	url := base + "/api/ingest/" + view.Slug
	out.Connector, out.CredentialID, out.IngestURL, out.Token = view, cred.ID, url, token
	if out.Instructions, out.Attachment, err = quickInstructions(p, url, token); err != nil {
		// The templates are checked by the preset tests; the connector is ready all the same.
		slog.Error("quick connect: instructions not rendered", "preset", p.ID, "err", err)
	}
	if out.Attachment != nil {
		out.MediaTypeYAML = out.Attachment.Content
	}
	return out, nil
}

// quickInstructions renders the snippet and the attachment of the preset for the connector.
func quickInstructions(p presets.Preset, url, token string) (QuickInstructions, *QuickAttachment, error) {
	q := QuickInstructions{Preset: p.ID, IngestURL: url, AuthHeader: "Authorization: Bearer " + token}
	vars := presets.Vars{URL: url, Token: token}
	var att *QuickAttachment
	if f := p.Quick.Snippet; f != nil {
		text, err := f.Render(vars)
		if err != nil {
			return q, nil, err
		}
		q.Snippet, q.SnippetKind, q.SnippetFile = text, f.Kind, f.Name
	}
	if f := p.Quick.Attachment; f != nil {
		text, err := f.Render(vars)
		if err != nil {
			return q, nil, err
		}
		att = &QuickAttachment{Name: f.Name, Kind: f.Kind, Content: text}
	}
	return q, att, nil
}

type TestEventInput struct {
	// CI: the name the test event gives its configuration item; empty means umbrella-test.
	CI string `json:"ci"`
}

type TestEventResult struct {
	IncidentID string `json:"incident_id"`
	RequestID  string `json:"request_id"`
	// Status of the request: pending while the workers have not got to it.
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// testBody is a delivery in the format of the connector's template that names the item.
func testBody(preset, nonce, ci string, sample []byte) []byte {
	if p, ok := presets.Get(preset); ok {
		body, ok, err := p.Quick.RenderTestBody(presets.Vars{Nonce: nonce, CI: ci, Title: TestEventTitle})
		if ok && err == nil {
			return body
		}
		if err != nil {
			slog.Error("test event: body not rendered", "preset", preset, "err", err)
		}
	}
	// A connector of its own format: its first sample, which it knows how to read.
	if len(sample) > 0 {
		return sample
	}
	return []byte("{}")
}

// markTestEvents turns what the connector made of a test request into one test event: the
// test title, label and key, a signal of its own (so it never folds into a real alert) and the
// chosen item. When the connector made no event of it, the event is made here, so the test
// still shows the route.
func markTestEvents(r ingest.Request, res *flow.Result) {
	nonce := r.Headers[testHeader]
	if nonce == "" || res == nil {
		return
	}
	if len(res.Events) == 0 {
		res.Events = []flow.Emitted{{Node: "umbrella-test"}}
	}
	res.Events = res.Events[:1]
	e := &res.Events[0].Event
	if ci := strings.TrimSpace(r.Headers[testCIHeader]); ci != "" {
		e.CI = ci
	} else if strings.TrimSpace(e.CI) == "" {
		e.CI = testEventCI
	}
	e.Title, e.Signal, e.Method = TestEventTitle, "umbrella_test_"+nonce, "other"
	e.Severity, e.Status, e.Key, e.ExternalID = flow.SeverityWarning, flow.StatusFiring, "umbrella-test-"+nonce, "umbrella-test-"+nonce
	if e.Labels == nil {
		e.Labels = map[string]string{}
	}
	delete(e.Labels, "ci")
	e.Labels[alert.TestLabel], e.Labels[alert.TestIDLabel] = "true", nonce
}

// TestEvent sends a test event through the published connector: the request is stored and
// processed by the workers like a delivery from the source, folds into an incident (never sent
// to PagerDuty, resolved after five minutes) and the incident is returned once it exists.
func (a *App) TestEvent(ctx context.Context, actor, id string, in TestEventInput) (TestEventResult, error) {
	var out TestEventResult
	if !a.ingestReady() || a.alerts == nil {
		return out, ErrIngestDown
	}
	ci := strings.TrimSpace(in.CI)
	if len(ci) > 255 {
		return out, invalid("ci_invalid", nil)
	}
	var c model.Connector
	found := false
	a.deps.Store.Read(func(d *store.Data) {
		if p := d.Connectors[id]; p != nil {
			c, found = *p, true
		}
	})
	if !found {
		return out, ErrNotFound
	}
	if c.Published == 0 {
		return out, ErrNotPublished
	}
	var sample []byte
	if len(c.Samples) > 0 {
		sample = c.Samples[0].Body
	}
	nonce := strings.ToLower(rand.Text()[:12])
	headers := map[string]string{"content-type": "application/json", testHeader: nonce, "x-umbrella-test-by": actor}
	if ci != "" {
		headers[testCIHeader] = ci
	}
	body := testBody(c.Preset, nonce, firstSet(ci, testEventCI), sample)
	rid, _, err := a.queue.Enqueue(ctx, ingest.Request{ConnectorID: id, Version: c.Published, RemoteIP: "127.0.0.1", Method: http.MethodPost,
		Headers: headers, Body: body}, "")
	if err != nil {
		return out, fmt.Errorf("%w: %v", ErrIngestDown, err)
	}
	out.RequestID, out.Status = strconv.FormatInt(rid, 10), ingest.StatusPending
	a.deps.Store.Write(func(d *store.Data) {
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "connector.test_event", Object: id, Detail: c.Name + ": request " + out.RequestID})
	})
	deadline := time.Now().Add(a.opt.Ingest.TestEventWait)
	for {
		err := a.alerts.DB().QueryRow(ctx, "SELECT id FROM alerts WHERE doc->'labels'->>'umbrella_test_id' = $1 ORDER BY seq DESC LIMIT 1", nonce).
			Scan(&out.IncidentID)
		if err == nil {
			out.Status = ingest.StatusDone
			return out, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
		req, err := a.queue.Request(ctx, id, rid)
		if err == nil && req.Status == ingest.StatusFailed {
			out.Status, out.Error = req.Status, req.Error
			return out, nil
		}
		if time.Now().After(deadline) {
			return out, nil
		}
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (a *App) registerSources(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/connectors/quick", a.authed(a.can("connectors:edit", a.can("connectors:publish", a.can("credentials:edit", a.quickConnect)))))
	mux.HandleFunc("POST /api/connectors/{id}/test-event", a.authed(a.canAny([]string{"connectors:edit", "monitoring:edit"}, a.testEvent)))
	mux.HandleFunc("PUT /api/monitoring/sources/{id}/connector", a.authed(a.can("monitoring:edit", a.linkSystemConnector)))
}

// canAny lets the request through when the user has one of the permissions.
func (a *App) canAny(perms []string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		have := a.access.Permissions(current(r).user)
		for _, p := range perms {
			if have.Has(p) {
				next(w, r)
				return
			}
		}
		writeError(w, forbidden)
	}
}

func (a *App) quickConnect(w http.ResponseWriter, r *http.Request) {
	var in QuickConnectInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	u := current(r).user
	if in.MonitoringID != "" && !a.access.Permissions(u).Has("monitoring:edit") {
		writeError(w, forbidden)
		return
	}
	out, err := a.QuickConnect(r.Context(), u, a.baseURL(r), in)
	if err != nil {
		if errors.Is(err, ErrSecretsDown) {
			writeError(w, err)
			return
		}
		writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusCreated, out)
}

func (a *App) testEvent(w http.ResponseWriter, r *http.Request) {
	var in TestEventInput
	if r.ContentLength != 0 && !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.TestEvent(r.Context(), current(r).user.Username, r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (a *App) linkSystemConnector(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ConnectorID string `json:"connector_id"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if err := a.monitoring.LinkConnector(current(r).user.Username, r.PathValue("id"), in.ConnectorID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
