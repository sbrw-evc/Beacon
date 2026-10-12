package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/credentials"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/netbox"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	netboxSyncTimeout = 10 * time.Minute
	netboxActor       = "netbox"
)

var (
	ErrNetBoxOff   = errors.New("NetBox is not connected")
	ErrSyncRunning = errors.New("a NetBox synchronization is already running")
	errURLChanged  = errors.New("the NetBox address changed: enter the API token again")
)

func init() {
	statuses = append(statuses,
		orgStatus{ErrNetBoxOff, http.StatusConflict, "netbox_off"},
		orgStatus{ErrSyncRunning, http.StatusConflict, "netbox_sync_running"},
	)
}

// netboxFailure is an error NetBox (or the way to it) returned while Umbrella acted there.
type netboxFailure struct{ err error }

func (e netboxFailure) Error() string { return e.err.Error() }
func (e netboxFailure) Unwrap() error { return e.err }

// NetBoxService keeps the NetBox connection and synchronizes configuration items, their
// responsible people and the matching computer objects of the domain controller.
type NetBoxService struct {
	st        *store.Store
	secrets   Secrets
	computers func(directory.Config, string) ([]directory.Computer, error)
	now       func() time.Time
	running   atomic.Bool
}

func NewNetBoxService(st *store.Store, secrets Secrets) *NetBoxService {
	return &NetBoxService{st: st, secrets: secrets, computers: directory.Computers, now: func() time.Time { return time.Now().UTC() }}
}

type NetBoxView struct {
	Config             netbox.Config   `json:"config"`
	TokenSet           bool            `json:"token_set"`
	Sync               model.SyncState `json:"sync"`
	Running            bool            `json:"running"`
	NextSyncAt         *time.Time      `json:"next_sync_at,omitempty"`
	DirectoryAvailable bool            `json:"directory_available"`
	Items              int             `json:"items"`
	Users              int             `json:"users"`
}

type NetBoxRequest struct {
	Config netbox.Config `json:"config"`
	Token  string        `json:"token"`
}

type NetBoxTestReport struct {
	OK    bool         `json:"ok"`
	Probe netbox.Probe `json:"probe"`
	Error string       `json:"error,omitempty"`
}

func (s *NetBoxService) stored() netbox.Config {
	var out netbox.Config
	s.st.Read(func(d *store.Data) { out = d.Settings.NetBox })
	return out
}

func directoryHasComputers(c directory.Config) bool { return c.Enabled && c.Kind == directory.KindAD }

func (s *NetBoxService) View() NetBoxView {
	var out NetBoxView
	s.st.Read(func(d *store.Data) {
		cfg := d.Settings.NetBox
		out.TokenSet = cfg.TokenRef != ""
		if cfg.URL == "" {
			cfg = netbox.Defaults()
		}
		out.Config, out.Sync = cfg, d.NetBoxSync
		out.DirectoryAvailable = directoryHasComputers(d.Settings.LDAP)
		for _, ci := range d.ConfigItems {
			if ci.NetBox != nil {
				out.Items++
			}
		}
		for _, u := range d.Users {
			if u.Source == model.SourceNetBox {
				out.Users++
			}
		}
		if cfg.Enabled && cfg.SyncMinutes > 0 {
			next := d.NetBoxSync.StartedAt.Add(time.Duration(cfg.SyncMinutes) * time.Minute)
			if d.NetBoxSync.StartedAt.IsZero() {
				next = s.now()
			}
			out.NextSyncAt = &next
		}
	})
	out.Running = s.running.Load()
	return out
}

// token returns the token given with the request or, for the same NetBox address, the stored one.
// The stored token is never sent to another address.
func (s *NetBoxService) token(cfg netbox.Config, given string) (string, error) {
	if given = strings.TrimSpace(given); given != "" {
		return given, nil
	}
	stored := s.stored()
	if stored.TokenRef == "" {
		return "", invalid("netbox_token_required", nil)
	}
	if !strings.EqualFold(stored.URL, cfg.URL) {
		return "", invalid("netbox_token_required", errURLChanged)
	}
	if s.secrets == nil {
		return "", credentials.ErrUnavailable
	}
	tok, err := s.secrets.Resolve(stored.TokenRef)
	if err != nil {
		return "", fmt.Errorf("%w: %v", credentials.ErrUnavailable, err)
	}
	return tok, nil
}

func (s *NetBoxService) Test(ctx context.Context, in NetBoxRequest) (NetBoxTestReport, error) {
	cfg, err := in.Config.Normalize()
	if err != nil {
		return NetBoxTestReport{Error: err.Error()}, nil
	}
	tok, err := s.token(cfg, in.Token)
	if err != nil {
		return NetBoxTestReport{}, err
	}
	c, err := netbox.New(cfg, tok)
	if err != nil {
		return NetBoxTestReport{Error: err.Error()}, nil
	}
	p, err := c.Test(ctx)
	if err != nil {
		return NetBoxTestReport{Probe: p, Error: err.Error()}, nil
	}
	return NetBoxTestReport{OK: true, Probe: p}, nil
}

func (s *NetBoxService) Save(ctx context.Context, actor string, in NetBoxRequest) (NetBoxView, error) {
	if !in.Config.Enabled {
		s.st.Write(func(d *store.Data) {
			if d.Settings.NetBox.Enabled {
				d.Settings.NetBox.Enabled = false
				d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.netbox", Detail: "disabled"})
			}
		})
		return s.View(), nil
	}
	cfg, err := in.Config.Normalize()
	if err != nil {
		return NetBoxView{}, invalid("netbox_invalid", err)
	}
	tok, err := s.token(cfg, in.Token)
	if err != nil {
		return NetBoxView{}, err
	}
	c, err := netbox.New(cfg, tok)
	if err != nil {
		return NetBoxView{}, invalid("netbox_invalid", err)
	}
	if _, err := c.Test(ctx); err != nil {
		return NetBoxView{}, invalid("netbox_unavailable", err)
	}
	cfg.TokenRef = s.stored().TokenRef
	if strings.TrimSpace(in.Token) != "" {
		if s.secrets == nil {
			return NetBoxView{}, credentials.ErrUnavailable
		}
		if cfg.TokenRef, err = s.secrets.PutRef(ctx, netbox.SecretPath, netbox.SecretKey, strings.TrimSpace(in.Token)); err != nil {
			return NetBoxView{}, fmt.Errorf("%w: %v", credentials.ErrUnavailable, err)
		}
	}
	s.st.Write(func(d *store.Data) {
		d.Settings.NetBox = cfg
		d.Settings.NetBoxUsersDecided = true
		note := ""
		if strings.TrimSpace(in.Token) != "" {
			note = ", token replaced"
		}
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.netbox", Detail: "enabled " + cfg.URL + note})
	})
	return s.View(), nil
}

// client connects with the stored settings.
func (s *NetBoxService) client() (*netbox.Client, netbox.Config, error) {
	cfg := s.stored()
	if !cfg.Enabled || cfg.TokenRef == "" {
		return nil, cfg, ErrNetBoxOff
	}
	if s.secrets == nil {
		return nil, cfg, credentials.ErrUnavailable
	}
	tok, err := s.secrets.Resolve(cfg.TokenRef)
	if err != nil {
		return nil, cfg, fmt.Errorf("%w: %v", credentials.ErrUnavailable, err)
	}
	c, err := netbox.New(cfg, tok)
	if err != nil {
		return nil, cfg, netboxFailure{err}
	}
	return c, cfg, nil
}

func (s *NetBoxService) Choices(ctx context.Context) (netbox.Choices, error) {
	c, _, err := s.client()
	if err != nil {
		return netbox.Choices{}, err
	}
	out, err := c.Choices(ctx)
	if err != nil {
		return out, netboxFailure{err}
	}
	return out, nil
}

// Run synchronizes on the configured interval until ctx ends.
func (s *NetBoxService) Run(ctx context.Context) {
	tk := time.NewTicker(time.Minute)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			if s.due() {
				if _, err := s.Sync(ctx, netboxActor); err != nil && !errors.Is(err, ErrSyncRunning) {
					slog.Error("netbox: synchronization failed", "err", err)
				}
			}
		}
	}
}

func (s *NetBoxService) due() bool {
	var cfg netbox.Config
	var last time.Time
	s.st.Read(func(d *store.Data) { cfg, last = d.Settings.NetBox, d.NetBoxSync.StartedAt })
	return cfg.Enabled && cfg.SyncMinutes > 0 && !s.now().Before(last.Add(time.Duration(cfg.SyncMinutes)*time.Minute))
}

// Sync reads NetBox and updates configuration items and user accounts. A failure to reach
// NetBox is recorded in the returned state; the error is only for a sync that cannot start.
func (s *NetBoxService) Sync(ctx context.Context, actor string) (model.SyncState, error) {
	return s.sync(ctx, actor, false)
}

// SyncConfirmed is Sync where the person confirmed that the items missing from NetBox are to be
// removed even when the answer looks incomplete (see holdMissing).
func (s *NetBoxService) SyncConfirmed(ctx context.Context, actor string) (model.SyncState, error) {
	return s.sync(ctx, actor, true)
}

func (s *NetBoxService) sync(ctx context.Context, actor string, confirmed bool) (model.SyncState, error) {
	if !s.running.CompareAndSwap(false, true) {
		return model.SyncState{}, ErrSyncRunning
	}
	defer s.running.Store(false)
	ctx, cancel := context.WithTimeout(ctx, netboxSyncTimeout)
	defer cancel()
	state := model.SyncState{StartedAt: s.now(), Actor: actor}
	c, cfg, err := s.client()
	if errors.Is(err, ErrNetBoxOff) {
		return state, err
	}
	var inv netbox.Inventory
	if err == nil {
		inv, err = c.Fetch(ctx)
	}
	if err == nil {
		s.st.Write(func(d *store.Data) { state.Stats = applyInventory(d, cfg, inv, s.now(), confirmed) })
		if st := state.Stats; st.Held > 0 {
			slog.Warn("netbox: items missing from the answer are kept, the answer looks incomplete",
				"objects", st.Objects, "missing", st.Held, "reason", st.HeldReason)
		}
		s.syncDirectory(cfg, &state.Stats)
		state.OK = true
	} else {
		state.Error = err.Error()
	}
	state.FinishedAt = s.now()
	s.st.Write(func(d *store.Data) {
		d.NetBoxSync = state
		st := state.Stats
		changed := st.Created+st.Updated+st.Deleted+st.Unlinked+st.UsersCreated+st.UsersUpdated+st.UsersLinked+st.Held > 0
		if actor != netboxActor || changed || !state.OK {
			detail := fmt.Sprintf("%d objects: %d created, %d updated, %d deleted, %d unlinked; users: %d created, %d updated, %d linked",
				st.Objects, st.Created, st.Updated, st.Deleted, st.Unlinked, st.UsersCreated, st.UsersUpdated, st.UsersLinked)
			if st.Held > 0 {
				detail += fmt.Sprintf("; %d missing items kept (%s)", st.Held, st.HeldReason)
			}
			if confirmed {
				detail += "; removal of missing items confirmed"
			}
			if !state.OK {
				detail = "failed: " + state.Error
			}
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "netbox.sync", Detail: detail})
		}
	})
	return state, nil
}

func (s *NetBoxService) syncDirectory(cfg netbox.Config, stats *model.SyncStats) {
	var ldap directory.Config
	s.st.Read(func(d *store.Data) { ldap = d.Settings.LDAP })
	if !cfg.SyncDirectory || !directoryHasComputers(ldap) {
		s.st.Write(func(d *store.Data) {
			for _, ci := range d.ConfigItems {
				ci.Directory = nil
			}
		})
		return
	}
	stats.DirectoryChecked = true
	bind, err := s.secrets.Resolve(ldap.BindPasswordRef)
	var computers []directory.Computer
	if err == nil {
		computers, err = s.computers(ldap, bind)
	}
	if err != nil {
		stats.DirectoryError = err.Error()
		return
	}
	s.st.Write(func(d *store.Data) {
		stats.DirectoryMatched, stats.DirectoryMissing = applyComputers(d, computers, s.now())
	})
}

var ciKindOf = map[string]string{netbox.KindDevice: model.CIKindDevice, netbox.KindVM: model.CIKindVM, netbox.KindService: model.CIKindService}

func refKey(kind string, id int) string { return kind + ":" + strconv.Itoa(id) }

// Reasons a synchronization keeps the items missing from NetBox's answer.
const (
	heldEmpty = "empty" // NetBox returned no objects at all
	heldShare = "share" // more than half of the linked items are missing
)

// holdMissing decides whether the items missing from the answer are kept instead of being
// deleted or unlinked: an empty answer or one without most of the known objects is far more
// likely a lost permission or a broken filter than a real cleanup, and removing the items loses
// their service bindings and IDs for good. A person can confirm the removal.
func holdMissing(objects, linked, missing int) string {
	switch {
	case missing == 0:
		return ""
	case objects == 0:
		return heldEmpty
	case 2*missing > linked:
		return heldShare
	}
	return ""
}

// applyInventory brings configuration items and the accounts of their responsible people in
// line with what was read from NetBox. Unless confirmed, items missing from a suspicious answer
// are kept (see holdMissing).
func applyInventory(d *store.Data, cfg netbox.Config, inv netbox.Inventory, now time.Time, confirmed bool) model.SyncStats {
	stats := model.SyncStats{Objects: len(inv.Objects)}
	users := map[int]string{}
	if cfg.SyncContacts {
		users = syncContacts(d, cfg, inv, now, &stats)
	}
	owners := map[string][]model.CIOwner{}
	for _, a := range inv.Assignments {
		if uid := users[a.ContactID]; uid != "" {
			k := refKey(a.Kind, a.ObjectID)
			owners[k] = append(owners[k], model.CIOwner{UserID: uid, Role: a.Role})
		}
	}
	byRef := map[string]*model.ConfigItem{}
	for _, ci := range d.ConfigItems {
		if ci.NetBox != nil {
			byRef[refKey(ci.NetBox.Kind, ci.NetBox.ID)] = ci
		}
	}
	seen := map[string]bool{}
	for _, o := range inv.Objects {
		seen[refKey(o.Kind, o.ID)] = true
	}
	linked, missing := 0, 0
	for k, ci := range byRef {
		if cfg.Imports(ci.NetBox.Kind) {
			linked++
			if !seen[k] {
				missing++
			}
		}
	}
	hold := ""
	if !confirmed {
		hold = holdMissing(len(inv.Objects), linked, missing)
	}
	for _, o := range inv.Objects {
		k := refKey(o.Kind, o.ID)
		ci := byRef[k]
		created := ci == nil
		if created {
			ci = &model.ConfigItem{ID: d.NextID("CI"), Source: model.SourceNetBox, CreatedAt: now, CreatedBy: netboxActor, UpdatedAt: now, UpdatedBy: netboxActor}
			d.ConfigItems[ci.ID] = ci
			stats.Created++
		}
		before := *ci
		ci.Name, ci.Kind, ci.Status, ci.Description = o.Name, ciKindOf[o.Kind], o.Status, o.Description
		ci.IPs, ci.Tags = orNil(o.IPs), orNil(o.Tags)
		ci.Attrs = model.CIAttrs{Site: o.Site, Role: o.Role, DeviceType: o.DeviceType, Cluster: o.Cluster, Tenant: o.Tenant,
			Platform: o.Platform, Parent: o.Parent, Ports: o.Ports, Serial: o.Serial}
		ci.NetBox = &model.NetBoxRef{Kind: o.Kind, ID: o.ID, URL: cfg.ObjectURL(o.Kind, o.ID)}
		if cfg.SyncContacts {
			ci.Owners = normalOwners(owners[k])
		}
		if !created && !reflect.DeepEqual(before, *ci) {
			ci.UpdatedAt, ci.UpdatedBy = now, netboxActor
			stats.Updated++
		}
		synced := now
		ci.SyncedAt = &synced
	}
	if hold != "" {
		stats.Held, stats.HeldReason = missing, hold
	}
	for id, ci := range d.ConfigItems {
		if hold != "" || ci.NetBox == nil || seen[refKey(ci.NetBox.Kind, ci.NetBox.ID)] || !cfg.Imports(ci.NetBox.Kind) {
			continue
		}
		if ci.Imported() {
			// The same cleanup as a manual deletion: services and monitoring hosts drop the item.
			delete(d.ConfigItems, id)
			dropCI(d, id)
			stats.Deleted++
			continue
		}
		ci.NetBox, ci.UpdatedAt, ci.UpdatedBy = nil, now, netboxActor
		stats.Unlinked++
	}
	applyServiceTags(d, cfg, inv.Tags, seen, now, &stats, hold != "")
	return stats
}

// orNil keeps empty lists as nil, the way a restored snapshot has them, so that a
// synchronization after a restart does not count every item as changed.
func orNil[T any](v []T) []T {
	if len(v) == 0 {
		return nil
	}
	return v
}

func normalOwners(in []model.CIOwner) []model.CIOwner {
	var out []model.CIOwner
	for _, o := range in {
		if !slices.Contains(out, o) {
			out = append(out, o)
		}
	}
	slices.SortFunc(out, func(a, b model.CIOwner) int {
		if c := strings.Compare(a.UserID, b.UserID); c != 0 {
			return c
		}
		return strings.Compare(a.Role, b.Role)
	})
	return out
}

// syncContacts finds or creates a user account for every contact assigned to an object and
// returns the account of each contact. Accounts made for contacts follow the contact; accounts
// found by e-mail keep their own source and data.
func syncContacts(d *store.Data, cfg netbox.Config, inv netbox.Inventory, now time.Time, stats *model.SyncStats) map[int]string {
	assigned := map[int]bool{}
	for _, a := range inv.Assignments {
		assigned[a.ContactID] = true
	}
	contacts := slices.Clone(inv.Contacts)
	slices.SortFunc(contacts, func(a, b netbox.Contact) int { return a.ID - b.ID })
	out := map[int]string{}
	for _, c := range contacts {
		if !assigned[c.ID] {
			continue
		}
		stats.Contacts++
		u := d.Users[d.NetBoxContacts[c.ID]]
		email := strings.TrimSpace(c.Email)
		if !model.ValidEmail(email) {
			email = ""
		}
		if u == nil && email != "" {
			if u = userByEmail(d, email); u != nil {
				stats.UsersLinked++
			}
		}
		if u == nil && !cfg.CreateUsers {
			stats.UsersSkipped++
			continue
		}
		if u == nil {
			u = &model.User{ID: d.NextID("USR"), Username: contactUsername(d, c.ID, email), Source: model.SourceNetBox,
				ExternalID: "netbox-contact-" + strconv.Itoa(c.ID), Role: d.NewUserRole(), CreatedAt: now}
			d.Users[u.ID] = u
			d.AddAudit(store.AuditEntry{Actor: netboxActor, Action: "user.create", Object: u.ID, Detail: u.Username + " (netbox contact " + strconv.Itoa(c.ID) + ")"})
			stats.UsersCreated++
		} else if u.Source == model.SourceNetBox && contactChanged(u, c, email) {
			stats.UsersUpdated++
		}
		if u.Source == model.SourceNetBox {
			applyContact(u, c, email)
		}
		out[c.ID] = u.ID
	}
	d.NetBoxContacts = out
	return out
}

func userByEmail(d *store.Data, email string) *model.User {
	var found *model.User
	for _, u := range d.Users {
		if strings.EqualFold(u.Email, email) && (found == nil || u.ID < found.ID) {
			found = u
		}
	}
	return found
}

func contactUsername(d *store.Data, id int, email string) string {
	base := "netbox-" + strconv.Itoa(id)
	if email != "" && ValidUsername(email) {
		base = strings.ToLower(email)
	}
	name := base
	for i := 2; d.UserByName(name) != nil; i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return name
}

// contactProfile splits the contact name the way people write it: "First Last" or
// "Last First Middle"; anything else stays whole.
func contactProfile(c netbox.Contact, email string) model.Profile {
	p := model.Profile{Title: c.Title, Email: email}
	parts := strings.Fields(c.Name)
	switch len(parts) {
	case 2:
		p.FirstName, p.LastName = parts[0], parts[1]
	case 3:
		p.LastName, p.FirstName, p.MiddleName = parts[0], parts[1], parts[2]
	default:
		p.LastName = strings.Join(parts, " ")
	}
	if n, err := p.Normalize(); err == nil {
		return n
	}
	return model.Profile{Email: email}
}

func contactChanged(u *model.User, c netbox.Contact, email string) bool {
	return u.Profile != contactProfile(c, email) || u.Name != strings.TrimSpace(c.Name)
}

func applyContact(u *model.User, c netbox.Contact, email string) {
	u.Profile = contactProfile(c, email)
	u.Name = strings.TrimSpace(c.Name)
	if u.Name == "" {
		u.Name = u.Username
	}
}

// applyComputers matches devices and virtual machines with computer objects by name or DNS name.
func applyComputers(d *store.Data, computers []directory.Computer, now time.Time) (matched, missing int) {
	index := map[string]directory.Computer{}
	for _, pc := range computers {
		index[strings.ToLower(pc.Name)] = pc
		if pc.DNSName != "" {
			index[pc.DNSName] = pc
		}
	}
	for _, ci := range d.ConfigItems {
		if ci.Kind != model.CIKindDevice && ci.Kind != model.CIKindVM {
			ci.Directory = nil
			continue
		}
		name := strings.ToLower(ci.Name)
		short, _, _ := strings.Cut(name, ".")
		pc, ok := index[name]
		if !ok {
			pc, ok = index[short]
		}
		if !ok {
			ci.Directory = &model.CIDirectory{Status: model.DirectoryMissing, CheckedAt: now}
			missing++
			continue
		}
		ci.Directory = &model.CIDirectory{Status: model.DirectoryMatched, DN: pc.DN, DNSName: pc.DNSName, OS: pc.OS, OSVersion: pc.OSVersion,
			LastLogon: pc.LastLogon, Disabled: pc.Disabled, CheckedAt: now}
		matched++
	}
	return matched, missing
}
