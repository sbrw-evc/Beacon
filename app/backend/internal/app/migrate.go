package app

import (
	"context"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/credentials"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func Migrate(ctx context.Context, st *store.Store, vault credentials.Vault) error {
	changed := false
	st.Write(func(d *store.Data) {
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
			if u.Source == model.SourceLocal && u.LastName == "" && u.FirstName == "" && u.MiddleName == "" && splitName(u, u.Name) {
				changed = true
			}
		}
		// Service accounts made before owners existed belong to the person who created them.
		for _, sa := range d.ServiceAccounts {
			if sa.OwnerID != "" || sa.CreatedBy == "" {
				continue
			}
			if u := d.UserByName(sa.CreatedBy); u != nil && u.Source != model.SourceService {
				sa.OwnerID = u.ID
				d.AddAudit(store.AuditEntry{Actor: "system", Action: "serviceaccount.update", Object: sa.ID, Detail: "owner " + u.Username})
				changed = true
			}
		}
	})
	if !changed {
		return nil
	}
	return st.Flush()
}

// splitName fills the last, first and middle names of the profile from a full name written
// "Last First Middle"; a name equal to the username is not a name.
func splitName(u *model.User, name string) bool {
	parts := strings.Fields(name)
	if len(parts) == 0 || strings.EqualFold(name, u.Username) {
		return false
	}
	u.LastName, u.FirstName, u.MiddleName = "", "", ""
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
