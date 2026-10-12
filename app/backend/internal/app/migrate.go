package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/credentials"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func Migrate(ctx context.Context, st *store.Store, vault credentials.Vault) error {
	changed := false
	st.Write(func(d *store.Data) {
		if d.Settings.Password.MinLength == 0 {
			d.Settings.Password = model.DefaultPasswordPolicy()
			d.AddAudit(store.AuditEntry{Actor: "system", Action: "settings.password_policy", Detail: "default policy applied"})
			changed = true
		}
		now := time.Now().UTC()
		if d.EnsureSystemRoles(now) {
			d.AddAudit(store.AuditEntry{Actor: "system", Action: "roles.seeded", Detail: "system roles created"})
			changed = true
		}
		if d.EnsurePresetRoles(d.Settings.DefaultLocale, now) {
			d.AddAudit(store.AuditEntry{Actor: "system", Action: "roles.seeded", Detail: "preset roles created, new users get " + d.NewUserRole()})
			changed = true
		}
		if !d.Settings.NetBoxUsersDecided {
			// NetBox configured before the setting existed kept creating accounts for contacts.
			if d.Settings.NetBox.URL != "" {
				d.Settings.NetBox.CreateUsers = true
				d.AddAudit(store.AuditEntry{Actor: "system", Action: "settings.netbox", Detail: "accounts for contacts: on (kept)"})
			}
			d.Settings.NetBoxUsersDecided = true
			changed = true
		}
		for _, u := range d.Users {
			if d.Roles[u.Role] == nil {
				u.Role = model.RoleUser
				changed = true
			}
			if splitName(u) {
				changed = true
			}
			if u.Source == model.SourceLocal && u.PasswordChangedAt.IsZero() {
				u.PasswordChangedAt = now
				changed = true
			}
		}
	})
	pending := map[string]string{}
	st.Read(func(d *store.Data) {
		for id, u := range d.Users {
			if u.PasswordHash != "" {
				pending[id] = u.PasswordHash
			}
		}
	})
	passwords := credentials.NewPasswords(vault)
	for id, hash := range pending {
		ref, err := passwords.Store(ctx, id, hash)
		if err != nil {
			return err
		}
		st.Write(func(d *store.Data) {
			if u := d.Users[id]; u != nil {
				u.PasswordRef, u.PasswordHash = ref, ""
				d.AddAudit(store.AuditEntry{Actor: "system", Action: "user.password_migrated", Object: id, Detail: "hash moved to OpenBao"})
			}
		})
		slog.Info("password hash moved to OpenBao", "user", id)
	}
	if len(pending) == 0 && !changed {
		return nil
	}
	return st.Flush()
}

func splitName(u *model.User) bool {
	if u.Source != model.SourceLocal || u.LastName != "" || u.FirstName != "" || u.MiddleName != "" {
		return false
	}
	parts := strings.Fields(u.Name)
	if len(parts) == 0 || strings.EqualFold(u.Name, u.Username) {
		return false
	}
	switch len(parts) {
	case 1:
		u.FirstName = parts[0]
	case 2:
		u.LastName, u.FirstName = parts[0], parts[1]
	default:
		u.LastName, u.FirstName, u.MiddleName = parts[0], parts[1], strings.Join(parts[2:], " ")
	}
	return true
}
