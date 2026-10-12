package app

import (
	"strings"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type SettingsService struct {
	st *store.Store
}

func NewSettingsService(st *store.Store) *SettingsService { return &SettingsService{st: st} }

type Defaults struct {
	Theme    *string `json:"default_theme"`
	Locale   *string `json:"default_locale"`
	Timezone *string `json:"default_timezone"`
}

func (s *SettingsService) Get() model.Settings {
	var out model.Settings
	s.st.Read(func(d *store.Data) { out = d.Settings })
	return out
}

func (s *SettingsService) UpdateDefaults(actor string, in Defaults) (model.Settings, error) {
	switch {
	case in.Theme != nil && !model.ValidTheme(*in.Theme):
		return model.Settings{}, invalid("invalid_theme", nil)
	case in.Locale != nil && !model.ValidLocale(*in.Locale):
		return model.Settings{}, invalid("invalid_locale", nil)
	case in.Timezone != nil && !model.ValidTimezone(*in.Timezone):
		return model.Settings{}, invalid("invalid_timezone", nil)
	}
	var out model.Settings
	s.st.Write(func(d *store.Data) {
		var changes []string
		apply := func(target *string, value *string, label string) {
			if value != nil && *value != *target {
				*target = *value
				changes = append(changes, label+" "+*value)
			}
		}
		apply(&d.Settings.DefaultTheme, in.Theme, "theme")
		apply(&d.Settings.DefaultLocale, in.Locale, "locale")
		apply(&d.Settings.DefaultTZ, in.Timezone, "timezone")
		if len(changes) > 0 {
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.update", Detail: strings.Join(changes, ", ")})
		}
		out = d.Settings
	})
	return out, nil
}

func DefaultTimezone(s model.Settings) string {
	if s.DefaultTZ == "" {
		return "UTC"
	}
	return s.DefaultTZ
}

type defaultsView struct {
	Theme          string               `json:"default_theme"`
	Locale         string               `json:"default_locale"`
	Timezone       string               `json:"default_timezone"`
	PasswordPolicy model.PasswordPolicy `json:"password_policy"`
}

func defaultsOf(s model.Settings) defaultsView {
	return defaultsView{Theme: s.DefaultTheme, Locale: s.DefaultLocale, Timezone: DefaultTimezone(s), PasswordPolicy: s.Password}
}
