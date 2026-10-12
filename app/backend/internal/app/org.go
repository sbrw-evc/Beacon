package app

import (
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	maxOrgName        = 80
	maxOrgDescription = 1000
	codeUnknownUser   = "unknown_user"
)

var (
	errOrgNameEmpty   = errors.New("the name is empty")
	errOrgNameLong    = errors.New("the name is longer than 80 characters")
	errOrgNameControl = errors.New("the name contains control characters")
	errOrgDescription = errors.New("the description is longer than 1000 characters")
	errOrgUnknownUser = errors.New("unknown user")
)

type orgStatus = errStatus

type OrgMember struct {
	ID            string   `json:"id"`
	Username      string   `json:"username"`
	Name          string   `json:"name"`
	FirstName     string   `json:"first_name,omitempty"`
	LastName      string   `json:"last_name,omitempty"`
	Title         string   `json:"title,omitempty"`
	Source        string   `json:"source"`
	Disabled      bool     `json:"disabled"`
	RoleID        string   `json:"role_id"`
	TeamIDs       []string `json:"team_ids"`
	HasAvatar     bool     `json:"has_avatar"`
	AvatarVersion string   `json:"avatar_version,omitempty"`
	Gravatar      string   `json:"gravatar,omitempty"`
}

func orgMemberOf(d *store.Data, u *model.User) OrgMember {
	v := newUserView(*u, "")
	return OrgMember{
		ID: u.ID, Username: u.Username, Name: u.Profile.DisplayName(u.Username), FirstName: u.FirstName, LastName: u.LastName, Title: u.Title,
		Source: u.Source, Disabled: u.Disabled, RoleID: d.RoleOf(u).ID, TeamIDs: teamList(u.TeamIDs),
		HasAvatar: v.HasAvatar, AvatarVersion: v.AvatarVersion, Gravatar: v.Gravatar,
	}
}

func sortMembers(ms []OrgMember) {
	slices.SortFunc(ms, func(x, y OrgMember) int { return byName(x.Name, y.Name) })
}

func orgName(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", errOrgNameEmpty
	case utf8.RuneCountInString(s) > maxOrgName:
		return "", errOrgNameLong
	case strings.IndexFunc(s, unicode.IsControl) >= 0:
		return "", errOrgNameControl
	}
	return s, nil
}

func orgDescription(s string) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxOrgDescription {
		return "", errOrgDescription
	}
	return s, nil
}

func orgUsers(d *store.Data, ids []string) ([]*model.User, error) {
	seen := map[string]bool{}
	out := []*model.User{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		u := d.Users[id]
		if u == nil {
			return nil, invalid(codeUnknownUser, errOrgUnknownUser)
		}
		out = append(out, u)
	}
	return out, nil
}

func usernames(us []*model.User) string {
	names := make([]string, 0, len(us))
	for _, u := range us {
		names = append(names, u.Username)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// teamList is a user's teams for JSON: never null.
func teamList(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return slices.Clone(ids)
}

const maxUserTeams = 50

// userTeams checks and normalizes the teams of a user: every one must exist; duplicates and
// blanks are dropped; the list is sorted.
func userTeams(d *store.Data, ids []string) ([]string, error) {
	var out []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || slices.Contains(out, id) {
			continue
		}
		if d.Teams[id] == nil {
			return nil, invalid("unknown_team", nil)
		}
		out = append(out, id)
	}
	if len(out) > maxUserTeams {
		return nil, invalid("too_many_teams", nil)
	}
	slices.Sort(out)
	return out, nil
}
