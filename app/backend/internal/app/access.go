package app

import (
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/access"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type AccessService struct {
	st *store.Store
}

func NewAccessService(st *store.Store) *AccessService {
	return &AccessService{st: st}
}

type Grant struct {
	RoleID   string
	RoleName string
	Perms    access.Set
}

func (s *AccessService) Grant(u model.User) Grant {
	g := Grant{RoleID: u.Role, Perms: access.Set{}}
	s.st.Read(func(d *store.Data) {
		r := d.RoleOf(&u)
		if r == nil {
			return
		}
		g.RoleID, g.RoleName = r.ID, r.Name
		if d.IsAdmin(&u) {
			g.Perms = access.NewSet(access.All())
			return
		}
		g.Perms = access.NewSet(r.Permissions)
	})
	return g
}

func (s *AccessService) Permissions(u model.User) access.Set {
	return s.Grant(u).Perms
}
