package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/ingest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/presets"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const maxImportBytes = 4 << 20

func (a *App) registerConnectors(mux *http.ServeMux) {
	view, edit, publish, payload := "connectors:view", "connectors:edit", "connectors:publish", "connectors:payload"
	mux.HandleFunc("POST /api/ingest/{slug}", a.ingest)
	mux.HandleFunc("PUT /api/ingest/{slug}", a.ingest)

	mux.HandleFunc("GET /api/connectors", a.authed(a.can(view, a.listConnectors)))
	mux.HandleFunc("POST /api/connectors", a.authed(a.can(edit, a.createConnector)))
	mux.HandleFunc("GET /api/connectors/node-types", a.authed(a.can(view, a.nodeTypes)))
	mux.HandleFunc("GET /api/connectors/presets", a.authed(a.can(view, a.listPresets)))
	mux.HandleFunc("GET /api/connectors/credentials", a.authed(a.can(view, a.credentialChoices)))
	mux.HandleFunc("POST /api/connectors/preview", a.authed(a.can(view, a.previewExpression)))
	mux.HandleFunc("POST /api/connectors/import/check", a.authed(a.can(edit, a.checkImport)))
	mux.HandleFunc("POST /api/connectors/import", a.authed(a.can(edit, a.importConnector)))
	mux.HandleFunc("GET /api/connectors/{id}", a.authed(a.can(view, a.getConnector)))
	mux.HandleFunc("PUT /api/connectors/{id}", a.authed(a.can(edit, a.updateConnector)))
	mux.HandleFunc("DELETE /api/connectors/{id}", a.authed(a.can(edit, a.deleteConnector)))
	mux.HandleFunc("PUT /api/connectors/{id}/draft", a.authed(a.can(edit, a.saveDraft)))
	mux.HandleFunc("POST /api/connectors/{id}/lock", a.authed(a.can(edit, a.lockConnector)))
	mux.HandleFunc("DELETE /api/connectors/{id}/lock", a.authed(a.can(edit, a.unlockConnector)))
	mux.HandleFunc("POST /api/connectors/{id}/publish", a.authed(a.can(publish, a.publishConnector)))
	mux.HandleFunc("POST /api/connectors/{id}/unpublish", a.authed(a.can(publish, a.unpublishConnector)))
	mux.HandleFunc("GET /api/connectors/{id}/versions/{n}", a.authed(a.can(view, a.getVersion)))
	mux.HandleFunc("POST /api/connectors/{id}/versions/{n}/restore", a.authed(a.can(edit, a.restoreVersion)))
	mux.HandleFunc("GET /api/connectors/{id}/export", a.authed(a.can(view, a.exportConnector)))
	mux.HandleFunc("GET /api/connectors/{id}/samples", a.authed(a.can(payload, a.listSamples)))
	mux.HandleFunc("GET /api/connectors/{id}/samples/{sid}", a.authed(a.can(payload, a.getSample)))
	mux.HandleFunc("POST /api/connectors/{id}/samples", a.authed(a.can(edit, a.can(payload, a.addSample))))
	mux.HandleFunc("DELETE /api/connectors/{id}/samples/{sid}", a.authed(a.can(edit, a.deleteSample)))
	mux.HandleFunc("POST /api/connectors/{id}/capture", a.authed(a.can(edit, a.can(payload, a.armCapture))))
	mux.HandleFunc("DELETE /api/connectors/{id}/capture", a.authed(a.can(edit, a.disarmCapture)))
	mux.HandleFunc("POST /api/connectors/{id}/test-run", a.authed(a.can(payload, a.testRun)))
	mux.HandleFunc("POST /api/connectors/{id}/test-all", a.authed(a.can(payload, a.testAll)))
	mux.HandleFunc("GET /api/connectors/{id}/requests", a.authed(a.can(payload, a.listRequests)))
	mux.HandleFunc("GET /api/connectors/{id}/requests/{rid}", a.authed(a.can(payload, a.getRequest)))
	mux.HandleFunc("GET /api/connectors/{id}/failures", a.authed(a.can(payload, a.listFailures)))
	mux.HandleFunc("POST /api/connectors/{id}/failures/reprocess", a.authed(a.can(publish, a.reprocessFailures)))
	mux.HandleFunc("GET /api/connectors/{id}/events", a.authed(a.can(view, a.listEvents)))
	mux.HandleFunc("GET /api/connectors/{id}/stats", a.authed(a.can(view, a.connectorStats)))

	mux.HandleFunc("GET /api/credentials", a.authed(a.can("credentials:view", a.listCredentials)))
	mux.HandleFunc("GET /api/credentials/kinds", a.authed(a.can("credentials:view", a.listCredentialKinds)))
	mux.HandleFunc("POST /api/credentials", a.authed(a.can("credentials:edit", a.createCredential)))
	mux.HandleFunc("GET /api/credentials/{id}", a.authed(a.can("credentials:view", a.getCredential)))
	mux.HandleFunc("PUT /api/credentials/{id}", a.authed(a.can("credentials:edit", a.updateCredential)))
	mux.HandleFunc("DELETE /api/credentials/{id}", a.authed(a.can("credentials:edit", a.deleteCredential)))
	a.registerSources(mux)
}

func (a *App) listConnectors(w http.ResponseWriter, r *http.Request) {
	list := a.connectors.List(current(r).user.ID)
	if a.ingestReady() {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		sums, err := a.queue.Summaries(ctx)
		cancel()
		if err != nil {
			slog.Warn("connector summaries", "err", err)
		}
		for i := range list {
			if s, ok := sums[list[i].ID]; ok {
				list[i].Stats = s
			}
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"connectors": list, "ingest": a.ingestReady()})
}

type createConnectorInput struct {
	ConnectorInput
	Credentials map[string]string `json:"credentials"`
}

func (a *App) createConnector(w http.ResponseWriter, r *http.Request) {
	var in createConnectorInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	var doc *flow.Document
	if in.Preset != "" {
		p, ok := presets.Get(in.Preset)
		if !ok {
			httpx.Error(w, http.StatusBadRequest, "unknown_preset", nil)
			return
		}
		doc = &p.Document
	}
	out, err := a.connectors.Create(current(r).user, in.ConnectorInput, doc, in.Credentials)
	reply(w, http.StatusCreated, out, err)
}

func (a *App) getConnector(w http.ResponseWriter, r *http.Request) {
	out, err := a.connectors.Get(r.PathValue("id"), current(r).user.ID)
	reply(w, http.StatusOK, out, err)
}

func (a *App) updateConnector(w http.ResponseWriter, r *http.Request) {
	var in ConnectorInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.connectors.Update(current(r).user, r.PathValue("id"), in)
	reply(w, http.StatusOK, out, err)
}

func (a *App) deleteConnector(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.connectors.Delete(current(r).user, id); err != nil {
		writeError(w, err)
		return
	}
	if a.ingestReady() {
		if err := a.queue.Forget(r.Context(), id); err != nil {
			slog.Warn("connector data not removed", "connector", id, "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) saveDraft(w http.ResponseWriter, r *http.Request) {
	var in DraftInput
	dec := json.NewDecoder(io.LimitReader(r.Body, maxGraphBytes+maxPinsBytes+4096))
	if err := dec.Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", errors.New("invalid JSON body"))
		return
	}
	out, err := a.connectors.SaveDraft(current(r).user, r.PathValue("id"), in)
	reply(w, http.StatusOK, out, err)
}

func (a *App) lockConnector(w http.ResponseWriter, r *http.Request) {
	out, err := a.connectors.Lock(current(r).user, r.PathValue("id"))
	reply(w, http.StatusOK, out, err)
}

func (a *App) unlockConnector(w http.ResponseWriter, r *http.Request) {
	if err := a.connectors.Unlock(current(r).user, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) publishConnector(w http.ResponseWriter, r *http.Request) {
	var in PublishInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.connectors.Publish(current(r).user, r.PathValue("id"), in)
	reply(w, http.StatusOK, out, err)
}

func (a *App) unpublishConnector(w http.ResponseWriter, r *http.Request) {
	out, err := a.connectors.Unpublish(current(r).user, r.PathValue("id"))
	reply(w, http.StatusOK, out, err)
}

func versionParam(r *http.Request) (int, bool) {
	n, err := strconv.Atoi(r.PathValue("n"))
	return n, err == nil && n > 0
}

func (a *App) getVersion(w http.ResponseWriter, r *http.Request) {
	n, ok := versionParam(r)
	if !ok {
		writeError(w, ErrNotFound)
		return
	}
	out, err := a.connectors.Version(r.PathValue("id"), n)
	reply(w, http.StatusOK, out, err)
}

func (a *App) restoreVersion(w http.ResponseWriter, r *http.Request) {
	n, ok := versionParam(r)
	if !ok {
		writeError(w, ErrNotFound)
		return
	}
	out, err := a.connectors.Restore(current(r).user, r.PathValue("id"), n)
	reply(w, http.StatusOK, out, err)
}

func (a *App) nodeTypes(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{"types": flow.Types(), "severities": flow.Severities})
}

type presetView struct {
	ID          string                `json:"id"`
	Title       flow.Text             `json:"title"`
	Description flow.Text             `json:"description"`
	Name        string                `json:"name"`
	Tags        []string              `json:"tags"`
	Credentials []flow.CredentialSlot `json:"credentials"`
	Samples     int                   `json:"samples"`
	// Quick is set for the presets quick connect offers.
	Quick *presets.QuickView `json:"quick,omitempty"`
}

func (a *App) listPresets(w http.ResponseWriter, r *http.Request) {
	out := []presetView{}
	for _, p := range presets.All() {
		out = append(out, presetView{ID: p.ID, Title: p.Title, Description: p.Description, Name: p.Document.Name, Tags: p.Document.Tags,
			Credentials: p.Document.Credentials, Samples: len(p.Document.Samples), Quick: p.Quick.View()})
	}
	httpx.JSON(w, http.StatusOK, out)
}

type credentialChoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// credentialChoices lets connector editors pick a credential without seeing anything else of it.
func (a *App) credentialChoices(w http.ResponseWriter, r *http.Request) {
	out := []credentialChoice{}
	for _, c := range a.creds.List() {
		out = append(out, credentialChoice{ID: c.ID, Name: c.Name, Type: c.Type})
	}
	httpx.JSON(w, http.StatusOK, out)
}

type previewInput struct {
	Kind    string         `json:"kind"`
	Expr    string         `json:"expr"`
	Data    map[string]any `json:"data"`
	Request map[string]any `json:"request"`
}

// previewExpression evaluates a template or CEL expression on the server, with the same engine
// the pipeline uses, so the preview never disagrees with the real run.
func (a *App) previewExpression(w http.ResponseWriter, r *http.Request) {
	var in previewInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	result := func(v any, err error) {
		if err != nil {
			httpx.JSON(w, http.StatusOK, map[string]any{"error": err.Error()})
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"value": v, "text": flow.Stringify(v)})
	}
	switch in.Kind {
	case "template":
		t, err := flow.CompileTemplate(in.Expr)
		if err != nil {
			result(nil, err)
			return
		}
		result(t.Value(in.Data))
	case "cel":
		x, err := flow.CompileExpr(in.Expr)
		if err != nil {
			result(nil, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		result(x.Eval(ctx, flow.Scope{Event: in.Data, Request: in.Request}))
	case "path":
		p, err := flow.ParsePath(in.Expr)
		if err != nil {
			result(nil, err)
			return
		}
		v, ok := p.Get(in.Data)
		if !ok {
			result(nil, fmt.Errorf("no value at %s", p))
			return
		}
		result(v, nil)
	default:
		httpx.Error(w, http.StatusBadRequest, "bad_request", errors.New("kind must be template, cel or path"))
	}
}

func (a *App) exportConnector(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	withSamples := r.URL.Query().Get("samples") == "true"
	if withSamples && !a.access.Permissions(current(r).user).Has("connectors:payload") {
		writeError(w, forbidden)
		return
	}
	var c model.Connector
	found := false
	a.deps.Store.Read(func(d *store.Data) {
		if x := d.Connectors[id]; x != nil {
			c, found = *x, true
		}
	})
	if !found {
		writeError(w, ErrNotFound)
		return
	}
	g, err := flow.ParseGraph(c.Draft.Graph)
	if err != nil {
		writeError(w, err)
		return
	}
	if v := r.URL.Query().Get("version"); v != "" {
		n, _ := strconv.Atoi(v)
		ver, err := a.connectors.Version(id, n)
		if err != nil {
			writeError(w, err)
			return
		}
		if g, err = flow.ParseGraph(ver.Graph); err != nil {
			writeError(w, err)
			return
		}
	}
	graph, slots := flow.Export(g, func(cid string) string {
		if v, err := a.creds.Get(cid); err == nil {
			return v.Name
		}
		return ""
	})
	doc := flow.Document{Format: flow.DocumentFormat, FormatVersion: flow.DocumentVersion, Name: c.Name, Description: c.Description,
		Tags: c.Tags, Graph: graph, Credentials: slots}
	if withSamples {
		for _, sm := range c.Samples {
			doc.Samples = append(doc.Samples, flow.DocSample{Name: sm.Name, Body: string(sm.Body), Headers: sm.Headers})
		}
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="connector-%s.json"`, c.Slug))
	httpx.JSON(w, http.StatusOK, doc)
}

type importInput struct {
	Document    json.RawMessage   `json:"document"`
	Name        string            `json:"name"`
	Slug        string            `json:"slug"`
	Credentials map[string]string `json:"credentials"`
}

func (a *App) readImport(w http.ResponseWriter, r *http.Request) (importInput, flow.Document, bool) {
	var in importInput
	dec := json.NewDecoder(io.LimitReader(r.Body, maxImportBytes))
	if err := dec.Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", errors.New("invalid JSON body"))
		return in, flow.Document{}, false
	}
	doc, err := flow.ParseDocument(in.Document)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "import_invalid", err)
		return in, doc, false
	}
	return in, doc, true
}

type importCheck struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Tags        []string              `json:"tags"`
	Nodes       int                   `json:"nodes"`
	Samples     int                   `json:"samples"`
	Credentials []flow.CredentialSlot `json:"credentials"`
	Choices     []credentialChoice    `json:"choices"`
}

// checkImport reads an export and lists the credential slots to map before importing.
func (a *App) checkImport(w http.ResponseWriter, r *http.Request) {
	_, doc, ok := a.readImport(w, r)
	if !ok {
		return
	}
	out := importCheck{Name: doc.Name, Description: doc.Description, Tags: doc.Tags, Nodes: len(doc.Graph.Nodes), Samples: len(doc.Samples),
		Credentials: doc.Credentials, Choices: []credentialChoice{}}
	if out.Credentials == nil {
		out.Credentials = []flow.CredentialSlot{}
	}
	for _, c := range a.creds.List() {
		out.Choices = append(out.Choices, credentialChoice{ID: c.ID, Name: c.Name, Type: c.Type})
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (a *App) importConnector(w http.ResponseWriter, r *http.Request) {
	in, doc, ok := a.readImport(w, r)
	if !ok {
		return
	}
	if len(doc.Samples) > 0 && !a.access.Permissions(current(r).user).Has("connectors:payload") {
		doc.Samples = nil
	}
	name := in.Name
	if strings.TrimSpace(name) == "" {
		name = doc.Name
	}
	out, err := a.connectors.Create(current(r).user, ConnectorInput{Name: name, Slug: in.Slug, Description: doc.Description, Tags: doc.Tags}, &doc, in.Credentials)
	reply(w, http.StatusCreated, out, err)
}

func (a *App) listSamples(w http.ResponseWriter, r *http.Request) {
	out, err := a.connectors.Samples(r.PathValue("id"))
	reply(w, http.StatusOK, out, err)
}

func (a *App) getSample(w http.ResponseWriter, r *http.Request) {
	sm, err := a.connectors.Sample(r.PathValue("id"), r.PathValue("sid"))
	reply(w, http.StatusOK, sampleView(sm, true), err)
}

type addSampleInput struct {
	Name        string            `json:"name"`
	Body        *string           `json:"body"`
	Headers     map[string]string `json:"headers"`
	FromRequest string            `json:"from_request"`
}

func (a *App) addSample(w http.ResponseWriter, r *http.Request) {
	var in addSampleInput
	dec := json.NewDecoder(io.LimitReader(r.Body, maxSampleBytes+64<<10))
	if err := dec.Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", errors.New("invalid JSON body"))
		return
	}
	id := r.PathValue("id")
	var sm model.Sample
	switch {
	case in.FromRequest != "":
		if !a.ingestReady() {
			httpx.Error(w, http.StatusServiceUnavailable, "ingest_unavailable", nil)
			return
		}
		rid, err := strconv.ParseInt(in.FromRequest, 10, 64)
		if err != nil {
			writeError(w, ErrNotFound)
			return
		}
		req, err := a.queue.Request(r.Context(), id, rid)
		if err != nil {
			writeError(w, err)
			return
		}
		sm = model.Sample{Name: in.Name, Source: SampleSourceRequest, Body: req.Body, Headers: req.Headers, Query: req.Query,
			RemoteIP: req.RemoteIP, Method: req.Method}
		if sm.Name == "" {
			sm.Name = fmt.Sprintf("Request %d", rid)
		}
	case in.Body != nil:
		headers := map[string]string{}
		for k, v := range in.Headers {
			if k = strings.ToLower(strings.TrimSpace(k)); k != "" && !sensitiveHeaders[k] {
				headers[k] = v
			}
		}
		sm = model.Sample{Name: in.Name, Source: SampleSourceManual, Body: []byte(*in.Body), Headers: headers, Method: "POST"}
	default:
		httpx.Error(w, http.StatusBadRequest, "sample_required", nil)
		return
	}
	out, err := a.connectors.AddSample(current(r).user, id, sm)
	reply(w, http.StatusCreated, out, err)
}

func (a *App) deleteSample(w http.ResponseWriter, r *http.Request) {
	if err := a.connectors.DeleteSample(r.PathValue("id"), r.PathValue("sid")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) armCapture(w http.ResponseWriter, r *http.Request) {
	var in CaptureInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.connectors.Arm(current(r).user, r.PathValue("id"), in)
	reply(w, http.StatusOK, out, err)
}

func (a *App) disarmCapture(w http.ResponseWriter, r *http.Request) {
	if err := a.connectors.Disarm(r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) testRun(w http.ResponseWriter, r *http.Request) {
	var in TestRunInput
	dec := json.NewDecoder(io.LimitReader(r.Body, maxGraphBytes+maxPinsBytes+maxSampleBytes+64<<10))
	if err := dec.Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", errors.New("invalid JSON body"))
		return
	}
	out, err := a.connectors.TestRun(r.Context(), r.PathValue("id"), in)
	reply(w, http.StatusOK, out, err)
}

func (a *App) testAll(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Graph json.RawMessage `json:"graph"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxGraphBytes+4096))
	if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", errors.New("invalid JSON body"))
		return
	}
	out, err := a.connectors.TestAll(r.Context(), r.PathValue("id"), in.Graph)
	reply(w, http.StatusOK, out, err)
}

// withQueue answers 503 when the ingest tables are not available, and 404 for unknown connectors.
func (a *App) withQueue(w http.ResponseWriter, r *http.Request) bool {
	if _, err := a.connectors.Get(r.PathValue("id"), ""); err != nil {
		writeError(w, err)
		return false
	}
	if !a.ingestReady() {
		httpx.Error(w, http.StatusServiceUnavailable, "ingest_unavailable", nil)
		return false
	}
	return true
}

func limitParam(r *http.Request, def, max int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return def
	}
	return min(n, max)
}

func (a *App) listRequests(w http.ResponseWriter, r *http.Request) {
	if !a.withQueue(w, r) {
		return
	}
	out, err := a.queue.Requests(r.Context(), r.PathValue("id"), r.URL.Query().Get("status"), limitParam(r, 100, 500))
	if out == nil {
		out = []ingest.Request{}
	}
	reply(w, http.StatusOK, out, err)
}

type requestView struct {
	ingest.Request
	Body   string `json:"body"`
	Format string `json:"format"`
}

func (a *App) getRequest(w http.ResponseWriter, r *http.Request) {
	if !a.withQueue(w, r) {
		return
	}
	rid, err := strconv.ParseInt(r.PathValue("rid"), 10, 64)
	if err != nil {
		writeError(w, ErrNotFound)
		return
	}
	req, err := a.queue.Request(r.Context(), r.PathValue("id"), rid)
	reply(w, http.StatusOK, requestView{Request: req, Body: string(req.Body), Format: flow.Sniff(req.Body)}, err)
}

func (a *App) listFailures(w http.ResponseWriter, r *http.Request) {
	if !a.withQueue(w, r) {
		return
	}
	out, err := a.queue.Failures(r.Context(), r.PathValue("id"), r.URL.Query().Get("resolved") == "true", limitParam(r, 100, 500))
	if out == nil {
		out = []ingest.Failure{}
	}
	reply(w, http.StatusOK, out, err)
}

func (a *App) reprocessFailures(w http.ResponseWriter, r *http.Request) {
	if !a.withQueue(w, r) {
		return
	}
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	c, _ := a.connectors.Get(id, "")
	if c.Published == 0 {
		httpx.Error(w, http.StatusConflict, "not_published", nil)
		return
	}
	if in.IDs == nil {
		in.IDs = []int64{}
	}
	out, err := a.queue.Reprocess(r.Context(), id, in.IDs, c.Published)
	if err == nil {
		a.deps.Store.Write(func(d *store.Data) {
			d.AddAudit(store.AuditEntry{Actor: current(r).user.Username, Action: "connector.reprocess", Object: id,
				Detail: fmt.Sprintf("%s: %d requests on version %d", c.Name, out.Requeued, c.Published)})
		})
	}
	reply(w, http.StatusOK, out, err)
}

func (a *App) listEvents(w http.ResponseWriter, r *http.Request) {
	if !a.withQueue(w, r) {
		return
	}
	out, err := a.queue.Events(r.Context(), r.PathValue("id"), limitParam(r, 100, 500))
	if out == nil {
		out = []ingest.EventRow{}
	}
	reply(w, http.StatusOK, out, err)
}

func (a *App) connectorStats(w http.ResponseWriter, r *http.Request) {
	if !a.withQueue(w, r) {
		return
	}
	hours, err := strconv.Atoi(r.URL.Query().Get("hours"))
	if err != nil || hours <= 0 || hours > 24*30 {
		hours = 24
	}
	step := time.Duration(max(1, hours*60/96)) * time.Minute
	out, err := a.queue.Stats(r.Context(), r.PathValue("id"), time.Now().Add(-time.Duration(hours)*time.Hour), step)
	if out == nil {
		out = []ingest.Bucket{}
	}
	reply(w, http.StatusOK, map[string]any{"step_minutes": int(step.Minutes()), "buckets": out}, err)
}

func (a *App) listCredentials(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, a.creds.List())
}

func (a *App) getCredential(w http.ResponseWriter, r *http.Request) {
	out, err := a.creds.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (a *App) createCredential(w http.ResponseWriter, r *http.Request) {
	var in CredentialInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.creds.Create(r.Context(), current(r).user.Username, in)
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (a *App) updateCredential(w http.ResponseWriter, r *http.Request) {
	var in CredentialInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := a.creds.Update(r.Context(), current(r).user.Username, r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (a *App) deleteCredential(w http.ResponseWriter, r *http.Request) {
	if err := a.creds.Delete(r.Context(), current(r).user.Username, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
