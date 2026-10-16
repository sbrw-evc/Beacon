package app

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"

	"github.com/sbrw-evc/beacon/app/backend/internal/model"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
)

type userView struct {
	model.User
	CSRF          string   `json:"csrf,omitempty"`
	Gravatar      string   `json:"gravatar,omitempty"`
	HasAvatar     bool     `json:"has_avatar"`
	AvatarVersion string   `json:"avatar_version,omitempty"`
	RoleName      string   `json:"role_name,omitempty"`
	Permissions   []string `json:"permissions,omitempty"`
	// Admins are whom a user without any permission asks for access.
	Admins []AdminContact `json:"admins,omitempty"`
	// AccountURL is Keystone's account page (password, passkeys, sessions): "Account and security".
	AccountURL string `json:"account_url,omitempty"`
}

type AdminContact struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

func (a *App) view(u model.User, csrf string) userView {
	v := newUserView(u, csrf)
	g := a.access.Grant(u)
	v.Role, v.RoleName, v.Permissions = g.RoleID, g.RoleName, g.Perms.List()
	if u.Source == model.SourceSPA {
		a.deps.Store.Read(func(d *store.Data) { v.AccountURL = d.Settings.SPA.AccountURL })
	}
	if len(v.Permissions) == 0 {
		v.Admins = a.adminContacts()
	}
	return v
}

func newUserView(u model.User, csrf string) userView {
	v := userView{User: u, CSRF: csrf, HasAvatar: len(u.Avatar) > 0}
	if e := strings.ToLower(strings.TrimSpace(u.Email)); e != "" {
		h := sha256.Sum256([]byte(e))
		v.Gravatar = hex.EncodeToString(h[:])
	}
	if u.AvatarAt != nil && v.HasAvatar {
		v.AvatarVersion = strconv.FormatInt(u.AvatarAt.UnixNano(), 10)
	}
	return v
}

// adminContacts lists the active administrators, those with an e-mail address first.
func (a *App) adminContacts() []AdminContact {
	out := []AdminContact{}
	a.deps.Store.Read(func(d *store.Data) {
		for _, u := range d.Users {
			if !u.Disabled && d.IsAdmin(u) {
				out = append(out, AdminContact{Name: u.Profile.DisplayName(u.Username), Email: u.Email})
			}
		}
	})
	slices.SortFunc(out, func(x, y AdminContact) int {
		if (x.Email == "") != (y.Email == "") {
			if x.Email == "" {
				return 1
			}
			return -1
		}
		return byName(x.Name, y.Name)
	})
	return out
}
