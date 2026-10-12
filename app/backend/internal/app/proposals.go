package app

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	// maxProposals caps the proposals kept; maxProposalBatch the proposals of one request.
	maxProposals     = 5000
	maxProposalBatch = 1000
	maxProposalKey   = 512
	maxProposalText  = 256
	maxProposalBody  = 8 << 20
)

var ErrProposalUnresolved = errors.New("the proposal implies no dependency between business services")

func init() {
	statuses = append(statuses, orgStatus{ErrProposalUnresolved, http.StatusConflict, "proposal_unresolved"})
}

// ProposalEndInput is one side of a proposed dependency as Umbrella Flow names it.
type ProposalEndInput struct {
	SourceRef string `json:"source_ref"`
	Name      string `json:"name"`
	IP        string `json:"ip"`
}

type ProposalInput struct {
	Key      string                 `json:"key"`
	From     ProposalEndInput       `json:"from"`
	To       ProposalEndInput       `json:"to"`
	Type     string                 `json:"type"`
	Evidence model.ProposalEvidence `json:"evidence"`
}

// ProposalBatch is the body of POST /api/cmdb/proposals (docs/integrations.md §3.3).
type ProposalBatch struct {
	Source      string          `json:"source"`
	GeneratedAt time.Time       `json:"generated_at"`
	Proposals   []ProposalInput `json:"proposals"`
}

// ProposalResult counts new proposals, refreshed ones and the ones left out: malformed, or an
// end that is no configuration item.
type ProposalResult struct {
	Accepted int `json:"accepted"`
	Updated  int `json:"updated"`
	Ignored  int `json:"ignored"`
}

type ProposalCI struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ProposalDependency is a dependency between business services a proposal implies: Service
// would depend on DependsOn. Exists: it is already there.
type ProposalDependency struct {
	ServiceID     string `json:"service_id"`
	ServiceName   string `json:"service_name"`
	DependsOnID   string `json:"depends_on_id"`
	DependsOnName string `json:"depends_on_name"`
	Exists        bool   `json:"exists"`
}

type ProposalView struct {
	model.DependencyProposal
	FromCI       *ProposalCI          `json:"from_ci"`
	ToCI         *ProposalCI          `json:"to_ci"`
	Dependencies []ProposalDependency `json:"dependencies"`
	// New: the proposal would add at least one dependency.
	New bool `json:"new"`
}

type ProposalKey struct {
	Key string `json:"key"`
}

// ProposalsService keeps the dependency proposals of Umbrella Flow: steady traffic between two
// configuration items suggests that the business service of one depends on the service of the
// other. People accept (the dependency is created) or reject them on the CMDB map.
type ProposalsService struct {
	st       *store.Store
	services *ServicesService
	now      func() time.Time
}

func NewProposalsService(st *store.Store, services *ServicesService) *ProposalsService {
	return &ProposalsService{st: st, services: services, now: func() time.Time { return time.Now().UTC() }}
}

func proposalText(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > maxProposalText || strings.ContainsFunc(v, unicode.IsControl) {
		return ""
	}
	return v
}

// clean checks one proposal: a key, the depends_on type (or none), at least one name for each
// end and sane evidence. A link to Flow that is not http(s) is dropped.
func (in ProposalInput) clean() (ProposalInput, bool) {
	in.Key = strings.TrimSpace(in.Key)
	if in.Key == "" || len(in.Key) > maxProposalKey || strings.ContainsFunc(in.Key, unicode.IsControl) {
		return in, false
	}
	in.Type = cmp.Or(strings.TrimSpace(in.Type), model.ProposalDependsOn)
	if in.Type != model.ProposalDependsOn {
		return in, false
	}
	for _, e := range []*ProposalEndInput{&in.From, &in.To} {
		e.SourceRef, e.Name, e.IP = proposalText(e.SourceRef), proposalText(e.Name), proposalText(e.IP)
		if e.SourceRef == "" && e.Name == "" && e.IP == "" {
			return in, false
		}
	}
	ev := &in.Evidence
	if ev.Bytes7d < 0 || ev.Flows7d < 0 || ev.DaysSeen < 0 || ev.DaysSeen > 366 || ev.Port < 0 || ev.Port > 65535 {
		return in, false
	}
	ev.Protocol, ev.App = proposalText(ev.Protocol), proposalText(ev.App)
	ev.FirstSeen, ev.LastSeen = proposalText(ev.FirstSeen), proposalText(ev.LastSeen)
	if !serviceLinkURL(ev.FlowURL) {
		ev.FlowURL = ""
	}
	return in, true
}

// grown: the evidence of a rejected proposal grew ProposalRegrowth times.
func grown(p *model.DependencyProposal, ev model.ProposalEvidence) bool {
	return (ev.Bytes7d > 0 && ev.Bytes7d >= p.RejectedBytes*model.ProposalRegrowth) ||
		(ev.Flows7d > 0 && ev.Flows7d >= p.RejectedFlows*model.ProposalRegrowth)
}

// expire drops the proposals not refreshed for model.ProposalTTL.
func expire(d *store.Data, now time.Time) bool {
	changed := false
	for k, p := range d.Proposals {
		if now.Sub(p.RefreshedAt) > model.ProposalTTL {
			delete(d.Proposals, k)
			changed = true
		}
	}
	return changed
}

// Submit stores the proposals of one batch: a new key is added, a known one refreshed (its
// evidence and ends replaced). A rejected proposal stays rejected unless its evidence grew
// ProposalRegrowth times. Proposals whose ends do not both resolve to configuration items (by
// source_ref, name or IP as events do) or resolve to the same item are ignored.
func (s *ProposalsService) Submit(actor string, in ProposalBatch) (ProposalResult, error) {
	var out ProposalResult
	if len(in.Proposals) > maxProposalBatch {
		return out, invalid("too_many_proposals", nil)
	}
	source := cmp.Or(proposalText(in.Source), "unknown")
	catalog := alert.NewCatalog(s.st)
	type resolved struct {
		in       ProposalInput
		from, to string
	}
	var good []resolved
	seen := map[string]bool{}
	for _, p := range in.Proposals {
		p, ok := p.clean()
		if !ok || seen[p.Key] {
			out.Ignored++
			continue
		}
		from := catalog.Find(p.From.SourceRef, p.From.Name, p.From.IP)
		to := catalog.Find(p.To.SourceRef, p.To.Name, p.To.IP)
		if from == nil || to == nil || from.ID == to.ID {
			out.Ignored++
			continue
		}
		seen[p.Key] = true
		good = append(good, resolved{in: p, from: from.ID, to: to.ID})
	}
	now := s.now()
	s.st.Write(func(d *store.Data) {
		expire(d, now)
		for _, r := range good {
			from := model.ProposalEnd{SourceRef: r.in.From.SourceRef, Name: r.in.From.Name, IP: r.in.From.IP, CIID: r.from}
			to := model.ProposalEnd{SourceRef: r.in.To.SourceRef, Name: r.in.To.Name, IP: r.in.To.IP, CIID: r.to}
			if p := d.Proposals[r.in.Key]; p != nil {
				if p.Status == model.ProposalRejected && grown(p, r.in.Evidence) {
					p.Status, p.DecidedAt, p.DecidedBy = model.ProposalPending, nil, ""
				}
				p.Source, p.From, p.To, p.Evidence, p.GeneratedAt, p.RefreshedAt = source, from, to, r.in.Evidence, in.GeneratedAt.UTC(), now
				out.Updated++
				continue
			}
			if len(d.Proposals) >= maxProposals {
				out.Ignored++
				continue
			}
			d.Proposals[r.in.Key] = &model.DependencyProposal{Key: r.in.Key, Source: source, Type: r.in.Type, From: from, To: to, Evidence: r.in.Evidence,
				Status: model.ProposalPending, GeneratedAt: in.GeneratedAt.UTC(), CreatedAt: now, RefreshedAt: now}
			out.Accepted++
		}
		d.AddAudit(store.AuditEntry{At: now, Actor: actor, Action: "cmdb.proposals", Object: source,
			Detail: fmt.Sprintf("%d new, %d updated, %d ignored", out.Accepted, out.Updated, out.Ignored)})
	})
	return out, nil
}

// dependencies are the service dependencies a proposal implies: every active or planned
// service of the From item depends on every such service of the To item, a service never on
// itself.
func dependencies(d *store.Data, p *model.DependencyProposal) []ProposalDependency {
	of := func(ci string) []*model.Service {
		var out []*model.Service
		for _, s := range d.Services {
			if s.Status != model.ServiceRetired && slices.Contains(s.CIIDs, ci) {
				out = append(out, s)
			}
		}
		slices.SortFunc(out, func(a, b *model.Service) int { return byName(a.Name, b.Name) })
		return out
	}
	out := []ProposalDependency{}
	targets := of(p.To.CIID)
	for _, from := range of(p.From.CIID) {
		for _, to := range targets {
			if from.ID == to.ID {
				continue
			}
			out = append(out, ProposalDependency{ServiceID: from.ID, ServiceName: from.Name, DependsOnID: to.ID, DependsOnName: to.Name,
				Exists: slices.Contains(from.DependsOn, to.ID)})
		}
	}
	return out
}

func proposalView(d *store.Data, p *model.DependencyProposal) ProposalView {
	v := ProposalView{DependencyProposal: *p, Dependencies: dependencies(d, p)}
	if ci := d.ConfigItems[p.From.CIID]; ci != nil {
		v.FromCI = &ProposalCI{ID: ci.ID, Name: ci.Name}
	}
	if ci := d.ConfigItems[p.To.CIID]; ci != nil {
		v.ToCI = &ProposalCI{ID: ci.ID, Name: ci.Name}
	}
	v.New = slices.ContainsFunc(v.Dependencies, func(x ProposalDependency) bool { return !x.Exists })
	return v
}

// List gives the pending proposals, the most traffic first; with all, also the accepted and
// rejected ones. Expired proposals are dropped first.
func (s *ProposalsService) List(all bool) []ProposalView {
	now := s.now()
	s.st.Update(func(d *store.Data) bool { return expire(d, now) })
	out := []ProposalView{}
	s.st.Read(func(d *store.Data) {
		for _, p := range d.Proposals {
			if all || p.Status == model.ProposalPending {
				out = append(out, proposalView(d, p))
			}
		}
	})
	slices.SortFunc(out, func(a, b ProposalView) int {
		return cmp.Or(cmp.Compare(b.Evidence.Bytes7d, a.Evidence.Bytes7d), cmp.Compare(a.Key, b.Key))
	})
	return out
}

// Accept creates the service dependencies the proposal implies (with the checks of a service
// edit, so no cycle) and marks it accepted.
func (s *ProposalsService) Accept(actor, key string) (ProposalView, error) {
	var deps []ProposalDependency
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		if p := d.Proposals[key]; p != nil {
			deps, err = dependencies(d, p), nil
		}
	})
	if err != nil {
		return ProposalView{}, err
	}
	if len(deps) == 0 {
		return ProposalView{}, ErrProposalUnresolved
	}
	for _, dep := range deps {
		if dep.Exists {
			continue
		}
		if _, err := s.services.SetDependencies(actor, dep.ServiceID, []string{dep.DependsOnID}, nil); err != nil {
			return ProposalView{}, err
		}
	}
	return s.decide(actor, key, model.ProposalAccepted)
}

// Reject hides the proposal until its evidence grows model.ProposalRegrowth times.
func (s *ProposalsService) Reject(actor, key string) (ProposalView, error) {
	return s.decide(actor, key, model.ProposalRejected)
}

func (s *ProposalsService) decide(actor, key, status string) (ProposalView, error) {
	var out ProposalView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		p := d.Proposals[key]
		if p == nil {
			return
		}
		now := s.now()
		p.Status, p.DecidedAt, p.DecidedBy, err = status, &now, actor, nil
		if status == model.ProposalRejected {
			p.RejectedBytes, p.RejectedFlows = p.Evidence.Bytes7d, p.Evidence.Flows7d
		}
		d.AddAudit(store.AuditEntry{At: now, Actor: actor, Action: "cmdb.proposal." + status, Object: key,
			Detail: p.From.Name + " -> " + p.To.Name})
		out = proposalView(d, p)
	})
	return out, err
}

func (a *App) registerProposals(mux *http.ServeMux) {
	// The contract lets Flow add fields, so unknown ones are allowed here, unlike other bodies.
	a.route(mux, "POST /api/cmdb/proposals", "cmdb:propose", func(w http.ResponseWriter, r *http.Request) {
		var in ProposalBatch
		if err := json.NewDecoder(io.LimitReader(r.Body, maxProposalBody)).Decode(&in); err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", errors.New("invalid JSON body"))
			return
		}
		out, err := a.proposals.Submit(actorName(r), in)
		reply(w, http.StatusOK, out, err)
	})
	a.route(mux, "GET /api/cmdb/proposals", "cmdb:view", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, a.proposals.List(r.URL.Query().Get("status") == "all"))
	})
	a.route(mux, "POST /api/cmdb/proposals/accept", "services:edit", submit(http.StatusOK, func(r *http.Request, in ProposalKey) (ProposalView, error) {
		return a.proposals.Accept(actorName(r), in.Key)
	}))
	a.route(mux, "POST /api/cmdb/proposals/reject", "services:edit", submit(http.StatusOK, func(r *http.Request, in ProposalKey) (ProposalView, error) {
		return a.proposals.Reject(actorName(r), in.Key)
	}))
}
