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
	bao, vault := secretstest.New(t)
	st := store.New()
	st.Write(func(d *store.Data) {
		d.Users["USR-1"] = &model.User{ID: "USR-1", Username: "admin", Source: model.SourceLocal, PasswordHash: "pbkdf2-sha256$1$c2FsdA$aGFzaA"}
		d.Users["USR-2"] = &model.User{ID: "USR-2", Username: "anna", Source: model.SourceLDAP, Name: "Anna Ivanova"}
		d.Users["USR-3"] = &model.User{ID: "USR-3", Username: "ops", Source: model.SourceLocal, Name: "Сидоров Иван Петрович"}
		d.Users["USR-4"] = &model.User{ID: "USR-4", Username: "root", Source: model.SourceLocal, Name: "root"}
	})
	if err := app.Migrate(context.Background(), st, vault); err != nil {
		t.Fatal(err)
	}
	if got := bao.Get("umbrella/users/USR-1")["password_hash"]; got != "pbkdf2-sha256$1$c2FsdA$aGFzaA" {
		t.Fatalf("hash in OpenBao = %v", got)
	}
	st.Read(func(d *store.Data) {
		u := d.Users["USR-1"]
		if u.PasswordHash != "" || u.PasswordRef != "openbao://umbrella/users/USR-1#password_hash" {
			t.Fatalf("user after migration = %+v", u)
		}
		if u := d.Users["USR-3"]; u.LastName != "Сидоров" || u.FirstName != "Иван" || u.MiddleName != "Петрович" || u.Profile.DisplayName(u.Username) != u.Name {
			t.Fatalf("name split = %+v", u.Profile)
		}
		if u := d.Users["USR-4"]; u.FirstName != "" {
			t.Fatalf("name equal to the login must not be split: %+v", u.Profile)
		}
		if u := d.Users["USR-2"]; u.LastName != "" {
			t.Fatalf("directory users keep directory data: %+v", u.Profile)
		}
		if d.Users["USR-1"].PasswordChangedAt.IsZero() || d.Users["USR-3"].PasswordChangedAt.IsZero() {
			t.Fatal("local users must get a password change time so they do not expire at once")
		}
		if !d.Users["USR-2"].PasswordChangedAt.IsZero() {
			t.Fatal("directory users have no local password age")
		}
		if d.Users["USR-2"].PasswordRef != "" {
			t.Fatal("directory user must not get a password")
		}
	})
	st.Read(func(d *store.Data) {
		if d.Settings.Password != model.DefaultPasswordPolicy() {
			t.Fatalf("missing policy must become the default, got %+v", d.Settings.Password)
		}
	})
	if bao.Get("umbrella/users/USR-2") != nil {
		t.Fatal("nothing must be written for users without a legacy hash")
	}
	if err := app.Migrate(context.Background(), st, vault); err != nil {
		t.Fatal(err)
	}
}
