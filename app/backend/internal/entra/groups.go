package entra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory"
)

type graphError struct {
	code int
	err  error
}

func (e graphError) Error() string { return e.err.Error() }
func (e graphError) Unwrap() error { return e.err }

// ErrGroupPermission means the application may not read the group membership of other users.
var ErrGroupPermission = errors.New("the application lacks the Microsoft Graph application permission GroupMember.Read.All (with admin consent)")

func lowerAll(v []string) []string {
	out := make([]string, 0, len(v))
	for _, s := range v {
		out = append(out, strings.ToLower(strings.TrimSpace(s)))
	}
	return out
}

// ValidGroupID reports whether s is an Entra group object ID.
func ValidGroupID(s string) bool { return guidRe.MatchString(strings.TrimSpace(s)) }

// Memberships reads the groups of known users with the application's own token (client
// credentials), so it works without the users signing in. A deleted user is reported as not found.
func Memberships(ctx context.Context, c Config, secret string, objectIDs []string) (map[string]directory.Membership, error) {
	c, err := c.Normalize()
	if err != nil {
		return nil, err
	}
	m, err := Discover(ctx, c)
	if err != nil {
		return nil, err
	}
	t, err := token(ctx, m, url.Values{"grant_type": {"client_credentials"}, "client_id": {c.ClientID}, "client_secret": {secret},
		"scope": {c.cloud().Graph + "/.default"}})
	if err != nil {
		return nil, err
	}
	out := make(map[string]directory.Membership, len(objectIDs))
	for _, oid := range objectIDs {
		if !guidRe.MatchString(oid) {
			out[oid] = directory.Membership{}
			continue
		}
		groups, err := memberOf(ctx, c, t.AccessToken, "/v1.0/users/"+url.PathEscape(oid))
		var ge graphError
		switch {
		case errors.As(err, &ge) && ge.code == http.StatusNotFound:
			out[oid] = directory.Membership{}
			continue
		case errors.As(err, &ge) && (ge.code == http.StatusForbidden || ge.code == http.StatusUnauthorized):
			return nil, fmt.Errorf("%w: %v", ErrGroupPermission, err)
		case err != nil:
			return nil, err
		}
		groups = lowerAll(groups)
		out[oid] = directory.Membership{Found: true, Admin: c.AdminGroupID != "" && slices.Contains(groups, c.AdminGroupID), Groups: groups}
	}
	return out, nil
}
