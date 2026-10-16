package app

import (
	"strings"
	"time"

	"github.com/sbrw-evc/beacon/app/backend/internal/avatar"
	"github.com/sbrw-evc/beacon/app/backend/internal/model"
	"github.com/sbrw-evc/beacon/app/backend/internal/notify"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
)

// UserService is what a person changes on their own account in Beacon: avatar, time zone,
// language and the Telegram chat. Name, e-mail, password and passkeys are managed in Keystone.
type UserService struct {
	st  *store.Store
	now func() time.Time
}

func NewUserService(st *store.Store) *UserService {
	return &UserService{st: st, now: func() time.Time { return time.Now().UTC() }}
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
