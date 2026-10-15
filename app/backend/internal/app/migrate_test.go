package app_test

import (
	"context"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/secrets/secretstest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func TestMigrate(t *testing.T) {
	_, vault := secretstest.New(t)
	st := store.New()
	st.Write(func(d *store.Data) {
		d.Users["USR-1"] = &model.User{ID: "USR-1", Username: "admin", Source: model.SourceLocal}
		d.Users["USR-2"] = &model.User{ID: "USR-2", Username: "anna", Source: model.SourceLDAP, Name: "Anna Ivanova"}
		d.Users["USR-3"] = &model.User{ID: "USR-3", Username: "ops", Source: model.SourceLocal, Name: "Сидоров Иван Петрович"}
		d.Users["USR-4"] = &model.User{ID: "USR-4", Username: "root", Source: model.SourceLocal, Name: "root"}
		d.ServiceAccounts["SA-1"] = &model.ServiceAccount{ID: "SA-1", Name: "flow", CreatedBy: "ops"}
		d.ServiceAccounts["SA-2"] = &model.ServiceAccount{ID: "SA-2", Name: "old", CreatedBy: "gone"}
	})
	if err := app.Migrate(context.Background(), st, vault); err != nil {
		t.Fatal(err)
	}
	st.Read(func(d *store.Data) {
		if u := d.Users["USR-3"]; u.LastName != "Сидоров" || u.FirstName != "Иван" || u.MiddleName != "Петрович" || u.Profile.DisplayName(u.Username) != u.Name {
			t.Fatalf("name split = %+v", u.Profile)
		}
		if u := d.Users["USR-4"]; u.FirstName != "" {
			t.Fatalf("name equal to the login must not be split: %+v", u.Profile)
		}
		if u := d.Users["USR-2"]; u.LastName != "" {
			t.Fatalf("directory users keep directory data: %+v", u.Profile)
		}
		if d.ServiceAccounts["SA-1"].OwnerID != "USR-3" || d.ServiceAccounts["SA-2"].OwnerID != "" {
			t.Fatalf("service account owners = %q %q", d.ServiceAccounts["SA-1"].OwnerID, d.ServiceAccounts["SA-2"].OwnerID)
		}
		if d.Roles[model.RoleAdmin] == nil || d.Roles[model.RoleUser] == nil {
			t.Fatal("system roles must exist")
		}
	})
	if err := app.Migrate(context.Background(), st, vault); err != nil {
		t.Fatal(err)
	}
}
