package directory

import (
	"errors"
	"fmt"
	"strings"

	"github.com/go-ldap/ldap/v3"
)

const (
	maxGroupDepth = 8
	maxGroups     = 2000
	groupPage     = 500
)

// Membership is what the directory says about a known user during a synchronization.
type Membership struct {
	Found  bool
	Admin  bool
	Groups []string
}

// NormalizeDN returns the DN in one canonical spelling, so that DNs written differently compare equal.
// A string that is not a DN is returned lower-cased.
func NormalizeDN(s string) string {
	dn, err := ldap.ParseDN(strings.TrimSpace(s))
	if err != nil || len(dn.RDNs) == 0 {
		return strings.ToLower(strings.TrimSpace(s))
	}
	return strings.ToLower(dn.String())
}

// ValidDN reports whether s is a non-empty DN.
func ValidDN(s string) bool {
	dn, err := ldap.ParseDN(strings.TrimSpace(s))
	return err == nil && len(dn.RDNs) > 0
}

// groupRoot is where groups are searched: the domain part (dc=…) of the search base, because the
// user search base is often an OU that does not hold the groups.
func groupRoot(base string) string {
	dn, err := ldap.ParseDN(base)
	if err != nil {
		return base
	}
	i := len(dn.RDNs)
	for i > 0 && len(dn.RDNs[i-1].Attributes) == 1 && strings.EqualFold(dn.RDNs[i-1].Attributes[0].Type, "dc") {
		i--
	}
	if i == len(dn.RDNs) {
		return base
	}
	return (&ldap.DN{RDNs: dn.RDNs[i:]}).String()
}

// groupsOf lists the normalized DNs of the groups the user belongs to, directly or through nested groups.
func groupsOf(conn *ldap.Conn, c Config, userDN, username string) ([]string, error) {
	root := groupRoot(c.BaseDN)
	search := func(filter string) ([]string, error) {
		res, err := conn.SearchWithPaging(ldap.NewSearchRequest(root, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, seconds()*3, false,
			filter, []string{"1.1"}, nil), groupPage)
		if ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("group search: %w", err)
		}
		out := make([]string, 0, len(res.Entries))
		for _, e := range res.Entries {
			out = append(out, e.DN)
		}
		return out, nil
	}
	if c.Kind == KindAD {
		dns, err := search("(&(objectClass=group)(member:" + adNestedMember + ":=" + ldap.EscapeFilter(userDN) + "))")
		if err != nil {
			return nil, err
		}
		return normalizeAll(dns), nil
	}
	seen := map[string]bool{}
	var out []string
	frontier := []string{userDN}
	for depth := 0; depth < maxGroupDepth && len(frontier) > 0 && len(out) < maxGroups; depth++ {
		var b strings.Builder
		b.WriteString("(|")
		for _, dn := range frontier {
			v := ldap.EscapeFilter(dn)
			b.WriteString("(member=" + v + ")(uniqueMember=" + v + ")")
		}
		if depth == 0 {
			b.WriteString("(memberUid=" + ldap.EscapeFilter(username) + ")")
		}
		b.WriteString(")")
		dns, err := search(b.String())
		if err != nil {
			return nil, err
		}
		frontier = frontier[:0]
		for _, dn := range dns {
			k := NormalizeDN(dn)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, k)
			frontier = append(frontier, dn)
		}
		// Keep the filter of the next round to a sane size.
		if len(frontier) > 200 {
			frontier = frontier[:200]
		}
	}
	return out, nil
}

func normalizeAll(dns []string) []string {
	out := make([]string, 0, len(dns))
	for _, dn := range dns {
		out = append(out, NormalizeDN(dn))
	}
	return out
}

// withGroups adds the group list to an identity; a failure is reported in the identity, not as an
// error, so that a sign-in still works when only the group search fails.
func withGroups(conn *ldap.Conn, c Config, id *Identity, userDN, username string) {
	groups, err := groupsOf(conn, c, userDN, username)
	if err != nil {
		id.GroupsError = err.Error()
		return
	}
	id.Groups, id.GroupsKnown = groups, true
}

// Memberships reads the administrators group and the group list of known users with the service
// account, without their passwords. A user missing from the directory is reported as not found.
func Memberships(c Config, bindPassword string, usernames []string) (map[string]Membership, error) {
	c, err := c.Normalize()
	if err != nil {
		return nil, err
	}
	conn, err := connect(c, bindPassword)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	out := make(map[string]Membership, len(usernames))
	for _, name := range usernames {
		if checkUsername(name) != nil {
			out[name] = Membership{}
			continue
		}
		e, err := findUser(conn, c, name)
		if errors.Is(err, ErrUserNotFound) || errors.Is(err, ErrAmbiguousUser) {
			out[name] = Membership{}
			continue
		}
		if err != nil {
			return nil, err
		}
		admin, err := isAdmin(conn, c, e, name)
		if err != nil {
			return nil, err
		}
		groups, err := groupsOf(conn, c, e.DN, name)
		if err != nil {
			return nil, err
		}
		out[name] = Membership{Found: true, Admin: admin, Groups: groups}
	}
	return out, nil
}
