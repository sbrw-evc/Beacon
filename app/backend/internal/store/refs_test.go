package store_test

import (
	"strings"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

func TestMapRefsAndUndo(t *testing.T) {
	st := store.New()
	st.Write(func(d *store.Data) {
		d.Settings.LDAP.BindPasswordRef = "old/ldap"
		d.Settings.APITokenKeyRef = "keep/api-tokens"
	})
	move := func(ref string) (string, bool) {
		rest, ok := strings.CutPrefix(ref, "old/")
		return "new/" + rest, ok
	}
	n, undo := st.MapRefs(move)
	if n != 1 {
		t.Fatalf("changed = %d, want 1", n)
	}
	st.Read(func(d *store.Data) {
		if d.Settings.LDAP.BindPasswordRef != "new/ldap" || d.Settings.APITokenKeyRef != "keep/api-tokens" {
			t.Fatalf("after map: %+v", d.Settings.LDAP)
		}
	})
	st.Write(func(d *store.Data) { d.Settings.LDAP.BindPasswordRef = "new/ldap-rotated" })
	undo()
	st.Read(func(d *store.Data) {
		if d.Settings.LDAP.BindPasswordRef != "new/ldap-rotated" {
			t.Fatal("undo must not overwrite a reference changed after the move")
		}
	})
}
