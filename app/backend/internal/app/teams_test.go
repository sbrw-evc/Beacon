package app_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type teamReply struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	ParentID    string           `json:"parent_id"`
	LeadID      string           `json:"lead_id"`
	Depth       int              `json:"depth"`
	ChildCount  int              `json:"child_count"`
	MemberCount int              `json:"member_count"`
	Members     []orgMemberReply `json:"members"`
	Lead        *orgMemberReply  `json:"lead"`
}

func (f orgFixture) team(name, parent string) teamReply {
	f.h.t.Helper()
	var out teamReply
	f.do(f.admin, http.MethodPost, "/api/teams", map[string]any{"name": name, "parent_id": parent}, http.StatusCreated, &out)
	return out
}

func (f orgFixture) teams() map[string]teamReply {
	f.h.t.Helper()
	var list []teamReply
	f.do(f.admin, http.MethodGet, "/api/teams", nil, http.StatusOK, &list)
	out := map[string]teamReply{}
	for _, t := range list {
		out[t.ID] = t
	}
	return out
}

func (f orgFixture) dropTeams() {
	f.h.t.Helper()
	for len(f.teams()) > 0 {
		for id := range f.teams() {
			f.expect(f.admin, http.MethodDelete, "/api/teams/"+id, nil, http.StatusNoContent, "")
			break
		}
	}
}

func TestTeamHierarchyRules(t *testing.T) {
	f := newOrgFixture(t)
	defer f.dropTeams()

	root := f.team("Platform", "")
	sre := f.team("SRE", root.ID)
	oncall := f.team("Oncall", sre.ID)
	if oncall.Depth != 3 || sre.ParentID != root.ID {
		t.Fatalf("oncall = %+v", oncall)
	}
	f.expect(f.admin, http.MethodPost, "/api/teams", map[string]any{"name": "sre", "parent_id": root.ID}, http.StatusConflict, "team_name_taken")
	other := f.team("SRE", "")
	f.expect(f.admin, http.MethodPost, "/api/teams", map[string]any{"name": "X", "parent_id": "TEAM-404"}, http.StatusBadRequest, "unknown_parent")
	f.expect(f.admin, http.MethodPost, "/api/teams", map[string]any{"name": "X", "lead_id": "USR-404"}, http.StatusBadRequest, "unknown_lead")
	f.expect(f.admin, http.MethodPost, "/api/teams", map[string]any{"name": ""}, http.StatusBadRequest, "invalid_team_name")

	f.expect(f.admin, http.MethodPut, "/api/teams/"+root.ID, map[string]any{"parent_id": oncall.ID}, http.StatusBadRequest, "team_cycle")
	f.expect(f.admin, http.MethodPut, "/api/teams/"+root.ID, map[string]any{"parent_id": root.ID}, http.StatusBadRequest, "team_cycle")
	f.expect(f.admin, http.MethodPut, "/api/teams/"+other.ID, map[string]any{"parent_id": root.ID}, http.StatusConflict, "team_name_taken")

	var moved teamReply
	f.do(f.admin, http.MethodPut, "/api/teams/"+other.ID, map[string]any{"name": "SRE Europe", "parent_id": root.ID}, http.StatusOK, &moved)
	if moved.ParentID != root.ID || moved.Depth != 2 {
		t.Fatalf("moved = %+v", moved)
	}

	parent := oncall.ID
	for i := 4; i <= 8; i++ {
		parent = f.team(fmt.Sprintf("Level %d", i), parent).ID
	}
	f.expect(f.admin, http.MethodPost, "/api/teams", map[string]any{"name": "Level 9", "parent_id": parent}, http.StatusBadRequest, "team_too_deep")
	f.team("Berlin", other.ID)
	f.expect(f.admin, http.MethodPut, "/api/teams/"+other.ID, map[string]any{"parent_id": f.teams()[parent].ParentID}, http.StatusBadRequest, "team_too_deep")

	bob, _ := f.signedIn("bob", model.RoleUser)
	f.expect(bob, http.MethodGet, "/api/teams", nil, http.StatusForbidden, "forbidden")
	f.expect(bob, http.MethodPost, "/api/teams", map[string]any{"name": "Mine"}, http.StatusForbidden, "forbidden")
}

func TestTeamMembershipAndDelete(t *testing.T) {
	f := newOrgFixture(t)
	defer f.dropTeams()
	u1, u2 := f.user("ivan"), f.user("olga")
	defer f.dropUsers(u1, u2)

	root := f.team("Platform", "")
	sre := f.team("SRE", root.ID)
	oncall := f.team("Oncall", sre.ID)
	db := f.team("Databases", sre.ID)

	var got teamReply
	f.do(f.admin, http.MethodPut, "/api/teams/"+sre.ID+"/members", map[string]any{"user_ids": []string{u1, u2}}, http.StatusOK, &got)
	if got.MemberCount != 2 {
		t.Fatalf("sre = %+v", got)
	}
	f.do(f.admin, http.MethodPut, "/api/teams/"+oncall.ID+"/members", map[string]any{"user_ids": []string{u2}}, http.StatusOK, &got)
	teams := f.teams()
	// A person can be in several teams: adding olga to Oncall keeps her in SRE.
	if teams[sre.ID].MemberCount != 2 || teams[oncall.ID].MemberCount != 1 || !slices.Equal(teams[oncall.ID].Members[0].TeamIDs, sorted(sre.ID, oncall.ID)) {
		t.Fatalf("after adding sre=%+v oncall=%+v", teams[sre.ID], teams[oncall.ID])
	}
	f.expect(f.admin, http.MethodPut, "/api/teams/"+sre.ID+"/members", map[string]any{"user_ids": []string{"USR-404"}}, http.StatusBadRequest, "unknown_user")

	f.do(f.admin, http.MethodPut, "/api/teams/"+sre.ID, map[string]any{"lead_id": u2}, http.StatusOK, &got)
	if got.Lead == nil || got.Lead.ID != u2 || got.LeadID != u2 {
		t.Fatalf("lead = %+v", got)
	}

	f.h.st.Write(func(d *store.Data) {
		d.Services["SVC-T"] = &model.Service{ID: "SVC-T", Name: "Billing", OwnerTeamID: sre.ID, TeamIDs: []string{root.ID, sre.ID}, CreatedAt: time.Now()}
	})
	defer f.h.st.Write(func(d *store.Data) { delete(d.Services, "SVC-T") })

	clash := f.team("Oncall", root.ID)
	f.expect(f.admin, http.MethodDelete, "/api/teams/"+sre.ID, nil, http.StatusConflict, "team_child_conflict")
	f.expect(f.admin, http.MethodDelete, "/api/teams/"+clash.ID, nil, http.StatusNoContent, "")

	f.expect(f.admin, http.MethodDelete, "/api/teams/"+sre.ID, nil, http.StatusNoContent, "")
	teams = f.teams()
	if _, ok := teams[sre.ID]; ok || teams[oncall.ID].ParentID != root.ID || teams[db.ID].ParentID != root.ID || teams[db.ID].Depth != 2 {
		t.Fatalf("after delete %+v", teams)
	}
	if f.userField(u1, userTeams) != "" || f.userField(u2, userTeams) != oncall.ID {
		t.Fatal("members of the deleted team lose only that team")
	}
	f.h.st.Read(func(d *store.Data) {
		s := d.Services["SVC-T"]
		if s.OwnerTeamID != "" || !slices.Equal(s.TeamIDs, []string{root.ID}) {
			t.Fatalf("service = %+v", s)
		}
	})

	f.do(f.admin, http.MethodPut, "/api/teams/"+oncall.ID+"/members", map[string]any{"user_ids": []string{}}, http.StatusOK, &got)
	if got.MemberCount != 0 || f.userField(u2, userTeams) != "" {
		t.Fatalf("cleared = %+v", got)
	}
	f.expect(f.admin, http.MethodDelete, "/api/teams/"+sre.ID, nil, http.StatusNotFound, "not_found")
}

func userTeams(u *model.User) string { return strings.Join(u.TeamIDs, ",") }

func sorted(ids ...string) []string {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}
