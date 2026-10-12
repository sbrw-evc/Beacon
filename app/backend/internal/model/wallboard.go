package model

import "time"

const (
	WallboardSortNewest = "newest"
	WallboardSortOldest = "oldest"

	WallboardThemeDark  = "dark"
	WallboardThemeLight = "light"
)

// Wallboard is a TV screen for the operations room: a page opened without signing in, only
// from the allowed client networks, that shows the active incidents matching its filters.
type Wallboard struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	// Scope: an incident matches when its configuration item is in CIIDs, or one of its
	// business services is in ServiceIDs, or its team is in TeamIDs. All empty: every incident.
	CIIDs      []string `json:"ci_ids"`
	ServiceIDs []string `json:"service_ids"`
	TeamIDs    []string `json:"team_ids"`
	// Severities and Methods: empty means all.
	Severities       []string `json:"severities"`
	Methods          []string `json:"methods"`
	ShowAcknowledged bool     `json:"show_acknowledged"`
	ShowSuppressed   bool     `json:"show_suppressed"`
	// ResolvedMinutes also shows incidents resolved within the last N minutes; 0 shows none.
	ResolvedMinutes int    `json:"resolved_minutes"`
	Sort            string `json:"sort"`
	RefreshSeconds  int    `json:"refresh_seconds"`
	Theme           string `json:"theme"`
	// Locale: empty uses the default locale of the installation.
	Locale string `json:"locale"`
	// Timezone of the clock and times on the screen (IANA name); empty uses the default time
	// zone of the installation.
	Timezone string `json:"timezone"`
	// AllowedNetworks are CIDRs or single addresses of the clients that may open the page.
	AllowedNetworks []string  `json:"allowed_networks"`
	CreatedBy       string    `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedBy       string    `json:"updated_by"`
	UpdatedAt       time.Time `json:"updated_at"`
}
