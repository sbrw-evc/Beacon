package model

import (
	"slices"
	"time"
)

const (
	CriticalityCritical = "critical"
	CriticalityHigh     = "high"
	CriticalityMedium   = "medium"
	CriticalityLow      = "low"

	ServiceActive  = "active"
	ServicePlanned = "planned"
	ServiceRetired = "retired"
)

var (
	Criticalities   = []string{CriticalityCritical, CriticalityHigh, CriticalityMedium, CriticalityLow}
	ServiceStatuses = []string{ServiceActive, ServicePlanned, ServiceRetired}
)

func ValidCriticality(v string) bool { return slices.Contains(Criticalities, v) }

// CriticalityRank orders criticalities by Criticalities: the most critical ranks highest,
// unknown ones rank 0.
func CriticalityRank(v string) int {
	if i := slices.Index(Criticalities, v); i >= 0 {
		return len(Criticalities) - i
	}
	return 0
}

func ValidServiceStatus(v string) bool { return slices.Contains(ServiceStatuses, v) }

type Link struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type Service struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	OwnerTeamID string   `json:"owner_team_id"`
	TeamIDs     []string `json:"team_ids"`
	Criticality string   `json:"criticality"`
	Status      string   `json:"status"`
	Tags        []string `json:"tags"`
	Links       []Link   `json:"links"`
	DependsOn   []string `json:"depends_on"`
	// CIIDs are the configuration items the service runs on.
	CIIDs     []string       `json:"ci_ids"`
	NetBox    *ServiceNetBox `json:"netbox,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// ServiceNetBox is the NetBox tag that stands for a business service: objects tagged with it
// in NetBox are the configuration items of the service.
type ServiceNetBox struct {
	TagID    int        `json:"tag_id"`
	Slug     string     `json:"slug"`
	Name     string     `json:"name"`
	URL      string     `json:"url"`
	SyncedAt *time.Time `json:"synced_at,omitempty"`
}

func (s Service) Involves(teams map[string]bool) bool {
	if teams[s.OwnerTeamID] {
		return true
	}
	for _, id := range s.TeamIDs {
		if teams[id] {
			return true
		}
	}
	return false
}
