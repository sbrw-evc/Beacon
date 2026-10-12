package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	maxCredentialName   = 120
	maxCredentialSecret = 8192
)

var (
	ErrCredentialInUse = errors.New("the credential is in use")
	ErrSecretsDown     = errors.New("OpenBao is unavailable")
)

// CredentialKind is a credential type with its plain fields and its secret fields.
type CredentialKind struct {
	Type    string   `json:"type"`
	Fields  []string `json:"fields"`
	Secrets []string `json:"secrets"`
}

// credentialKinds are the credential types in the order the web app offers them; it reads
// them from GET /api/credentials/kinds.
var credentialKinds = []CredentialKind{
	{flow.CredBearer, []string{}, []string{"token"}},
	{flow.CredBasic, []string{"username"}, []string{"password"}},
	{flow.CredHeader, []string{"header"}, []string{"value"}},
	{flow.CredHMAC, []string{}, []string{"secret"}},
}

func credentialKind(typ string) (CredentialKind, bool) {
	i := slices.IndexFunc(credentialKinds, func(k CredentialKind) bool { return k.Type == typ })
	if i < 0 {
		return CredentialKind{}, false
	}
	return credentialKinds[i], true
}

func (a *App) listCredentialKinds(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, credentialKinds)
}

type CredentialInput struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Description string            `json:"description"`
	Fields      map[string]string `json:"fields"`
	Secrets     map[string]string `json:"secrets"`
}

type CredentialView struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Description string            `json:"description"`
	Fields      map[string]string `json:"fields"`
	SecretsSet  []string          `json:"secrets_set"`
	Version     int               `json:"version"`
	UsedBy      []CredentialUse   `json:"used_by"`
	CreatedAt   time.Time         `json:"created_at"`
	CreatedBy   string            `json:"created_by"`
	UpdatedAt   time.Time         `json:"updated_at"`
	UpdatedBy   string            `json:"updated_by"`
}

// Kinds of what uses a credential.
const (
	UseConnector     = "connector"
	UseMonitoring    = "monitoring"
	UseMetricSource  = "metric_source"
	UseLogSource     = "log_source"
	credentialUseMax = 50
)

// CredentialUse is a connector, monitoring system or metric source that uses a credential.
type CredentialUse struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CredentialInUseError names what still uses a credential that was asked to be deleted.
type CredentialInUseError struct{ Uses []CredentialUse }

func (e *CredentialInUseError) Error() string {
	names := make([]string, 0, len(e.Uses))
	for _, u := range e.Uses {
		names = append(names, u.Name)
	}
	return "the credential is used by " + strings.Join(names, ", ")
}

func (e *CredentialInUseError) Unwrap() error { return ErrCredentialInUse }

// credentialUses lists everything that uses the credential: connectors (draft or a version),
// monitoring systems and metric sources, by kind and name.
func credentialUses(d *store.Data, id string) []CredentialUse {
	out := []CredentialUse{}
	for _, cn := range d.Connectors {
		if connectorUses(cn, id) {
			out = append(out, CredentialUse{Kind: UseConnector, ID: cn.ID, Name: cn.Name})
		}
	}
	for _, src := range d.MonitoringSources {
		if src.CredentialID == id {
			out = append(out, CredentialUse{Kind: UseMonitoring, ID: src.ID, Name: src.Name})
		}
	}
	for _, src := range d.MetricSources {
		if src.CredentialID == id {
			out = append(out, CredentialUse{Kind: UseMetricSource, ID: src.ID, Name: src.Name})
		}
	}
	for _, src := range d.LogSources {
		if src.CredentialID == id {
			out = append(out, CredentialUse{Kind: UseLogSource, ID: src.ID, Name: src.Name})
		}
	}
	order := map[string]int{UseConnector: 0, UseMonitoring: 1, UseMetricSource: 2, UseLogSource: 3}
	slices.SortFunc(out, func(a, b CredentialUse) int {
		if a.Kind != b.Kind {
			return order[a.Kind] - order[b.Kind]
		}
		return byName(a.Name, b.Name)
	})
	if len(out) > credentialUseMax {
		out = out[:credentialUseMax]
	}
	return out
}

type CredentialsService struct {
	st    *store.Store
	vault Secrets
	now   func() time.Time
}

func NewCredentialsService(st *store.Store, vault Secrets) *CredentialsService {
	return &CredentialsService{st: st, vault: vault, now: func() time.Time { return time.Now().UTC() }}
}

func credentialPath(id string) string { return "credentials/" + id }

func (s *CredentialsService) view(d *store.Data, c *model.Credential) CredentialView {
	v := CredentialView{ID: c.ID, Name: c.Name, Type: c.Type, Description: c.Description, Fields: map[string]string{},
		SecretsSet: []string{}, Version: c.Version, UsedBy: credentialUses(d, c.ID), CreatedAt: c.CreatedAt, CreatedBy: c.CreatedBy,
		UpdatedAt: c.UpdatedAt, UpdatedBy: c.UpdatedBy}
	for k, val := range c.Fields {
		v.Fields[k] = val
	}
	for k := range c.Secrets {
		v.SecretsSet = append(v.SecretsSet, k)
	}
	slices.Sort(v.SecretsSet)
	return v
}

// connectorUses: the draft or any published version refers to the credential.
func connectorUses(c *model.Connector, credID string) bool {
	graphs := [][]byte{c.Draft.Graph}
	for _, v := range c.Versions {
		graphs = append(graphs, v.Graph)
	}
	for _, b := range graphs {
		g, err := flow.ParseGraph(b)
		if err != nil {
			continue
		}
		for _, n := range g.Nodes {
			for _, ref := range credentialParams(n) {
				if ref == credID {
					return true
				}
			}
		}
	}
	return false
}

func credentialParams(n flow.Node) map[string]string {
	out := map[string]string{}
	t, ok := flow.TypeOf(n.Type, n.TypeVersion)
	if !ok {
		return out
	}
	for _, p := range t.Params {
		if p.Kind != flow.KindCredential {
			continue
		}
		if v, _ := n.Params[p.Key].(string); v != "" {
			out[p.Key] = v
		}
	}
	return out
}

func (s *CredentialsService) List() []CredentialView {
	out := []CredentialView{}
	s.st.Read(func(d *store.Data) {
		for _, c := range d.Credentials {
			out = append(out, s.view(d, c))
		}
	})
	slices.SortFunc(out, func(a, b CredentialView) int { return byName(a.Name, b.Name) })
	return out
}

func (s *CredentialsService) Get(id string) (CredentialView, error) {
	var out CredentialView
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		if c := d.Credentials[id]; c != nil {
			out, err = s.view(d, c), nil
		}
	})
	return out, err
}

// TypeOf reports a credential's type for the flow compiler.
func (s *CredentialsService) TypeOf(id string) (string, bool) {
	var typ string
	s.st.Read(func(d *store.Data) {
		if c := d.Credentials[id]; c != nil {
			typ = c.Type
		}
	})
	return typ, typ != ""
}

func (in *CredentialInput) normalize(create bool) error {
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	in.Description = strings.TrimSpace(in.Description)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > maxCredentialName {
		return invalid("invalid_credential_name", nil)
	}
	if utf8.RuneCountInString(in.Description) > maxServiceDescription {
		return invalid("invalid_credential_description", nil)
	}
	kind, ok := credentialKind(in.Type)
	if !ok {
		return invalid("invalid_credential_type", nil)
	}
	fields := map[string]string{}
	for _, f := range kind.Fields {
		v := strings.TrimSpace(in.Fields[f])
		if v == "" || len(v) > 256 {
			return invalid("invalid_credential_field", fmt.Errorf("%s is required", f))
		}
		if f == "header" && !validHeaderName(v) {
			return invalid("invalid_credential_field", fmt.Errorf("%q is not a header name", v))
		}
		fields[f] = v
	}
	in.Fields = fields
	secretsIn := map[string]string{}
	for _, k := range kind.Secrets {
		v := in.Secrets[k]
		if v == "" {
			if create {
				return invalid("invalid_credential_secret", fmt.Errorf("%s is required", k))
			}
			continue
		}
		if len(v) > maxCredentialSecret || strings.ContainsAny(v, "\r\n") {
			return invalid("invalid_credential_secret", fmt.Errorf("%s is too long or has line breaks", k))
		}
		secretsIn[k] = v
	}
	for k := range in.Secrets {
		if !slices.Contains(kind.Secrets, k) {
			return invalid("invalid_credential_secret", fmt.Errorf("%s is not a secret of type %s", k, in.Type))
		}
	}
	in.Secrets = secretsIn
	return nil
}

func validHeaderName(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if !(r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func (s *CredentialsService) store(ctx context.Context, id string, values map[string]string) (map[string]string, error) {
	refs := map[string]string{}
	if len(values) == 0 {
		return refs, nil
	}
	if s.vault == nil {
		return nil, ErrSecretsDown
	}
	for k, v := range values {
		ref, err := s.vault.PutRef(ctx, credentialPath(id), k, v)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSecretsDown, err)
		}
		refs[k] = ref
	}
	return refs, nil
}

func (s *CredentialsService) Create(ctx context.Context, actor string, in CredentialInput) (CredentialView, error) {
	if err := in.normalize(true); err != nil {
		return CredentialView{}, err
	}
	var id string
	s.st.Write(func(d *store.Data) { id = d.NextID("CRD") })
	refs, err := s.store(ctx, id, in.Secrets)
	if err != nil {
		return CredentialView{}, err
	}
	var out CredentialView
	s.st.Write(func(d *store.Data) {
		now := s.now()
		c := &model.Credential{ID: id, Name: in.Name, Type: in.Type, Description: in.Description, Fields: in.Fields, Secrets: refs,
			Version: 1, CreatedAt: now, CreatedBy: actor, UpdatedAt: now, UpdatedBy: actor}
		d.Credentials[id] = c
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "credential.create", Object: id, Detail: c.Name + " (" + c.Type + ")"})
		out = s.view(d, c)
	})
	return out, nil
}

func (s *CredentialsService) Update(ctx context.Context, actor, id string, in CredentialInput) (CredentialView, error) {
	cur, err := s.Get(id)
	if err != nil {
		return CredentialView{}, err
	}
	if in.Type == "" {
		in.Type = cur.Type
	}
	if in.Type != cur.Type {
		return CredentialView{}, invalid("credential_type_fixed", nil)
	}
	if err := in.normalize(false); err != nil {
		return CredentialView{}, err
	}
	refs, err := s.store(ctx, id, in.Secrets)
	if err != nil {
		return CredentialView{}, err
	}
	var out CredentialView
	err = ErrNotFound
	s.st.Write(func(d *store.Data) {
		c := d.Credentials[id]
		if c == nil {
			return
		}
		err = nil
		c.Name, c.Description, c.Fields = in.Name, in.Description, in.Fields
		if c.Secrets == nil {
			c.Secrets = map[string]string{}
		}
		for k, ref := range refs {
			c.Secrets[k] = ref
		}
		detail := c.Name
		if len(refs) > 0 {
			c.Version++
			detail += ": secret replaced"
		}
		c.UpdatedAt, c.UpdatedBy = s.now(), actor
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "credential.update", Object: id, Detail: detail})
		out = s.view(d, c)
	})
	return out, err
}

func (s *CredentialsService) Delete(ctx context.Context, actor, id string) error {
	var err error
	var name string
	s.st.Write(func(d *store.Data) {
		c := d.Credentials[id]
		if c == nil {
			err = ErrNotFound
			return
		}
		if uses := credentialUses(d, id); len(uses) > 0 {
			err = &CredentialInUseError{Uses: uses}
			return
		}
		name = c.Name
		delete(d.Credentials, id)
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "credential.delete", Object: id, Detail: name})
	})
	if err != nil {
		return err
	}
	if v, ok := s.vault.(interface {
		Delete(ctx context.Context, path string) error
	}); ok {
		// The credential is gone either way; a secret left behind is only garbage in OpenBao.
		_ = v.Delete(ctx, credentialPath(id))
	}
	return nil
}

// Resolved is a credential with its secret values, used by the intake to check a request.
type Resolved struct {
	Type    string
	Fields  map[string]string
	Secrets map[string]string
}

func (s *CredentialsService) Resolve(id string) (Resolved, error) {
	var c *model.Credential
	s.st.Read(func(d *store.Data) {
		if x := d.Credentials[id]; x != nil {
			cp := *x
			c = &cp
		}
	})
	if c == nil {
		return Resolved{}, ErrNotFound
	}
	out := Resolved{Type: c.Type, Fields: c.Fields, Secrets: map[string]string{}}
	if s.vault == nil {
		return out, ErrSecretsDown
	}
	for k, ref := range c.Secrets {
		v, err := secrets.Value(s.vault, ref)
		if err != nil {
			return out, fmt.Errorf("%w: %v", ErrSecretsDown, err)
		}
		out.Secrets[k] = v
	}
	return out, nil
}
