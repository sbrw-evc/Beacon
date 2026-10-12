package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/credentials"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/inventorydb"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// Inventory DB is an optional source of devices next to NetBox: Umbrella reads its devices as
// configuration items and sends it the state of the alerts on them, so the device card there
// shows what is on fire. It is off until someone connects it.

const (
	inventoryActor       = "inventory-db"
	inventorySyncTimeout = 10 * time.Minute
	inventoryPushEvery   = 30 * time.Second
)

var (
	ErrInventoryOff     = errors.New("Inventory DB is not connected")
	ErrInventoryRunning = errors.New("an Inventory DB synchronization is already running")
	errInventoryMoved   = errors.New("the Inventory DB address or integration changed: enter the API token and signing secret again")
)

func init() {
	statuses = append(statuses,
		orgStatus{ErrInventoryOff, http.StatusConflict, "inventory_db_off"},
		orgStatus{ErrInventoryRunning, http.StatusConflict, "inventory_db_sync_running"},
	)
	structured = append(structured, as(func(e inventoryFailure) answer {
		return answer{status: http.StatusBadGateway, code: "inventory_db_failed", detail: e.err}
	}))
}

// inventoryFailure is an error Inventory DB (or the way to it) returned.
type inventoryFailure struct{ err error }

func (e inventoryFailure) Error() string { return e.err.Error() }
func (e inventoryFailure) Unwrap() error { return e.err }

type InventoryDBService struct {
	st      *store.Store
	secrets Secrets
	active  activeSource
	now     func() time.Time
	running atomic.Bool
	pushing atomic.Bool
	// newClient is replaced in tests.
	newClient func(cfg inventorydb.Config, token, secret string) (*inventorydb.Client, error)
}

func NewInventoryDBService(st *store.Store, secrets Secrets, active activeSource) *InventoryDBService {
	return &InventoryDBService{st: st, secrets: secrets, active: active, now: func() time.Time { return time.Now().UTC() }, newClient: inventorydb.New}
}

type InventoryDBView struct {
	Config    inventorydb.Config `json:"config"`
	TokenSet  bool               `json:"token_set"`
	SecretSet bool               `json:"secret_set"`
	Sync      model.SyncState    `json:"sync"`
	Running   bool               `json:"running"`
	// Items: configuration items linked to Inventory DB devices; Pushed: alerts whose state
	// Inventory DB has.
	Items      int        `json:"items"`
	Pushed     int        `json:"pushed"`
	NextSyncAt *time.Time `json:"next_sync_at,omitempty"`
	// PublicURLSet: the public address of Umbrella is set, so alert states carry links back.
	PublicURLSet bool `json:"public_url_set"`
}

type InventoryDBRequest struct {
	Config inventorydb.Config `json:"config"`
	Token  string             `json:"token"`
	Secret string             `json:"secret"`
}

type InventoryDBTestReport struct {
	OK    bool              `json:"ok"`
	Probe inventorydb.Probe `json:"probe"`
	Error string            `json:"error,omitempty"`
}

func (s *InventoryDBService) stored() inventorydb.Config {
	var out inventorydb.Config
	s.st.Read(func(d *store.Data) { out = d.Settings.InventoryDB })
	return out
}

func (s *InventoryDBService) View() InventoryDBView {
	var out InventoryDBView
	s.st.Read(func(d *store.Data) {
		cfg := d.Settings.InventoryDB
		out.TokenSet, out.SecretSet = cfg.TokenRef != "", cfg.SecretRef != ""
		if cfg.URL == "" {
			cfg = inventorydb.Defaults()
		}
		out.Config, out.Sync, out.Pushed = cfg, d.InventoryDBSync, len(d.InventoryDBPushed)
		out.PublicURLSet = d.Settings.Alerting.PublicURL != ""
		for _, ci := range d.ConfigItems {
			if ci.InventoryDB != nil {
				out.Items++
			}
		}
		if cfg.Enabled && cfg.SyncMinutes > 0 {
			next := d.InventoryDBSync.StartedAt.Add(time.Duration(cfg.SyncMinutes) * time.Minute)
			if d.InventoryDBSync.StartedAt.IsZero() {
				next = s.now()
			}
			out.NextSyncAt = &next
		}
	})
	out.Running = s.running.Load()
	return out
}

func sameInventory(a, b inventorydb.Config) bool {
	return strings.EqualFold(a.URL, b.URL) && a.IntegrationID == b.IntegrationID
}

// secret returns the value given with the request or, for the same Inventory DB address and
// integration, the stored one. Stored secrets are never sent to another address.
func (s *InventoryDBService) secret(cfg inventorydb.Config, given, ref, code string) (string, error) {
	if given = strings.TrimSpace(given); given != "" {
		return given, nil
	}
	stored := s.stored()
	if ref == "" {
		return "", invalid(code, nil)
	}
	if !sameInventory(stored, cfg) {
		return "", invalid(code, errInventoryMoved)
	}
	if s.secrets == nil {
		return "", credentials.ErrUnavailable
	}
	v, err := s.secrets.Resolve(ref)
	if err != nil {
		return "", fmt.Errorf("%w: %v", credentials.ErrUnavailable, err)
	}
	return v, nil
}

func (s *InventoryDBService) Test(ctx context.Context, in InventoryDBRequest) (InventoryDBTestReport, error) {
	cfg, err := in.Config.Normalize()
	if err != nil {
		return InventoryDBTestReport{Error: err.Error()}, nil
	}
	tok, err := s.secret(cfg, in.Token, s.stored().TokenRef, "inventory_db_token_required")
	if err != nil {
		return InventoryDBTestReport{}, err
	}
	c, err := s.newClient(cfg, tok, "")
	if err != nil {
		return InventoryDBTestReport{Error: err.Error()}, nil
	}
	p, err := c.Test(ctx)
	if err != nil {
		return InventoryDBTestReport{Probe: p, Error: err.Error()}, nil
	}
	return InventoryDBTestReport{OK: true, Probe: p}, nil
}

func (s *InventoryDBService) Save(ctx context.Context, actor string, in InventoryDBRequest) (InventoryDBView, error) {
	if !in.Config.Enabled {
		s.st.Write(func(d *store.Data) {
			if d.Settings.InventoryDB.Enabled {
				d.Settings.InventoryDB.Enabled = false
				d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.inventory_db", Detail: "disabled"})
			}
		})
		return s.View(), nil
	}
	cfg, err := in.Config.Normalize()
	if err != nil {
		return InventoryDBView{}, invalid("inventory_db_invalid", err)
	}
	stored := s.stored()
	tok, err := s.secret(cfg, in.Token, stored.TokenRef, "inventory_db_token_required")
	if err != nil {
		return InventoryDBView{}, err
	}
	if cfg.PushStatus {
		if _, err := s.secret(cfg, in.Secret, stored.SecretRef, "inventory_db_secret_required"); err != nil {
			return InventoryDBView{}, err
		}
	}
	c, err := s.newClient(cfg, tok, "")
	if err != nil {
		return InventoryDBView{}, invalid("inventory_db_invalid", err)
	}
	if _, err := c.Test(ctx); err != nil {
		return InventoryDBView{}, invalid("inventory_db_unavailable", err)
	}
	cfg.TokenRef, cfg.SecretRef = stored.TokenRef, stored.SecretRef
	if !sameInventory(stored, cfg) {
		cfg.SecretRef = "" // a secret of another integration would never verify
	}
	put := func(key, value string, ref *string) error {
		if value = strings.TrimSpace(value); value == "" {
			return nil
		}
		if s.secrets == nil {
			return credentials.ErrUnavailable
		}
		r, err := s.secrets.PutRef(ctx, inventorydb.SecretPath, key, value)
		if err != nil {
			return fmt.Errorf("%w: %v", credentials.ErrUnavailable, err)
		}
		*ref = r
		return nil
	}
	if err := put(inventorydb.TokenKey, in.Token, &cfg.TokenRef); err != nil {
		return InventoryDBView{}, err
	}
	if err := put(inventorydb.SignatureKey, in.Secret, &cfg.SecretRef); err != nil {
		return InventoryDBView{}, err
	}
	s.st.Write(func(d *store.Data) {
		if !sameInventory(d.Settings.InventoryDB, cfg) {
			// The alert states sent so far are in the other integration.
			clear(d.InventoryDBPushed)
		}
		d.Settings.InventoryDB = cfg
		note := ""
		if strings.TrimSpace(in.Token) != "" {
			note += ", token replaced"
		}
		if strings.TrimSpace(in.Secret) != "" {
			note += ", signing secret replaced"
		}
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.inventory_db", Detail: "enabled " + cfg.URL + " " + cfg.IntegrationID + note})
	})
	return s.View(), nil
}

// client connects with the stored settings; withSecret also resolves the signing secret.
func (s *InventoryDBService) client(withSecret bool) (*inventorydb.Client, inventorydb.Config, error) {
	cfg := s.stored()
	if !cfg.Enabled || cfg.TokenRef == "" || (withSecret && cfg.SecretRef == "") {
		return nil, cfg, ErrInventoryOff
	}
	if s.secrets == nil {
		return nil, cfg, credentials.ErrUnavailable
	}
	tok, err := s.secrets.Resolve(cfg.TokenRef)
	if err != nil {
		return nil, cfg, fmt.Errorf("%w: %v", credentials.ErrUnavailable, err)
	}
	sec := ""
	if withSecret {
		if sec, err = s.secrets.Resolve(cfg.SecretRef); err != nil {
			return nil, cfg, fmt.Errorf("%w: %v", credentials.ErrUnavailable, err)
		}
	}
	c, err := s.newClient(cfg, tok, sec)
	if err != nil {
		return nil, cfg, inventoryFailure{err}
	}
	return c, cfg, nil
}

// Run reads devices on the configured interval and sends alert states until ctx ends.
func (s *InventoryDBService) Run(ctx context.Context) {
	tk := time.NewTicker(inventoryPushEvery)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			if s.due() {
				if _, err := s.Sync(ctx, inventoryActor); err != nil && !errors.Is(err, ErrInventoryRunning) {
					slog.Error("inventory-db: synchronization failed", "err", err)
				}
			}
			if _, err := s.Push(ctx); err != nil && !errors.Is(err, ErrInventoryOff) && !errors.Is(err, errEventsNotReady) {
				slog.Warn("inventory-db: alert states not sent, retrying", "err", err)
			}
		}
	}
}

func (s *InventoryDBService) due() bool {
	var cfg inventorydb.Config
	var last time.Time
	s.st.Read(func(d *store.Data) { cfg, last = d.Settings.InventoryDB, d.InventoryDBSync.StartedAt })
	return cfg.Enabled && cfg.ImportDevices && cfg.SyncMinutes > 0 && !s.now().Before(last.Add(time.Duration(cfg.SyncMinutes)*time.Minute))
}

// Sync reads devices from Inventory DB and updates configuration items. A failure to reach
// Inventory DB is recorded in the returned state.
func (s *InventoryDBService) Sync(ctx context.Context, actor string) (model.SyncState, error) {
	return s.sync(ctx, actor, false)
}

// SyncConfirmed removes the items missing from the answer even when it looks incomplete.
func (s *InventoryDBService) SyncConfirmed(ctx context.Context, actor string) (model.SyncState, error) {
	return s.sync(ctx, actor, true)
}

func (s *InventoryDBService) sync(ctx context.Context, actor string, confirmed bool) (model.SyncState, error) {
	if !s.running.CompareAndSwap(false, true) {
		return model.SyncState{}, ErrInventoryRunning
	}
	defer s.running.Store(false)
	ctx, cancel := context.WithTimeout(ctx, inventorySyncTimeout)
	defer cancel()
	state := model.SyncState{StartedAt: s.now(), Actor: actor}
	c, cfg, err := s.client(false)
	if errors.Is(err, ErrInventoryOff) {
		return state, err
	}
	if err == nil && !cfg.ImportDevices {
		err = errors.New("reading devices is turned off")
	}
	var feed []inventorydb.CI
	if err == nil {
		feed, err = c.Feed(ctx)
	}
	if err == nil {
		s.st.Write(func(d *store.Data) { state.Stats = applyInventoryDB(d, feed, s.now(), confirmed) })
		if st := state.Stats; st.Held > 0 {
			slog.Warn("inventory-db: items missing from the answer are kept, the answer looks incomplete",
				"objects", st.Objects, "missing", st.Held, "reason", st.HeldReason)
		}
		state.OK = true
	} else {
		state.Error = err.Error()
	}
	state.FinishedAt = s.now()
	s.st.Write(func(d *store.Data) {
		d.InventoryDBSync = state
		st := state.Stats
		changed := st.Created+st.Updated+st.Deleted+st.Unlinked+st.Linked+st.Held > 0
		if actor != inventoryActor || changed || !state.OK {
			detail := fmt.Sprintf("%d devices: %d created, %d updated, %d linked, %d deleted, %d unlinked",
				st.Objects, st.Created, st.Updated, st.Linked, st.Deleted, st.Unlinked)
			if st.Held > 0 {
				detail += fmt.Sprintf("; %d missing items kept (%s)", st.Held, st.HeldReason)
			}
			if confirmed {
				detail += "; removal of missing items confirmed"
			}
			if !state.OK {
				detail = "failed: " + state.Error
			}
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "inventory_db.sync", Detail: detail})
		}
	})
	return state, nil
}

// inventoryStatus maps the lifecycle status of a device to the statuses of items.
func inventoryStatus(v string) string {
	switch {
	case v == "inventory":
		return model.CIStatusPlanned
	case model.ValidCIStatus(v):
		return v
	}
	return model.CIStatusActive
}

// applyInventoryDB brings the items read from Inventory DB in line with its devices. A device
// whose name an item not yet linked to Inventory DB already has (made by hand, read from NetBox)
// is attached to that item instead of creating a second one; such items keep their own data.
// Unless confirmed, items missing from a suspicious answer are kept (see holdMissing).
func applyInventoryDB(d *store.Data, feed []inventorydb.CI, now time.Time, confirmed bool) model.SyncStats {
	var devices []inventorydb.CI
	for _, c := range feed {
		if c.Type == "device" && c.ID() > 0 && strings.TrimSpace(c.Name) != "" {
			devices = append(devices, c)
		}
	}
	stats := model.SyncStats{Objects: len(devices)}
	byID, byName := map[int]*model.ConfigItem{}, map[string]*model.ConfigItem{}
	for _, ci := range d.ConfigItems {
		if ci.InventoryDB != nil {
			byID[ci.InventoryDB.ID] = ci
		} else if k := strings.ToLower(ci.Name); byName[k] == nil || ci.ID < byName[k].ID {
			byName[k] = ci
		}
	}
	seen := map[int]bool{}
	for _, c := range devices {
		seen[c.ID()] = true
	}
	missing := 0
	for id := range byID {
		if !seen[id] {
			missing++
		}
	}
	hold := ""
	if !confirmed {
		hold = holdMissing(len(devices), len(byID), missing)
	}
	for _, c := range devices {
		ref := &model.InventoryRef{ID: c.ID(), URL: c.URL, Ref: c.SourceRef}
		ci := byID[c.ID()]
		if ci == nil {
			if ci = byName[strings.ToLower(c.Name)]; ci != nil {
				delete(byName, strings.ToLower(c.Name))
				ci.InventoryDB, ci.UpdatedAt, ci.UpdatedBy = ref, now, inventoryActor
				stats.Linked++
				continue
			}
			ci = &model.ConfigItem{ID: d.NextID("CI"), Source: model.SourceInventoryDB, CreatedAt: now, CreatedBy: inventoryActor, UpdatedAt: now, UpdatedBy: inventoryActor}
			d.ConfigItems[ci.ID] = ci
			stats.Created++
		}
		before := *ci
		ci.InventoryDB = ref
		if ci.Source == model.SourceInventoryDB {
			ci.Name, ci.Kind, ci.Status = c.Name, model.CIKindDevice, inventoryStatus(c.Status)
			ci.IPs, ci.Tags = orNil(c.Identities.IP), orNil(c.Tags())
			ci.Attrs = model.CIAttrs{Site: c.Attr("site"), Role: c.Attr("role"), DeviceType: strings.TrimSpace(c.Attr("manufacturer") + " " + c.Attr("model")),
				Tenant: c.Attr("tenant"), Platform: c.Attr("platform"), Parent: c.Attr("rack"), Serial: c.Identities.Serial}
			synced := now
			ci.SyncedAt = &synced
			before.SyncedAt = ci.SyncedAt
		}
		if before.CreatedAt != now && !reflect.DeepEqual(before, *ci) {
			ci.UpdatedAt, ci.UpdatedBy = now, inventoryActor
			stats.Updated++
		}
	}
	if hold != "" {
		stats.Held, stats.HeldReason = missing, hold
		return stats
	}
	for id, ci := range d.ConfigItems {
		if ci.InventoryDB == nil || seen[ci.InventoryDB.ID] {
			continue
		}
		if ci.Source == model.SourceInventoryDB {
			delete(d.ConfigItems, id)
			dropCI(d, id)
			stats.Deleted++
			continue
		}
		ci.InventoryDB, ci.UpdatedAt, ci.UpdatedBy = nil, now, inventoryActor
		stats.Unlinked++
	}
	return stats
}

// inventorySeverity: Inventory DB knows the four PagerDuty levels; low (P4) is shown there as info.
func inventorySeverity(v string) string {
	if v == model.SeverityLow {
		return model.SeverityInfo
	}
	return v
}

// PushReport is what one sending of alert states did.
type PushReport struct {
	Sent     int `json:"sent"`
	Resolved int `json:"resolved"`
}

// Push sends Inventory DB the alerts on its devices whose state changed since the last sending,
// and reports alerts that are no longer active as resolved.
func (s *InventoryDBService) Push(ctx context.Context) (PushReport, error) {
	var out PushReport
	cfg := s.stored()
	if !cfg.Enabled || !cfg.PushStatus || s.active == nil {
		return out, ErrInventoryOff
	}
	if !s.pushing.CompareAndSwap(false, true) {
		return out, nil
	}
	defer s.pushing.Store(false)
	alerts, err := s.active(ctx)
	if err != nil {
		return out, err
	}
	now := s.now()
	var events []inventorydb.AlertEvent
	next := map[string]*model.PushedAlert{}
	s.st.Read(func(d *store.Data) {
		base := strings.TrimRight(d.Settings.Alerting.PublicURL, "/")
		grafana := d.Settings.Alerting.Grafana.DashboardURL != ""
		current := map[string]bool{}
		for _, a := range alerts {
			ci := d.ConfigItems[a.CIID]
			if ci == nil || ci.InventoryDB == nil {
				continue
			}
			current[a.ID] = true
			sev := inventorySeverity(a.Severity)
			key := strings.Join([]string{a.Status, sev, a.Title, ci.InventoryDB.Ref}, "\x00")
			if p, ok := d.InventoryDBPushed[a.ID]; ok && p.Key == key {
				continue
			}
			ev := inventorydb.AlertEvent{AlertID: a.ID, Status: a.Status, Severity: sev, Title: a.Title, Signal: a.Signal,
				CI: inventorydb.AlertCI{SourceRefs: []string{ci.InventoryDB.Ref}, Name: ci.Name}, UpdatedAt: now}
			if base != "" {
				ev.Links.Incident = model.IncidentURL(base, a.ID)
				if grafana {
					ev.Links.Grafana = model.GrafanaHopURL(base, a.ID)
				}
			}
			events = append(events, ev)
			next[a.ID] = &model.PushedAlert{Key: key, Ref: ci.InventoryDB.Ref, Title: a.Title, Sev: sev, At: now}
		}
		for id, p := range d.InventoryDBPushed {
			if current[id] {
				continue
			}
			events = append(events, inventorydb.AlertEvent{AlertID: id, Status: alert.StatusResolved, Severity: p.Sev, Title: p.Title,
				CI: inventorydb.AlertCI{SourceRefs: []string{p.Ref}}, UpdatedAt: now})
			next[id] = nil
			out.Resolved++
		}
	})
	if len(events) == 0 {
		return out, nil
	}
	c, _, err := s.client(true)
	if err != nil {
		return PushReport{}, err
	}
	if err := c.Push(ctx, events); err != nil {
		return PushReport{}, inventoryFailure{err}
	}
	out.Sent = len(events) - out.Resolved
	s.st.Write(func(d *store.Data) {
		for id, p := range next {
			if p == nil {
				delete(d.InventoryDBPushed, id)
			} else {
				d.InventoryDBPushed[id] = *p
			}
		}
	})
	return out, nil
}
