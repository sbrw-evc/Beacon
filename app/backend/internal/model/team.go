package model

import "time"

// MaxTeamDepth is the deepest a team hierarchy goes: a team and its ancestors are at most this
// many teams.
const MaxTeamDepth = 8

type Team struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ParentID    string `json:"parent_id"`
	LeadID      string `json:"lead_id"`
	// Email, Telegram, Teams and Zoom are the team's own channel: a duty mailbox, a group chat,
	// the webhook URL of a Microsoft Teams channel or of a Zoom chat. With any of them set,
	// backup notification goes to the channel and the lead instead of every member.
	Email     string    `json:"email"`
	Telegram  string    `json:"telegram"`
	Teams     string    `json:"teams"`
	Zoom      string    `json:"zoom"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
