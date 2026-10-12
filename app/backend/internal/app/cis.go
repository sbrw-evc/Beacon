package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/netbox"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	maxCIName   = 200
	maxCIIPs    = 20
	maxCIOwners = 50
)

var (
	ErrCIImported     = errors.New("the configuration item is imported from NetBox and changes only there")
	ErrCIRegistered   = errors.New("the configuration item is already in NetBox")
	ErrNotRegistrable = errors.New("only devices and virtual machines can be registered in NetBox")
)

func init() {
	statuses = append(statuses,
		orgStatus{ErrCIImported, http.StatusConflict, "ci_imported"},
		orgStatus{ErrCIRegistered, http.StatusConflict, "ci_registered"},
		orgStatus{ErrNotRegistrable, http.StatusBadRequest, "ci_not_registrable"},
	)
}

// netboxKindOf is the NetBox object type a locally created item is registered as.
var netboxKindOf = map[string]string{model.CIKindDevice: netbox.KindDevice, model.CIKindVM: netbox.KindVM}

type CIInput struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Status      string   `json:"status"`
	Description string   `json:"description"`
	OwnerIDs    []string `json:"owner_ids"`
	IPs         []string `json:"ips"`
	Tags        []string `json:"tags"`
	// Register also creates the item in NetBox.
	Register bool `json:"register"`
}

type CIOwnerView struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Source   string `json:"source"`
	Role     string `json:"role"`
	Deleted  bool   `json:"deleted"`
}

type CIView struct {
	model.ConfigItem
	Owners     []CIOwnerView `json:"owners"`
	Services   []ServiceRef  `json:"services"`
	Monitoring []CIMonitor   `json:"monitoring"`
	// Presence: the systems the item is in and those it is missing from.
	Presence []CIPresence `json:"presence"`
	// NotMonitored: a device or virtual machine no monitoring system covers.
	NotMonitored bool `json:"not_monitored"`
	Editable     bool `json:"editable"`
	Registrable  bool `json:"registrable"`
	// FlowURL leads to the recent traffic of the item in Umbrella Flow through Umbrella
	// (/go/cis/{id}/flow); empty while the Flow address is not set.
	FlowURL string `json:"flow_url,omitempty"`
}

type CISummary struct {
	Total            int `json:"total"`
	NetBox           int `json:"netbox"`
	InventoryDB      int `json:"inventory_db"`
	Local            int `json:"local"`
	Registered       int `json:"registered"`
	NoOwners         int `json:"no_owners"`
	DirectoryMissing int `json:"directory_missing"`
	NotMonitored     int `json:"not_monitored"`
	// MonitoringSources counts the turned-on monitoring systems; without any, coverage is not judged.
	MonitoringSources int `json:"monitoring_sources"`
}

type CIList struct {
	Items   []CIView  `json:"items"`
	Tags    []string  `json:"tags"`
	Summary CISummary `json:"summary"`
}

type CIFilter struct {
	Query  string
	Kind   string
	Source string
	Status string
	Owner  string
	// Flag: no_owners, directory_missing or not_monitored.
	Flag string
}

type CIService struct {
	st     *store.Store
	netbox *NetBoxService
	now    func() time.Time
}

func NewCIService(st *store.Store, nb *NetBoxService) *CIService {
	return &CIService{st: st, netbox: nb, now: func() time.Time { return time.Now().UTC() }}
}

// servicesByCI lists the business services of every configuration item.
func servicesByCI(d *store.Data) map[string][]ServiceRef {
	out := map[string][]ServiceRef{}
	for _, svc := range d.Services {
		for _, id := range svc.CIIDs {
			out[id] = append(out[id], ServiceRef{ID: svc.ID, Name: svc.Name})
		}
	}
	for _, refs := range out {
		slices.SortFunc(refs, func(x, y ServiceRef) int { return byName(x.Name, y.Name) })
	}
	return out
}

func ciView(d *store.Data, ci *model.ConfigItem) CIView {
	return ciViewWith(d, ci, servicesByCI(d), monitorsByCI(d), enabledSourceList(d))
}

func ciViewWith(d *store.Data, ci *model.ConfigItem, services map[string][]ServiceRef, monitors map[string][]CIMonitor, sources []*model.MonitoringSource) CIView {
	v := CIView{ConfigItem: *ci, Owners: []CIOwnerView{}, Services: services[ci.ID], Monitoring: monitors[ci.ID], Editable: !ci.Imported()}
	if v.Services == nil {
		v.Services = []ServiceRef{}
	}
	if v.Monitoring == nil {
		v.Monitoring = []CIMonitor{}
	}
	v.NotMonitored = len(sources) > 0 && notMonitored(ci, v.Monitoring)
	v.Presence = presence(d, ci, v.Monitoring, sources)
	v.Registrable = !ci.Imported() && ci.NetBox == nil && netboxKindOf[ci.Kind] != ""
	if d.Settings.Alerting.Flow.URL != "" {
		v.FlowURL = model.CIFlowHopURL("", ci.ID)
	}
	if v.IPs == nil {
		v.IPs = []string{}
	}
	if v.Tags == nil {
		v.Tags = []string{}
	}
	for _, o := range ci.Owners {
		u := d.Users[o.UserID]
		if u == nil {
			v.Owners = append(v.Owners, CIOwnerView{ID: o.UserID, Role: o.Role, Deleted: true})
			continue
		}
		v.Owners = append(v.Owners, CIOwnerView{ID: u.ID, Username: u.Username, Name: firstSet(u.Profile.DisplayName(""), u.Name, u.Username),
			Email: u.Email, Source: u.Source, Role: o.Role})
	}
	slices.SortStableFunc(v.Owners, func(a, b CIOwnerView) int { return byName(a.Name, b.Name) })
	return v
}

func firstSet(v ...string) string {
	for _, x := range v {
		if x != "" {
			return x
		}
	}
	return ""
}

func (s *CIService) List(f CIFilter) CIList {
	out := CIList{Items: []CIView{}, Tags: []string{}}
	q := strings.ToLower(strings.TrimSpace(f.Query))
	s.st.Read(func(d *store.Data) {
		tags := map[string]bool{}
		services, monitors := servicesByCI(d), monitorsByCI(d)
		sources := enabledSourceList(d)
		out.Summary.MonitoringSources = len(sources)
		for _, ci := range d.ConfigItems {
			sum := &out.Summary
			sum.Total++
			if ci.Source == model.SourceInventoryDB {
				sum.InventoryDB++
			} else if ci.Imported() {
				sum.NetBox++
			} else {
				sum.Local++
				if ci.NetBox != nil {
					sum.Registered++
				}
			}
			if len(ci.Owners) == 0 {
				sum.NoOwners++
			}
			if ci.Directory != nil && ci.Directory.Status == model.DirectoryMissing {
				sum.DirectoryMissing++
			}
			for _, t := range ci.Tags {
				tags[t] = true
			}
			v := ciViewWith(d, ci, services, monitors, sources)
			if v.NotMonitored {
				sum.NotMonitored++
			}
			if f.matches(v, q) {
				out.Items = append(out.Items, v)
			}
		}
		for t := range tags {
			out.Tags = append(out.Tags, t)
		}
	})
	slices.Sort(out.Tags)
	slices.SortFunc(out.Items, func(a, b CIView) int {
		if c := byName(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func (f CIFilter) matches(v CIView, q string) bool {
	switch {
	case f.Kind != "" && v.Kind != f.Kind,
		f.Status != "" && v.Status != f.Status,
		f.Source != "" && v.Source != f.Source,
		f.Owner != "" && !slices.ContainsFunc(v.Owners, func(o CIOwnerView) bool { return o.ID == f.Owner }),
		f.Flag == "no_owners" && len(v.Owners) > 0,
		f.Flag == "directory_missing" && (v.Directory == nil || v.Directory.Status != model.DirectoryMissing),
		f.Flag == "not_monitored" && !v.NotMonitored:
		return false
	}
	if q == "" {
		return true
	}
	fields := []string{v.Name, v.Description, v.ID, v.Attrs.Site, v.Attrs.Tenant, v.Attrs.Parent, v.Attrs.Role, v.Attrs.Platform, v.Attrs.Serial}
	fields = append(fields, v.IPs...)
	fields = append(fields, v.Aliases...)
	fields = append(fields, v.Tags...)
	for _, o := range v.Owners {
		fields = append(fields, o.Name, o.Username, o.Email)
	}
	for _, svc := range v.Services {
		fields = append(fields, svc.Name)
	}
	for _, m := range v.Monitoring {
		fields = append(fields, m.Host, m.Name)
	}
	if v.NetBox != nil {
		fields = append(fields, "netbox:"+strconv.Itoa(v.NetBox.ID))
	}
	if v.InventoryDB != nil {
		fields = append(fields, v.InventoryDB.Ref)
	}
	if v.Directory != nil {
		fields = append(fields, v.Directory.DNSName)
	}
	return slices.ContainsFunc(fields, func(x string) bool { return strings.Contains(strings.ToLower(x), q) })
}

func (s *CIService) Get(id string) (CIView, error) {
	var out CIView
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		if ci := d.ConfigItems[id]; ci != nil {
			out, err = ciView(d, ci), nil
		}
	})
	return out, err
}

func (in CIInput) normalize() (CIInput, error) {
	out := CIInput{Name: strings.Join(strings.Fields(in.Name), " "), Kind: strings.TrimSpace(in.Kind), Status: strings.TrimSpace(in.Status),
		Description: strings.TrimSpace(in.Description), Register: in.Register}
	if n := utf8.RuneCountInString(out.Name); n == 0 || n > maxCIName || strings.ContainsFunc(out.Name, unicode.IsControl) {
		return out, invalid("invalid_ci_name", nil)
	}
	if utf8.RuneCountInString(out.Description) > maxServiceDescription {
		return out, invalid("invalid_ci_description", nil)
	}
	if out.Kind == "" {
		out.Kind = model.CIKindOther
	}
	if !model.ValidCIKind(out.Kind) {
		return out, invalid("invalid_ci_kind", nil)
	}
	if out.Status == "" {
		out.Status = model.CIStatusActive
	}
	if !model.ValidCIStatus(out.Status) {
		return out, invalid("invalid_ci_status", nil)
	}
	var err error
	if out.OwnerIDs, err = serviceIDs(in.OwnerIDs, maxCIOwners, "too_many_owners"); err != nil {
		return out, err
	}
	for _, raw := range in.IPs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		ip := net.ParseIP(raw)
		if ip == nil {
			return out, invalid("invalid_ip", nil)
		}
		if s := ip.String(); !slices.Contains(out.IPs, s) {
			out.IPs = append(out.IPs, s)
		}
	}
	if len(out.IPs) > maxCIIPs {
		return out, invalid("too_many_ips", nil)
	}
	if out.Tags, err = serviceTags(in.Tags); err != nil {
		return out, err
	}
	if out.Register && netboxKindOf[out.Kind] == "" {
		return out, ErrNotRegistrable
	}
	return out, nil
}

func (in CIInput) check(d *store.Data) error {
	for _, id := range in.OwnerIDs {
		if d.Users[id] == nil {
			return invalid("unknown_user", nil)
		}
	}
	return nil
}

// apply sets the fields a person edits. Owners keep the role NetBox gave them.
func (in CIInput) apply(ci *model.ConfigItem) {
	ci.Name, ci.Kind, ci.Status, ci.Description = in.Name, in.Kind, in.Status, in.Description
	ci.IPs, ci.Tags = orNil(in.IPs), orNil(in.Tags)
	roles := map[string]string{}
	for _, o := range ci.Owners {
		roles[o.UserID] = o.Role
	}
	var owners []model.CIOwner
	for _, id := range in.OwnerIDs {
		owners = append(owners, model.CIOwner{UserID: id, Role: roles[id]})
	}
	ci.Owners = owners
}

func (in CIInput) spec() netbox.Spec {
	return netbox.Spec{Name: in.Name, Status: in.Status, Description: in.Description}
}

func (s *CIService) register(ctx context.Context, in CIInput) (*model.NetBoxRef, error) {
	c, cfg, err := s.netbox.client()
	if err != nil {
		return nil, err
	}
	kind := netboxKindOf[in.Kind]
	o, err := c.Create(ctx, kind, in.spec())
	if errors.Is(err, netbox.ErrDefaults) {
		return nil, err
	}
	if err != nil {
		return nil, netboxFailure{err}
	}
	return &model.NetBoxRef{Kind: kind, ID: o.ID, URL: cfg.ObjectURL(kind, o.ID)}, nil
}

func (s *CIService) Create(ctx context.Context, actor string, in CIInput) (CIView, error) {
	in, err := in.normalize()
	if err != nil {
		return CIView{}, err
	}
	s.st.Read(func(d *store.Data) { err = in.check(d) })
	if err != nil {
		return CIView{}, err
	}
	var ref *model.NetBoxRef
	if in.Register {
		if ref, err = s.register(ctx, in); err != nil {
			return CIView{}, err
		}
	}
	var out CIView
	s.st.Write(func(d *store.Data) {
		now := s.now()
		ci := &model.ConfigItem{ID: d.NextID("CI"), Source: model.SourceLocal, NetBox: ref, CreatedAt: now, CreatedBy: actor, UpdatedAt: now, UpdatedBy: actor}
		in.apply(ci)
		d.ConfigItems[ci.ID] = ci
		detail := ci.Name + " (" + ci.Kind + ")"
		if ref != nil {
			detail += ", registered in NetBox as " + ref.Kind + " " + strconv.Itoa(ref.ID)
		}
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "ci.create", Object: ci.ID, Detail: detail})
		out = ciView(d, ci)
	})
	return out, nil
}

func (s *CIService) current(id string) (model.ConfigItem, error) {
	var out model.ConfigItem
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		if ci := d.ConfigItems[id]; ci != nil {
			out, err = *ci, nil
		}
	})
	return out, err
}

func (s *CIService) Update(ctx context.Context, actor, id string, in CIInput) (CIView, error) {
	cur, err := s.current(id)
	if err != nil {
		return CIView{}, err
	}
	if cur.Imported() {
		return CIView{}, ErrCIImported
	}
	if in, err = in.normalize(); err != nil {
		return CIView{}, err
	}
	s.st.Read(func(d *store.Data) { err = in.check(d) })
	if err != nil {
		return CIView{}, err
	}
	ref, note := cur.NetBox, ""
	if ref != nil && netboxKindOf[in.Kind] != ref.Kind {
		return CIView{}, invalid("ci_kind_fixed", nil)
	}
	if ref != nil {
		c, _, err := s.netbox.client()
		if err != nil {
			return CIView{}, err
		}
		switch err := c.Update(ctx, ref.Kind, ref.ID, in.spec()); {
		case errors.Is(err, netbox.ErrNotFound):
			ref, note = nil, ", gone from NetBox"
		case err != nil:
			return CIView{}, netboxFailure{err}
		}
	} else if in.Register {
		if ref, err = s.register(ctx, in); err != nil {
			return CIView{}, err
		}
		note = ", registered in NetBox as " + ref.Kind + " " + strconv.Itoa(ref.ID)
	}
	var out CIView
	err = ErrNotFound
	s.st.Write(func(d *store.Data) {
		ci := d.ConfigItems[id]
		if ci == nil {
			return
		}
		err = nil
		in.apply(ci)
		ci.NetBox, ci.UpdatedAt, ci.UpdatedBy = ref, s.now(), actor
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "ci.update", Object: id, Detail: ci.Name + note})
		out = ciView(d, ci)
	})
	return out, err
}

// Register creates a local item in NetBox and keeps it linked there.
func (s *CIService) Register(ctx context.Context, actor, id string) (CIView, error) {
	cur, err := s.current(id)
	if err != nil {
		return CIView{}, err
	}
	switch {
	case cur.Imported():
		return CIView{}, ErrCIImported
	case cur.NetBox != nil:
		return CIView{}, ErrCIRegistered
	case netboxKindOf[cur.Kind] == "":
		return CIView{}, ErrNotRegistrable
	}
	in := CIInput{Name: cur.Name, Kind: cur.Kind, Status: cur.Status, Description: cur.Description}
	ref, err := s.register(ctx, in)
	if err != nil {
		return CIView{}, err
	}
	var out CIView
	err = ErrNotFound
	s.st.Write(func(d *store.Data) {
		ci := d.ConfigItems[id]
		if ci == nil {
			return
		}
		err = nil
		ci.NetBox, ci.UpdatedAt, ci.UpdatedBy = ref, s.now(), actor
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "ci.register", Object: id, Detail: ci.Name + " as NetBox " + ref.Kind + " " + strconv.Itoa(ref.ID)})
		out = ciView(d, ci)
	})
	return out, err
}

// Delete removes the item here and, when it is kept in NetBox, there too.
func (s *CIService) Delete(ctx context.Context, actor, id string) error {
	cur, err := s.current(id)
	if err != nil {
		return err
	}
	note := ""
	if ref := cur.NetBox; ref != nil {
		c, _, err := s.netbox.client()
		if err != nil {
			return err
		}
		if err := c.Delete(ctx, ref.Kind, ref.ID); err != nil && !errors.Is(err, netbox.ErrNotFound) {
			return netboxFailure{err}
		}
		note = ", deleted in NetBox " + ref.Kind + " " + strconv.Itoa(ref.ID)
	}
	s.st.Write(func(d *store.Data) {
		if d.ConfigItems[id] == nil {
			return
		}
		delete(d.ConfigItems, id)
		dropCI(d, id)
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "ci.delete", Object: id, Detail: cur.Name + note})
	})
	return nil
}
