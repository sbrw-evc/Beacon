package app_test

import (
	"slices"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	orgAdminPass = "Admin-pass-2026"
	orgUserPass  = "User-pass-2026x"
)

type orgFixture struct {
	h       *harness
	admin   *client
	adminID string
}

func newOrgFixture(t *testing.T) orgFixture {
	h := newHarness(t)
	h.st.Write(func(d *store.Data) { d.EnsureSystemRoles(time.Now().UTC()) })
	id := h.addLocal("admin", orgAdminPass, model.RoleAdmin, time.Now())
	admin := h.client()
	admin.login("admin", orgAdminPass)
	return orgFixture{h: h, admin: admin, adminID: id}
}

func (f orgFixture) signedIn(username, role string) (*client, string) {
	f.h.t.Helper()
	id := f.h.addLocal(username, orgUserPass, role, time.Now())
	c := f.h.client()
	c.login(username, orgUserPass)
	return c, id
}

func (f orgFixture) user(username string) string {
	var id string
	f.h.st.Write(func(d *store.Data) {
		id = d.NextID("USR")
		d.Users[id] = &model.User{ID: id, Username: username, Name: username, Source: model.SourceLocal, Role: model.RoleUser, CreatedAt: time.Now()}
	})
	return id
}

func (f orgFixture) dropUsers(ids ...string) {
	f.h.st.Write(func(d *store.Data) {
		for _, id := range ids {
			delete(d.Users, id)
		}
	})
}

func (f orgFixture) expect(c *client, method, path string, body any, status int, code string) map[string]any {
	f.h.t.Helper()
	var out map[string]any
	if got := c.call(method, path, body, &out); got != status || (code != "" && out["error"] != code) {
		f.h.t.Fatalf("%s %s = %d %v, want %d %s", method, path, got, out, status, code)
	}
	return out
}

func (f orgFixture) do(c *client, method, path string, body any, status int, out any) {
	f.h.t.Helper()
	if got := c.call(method, path, body, out); got != status {
		f.h.t.Fatalf("%s %s = %d, want %d", method, path, got, status)
	}
}

func (f orgFixture) userField(id string, get func(u *model.User) string) string {
	var out string
	f.h.st.Read(func(d *store.Data) { out = get(d.Users[id]) })
	return out
}

func memberIDs(ms []orgMemberReply) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m.ID)
	}
	slices.Sort(out)
	return out
}

type orgMemberReply struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	RoleID   string   `json:"role_id"`
	TeamIDs  []string `json:"team_ids"`
}
