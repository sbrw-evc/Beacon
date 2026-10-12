package app

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/netbox"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	maxBulkServices = 50
	maxBulkCIs      = 1000
	maxBulkHosts    = 500
	// bulkGiveUp: after this many failures in a row on the same cause the rest is not tried, so
	// a NetBox that is down does not make every item wait for its timeout.
	bulkGiveUp = 3
)

// Outcomes of one item of a bulk action.
const (
	BulkBound    = "bound"
	BulkUnbound  = "unbound"
	BulkAlready  = "already"
	BulkNotBound = "not_bound"
	BulkCreated  = "created"
	BulkLinked   = "linked"
	BulkMatched  = "matched"
	BulkFailed   = "failed"
	BulkSkipped  = "skipped"
)

// BulkSummary counts the outcomes of a bulk action.
type BulkSummary map[string]int

// errorCode is the code a request would answer with for the error, and the cause when there
// is one worth showing.
func errorCode(err error) (string, string) {
	a := answerOf(err)
	if a.detail == nil || a.status == http.StatusInternalServerError {
		return a.code, ""
	}
	return a.code, a.detail.Error()
}

// giveUp counts failures in a row with the same code.
type giveUp struct {
	code string
	n    int
}

func (g *giveUp) fail(code string) {
	if code == g.code {
		g.n++
	} else {
		g.code, g.n = code, 1
	}
}

func (g *giveUp) ok()           { g.code, g.n = "", 0 }
func (g *giveUp) stopped() bool { return g.n >= bulkGiveUp }

// BulkCIsInput binds or unbinds every item to every service.
type BulkCIsInput struct {
	ServiceIDs []string `json:"service_ids"`
	CIIDs      []string `json:"ci_ids"`
	// Action: bind or unbind.
	Action string `json:"action"`
}

type BulkCIItem struct {
	ServiceID   string `json:"service_id"`
	ServiceName string `json:"service_name"`
	CIID        string `json:"ci_id"`
	CIName      string `json:"ci_name"`
	Result      string `json:"result"`
	Error       string `json:"error,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

type BulkCIsResult struct {
	Items   []BulkCIItem `json:"items"`
	Summary BulkSummary  `json:"summary"`
}

// bulkPlan is the work for one service: what NetBox needs and which items change.
type bulkPlan struct {
	id    string
	link  *model.ServiceNetBox
	items []int // indexes into the result
	refs  map[int]model.NetBoxRef
}

// BulkCIs binds the items to the services or unbinds them, keeping the NetBox tags of linked
// services in step. Every pair gets its own outcome: a NetBox failure on one item leaves that
// binding unchanged and goes on with the rest.
func (s *ServicesService) BulkCIs(ctx context.Context, actor string, in BulkCIsInput) (BulkCIsResult, error) {
	bind := in.Action == "bind"
	if !bind && in.Action != "unbind" {
		return BulkCIsResult{}, invalid("bulk_action", nil)
	}
	svcIDs, err := serviceIDs(in.ServiceIDs, maxBulkServices, "too_many_services")
	if err != nil {
		return BulkCIsResult{}, err
	}
	ciIDs, err := serviceIDs(in.CIIDs, maxBulkCIs, "too_many_cis")
	if err != nil {
		return BulkCIsResult{}, err
	}
	if len(svcIDs) == 0 {
		return BulkCIsResult{}, invalid("no_services", nil)
	}
	if len(ciIDs) == 0 {
		return BulkCIsResult{}, invalid("no_cis", nil)
	}

	out := BulkCIsResult{Items: []BulkCIItem{}, Summary: BulkSummary{}}
	var plans []*bulkPlan
	s.st.Read(func(d *store.Data) {
		for _, id := range svcIDs {
			if d.Services[id] == nil {
				err = invalid("unknown_service", nil)
				return
			}
		}
		for _, id := range ciIDs {
			if d.ConfigItems[id] == nil {
				err = invalid("unknown_ci", nil)
				return
			}
		}
		for _, id := range svcIDs {
			svc := d.Services[id]
			p := &bulkPlan{id: id, link: svc.NetBox, refs: map[int]model.NetBoxRef{}}
			plans = append(plans, p)
			added := 0
			for _, ciID := range ciIDs {
				ci := d.ConfigItems[ciID]
				it := BulkCIItem{ServiceID: id, ServiceName: svc.Name, CIID: ciID, CIName: ci.Name}
				bound := slices.Contains(svc.CIIDs, ciID)
				switch {
				case bind && bound:
					it.Result = BulkAlready
				case !bind && !bound:
					it.Result = BulkNotBound
				case bind && len(svc.CIIDs)+added >= maxServiceCIs:
					it.Result, it.Error = BulkFailed, "too_many_cis"
				default:
					if bind {
						added++
					}
					p.items = append(p.items, len(out.Items))
					if svc.NetBox != nil && ci.NetBox != nil {
						p.refs[len(out.Items)] = *ci.NetBox
					}
				}
				out.Items = append(out.Items, it)
			}
		}
	})
	if err != nil {
		return BulkCIsResult{}, err
	}

	// NetBox first, item by item; only what NetBox took changes here.
	var stop giveUp
	for _, p := range plans {
		if p.link == nil || len(p.refs) == 0 {
			continue
		}
		c, _, cerr := s.netbox.client()
		for _, i := range p.items {
			ref, ok := p.refs[i]
			if !ok {
				continue
			}
			it := &out.Items[i]
			if cerr == nil && stop.stopped() {
				it.Result, it.Error = BulkSkipped, "stopped"
				continue
			}
			e := cerr
			if e == nil {
				if e = c.SetTag(ctx, ref.Kind, ref.ID, p.link.TagID, bind); errors.Is(e, netbox.ErrNotFound) {
					e = nil
				} else if e != nil {
					e = netboxFailure{e}
				}
			}
			if e != nil {
				it.Result = BulkFailed
				it.Error, it.Detail = errorCode(e)
				stop.fail(it.Error)
				continue
			}
			stop.ok()
		}
	}

	s.st.Write(func(d *store.Data) {
		for _, p := range plans {
			svc := d.Services[p.id]
			var names []string
			for _, i := range p.items {
				it := &out.Items[i]
				if it.Result != "" {
					continue
				}
				ci := d.ConfigItems[it.CIID]
				if svc == nil || ci == nil {
					it.Result, it.Error = BulkFailed, "not_found"
					continue
				}
				has := slices.Contains(svc.CIIDs, it.CIID)
				switch {
				case bind && has:
					it.Result = BulkAlready
				case bind:
					svc.CIIDs = append(svc.CIIDs, it.CIID)
					it.Result = BulkBound
					names = append(names, ci.Name)
				case !has:
					it.Result = BulkNotBound
				default:
					svc.CIIDs = orNil(slices.DeleteFunc(slices.Clone(svc.CIIDs), func(x string) bool { return x == it.CIID }))
					it.Result = BulkUnbound
					names = append(names, ci.Name)
				}
				if svc.NetBox != nil && (it.Result == BulkBound || it.Result == BulkUnbound) {
					markTag(ci, svc.NetBox.Slug, bind)
				}
			}
			if len(names) > 0 {
				svc.UpdatedAt = s.now()
				action := "service.unbind"
				if bind {
					action = "service.bind"
				}
				d.AddAudit(store.AuditEntry{Actor: actor, Action: action, Object: p.id, Detail: svc.Name + ": " + strings.Join(names, ", ")})
			}
		}
	})
	for _, it := range out.Items {
		out.Summary[it.Result]++
	}
	return out, nil
}

// HostKey names a host of a source.
type HostKey struct {
	SourceID string `json:"source_id"`
	Key      string `json:"key"`
}

// BulkHostsInput makes configuration items of hosts.
type BulkHostsInput struct {
	Hosts    []HostKey `json:"hosts"`
	Kind     string    `json:"kind"`
	Register bool      `json:"register"`
}

type BulkHostItem struct {
	SourceID   string      `json:"source_id"`
	SourceName string      `json:"source_name"`
	Key        string      `json:"key"`
	Host       string      `json:"host"`
	Result     string      `json:"result"`
	CI         *ServiceRef `json:"ci,omitempty"`
	Error      string      `json:"error,omitempty"`
	Detail     string      `json:"detail,omitempty"`
}

type BulkHostsResult struct {
	Items   []BulkHostItem `json:"items"`
	Summary BulkSummary    `json:"summary"`
}

// BulkCreateCIs makes a configuration item of every host in turn, the way CreateCI does for
// one. A host matched to an item by then is not made again: one machine seen by several
// systems, or named the same as an item made earlier in the batch, ends up linked to that
// item. Every host gets its own outcome.
func (s *MonitoringService) BulkCreateCIs(ctx context.Context, actor string, in BulkHostsInput) (BulkHostsResult, error) {
	var keys []HostKey
	for _, k := range in.Hosts {
		if k.SourceID != "" && k.Key != "" && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return BulkHostsResult{}, invalid("no_hosts", nil)
	}
	if len(keys) > maxBulkHosts {
		return BulkHostsResult{}, invalid("too_many_hosts", nil)
	}
	// Zabbix hosts go first: one machine also seen by Prometheus is then named after its
	// technical name in Zabbix rather than the address Prometheus scrapes.
	kinds := map[HostKey]string{}
	s.st.Read(func(d *store.Data) {
		for _, k := range keys {
			if src := d.MonitoringSources[k.SourceID]; src != nil {
				kinds[k] = src.Kind
			}
		}
	})
	order := make([]int, len(keys))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		rank := func(k HostKey) int {
			if kinds[k] == model.MonitoringZabbix {
				return 0
			}
			return 1
		}
		return rank(keys[a]) - rank(keys[b])
	})

	out := BulkHostsResult{Items: make([]BulkHostItem, len(keys)), Summary: BulkSummary{}}
	made := map[string]bool{}
	var stop giveUp
	for _, i := range order {
		k := keys[i]
		it := BulkHostItem{SourceID: k.SourceID, Key: k.Key, Host: k.Key}
		v, err := s.host(k.SourceID, k.Key)
		switch {
		case err != nil:
			it.Result = BulkFailed
			it.Error, it.Detail = errorCode(err)
		case v.CI != nil:
			it.Result, it.CI = BulkMatched, v.CI
			if made[v.CI.ID] {
				it.Result = BulkLinked
			}
		case v.Match == MatchExcluded:
			it.Result, it.Error = BulkSkipped, "excluded"
		case stop.stopped():
			it.Result, it.Error = BulkSkipped, "stopped"
		default:
			ci, err := s.createCI(ctx, actor, v, in.Kind, in.Register)
			if err != nil {
				it.Result = BulkFailed
				it.Error, it.Detail = errorCode(err)
				stop.fail(it.Error)
				break
			}
			stop.ok()
			made[ci.ID] = true
			it.Result, it.CI = BulkCreated, &ServiceRef{ID: ci.ID, Name: ci.Name}
		}
		if err == nil {
			it.SourceName, it.Host = v.SourceName, firstSet(v.Name, v.Host)
		}
		out.Items[i] = it
	}
	for _, it := range out.Items {
		out.Summary[it.Result]++
	}
	return out, nil
}
