package app

import (
	"bytes"
	"context"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/avatar"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/credentials"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/entra"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/notify"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type UserService struct {
	st        *store.Store
	passwords *credentials.Passwords
	now       func() time.Time
}

func NewUserService(st *store.Store, passwords *credentials.Passwords) *UserService {
	return &UserService{st: st, passwords: passwords, now: func() time.Time { return time.Now().UTC() }}
}

func (s *UserService) Get(id string) (model.User, error) {
	var out model.User
	found := false
	s.st.Read(func(d *store.Data) {
		if u := d.Users[id]; u != nil {
			out, found = *u, true
		}
	})
	if !found {
		return out, ErrNotFound
	}
	return out, nil
}

func (s *UserService) update(id string, fn func(d *store.Data, u *model.User)) (model.User, error) {
	var out model.User
	found := false
	s.st.Write(func(d *store.Data) {
		u := d.Users[id]
		if u == nil {
			return
		}
		fn(d, u)
		out, found = *u, true
	})
	if !found {
		return out, ErrNotFound
	}
	return out, nil
}

func (s *UserService) local(id string) (model.User, error) {
	u, err := s.Get(id)
	if err != nil {
		return u, err
	}
	if u.Source != model.SourceLocal {
		return u, ErrManagedByDirectory
	}
	return u, nil
}

func (s *UserService) UpdateProfile(id string, in model.Profile) (model.User, error) {
	if _, err := s.local(id); err != nil {
		return model.User{}, err
	}
	p, err := in.Normalize()
	if err != nil {
		return model.User{}, invalid("invalid_name", err)
	}
	if !model.ValidEmail(p.Email) {
		return model.User{}, invalid("invalid_email", nil)
	}
	return s.update(id, func(d *store.Data, u *model.User) {
		u.Profile, u.Name = p, p.DisplayName(u.Username)
		d.AddAudit(store.AuditEntry{Actor: u.Username, Action: "user.profile", Object: u.ID})
	})
}

func (s *UserService) ChangePassword(ctx context.Context, id, current, next string) error {
	u, err := s.local(id)
	if err != nil {
		return err
	}
	ok, err := s.passwords.Verify(u.PasswordRef, current)
	if err != nil {
		return err
	}
	if !ok {
		return ErrWrongPassword
	}
	var policy model.PasswordPolicy
	s.st.Read(func(d *store.Data) { policy = d.Settings.Password })
	if err := policy.Validate(next, u.Username); err != nil {
		return err
	}
	if next == current {
		return ErrSamePassword
	}
	ref, err := s.passwords.Set(ctx, u.ID, next)
	if err != nil {
		return err
	}
	now := s.now()
	_, err = s.update(id, func(d *store.Data, u *model.User) {
		u.PasswordRef, u.PasswordChangedAt, u.MustChangePassword = ref, now, false
		d.AddAudit(store.AuditEntry{Actor: u.Username, Action: "user.password", Object: u.ID})
	})
	return err
}

func (s *UserService) SetAvatar(id string, data []byte) (model.User, error) {
	img, err := avatar.Normalize(data)
	if err != nil {
		return model.User{}, ErrAvatarInvalid
	}
	now := s.now()
	return s.update(id, func(d *store.Data, u *model.User) {
		u.Avatar, u.AvatarSource, u.AvatarAt = img, model.AvatarUpload, &now
		d.AddAudit(store.AuditEntry{Actor: u.Username, Action: "user.avatar", Object: u.ID, Detail: "upload"})
	})
}

func (s *UserService) RemoveAvatar(id string) (model.User, error) {
	return s.update(id, func(d *store.Data, u *model.User) {
		if u.AvatarSource != model.AvatarUpload {
			return
		}
		u.Avatar, u.AvatarSource, u.AvatarAt = nil, "", nil
		d.AddAudit(store.AuditEntry{Actor: u.Username, Action: "user.avatar", Object: u.ID, Detail: "removed"})
	})
}

func (s *UserService) Avatar(id string) ([]byte, time.Time, error) {
	u, err := s.Get(id)
	if err != nil || len(u.Avatar) == 0 {
		return nil, time.Time{}, ErrNotFound
	}
	var at time.Time
	if u.AvatarAt != nil {
		at = *u.AvatarAt
	}
	return u.Avatar, at, nil
}

func (s *UserService) SetTimezone(id string, tz string) (model.User, error) {
	if tz != "" && !model.ValidTimezone(tz) {
		return model.User{}, invalid("invalid_timezone", nil)
	}
	return s.update(id, func(d *store.Data, u *model.User) {
		if u.Timezone == tz {
			return
		}
		u.Timezone = tz
		detail := tz
		if detail == "" {
			detail = "default"
		}
		d.AddAudit(store.AuditEntry{Actor: u.Username, Action: "user.preferences", Object: u.ID, Detail: "timezone " + detail})
	})
}

// SetLocale keeps the interface language of the user; empty is the default of the installation.
func (s *UserService) SetLocale(id string, locale string) (model.User, error) {
	if locale != "" && !model.ValidLocale(locale) {
		return model.User{}, invalid("invalid_locale", nil)
	}
	return s.update(id, func(d *store.Data, u *model.User) { u.Locale = locale })
}

// SetTelegram sets the Telegram chat backup notification sends to; empty clears it.
func (s *UserService) SetTelegram(id string, chat string) (model.User, error) {
	chat = strings.TrimSpace(chat)
	if chat != "" && !notify.ValidChat(chat) {
		return model.User{}, invalid("invalid_telegram", nil)
	}
	return s.update(id, func(d *store.Data, u *model.User) {
		if u.Telegram == chat {
			return
		}
		u.Telegram = chat
		d.AddAudit(store.AuditEntry{Actor: u.Username, Action: "user.preferences", Object: u.ID, Detail: "telegram chat changed"})
	})
}

// LinkTelegram ties a Telegram private chat to a user through the bot: the chat is taken away
// from any other user, so a Telegram account acts for one user only.
func (s *UserService) LinkTelegram(id, chat string) (model.User, error) {
	if !notify.ValidChat(chat) {
		return model.User{}, invalid("invalid_telegram", nil)
	}
	return s.update(id, func(d *store.Data, u *model.User) {
		for _, other := range d.Users {
			if other.ID != u.ID && other.Telegram == chat {
				other.Telegram = ""
				d.AddAudit(store.AuditEntry{Actor: u.Username, Action: "user.preferences", Object: other.ID, Detail: "telegram chat moved to another account"})
			}
		}
		u.Telegram = chat
		d.AddAudit(store.AuditEntry{Actor: u.Username, Action: "user.preferences", Object: u.ID, Detail: "telegram linked by the bot"})
	})
}

func (s *UserService) SyncDirectory(id directory.Identity) (model.User, error) {
	return s.syncExternal(model.SourceLDAP, "", id)
}

// SyncEntra creates or updates the account of a Microsoft Entra ID user. The account is bound to
// the Entra object ID, so a reused sign-in name cannot take over somebody else's account.
func (s *UserService) SyncEntra(a entra.Account) (model.User, error) {
	return s.syncExternal(model.SourceEntra, a.ObjectID, a.Identity)
}

func (s *UserService) syncExternal(source, externalID string, id directory.Identity) (model.User, error) {
	var photo []byte
	if id.HasPhoto {
		if img, err := avatar.Normalize(id.Photo); err == nil {
			photo = img
		}
	}
	profile, _ := model.Profile{LastName: id.LastName, FirstName: id.FirstName, MiddleName: id.MiddleName, Title: id.Title,
		Department: id.Department, Manager: id.Manager, Email: id.Email}.Normalize()
	if !model.ValidEmail(profile.Email) {
		profile.Email = ""
	}
	now := s.now()
	var out model.User
	s.st.Write(func(d *store.Data) {
		x := d.UserByName(id.Username)
		if x == nil && profile.Email != "" {
			// A person first seen as a NetBox contact takes over that account on first sign-in.
			if c := userByEmail(d, profile.Email); c != nil && c.Source == model.SourceNetBox {
				c.Username = id.Username
				x = c
			}
		}
		if x != nil && x.Source == model.SourceNetBox && !x.Disabled {
			x.Source, x.ExternalID = source, externalID
		}
		if x == nil {
			x = &model.User{ID: d.NextID("USR"), Username: id.Username, Source: source, ExternalID: externalID, CreatedAt: now}
			d.Users[x.ID] = x
		}
		if x.Source != source || x.Disabled {
			out = *x
			return
		}
		if externalID != "" {
			if x.ExternalID == "" {
				x.ExternalID = externalID
			}
			if x.ExternalID != externalID {
				out = *x
				return
			}
		}
		x.Profile, x.Name = profile, id.Name
		if changes := assignFromGroups(d, x, source, id.Admin, id.Groups, id.GroupsKnown); len(changes) > 0 && !x.CreatedAt.Equal(now) {
			d.AddAudit(store.AuditEntry{At: now, Actor: groupSyncActor, Action: "user.groups", Object: x.ID, Detail: x.Username + " " + strings.Join(changes, ", ")})
		}
		if id.Name == id.Username {
			x.Name = profile.DisplayName(id.Username)
		}
		switch {
		case x.AvatarSource == model.AvatarUpload:
		case photo != nil && !bytes.Equal(photo, x.Avatar):
			x.Avatar, x.AvatarSource, x.AvatarAt = photo, model.AvatarLDAP, &now
		case photo == nil && x.AvatarSource == model.AvatarLDAP:
			x.Avatar, x.AvatarSource, x.AvatarAt = nil, "", nil
		}
		out = *x
	})
	if out.Source != source || out.Disabled || out.ExternalID != externalID {
		return model.User{}, ErrInvalidCredentials
	}
	return out, nil
}
