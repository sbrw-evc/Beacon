package app

import (
	"errors"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func TestGuardKeepsAdministrators(t *testing.T) {
	st := store.New()
	st.Write(func(d *store.Data) {
		d.EnsureSystemRoles(d.Settings.SetupAt)
		d.Users["L"] = &model.User{ID: "L", Username: "local", Source: model.SourceLocal, Role: model.RoleAdmin, PasswordRef: "ref"}
		d.Users["D"] = &model.User{ID: "D", Username: "dir", Source: model.SourceLDAP, Role: model.RoleAdmin}
	})
	actor := Actor{User: model.User{ID: "X", Role: model.RoleAdmin}}
	check := func(id string, change func(u *model.User), want error) {
		t.Helper()
		st.Read(func(d *store.Data) {
			before := d.Users[id]
			var after *model.User
			if change != nil {
				c := *before
				change(&c)
				after = &c
			}
			if err := guardUserChange(d, actor, before, after); !errors.Is(err, want) {
				t.Fatalf("%s: err = %v, want %v", id, err, want)
			}
		})
	}
	lock := func(u *model.User) { u.Disabled = true }
	demote := func(u *model.User) { u.Role = model.RoleUser }

	check("L", lock, ErrNoLocalAdmin)
	check("L", demote, ErrNoLocalAdmin)
	check("L", nil, ErrNoLocalAdmin)
	check("D", lock, nil)
	check("D", demote, nil)

	st.Write(func(d *store.Data) { delete(d.Users, "L") })
	check("D", lock, ErrLastAdmin)
	check("D", demote, ErrLastAdmin)
	check("D", nil, ErrLastAdmin)
	check("D", func(u *model.User) { u.TeamIDs = []string{"T"} }, nil)

	plain := Actor{User: model.User{ID: "Y", Role: model.RoleUser}}
	st.Read(func(d *store.Data) {
		if err := guardUserChange(d, plain, d.Users["D"], d.Users["D"]); !errors.Is(err, ErrAdminOnly) {
			t.Fatalf("non-admin actor: err = %v", err)
		}
	})
}
