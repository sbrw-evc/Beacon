package store_test

import (
	"strings"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func TestMapRefsAndUndo(t *testing.T) {
	st := store.New()
	st.Write(func(d *store.Data) {
		d.Settings.LDAP.BindPasswordRef = "old/ldap"
		d.Users["USR-1"] = &model.User{ID: "USR-1", PasswordRef: "old/users/USR-1"}
		d.Users["USR-2"] = &model.User{ID: "USR-2", PasswordRef: "keep/users/USR-2"}
		d.Users["USR-3"] = &model.User{ID: "USR-3"}
	})
	move := func(ref string) (string, bool) {
		rest, ok := strings.CutPrefix(ref, "old/")
		return "new/" + rest, ok
	}
	n, undo := st.MapRefs(move)
	if n != 2 {
		t.Fatalf("changed = %d, want 2", n)
	}
	st.Read(func(d *store.Data) {
		if d.Settings.LDAP.BindPasswordRef != "new/ldap" || d.Users["USR-1"].PasswordRef != "new/users/USR-1" ||
			d.Users["USR-2"].PasswordRef != "keep/users/USR-2" || d.Users["USR-3"].PasswordRef != "" {
			t.Fatalf("after map: %+v %+v", d.Settings.LDAP, d.Users)
		}
	})
	st.Write(func(d *store.Data) { d.Users["USR-1"].PasswordRef = "new/users/USR-1-rotated" })
	undo()
	st.Read(func(d *store.Data) {
		if d.Settings.LDAP.BindPasswordRef != "old/ldap" {
			t.Fatalf("ldap ref not restored: %s", d.Settings.LDAP.BindPasswordRef)
		}
		if d.Users["USR-1"].PasswordRef != "new/users/USR-1-rotated" {
			t.Fatal("undo must not overwrite a reference changed after the move")
		}
	})
}
