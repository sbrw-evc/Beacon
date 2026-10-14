package store

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/access"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

const MaxAudit = 5000

type Data struct {
	Users         map[string]*model.User
	Roles         map[string]*model.Role
	Teams         map[string]*model.Team
	Services      map[string]*model.Service
	Connectors    map[string]*model.Connector
	Credentials   map[string]*model.Credential
	ConfigItems   map[string]*model.ConfigItem
	Maintenance   map[string]*model.Maintenance
	Wallboards    map[string]*model.Wallboard
	MetricSources map[string]*model.MetricSource
	Rules         map[string]*model.Rule
	// MonitoringSources are the Zabbix, Prometheus, Grafana and Graylog systems hosts are read from.
	MonitoringSources map[string]*model.MonitoringSource
	// LogSources are the log stores the incident card reads the lines of a machine from.
	LogSources map[string]*model.LogSource
	// NetBoxContacts maps NetBox contact IDs to the user accounts made or found for them.
	NetBoxContacts map[int]string
	NetBoxSync     model.SyncState
	// InventoryDBSync is the last read of devices from Inventory DB.
	InventoryDBSync model.SyncState
	// InventoryDBPushed is the last alert state sent to Inventory DB, by alert ID.
	InventoryDBPushed map[string]model.PushedAlert
	GroupSync         model.GroupSyncState
	// ServiceAccounts are the machine identities that call the API with API tokens.
	ServiceAccounts map[string]*model.ServiceAccount
	// APITokens are the tokens of service accounts, by token ID (only keyed hashes are kept).
	APITokens map[string]*model.APIToken
	// Proposals are the dependency proposals of Umbrella Flow, by proposal key.
	Proposals map[string]*model.DependencyProposal
	// Applications are the applications connected to Umbrella Identity, by ID.
	Applications map[string]*model.Application
	Settings     model.Settings
	Audit        []AuditEntry
	Seq          map[string]int
}

type AuditEntry struct {
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Object string    `json:"object,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

type Store struct {
	mu      sync.RWMutex
	d       Data
	version uint64

	persistMu sync.Mutex
	backend   Backend
	saved     uint64
	lastErr   error
}

func New() *Store {
	s := &Store{}
	s.d.init()
	return s
}

func (d *Data) init() {
	if d.Users == nil {
		d.Users = map[string]*model.User{}
	}
	if d.Roles == nil {
		d.Roles = map[string]*model.Role{}
	}
	if d.Teams == nil {
		d.Teams = map[string]*model.Team{}
	}
	if d.Services == nil {
		d.Services = map[string]*model.Service{}
	}
	if d.Connectors == nil {
		d.Connectors = map[string]*model.Connector{}
	}
	if d.Credentials == nil {
		d.Credentials = map[string]*model.Credential{}
	}
	if d.ConfigItems == nil {
		d.ConfigItems = map[string]*model.ConfigItem{}
	}
	if d.Maintenance == nil {
		d.Maintenance = map[string]*model.Maintenance{}
	}
	if d.Wallboards == nil {
		d.Wallboards = map[string]*model.Wallboard{}
	}
	if d.MetricSources == nil {
		d.MetricSources = map[string]*model.MetricSource{}
	}
	if d.Rules == nil {
		d.Rules = map[string]*model.Rule{}
	}
	if d.MonitoringSources == nil {
		d.MonitoringSources = map[string]*model.MonitoringSource{}
	}
	if d.LogSources == nil {
		d.LogSources = map[string]*model.LogSource{}
	}
	// gob drops empty lists: a snapshot gives back nil where the hosts had [].
	for _, src := range d.MonitoringSources {
		if src != nil {
			src.NormalizeHosts()
		}
	}
	// Older snapshots have one team per user.
	for _, u := range d.Users {
		if u != nil {
			u.MigrateTeams()
			u.MigrateScope()
		}
	}
	if d.ServiceAccounts == nil {
		d.ServiceAccounts = map[string]*model.ServiceAccount{}
	}
	if d.APITokens == nil {
		d.APITokens = map[string]*model.APIToken{}
	}
	if d.Applications == nil {
		d.Applications = map[string]*model.Application{}
	}
	if d.Proposals == nil {
		d.Proposals = map[string]*model.DependencyProposal{}
	}
	if d.InventoryDBPushed == nil {
		d.InventoryDBPushed = map[string]model.PushedAlert{}
	}
	if d.NetBoxContacts == nil {
		d.NetBoxContacts = map[int]string{}
	}
	if d.Seq == nil {
		d.Seq = map[string]int{}
	}
}

func (s *Store) Read(f func(d *Data)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f(&s.d)
}

func (s *Store) Write(f func(d *Data)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	f(&s.d)
}

// Update is Write for changes that often turn out to be none: f reports whether it changed
// anything, and only then do readers and persistence see a new version.
func (s *Store) Update(f func(d *Data) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f(&s.d) {
		s.version++
	}
}

// Version changes with every write, so readers can tell whether a copy they keep is stale.
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

func (d *Data) NextID(prefix string) string {
	d.Seq[prefix]++
	return fmt.Sprintf("%s-%d", prefix, d.Seq[prefix])
}

func (d *Data) AddAudit(a AuditEntry) {
	if a.At.IsZero() {
		a.At = time.Now().UTC()
	}
	d.Audit = append(d.Audit, a)
	if len(d.Audit) > MaxAudit {
		d.Audit = append([]AuditEntry(nil), d.Audit[len(d.Audit)-MaxAudit:]...)
	}
}

func (d *Data) EnsureSystemRoles(now time.Time) bool {
	added := false
	for _, r := range model.SystemRoles(now) {
		if d.Roles[r.ID] == nil {
			d.Roles[r.ID] = r
			added = true
		}
	}
	return added
}

// EnsurePresetRoles creates the preset roles once. A preset whose name is already taken by
// another role is skipped. The role for new users becomes the viewer preset unless it is set.
func (d *Data) EnsurePresetRoles(locale string, now time.Time) bool {
	if d.Settings.PresetRolesSeeded {
		return false
	}
	d.Settings.PresetRolesSeeded = true
	taken := func(name string) bool {
		for _, r := range d.Roles {
			if strings.EqualFold(r.Name, name) {
				return true
			}
		}
		return false
	}
	for _, r := range model.PresetRoles(locale, now) {
		if d.Roles[r.ID] != nil || taken(r.Name) {
			continue
		}
		r.Permissions = access.Normalize(r.Permissions)
		d.Roles[r.ID] = r
	}
	if d.Settings.NewUserRole == "" && d.Roles[model.RoleViewer] != nil {
		d.Settings.NewUserRole = model.RoleViewer
	}
	return true
}

// NewUserRole is the role given to an account a directory or NetBox creates without a mapped
// role: the configured one while it exists and is not the administrator role, otherwise "user".
func (d *Data) NewUserRole() string {
	if id := d.Settings.NewUserRole; id != "" && id != model.RoleAdmin && d.Roles[id] != nil {
		return id
	}
	return model.RoleUser
}

func (d *Data) RoleOf(u *model.User) *model.Role {
	if r := d.Roles[u.Role]; r != nil {
		return r
	}
	id := model.RoleUser
	if u.Role == model.RoleAdmin {
		id = model.RoleAdmin
	}
	if r := d.Roles[id]; r != nil {
		return r
	}
	for _, r := range model.SystemRoles(time.Time{}) {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// IsAdmin reports whether u holds the administrator role, the one role that has every
// permission and sees everything.
func (d *Data) IsAdmin(u *model.User) bool {
	if u == nil {
		return false
	}
	r := d.RoleOf(u)
	return r != nil && r.ID == model.RoleAdmin
}

func (d *Data) UserByName(username string) *model.User {
	for _, u := range d.Users {
		if strings.EqualFold(u.Username, username) {
			return u
		}
	}
	return nil
}
