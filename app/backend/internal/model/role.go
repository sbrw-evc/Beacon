package model

import (
	_ "embed"
	"encoding/json"
	"time"
)

type Role struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Permissions []string  `json:"permissions"`
	System      bool      `json:"system"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func SystemRoles(now time.Time) []*Role {
	return []*Role{
		{ID: RoleAdmin, Name: "Administrator", System: true, CreatedAt: now, UpdatedAt: now},
		{ID: RoleUser, Name: "User", System: true, Permissions: []string{}, CreatedAt: now, UpdatedAt: now},
	}
}

// Preset roles are created once, on the first start of a version that has them, and are
// ordinary roles after that: they can be renamed, changed and deleted, and are not created again.
const (
	RoleViewer       = "preset-viewer"
	RoleOnCall       = "preset-oncall"
	RoleServiceOwner = "preset-service-owner"
	RoleIntegrations = "preset-integrations"
	RoleNOCLead      = "preset-noc-lead"
)

//go:embed presets.json
var presetsJSON []byte

type presetText struct {
	EN string `json:"en"`
	RU string `json:"ru"`
}

func (t presetText) in(locale string) string {
	if locale == LocaleRU {
		return t.RU
	}
	return t.EN
}

type presetRole struct {
	ID          string     `json:"id"`
	Name        presetText `json:"name"`
	Description presetText `json:"description"`
	Permissions []string   `json:"permissions"`
}

// presetRoles are read from presets.json: adding or changing a ready-made role is a data change.
var presetRoles = func() []presetRole {
	var out []presetRole
	if err := json.Unmarshal(presetsJSON, &out); err != nil {
		panic("model: presets.json: " + err.Error())
	}
	return out
}()

// PresetRoles are the ready-made roles, named in the language of the installation. Permissions
// are not normalized here: the caller adds the view permission of every page it grants.
func PresetRoles(locale string, now time.Time) []*Role {
	out := make([]*Role, 0, len(presetRoles))
	for _, p := range presetRoles {
		out = append(out, &Role{ID: p.ID, Name: p.Name.in(locale), Description: p.Description.in(locale),
			Permissions: append([]string{}, p.Permissions...), CreatedAt: now, UpdatedAt: now})
	}
	return out
}
