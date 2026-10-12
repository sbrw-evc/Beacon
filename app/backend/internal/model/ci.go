package model

import (
	"slices"
	"time"
)

const (
	SourceNetBox      = "netbox"
	SourceInventoryDB = "inventory-db"

	CIKindDevice  = "device"
	CIKindVM      = "vm"
	CIKindService = "service"
	CIKindOther   = "other"

	CIStatusActive          = "active"
	CIStatusPlanned         = "planned"
	CIStatusStaged          = "staged"
	CIStatusOffline         = "offline"
	CIStatusFailed          = "failed"
	CIStatusDecommissioning = "decommissioning"

	DirectoryMatched = "matched"
	DirectoryMissing = "missing"
)

var (
	CIKinds    = []string{CIKindDevice, CIKindVM, CIKindService, CIKindOther}
	CIStatuses = []string{CIStatusActive, CIStatusPlanned, CIStatusStaged, CIStatusOffline, CIStatusFailed, CIStatusDecommissioning}
)

func ValidCIKind(v string) bool   { return slices.Contains(CIKinds, v) }
func ValidCIStatus(v string) bool { return slices.Contains(CIStatuses, v) }

// CIOwner is a person responsible for a configuration item, with the role NetBox gives them.
type CIOwner struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

// CIAttrs are the descriptive fields NetBox fills in.
type CIAttrs struct {
	Site       string `json:"site,omitempty"`
	Role       string `json:"role,omitempty"`
	DeviceType string `json:"device_type,omitempty"`
	Cluster    string `json:"cluster,omitempty"`
	Tenant     string `json:"tenant,omitempty"`
	Platform   string `json:"platform,omitempty"`
	Parent     string `json:"parent,omitempty"`
	Ports      string `json:"ports,omitempty"`
	Serial     string `json:"serial,omitempty"`
}

// NetBoxRef points at the NetBox object a configuration item is kept in.
type NetBoxRef struct {
	Kind string `json:"kind"`
	ID   int    `json:"id"`
	URL  string `json:"url"`
}

// InventoryRef points at the Inventory DB device a configuration item is read from.
type InventoryRef struct {
	ID  int    `json:"id"`
	URL string `json:"url"`
	// Ref is the source reference Inventory DB gives the device (inventory-db:dcim.device:12).
	Ref string `json:"ref"`
}

// CIDirectory is the computer object of the domain controller matched to the item.
type CIDirectory struct {
	Status    string     `json:"status"`
	DN        string     `json:"dn,omitempty"`
	DNSName   string     `json:"dns_name,omitempty"`
	OS        string     `json:"os,omitempty"`
	OSVersion string     `json:"os_version,omitempty"`
	LastLogon *time.Time `json:"last_logon,omitempty"`
	Disabled  bool       `json:"disabled"`
	CheckedAt time.Time  `json:"checked_at"`
}

// ConfigItem is a configuration item (CI): a device, virtual machine, service or other
// object monitoring refers to. Items imported from NetBox are kept read-only here.
type ConfigItem struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	Description string     `json:"description"`
	Source      string     `json:"source"`
	Owners      []CIOwner  `json:"-"`
	IPs         []string   `json:"ips"`
	Tags        []string   `json:"tags"`
	Attrs       CIAttrs    `json:"attrs"`
	NetBox      *NetBoxRef `json:"netbox,omitempty"`
	// InventoryDB is set on items read from Inventory DB.
	InventoryDB *InventoryRef `json:"inventory_db,omitempty"`
	Directory   *CIDirectory  `json:"directory,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
	CreatedBy   string        `json:"created_by"`
	UpdatedAt   time.Time     `json:"updated_at"`
	UpdatedBy   string        `json:"updated_by"`
	SyncedAt    *time.Time    `json:"synced_at,omitempty"`

	// Aliases are other names events call the item by (a host name in a monitoring system, a
	// Prometheus instance). They are kept in Umbrella, also for items imported from NetBox.
	Aliases []string `json:"aliases,omitempty"`
}

// Imported items mirror NetBox or Inventory DB and change only there.
func (c *ConfigItem) Imported() bool {
	return c.Source == SourceNetBox || c.Source == SourceInventoryDB
}

// SyncStats counts what one NetBox synchronization changed.
type SyncStats struct {
	Objects  int `json:"objects"`
	Created  int `json:"created"`
	Updated  int `json:"updated"`
	Deleted  int `json:"deleted"`
	Unlinked int `json:"unlinked"`
	// Linked: existing items an Inventory DB device was attached to by name.
	Linked       int `json:"linked,omitempty"`
	Contacts     int `json:"contacts"`
	UsersCreated int `json:"users_created"`
	UsersUpdated int `json:"users_updated"`
	UsersLinked  int `json:"users_linked"`
	// UsersSkipped: contacts without an account that were not created (CreateUsers is off).
	UsersSkipped     int    `json:"users_skipped"`
	DirectoryChecked bool   `json:"directory_checked"`
	DirectoryMatched int    `json:"directory_matched"`
	DirectoryMissing int    `json:"directory_missing"`
	DirectoryError   string `json:"directory_error,omitempty"`
	ServiceBound     int    `json:"service_bound"`
	ServiceUnbound   int    `json:"service_unbound"`
	ServiceUnlinked  int    `json:"service_unlinked"`
	// Held counts the linked items missing from an answer that looks incomplete; they were kept
	// instead of being deleted or unlinked. HeldReason: empty (no objects at all) or share (most
	// of the linked items are missing).
	Held       int    `json:"held,omitempty"`
	HeldReason string `json:"held_reason,omitempty"`
}

// SyncState is the last NetBox synchronization.
type SyncState struct {
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
	Actor      string    `json:"actor"`
	Stats      SyncStats `json:"stats"`
}

// PushedAlert is the alert state last sent to Inventory DB, to send only changes and to report
// the alert resolved once it is no longer active.
type PushedAlert struct {
	Key   string    `json:"key"`
	Ref   string    `json:"ref"`
	Title string    `json:"title"`
	Sev   string    `json:"severity"`
	At    time.Time `json:"at"`
}
