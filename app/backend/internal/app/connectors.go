package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/presets"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	maxConnectorName  = 120
	maxGraphBytes     = 512 << 10
	maxPinsBytes      = 1 << 20
	maxSamples        = 50
	maxSampleBytes    = 512 << 10
	maxVersions       = 200
	maxCaptureCount   = 20
	maxCaptureMinutes = 60
	lockLease         = 60 * time.Second
	testRunTimeout    = 10 * time.Second

	StatusDraft         = "draft"
	StatusPublished     = "published"
	StatusChanged       = "changed"
	StatusPublishFailed = "publish_failed"
)

var (
	ErrSlugTaken     = errors.New("a connector with this address already exists")
	ErrDraftConflict = errors.New("the draft was changed by someone else")
	ErrLocked        = errors.New("someone else is editing the connector")
	ErrPublishFailed = errors.New("the draft has errors")
	ErrTooMany       = errors.New("too many items")

	slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
)

// PublishError carries the issues that stopped a publish, so the editor can mark the nodes.
type PublishError struct{ Issues []flow.Issue }

func (e *PublishError) Error() string { return ErrPublishFailed.Error() }
func (e *PublishError) Unwrap() error { return ErrPublishFailed }

// LockError names who holds the edit lock.
type LockError struct{ Lock LockView }

func (e *LockError) Error() string { return ErrLocked.Error() }
func (e *LockError) Unwrap() error { return ErrLocked }

type ConnectorInput struct {
	Name        string   `json:"name"`
	Slug        string   `json:"slug"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Preset      string   `json:"preset,omitempty"`
}

type LockView struct {
	UserID   string    `json:"user_id"`
	Username string    `json:"username"`
	Name     string    `json:"name"`
	Until    time.Time `json:"until"`
	Mine     bool      `json:"mine"`
}

type CaptureView struct {
	Remaining int       `json:"remaining"`
	Until     time.Time `json:"until"`
	By        string    `json:"by"`
}

type ConnectorSummary struct {
	ID          string       `json:"id"`
	Slug        string       `json:"slug"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Tags        []string     `json:"tags"`
	Preset      string       `json:"preset,omitempty"`
	Status      string       `json:"status"`
	Published   int          `json:"published"`
	PublishedAt *time.Time   `json:"published_at,omitempty"`
	PublishedBy string       `json:"published_by,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	IngestPath  string       `json:"ingest_path"`
	Lock        *LockView    `json:"lock,omitempty"`
	Capture     *CaptureView `json:"capture,omitempty"`
	Samples     int          `json:"samples"`
	Stats       any          `json:"stats,omitempty"`
}

type DraftView struct {
	Graph     json.RawMessage `json:"graph"`
	Pins      json.RawMessage `json:"pins"`
	Revision  int             `json:"revision"`
	UpdatedAt time.Time       `json:"updated_at"`
	UpdatedBy string          `json:"updated_by"`
}

type VersionView struct {
	Number    int             `json:"number"`
	Name      string          `json:"name"`
	Comment   string          `json:"comment"`
	CreatedAt time.Time       `json:"created_at"`
	CreatedBy string          `json:"created_by"`
	Current   bool            `json:"current"`
	Graph     json.RawMessage `json:"graph,omitempty"`
}

type SampleView struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Source    string            `json:"source"`
	Size      int               `json:"size"`
	Format    string            `json:"format"`
	Method    string            `json:"method,omitempty"`
	RemoteIP  string            `json:"remote_ip,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	CreatedBy string            `json:"created_by"`
	Body      *string           `json:"body,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Query     map[string]string `json:"query,omitempty"`
}

type ConnectorView struct {
	ConnectorSummary
	Draft        DraftView     `json:"draft"`
	Issues       []flow.Issue  `json:"issues"`
	Versions     []VersionView `json:"versions"`
	PublishError string        `json:"publish_error,omitempty"`
}

type ConnectorsService struct {
	st    *store.Store
	creds *CredentialsService
	now   func() time.Time

	mu    sync.Mutex
	cache map[string]*flow.Pipeline
}

func NewConnectorsService(st *store.Store, creds *CredentialsService) *ConnectorsService {
	return &ConnectorsService{st: st, creds: creds, now: func() time.Time { return time.Now().UTC() }, cache: map[string]*flow.Pipeline{}}
}

func (s *ConnectorsService) compileOptions(partial bool) flow.CompileOptions {
	return flow.CompileOptions{Partial: partial, Credential: s.creds.TypeOf}
}

// graphHash ignores where nodes sit on the canvas: moving a node does not make a published
// connector "changed".
func graphHash(b []byte) string {
	g, err := flow.ParseGraph(b)
	if err != nil {
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}
	for i := range g.Nodes {
		g.Nodes[i].Position = flow.Position{}
	}
	sum := sha256.Sum256(g.JSON())
	return hex.EncodeToString(sum[:])
}

func versionOf(c *model.Connector, n int) *model.ConnectorVersion {
	for i := range c.Versions {
		if c.Versions[i].Number == n {
			return &c.Versions[i]
		}
	}
	return nil
}

func statusOf(c *model.Connector) string {
	switch {
	case c.PublishError != "":
		return StatusPublishFailed
	case c.Published == 0:
		return StatusDraft
	}
	if v := versionOf(c, c.Published); v != nil && v.Hash == graphHash(c.Draft.Graph) {
		return StatusPublished
	}
	return StatusChanged
}

func (s *ConnectorsService) lockView(l *model.EditLock, me string) *LockView {
	if l == nil || !l.Until.After(s.now()) {
		return nil
	}
	return &LockView{UserID: l.UserID, Username: l.Username, Name: l.Name, Until: l.Until, Mine: l.UserID == me}
}

func (s *ConnectorsService) summary(c *model.Connector, me string) ConnectorSummary {
	out := ConnectorSummary{ID: c.ID, Slug: c.Slug, Name: c.Name, Description: c.Description, Tags: slices.Clone(c.Tags), Preset: c.Preset,
		Status: statusOf(c), Published: c.Published, PublishedAt: c.PublishedAt, PublishedBy: c.PublishedBy, CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt, IngestPath: "/api/ingest/" + c.Slug, Lock: s.lockView(c.Lock, me), Samples: len(c.Samples)}
	if out.Tags == nil {
		out.Tags = []string{}
	}
	if cp := c.Capture; cp != nil && cp.Remaining > 0 && cp.Until.After(s.now()) {
		out.Capture = &CaptureView{Remaining: cp.Remaining, Until: cp.Until, By: cp.By}
	}
	return out
}

func rawOrEmpty(b []byte, empty string) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage(empty)
	}
	return json.RawMessage(b)
}

func (s *ConnectorsService) view(c *model.Connector, me string) ConnectorView {
	out := ConnectorView{ConnectorSummary: s.summary(c, me), PublishError: c.PublishError, Versions: []VersionView{}}
	out.Draft = DraftView{Graph: rawOrEmpty(c.Draft.Graph, `{"nodes":[],"edges":[]}`), Pins: rawOrEmpty(c.Draft.Pins, "{}"),
		Revision: c.Draft.Revision, UpdatedAt: c.Draft.UpdatedAt, UpdatedBy: c.Draft.UpdatedBy}
	for i := len(c.Versions) - 1; i >= 0; i-- {
		v := c.Versions[i]
		out.Versions = append(out.Versions, VersionView{Number: v.Number, Name: v.Name, Comment: v.Comment, CreatedAt: v.CreatedAt,
			CreatedBy: v.CreatedBy, Current: v.Number == c.Published})
	}
	return out
}

func (s *ConnectorsService) issues(graph []byte) []flow.Issue {
	g, err := flow.ParseGraph(graph)
	if err != nil {
		return []flow.Issue{{Level: flow.LevelError, Code: "graph", Message: err.Error()}}
	}
	_, issues := flow.Compile(g, s.compileOptions(false))
	if issues == nil {
		issues = []flow.Issue{}
	}
	return issues
}

func (s *ConnectorsService) List(me string) []ConnectorSummary {
	out := []ConnectorSummary{}
	s.st.Read(func(d *store.Data) {
		for _, c := range d.Connectors {
			out = append(out, s.summary(c, me))
		}
	})
	slices.SortFunc(out, func(a, b ConnectorSummary) int { return byName(a.Name, b.Name) })
	return out
}

func (s *ConnectorsService) Get(id, me string) (ConnectorView, error) {
	var out ConnectorView
	var graph []byte
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		if c := d.Connectors[id]; c != nil {
			out, graph, err = s.view(c, me), c.Draft.Graph, nil
		}
	})
	if err == nil {
		out.Issues = s.issues(graph)
	}
	return out, err
}

func (in *ConnectorInput) normalize() error {
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	in.Description = strings.TrimSpace(in.Description)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > maxConnectorName {
		return invalid("invalid_connector_name", nil)
	}
	if !slugPattern.MatchString(in.Slug) {
		return invalid("invalid_connector_slug", nil)
	}
	if utf8.RuneCountInString(in.Description) > maxServiceDescription {
		return invalid("invalid_connector_description", nil)
	}
	tags, err := normalizeTags(in.Tags)
	if err != nil {
		return err
	}
	in.Tags = tags
	return nil
}

func normalizeTags(in []string) ([]string, error) {
	out := []string{}
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || slices.Contains(out, t) {
			continue
		}
		if utf8.RuneCountInString(t) > maxServiceTag || strings.ContainsAny(t, " \t\n") {
			return nil, invalid("invalid_connector_tags", nil)
		}
		out = append(out, t)
	}
	if len(out) > maxServiceTags {
		return nil, invalid("invalid_connector_tags", nil)
	}
	slices.Sort(out)
	return out, nil
}

func slugTaken(d *store.Data, slug, except string) bool {
	for _, c := range d.Connectors {
		if c.ID != except && c.Slug == slug {
			return true
		}
	}
	return false
}

// StarterGraph is a new connector's draft: webhook → JSON → event mapping → event, the
// starter graph of the presets.
func StarterGraph() flow.Graph { return presets.StarterGraph() }

// Create makes a connector from a document (a preset or an import) or from the starter graph.
func (s *ConnectorsService) Create(actor model.User, in ConnectorInput, doc *flow.Document, mapping map[string]string) (ConnectorView, error) {
	if err := in.normalize(); err != nil {
		return ConnectorView{}, err
	}
	graph := StarterGraph()
	var samples []model.Sample
	if doc != nil {
		g, err := doc.Apply(mapping, s.creds.TypeOf)
		if err != nil {
			return ConnectorView{}, invalid("invalid_credential_mapping", err)
		}
		graph = g
		for _, ds := range doc.Samples {
			if len(samples) == maxSamples {
				break
			}
			samples = append(samples, model.Sample{Name: ds.Name, Source: SampleSourceImport, Body: []byte(ds.Body), Headers: ds.Headers})
		}
	}
	var out ConnectorView
	var err error
	s.st.Write(func(d *store.Data) {
		if slugTaken(d, in.Slug, "") {
			err = ErrSlugTaken
			return
		}
		now := s.now()
		c := &model.Connector{ID: d.NextID("CON"), Slug: in.Slug, Name: in.Name, Description: in.Description, Tags: in.Tags,
			Preset: in.Preset, CreatedAt: now, CreatedBy: actor.Username, UpdatedAt: now,
			Draft: model.ConnectorDraft{Graph: graph.JSON(), Revision: 1, UpdatedAt: now, UpdatedBy: actor.Username}}
		for _, sm := range samples {
			sm.ID = d.NextID("SMP")
			sm.CreatedAt, sm.CreatedBy = now, actor.Username
			c.Samples = append(c.Samples, sm)
		}
		d.Connectors[c.ID] = c
		d.AddAudit(store.AuditEntry{Actor: actor.Username, Action: "connector.create", Object: c.ID, Detail: c.Name})
		out = s.view(c, actor.ID)
	})
	if err == nil {
		out.Issues = s.issues(out.Draft.Graph)
	}
	return out, err
}

func (s *ConnectorsService) Update(actor model.User, id string, in ConnectorInput) (ConnectorView, error) {
	if err := in.normalize(); err != nil {
		return ConnectorView{}, err
	}
	var out ConnectorView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		c := d.Connectors[id]
		if c == nil {
			return
		}
		if slugTaken(d, in.Slug, id) {
			err = ErrSlugTaken
			return
		}
		err = nil
		var changed []string
		if c.Name != in.Name {
			changed = append(changed, "name")
		}
		if c.Slug != in.Slug {
			changed = append(changed, "address "+c.Slug+" → "+in.Slug)
		}
		if c.Description != in.Description {
			changed = append(changed, "description")
		}
		if !slices.Equal(c.Tags, in.Tags) {
			changed = append(changed, "tags")
		}
		c.Name, c.Slug, c.Description, c.Tags = in.Name, in.Slug, in.Description, in.Tags
		if len(changed) > 0 {
			c.UpdatedAt = s.now()
			d.AddAudit(store.AuditEntry{Actor: actor.Username, Action: "connector.update", Object: id, Detail: c.Name + ": " + strings.Join(changed, ", ")})
		}
		out = s.view(c, actor.ID)
	})
	if err == nil {
		out.Issues = s.issues(out.Draft.Graph)
	}
	return out, err
}

func (s *ConnectorsService) Delete(actor model.User, id string) error {
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		c := d.Connectors[id]
		if c == nil {
			return
		}
		if l := s.lockView(c.Lock, actor.ID); l != nil && !l.Mine {
			err = &LockError{Lock: *l}
			return
		}
		err = nil
		delete(d.Connectors, id)
		d.AddAudit(store.AuditEntry{Actor: actor.Username, Action: "connector.delete", Object: id, Detail: c.Name})
	})
	if err == nil {
		s.mu.Lock()
		for k := range s.cache {
			if strings.HasPrefix(k, id+"@") {
				delete(s.cache, k)
			}
		}
		s.mu.Unlock()
	}
	return err
}

// editable checks the edit lock: another user's live lock blocks changes.
func (s *ConnectorsService) editable(c *model.Connector, user model.User) error {
	if l := s.lockView(c.Lock, user.ID); l != nil && !l.Mine {
		return &LockError{Lock: *l}
	}
	return nil
}

func (s *ConnectorsService) Lock(user model.User, id string) (LockView, error) {
	var out LockView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		c := d.Connectors[id]
		if c == nil {
			return
		}
		if err = s.editable(c, user); err != nil {
			return
		}
		c.Lock = &model.EditLock{UserID: user.ID, Username: user.Username, Name: user.Name, Until: s.now().Add(lockLease)}
		out = *s.lockView(c.Lock, user.ID)
	})
	return out, err
}

func (s *ConnectorsService) Unlock(user model.User, id string) error {
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		c := d.Connectors[id]
		if c == nil {
			return
		}
		err = nil
		if c.Lock != nil && c.Lock.UserID == user.ID {
			c.Lock = nil
		}
	})
	return err
}

type DraftInput struct {
	Graph    json.RawMessage `json:"graph"`
	Pins     json.RawMessage `json:"pins"`
	Revision int             `json:"revision"`
}

type DraftSaved struct {
	Revision int          `json:"revision"`
	Status   string       `json:"status"`
	Issues   []flow.Issue `json:"issues"`
}

// SaveDraft stores the editor's graph. The revision is an optimistic lock: a save based on an
// older revision is refused instead of silently overwriting someone else's change.
func (s *ConnectorsService) SaveDraft(user model.User, id string, in DraftInput) (DraftSaved, error) {
	if len(in.Graph) > maxGraphBytes || len(in.Pins) > maxPinsBytes {
		return DraftSaved{}, invalid("graph_too_large", nil)
	}
	g, err := flow.ParseGraph(in.Graph)
	if err != nil {
		return DraftSaved{}, invalid("invalid_graph", err)
	}
	pins := []byte(nil)
	if len(in.Pins) > 0 && string(in.Pins) != "null" {
		if _, perr := flow.ParsePins(in.Pins); perr != nil {
			return DraftSaved{}, invalid("invalid_pins", perr)
		}
		pins = slices.Clone([]byte(in.Pins))
	}
	graph := g.JSON()
	var out DraftSaved
	err = ErrNotFound
	s.st.Write(func(d *store.Data) {
		c := d.Connectors[id]
		if c == nil {
			return
		}
		if err = s.editable(c, user); err != nil {
			return
		}
		if in.Revision != c.Draft.Revision {
			err = ErrDraftConflict
			return
		}
		now := s.now()
		c.Draft = model.ConnectorDraft{Graph: graph, Pins: pins, Revision: c.Draft.Revision + 1, UpdatedAt: now, UpdatedBy: user.Username}
		c.UpdatedAt = now
		if c.Lock != nil && c.Lock.UserID == user.ID {
			c.Lock.Until = now.Add(lockLease)
		}
		out = DraftSaved{Revision: c.Draft.Revision, Status: statusOf(c)}
	})
	if err == nil {
		out.Issues = s.issues(graph)
	}
	return out, err
}

type PublishInput struct {
	Revision int    `json:"revision"`
	Name     string `json:"name"`
	Comment  string `json:"comment"`
}

// Publish compiles the draft and, when it is valid, makes it the version that processes new
// requests. Requests already received keep the version they arrived for.
func (s *ConnectorsService) Publish(user model.User, id string, in PublishInput) (ConnectorView, error) {
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	in.Comment = strings.TrimSpace(in.Comment)
	if utf8.RuneCountInString(in.Name) > maxConnectorName || utf8.RuneCountInString(in.Comment) > maxServiceDescription {
		return ConnectorView{}, invalid("invalid_version_name", nil)
	}
	var graph []byte
	var rev int
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		if c := d.Connectors[id]; c != nil {
			graph, rev, err = slices.Clone(c.Draft.Graph), c.Draft.Revision, nil
		}
	})
	if err != nil {
		return ConnectorView{}, err
	}
	if in.Revision != 0 && in.Revision != rev {
		return ConnectorView{}, ErrDraftConflict
	}
	g, perr := flow.ParseGraph(graph)
	var issues []flow.Issue
	if perr != nil {
		issues = []flow.Issue{{Level: flow.LevelError, Code: "graph", Message: perr.Error()}}
	} else {
		_, issues = flow.Compile(g, s.compileOptions(false))
	}
	var out ConnectorView
	s.st.Write(func(d *store.Data) {
		c := d.Connectors[id]
		if c == nil {
			err = ErrNotFound
			return
		}
		if c.Draft.Revision != rev {
			err = ErrDraftConflict
			return
		}
		if flow.HasErrors(issues) {
			for _, i := range issues {
				if i.Level == flow.LevelError {
					c.PublishError = i.Error()
					break
				}
			}
			d.AddAudit(store.AuditEntry{Actor: user.Username, Action: "connector.publish_failed", Object: id, Detail: c.Name + ": " + c.PublishError})
			err = &PublishError{Issues: issues}
			return
		}
		now := s.now()
		num := 1
		if n := len(c.Versions); n > 0 {
			num = c.Versions[n-1].Number + 1
		}
		c.Versions = append(c.Versions, model.ConnectorVersion{Number: num, Graph: graph, Hash: graphHash(graph), Name: in.Name,
			Comment: in.Comment, CreatedAt: now, CreatedBy: user.Username})
		c.Versions = pruneVersions(c.Versions, num)
		c.Published, c.PublishedAt, c.PublishedBy, c.PublishError = num, &now, user.Username, ""
		c.UpdatedAt = now
		d.AddAudit(store.AuditEntry{Actor: user.Username, Action: "connector.publish", Object: id, Detail: fmt.Sprintf("%s: version %d", c.Name, num)})
		out = s.view(c, user.ID)
	})
	if err == nil {
		out.Issues = issues
		if out.Issues == nil {
			out.Issues = []flow.Issue{}
		}
	}
	return out, err
}

// pruneVersions drops the oldest unnamed versions beyond the limit; named and current ones stay.
func pruneVersions(vs []model.ConnectorVersion, current int) []model.ConnectorVersion {
	for len(vs) > maxVersions {
		i := slices.IndexFunc(vs, func(v model.ConnectorVersion) bool { return v.Name == "" && v.Number != current })
		if i < 0 {
			break
		}
		vs = slices.Delete(vs, i, i+1)
	}
	return vs
}

func (s *ConnectorsService) Unpublish(user model.User, id string) (ConnectorView, error) {
	var out ConnectorView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		c := d.Connectors[id]
		if c == nil {
			return
		}
		err = nil
		if c.Published != 0 {
			d.AddAudit(store.AuditEntry{Actor: user.Username, Action: "connector.unpublish", Object: id, Detail: fmt.Sprintf("%s: version %d", c.Name, c.Published)})
		}
		c.Published, c.PublishedAt, c.PublishedBy = 0, nil, ""
		c.UpdatedAt = s.now()
		out = s.view(c, user.ID)
	})
	if err == nil {
		out.Issues = s.issues(out.Draft.Graph)
	}
	return out, err
}

func (s *ConnectorsService) Version(id string, n int) (VersionView, error) {
	var out VersionView
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		c := d.Connectors[id]
		if c == nil {
			return
		}
		if v := versionOf(c, n); v != nil {
			out = VersionView{Number: v.Number, Name: v.Name, Comment: v.Comment, CreatedAt: v.CreatedAt, CreatedBy: v.CreatedBy,
				Current: v.Number == c.Published, Graph: slices.Clone(v.Graph)}
			err = nil
		}
	})
	return out, err
}

// Restore copies a version into the draft. Publishing it again rolls the connector back.
func (s *ConnectorsService) Restore(user model.User, id string, n int) (ConnectorView, error) {
	var out ConnectorView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		c := d.Connectors[id]
		if c == nil {
			return
		}
		v := versionOf(c, n)
		if v == nil {
			return
		}
		if err = s.editable(c, user); err != nil {
			return
		}
		now := s.now()
		c.Draft = model.ConnectorDraft{Graph: slices.Clone(v.Graph), Pins: c.Draft.Pins, Revision: c.Draft.Revision + 1, UpdatedAt: now, UpdatedBy: user.Username}
		c.UpdatedAt = now
		d.AddAudit(store.AuditEntry{Actor: user.Username, Action: "connector.restore", Object: id, Detail: fmt.Sprintf("%s: version %d into the draft", c.Name, n)})
		out = s.view(c, user.ID)
	})
	if err == nil {
		out.Issues = s.issues(out.Draft.Graph)
	}
	return out, err
}

// Pipeline returns the compiled version, compiling it once.
func (s *ConnectorsService) Pipeline(id string, version int) (*flow.Pipeline, error) {
	key := fmt.Sprintf("%s@%d", id, version)
	s.mu.Lock()
	p := s.cache[key]
	s.mu.Unlock()
	if p != nil {
		return p, nil
	}
	var graph []byte
	s.st.Read(func(d *store.Data) {
		if c := d.Connectors[id]; c != nil {
			if v := versionOf(c, version); v != nil {
				graph = v.Graph
			}
		}
	})
	if graph == nil {
		return nil, fmt.Errorf("version %d of connector %s does not exist", version, id)
	}
	g, err := flow.ParseGraph(graph)
	if err != nil {
		return nil, err
	}
	p, issues := flow.Compile(g, s.compileOptions(false))
	if p == nil {
		for _, i := range issues {
			if i.Level == flow.LevelError {
				return nil, fmt.Errorf("version %d does not compile: %s", version, i.Error())
			}
		}
	}
	s.mu.Lock()
	s.cache[key] = p
	s.mu.Unlock()
	return p, nil
}

// Target is what the intake needs to accept a request for a connector address.
type Target struct {
	ConnectorID string
	Version     int
	Pipeline    *flow.Pipeline
	Webhook     *flow.Webhook
	Capturing   bool
}

func (s *ConnectorsService) Target(slug string) (Target, error) {
	var t Target
	var draft []byte
	found := false
	s.st.Read(func(d *store.Data) {
		for _, c := range d.Connectors {
			if c.Slug != slug {
				continue
			}
			found = true
			t.ConnectorID, t.Version = c.ID, c.Published
			t.Capturing = c.Capture != nil && c.Capture.Remaining > 0 && c.Capture.Until.After(s.now())
			draft = c.Draft.Graph
			return
		}
	})
	if !found {
		return t, ErrNotFound
	}
	if t.Version != 0 {
		p, err := s.Pipeline(t.ConnectorID, t.Version)
		if err != nil {
			return t, err
		}
		t.Pipeline, t.Webhook = p, p.Webhook()
		return t, nil
	}
	if !t.Capturing {
		return t, ErrNotFound
	}
	g, err := flow.ParseGraph(draft)
	if err != nil {
		return t, err
	}
	w, issues := flow.CompileWebhook(g, s.compileOptions(true))
	if w == nil {
		return t, &PublishError{Issues: issues}
	}
	t.Webhook = w
	return t, nil
}

const (
	SampleSourceManual  = "manual"
	SampleSourceCapture = "capture"
	SampleSourceRequest = "request"
	SampleSourceImport  = "import"
)

func sampleView(sm model.Sample, full bool) SampleView {
	v := SampleView{ID: sm.ID, Name: sm.Name, Source: sm.Source, Size: len(sm.Body), Format: flow.Sniff(sm.Body), Method: sm.Method,
		RemoteIP: sm.RemoteIP, CreatedAt: sm.CreatedAt, CreatedBy: sm.CreatedBy}
	if full {
		body := string(sm.Body)
		v.Body, v.Headers, v.Query = &body, sm.Headers, sm.Query
	}
	return v
}

func (s *ConnectorsService) Samples(id string) ([]SampleView, error) {
	out := []SampleView{}
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		if c := d.Connectors[id]; c != nil {
			err = nil
			for i := len(c.Samples) - 1; i >= 0; i-- {
				out = append(out, sampleView(c.Samples[i], false))
			}
		}
	})
	return out, err
}

func (s *ConnectorsService) Sample(id, sampleID string) (model.Sample, error) {
	var out model.Sample
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		if c := d.Connectors[id]; c != nil {
			for _, sm := range c.Samples {
				if sm.ID == sampleID {
					out, err = sm, nil
				}
			}
		}
	})
	return out, err
}

func (s *ConnectorsService) AddSample(user model.User, id string, sm model.Sample) (SampleView, error) {
	sm.Name = strings.Join(strings.Fields(sm.Name), " ")
	if utf8.RuneCountInString(sm.Name) > maxConnectorName {
		return SampleView{}, invalid("invalid_sample_name", nil)
	}
	if len(sm.Body) > maxSampleBytes {
		return SampleView{}, invalid("sample_too_large", nil)
	}
	var out SampleView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		c := d.Connectors[id]
		if c == nil {
			return
		}
		err = nil
		sm.ID = d.NextID("SMP")
		if sm.Name == "" {
			sm.Name = fmt.Sprintf("%s %s", sm.Source, s.now().Format("2006-01-02 15:04:05"))
		}
		sm.CreatedAt, sm.CreatedBy = s.now(), user.Username
		c.Samples = append(c.Samples, sm)
		if len(c.Samples) > maxSamples {
			c.Samples = slices.Clone(c.Samples[len(c.Samples)-maxSamples:])
		}
		out = sampleView(sm, false)
	})
	return out, err
}

func (s *ConnectorsService) DeleteSample(id, sampleID string) error {
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		c := d.Connectors[id]
		if c == nil {
			return
		}
		i := slices.IndexFunc(c.Samples, func(sm model.Sample) bool { return sm.ID == sampleID })
		if i < 0 {
			return
		}
		c.Samples = slices.Delete(c.Samples, i, i+1)
		err = nil
	})
	return err
}

type CaptureInput struct {
	Count   int `json:"count"`
	Minutes int `json:"minutes"`
}

// Arm makes the next requests to the address become samples. It works for unpublished
// connectors too, so the source can be pointed at Umbrella before the mapping exists.
func (s *ConnectorsService) Arm(user model.User, id string, in CaptureInput) (*CaptureView, error) {
	if in.Count == 0 {
		in.Count = 1
	}
	if in.Minutes == 0 {
		in.Minutes = 10
	}
	if in.Count < 1 || in.Count > maxCaptureCount || in.Minutes < 1 || in.Minutes > maxCaptureMinutes {
		return nil, invalid("invalid_capture", nil)
	}
	var out *CaptureView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		c := d.Connectors[id]
		if c == nil {
			return
		}
		err = nil
		c.Capture = &model.Capture{Remaining: in.Count, Until: s.now().Add(time.Duration(in.Minutes) * time.Minute), By: user.Username}
		out = &CaptureView{Remaining: c.Capture.Remaining, Until: c.Capture.Until, By: c.Capture.By}
	})
	return out, err
}

func (s *ConnectorsService) Disarm(id string) error {
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		if c := d.Connectors[id]; c != nil {
			c.Capture, err = nil, nil
		}
	})
	return err
}

// captured stores a request as a sample while capture is armed.
func (s *ConnectorsService) captured(id string, sm model.Sample) {
	s.st.Write(func(d *store.Data) {
		c := d.Connectors[id]
		if c == nil || c.Capture == nil || c.Capture.Remaining <= 0 || !c.Capture.Until.After(s.now()) {
			return
		}
		sm.ID = d.NextID("SMP")
		sm.Source = SampleSourceCapture
		sm.CreatedAt, sm.CreatedBy = s.now(), c.Capture.By
		sm.Name = "Captured " + sm.CreatedAt.Format("2006-01-02 15:04:05")
		c.Samples = append(c.Samples, sm)
		if len(c.Samples) > maxSamples {
			c.Samples = slices.Clone(c.Samples[len(c.Samples)-maxSamples:])
		}
		c.Capture.Remaining--
		if c.Capture.Remaining == 0 {
			c.Capture = nil
		}
	})
}

type TestRunInput struct {
	Graph    json.RawMessage   `json:"graph"`
	Pins     json.RawMessage   `json:"pins"`
	SampleID string            `json:"sample_id"`
	Body     *string           `json:"body"`
	Headers  map[string]string `json:"headers"`
	StopAt   string            `json:"stop_at"`
	UsePins  bool              `json:"use_pins"`
}

type TestRunResult struct {
	Issues []flow.Issue   `json:"issues"`
	Result *flow.Result   `json:"result"`
	Events []EventPreview `json:"events"`
}

// EventPreview is an event as the test run would hand it over, without writing anything.
type EventPreview struct {
	flow.Event
	Item int    `json:"item"`
	Node string `json:"node"`
}

func (s *ConnectorsService) graphFor(id string, in json.RawMessage) (flow.Graph, []byte, error) {
	var draft, pins []byte
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		if c := d.Connectors[id]; c != nil {
			draft, pins, err = c.Draft.Graph, c.Draft.Pins, nil
		}
	})
	if err != nil {
		return flow.Graph{}, nil, err
	}
	if len(in) > 0 && string(in) != "null" {
		if len(in) > maxGraphBytes {
			return flow.Graph{}, nil, invalid("graph_too_large", nil)
		}
		draft = in
	}
	g, perr := flow.ParseGraph(draft)
	if perr != nil {
		return g, nil, invalid("invalid_graph", perr)
	}
	return g, pins, nil
}

func sampleInput(sm model.Sample, connector map[string]any) flow.Input {
	method := sm.Method
	if method == "" {
		method = "POST"
	}
	return flow.Input{RequestID: "test-" + sm.ID, Body: sm.Body, Headers: sm.Headers, Query: sm.Query, RemoteIP: sm.RemoteIP, Method: method, Connector: connector}
}

// TestRun runs the editor's graph (or the saved draft) on a sample without side effects: the
// "Event" node only shows what it would hand over.
func (s *ConnectorsService) TestRun(ctx context.Context, id string, in TestRunInput) (TestRunResult, error) {
	out := TestRunResult{Issues: []flow.Issue{}, Events: []EventPreview{}}
	g, savedPins, err := s.graphFor(id, in.Graph)
	if err != nil {
		return out, err
	}
	var sm model.Sample
	switch {
	case in.Body != nil:
		if len(*in.Body) > maxSampleBytes {
			return out, invalid("sample_too_large", nil)
		}
		sm = model.Sample{ID: "inline", Body: []byte(*in.Body), Headers: in.Headers}
	case in.SampleID != "":
		if sm, err = s.Sample(id, in.SampleID); err != nil {
			return out, err
		}
	default:
		return out, invalid("sample_required", nil)
	}
	p, issues := flow.Compile(g, s.compileOptions(true))
	if issues != nil {
		out.Issues = issues
	}
	if p == nil {
		return out, nil
	}
	if in.StopAt != "" && !p.Has(in.StopAt) {
		return out, invalid("unknown_node", nil)
	}
	opt := flow.RunOptions{StopAt: in.StopAt, Trace: true, TraceLimit: 50}
	if in.UsePins {
		raw := []byte(in.Pins)
		if len(raw) == 0 || string(raw) == "null" {
			raw = savedPins
		}
		pins, perr := flow.ParsePins(raw)
		if perr != nil {
			return out, invalid("invalid_pins", perr)
		}
		opt.Pinned = pins
	}
	cctx, cancel := context.WithTimeout(ctx, testRunTimeout)
	defer cancel()
	res, err := p.Run(cctx, sampleInput(sm, s.connectorScope(id)), opt)
	if err != nil {
		return out, invalid("test_run_timeout", err)
	}
	out.Result = res
	for _, e := range res.Events {
		out.Events = append(out.Events, EventPreview{Event: e.Event, Item: e.Lineage.Item, Node: e.Node})
	}
	return out, nil
}

func (s *ConnectorsService) connectorScope(id string) map[string]any {
	out := map[string]any{"id": id}
	s.st.Read(func(d *store.Data) {
		if c := d.Connectors[id]; c != nil {
			out["slug"], out["name"] = c.Slug, c.Name
		}
	})
	return out
}

type SampleCheck struct {
	SampleID string         `json:"sample_id"`
	Name     string         `json:"name"`
	Events   []EventPreview `json:"events"`
	Failures []flow.Failure `json:"failures"`
	Filtered int            `json:"filtered"`
	Skipped  int            `json:"skipped"`
	Error    string         `json:"error,omitempty"`
}

type TestAllResult struct {
	Issues  []flow.Issue  `json:"issues"`
	Samples []SampleCheck `json:"samples"`
}

// TestAll runs every sample through the graph: a regression check before publishing.
func (s *ConnectorsService) TestAll(ctx context.Context, id string, graph json.RawMessage) (TestAllResult, error) {
	out := TestAllResult{Issues: []flow.Issue{}, Samples: []SampleCheck{}}
	g, _, err := s.graphFor(id, graph)
	if err != nil {
		return out, err
	}
	p, issues := flow.Compile(g, s.compileOptions(false))
	if issues != nil {
		out.Issues = issues
	}
	if p == nil {
		return out, nil
	}
	var samples []model.Sample
	s.st.Read(func(d *store.Data) {
		if c := d.Connectors[id]; c != nil {
			samples = slices.Clone(c.Samples)
		}
	})
	scope := s.connectorScope(id)
	cctx, cancel := context.WithTimeout(ctx, testRunTimeout)
	defer cancel()
	for _, sm := range samples {
		chk := SampleCheck{SampleID: sm.ID, Name: sm.Name, Events: []EventPreview{}, Failures: []flow.Failure{}}
		res, err := p.Run(cctx, sampleInput(sm, scope), flow.RunOptions{})
		if err != nil {
			chk.Error = err.Error()
		} else {
			for _, e := range res.Events {
				chk.Events = append(chk.Events, EventPreview{Event: e.Event, Item: e.Lineage.Item, Node: e.Node})
			}
			chk.Failures, chk.Filtered, chk.Skipped = res.Failures, res.Filtered, res.Skipped
		}
		out.Samples = append(out.Samples, chk)
	}
	return out, nil
}
