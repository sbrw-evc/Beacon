package model

import (
	"slices"
	"time"
)

const (
	MaintenancePlanned  = "planned"
	MaintenanceActive   = "active"
	MaintenanceFinished = "finished"
)

// Maintenance is a window of planned work: alerts of the listed configuration items and of the
// items of the listed business services are kept, but nothing is sent to PagerDuty or by
// backup notification while it lasts.
type Maintenance struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Comment    string    `json:"comment"`
	CIIDs      []string  `json:"ci_ids"`
	ServiceIDs []string  `json:"service_ids"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	CreatedBy  string    `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedBy  string    `json:"updated_by"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (m Maintenance) State(now time.Time) string {
	switch {
	case now.Before(m.Start):
		return MaintenancePlanned
	case !now.Before(m.End):
		return MaintenanceFinished
	}
	return MaintenanceActive
}

// Covers reports whether the window applies to an item that belongs to the given services.
func (m Maintenance) Covers(ciID string, serviceIDs []string) bool {
	if ciID != "" && slices.Contains(m.CIIDs, ciID) {
		return true
	}
	for _, id := range serviceIDs {
		if slices.Contains(m.ServiceIDs, id) {
			return true
		}
	}
	return false
}
