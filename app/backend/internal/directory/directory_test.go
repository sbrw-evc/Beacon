package directory_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory/directorytest"
)

const (
	base    = "dc=example,dc=org"
	svcDN   = "cn=umbrella,ou=services,dc=example,dc=org"
	svcPass = "svc-secret-1"
	admins  = "cn=umbrella-admins,ou=groups,dc=example,dc=org"
)

func openLDAP(t *testing.T) (*directorytest.Server, directory.Config) {
	s := directorytest.Start(t,
		directorytest.Entry{DN: base},
		directorytest.Entry{DN: svcDN, Password: svcPass},
		directorytest.Entry{DN: "uid=anna,ou=people,dc=example,dc=org", Password: "anna-pass-1", Attrs: map[string][]string{
			"objectClass": {"inetOrgPerson"}, "uid": {"anna"}, "cn": {"Anna Ivanova"}, "mail": {"anna@example.org"}}},
		directorytest.Entry{DN: "uid=boris,ou=people,dc=example,dc=org", Password: "boris-pass-1", Attrs: map[string][]string{
			"objectClass": {"inetOrgPerson"}, "uid": {"boris"}, "cn": {"Boris Petrov"}}},
		directorytest.Entry{DN: admins, Attrs: map[string][]string{
			"objectClass": {"groupOfNames"}, "member": {"uid=anna,ou=people,dc=example,dc=org"}}},
	)
	return s, directory.Config{Enabled: true, Kind: directory.KindOpenLDAP, URL: s.URL, BindDN: svcDN, BaseDN: base, AdminGroupDN: admins}
}

func TestAuthenticateOpenLDAP(t *testing.T) {
	_, cfg := openLDAP(t)
	id, err := directory.Authenticate(cfg, svcPass, "anna", "anna-pass-1")
	if err != nil {
		t.Fatal(err)
	}
	if id.Username != "anna" || id.Name != "Anna Ivanova" || id.Email != "anna@example.org" || !id.Admin {
		t.Fatalf("identity = %+v", id)
	}
	id, err = directory.Authenticate(cfg, svcPass, "boris", "boris-pass-1")
	if err != nil || id.Admin || id.Name != "Boris Petrov" {
		t.Fatalf("boris = %+v, %v", id, err)
	}
}

func TestAuthenticateRejects(t *testing.T) {
	s, cfg := openLDAP(t)
	cases := []struct {
		user, pass string
		want       error
	}{
		{"anna", "wrong-password", directory.ErrInvalidCredentials},
		{"anna", "", directory.ErrInvalidCredentials},
		{"nobody", "whatever-1", directory.ErrUserNotFound},
		{"*", "anna-pass-1", directory.ErrUserNotFound},
		{"anna)(uid=*", "anna-pass-1", directory.ErrUserNotFound},
	}
	for _, c := range cases {
		if _, err := directory.Authenticate(cfg, svcPass, c.user, c.pass); !errors.Is(err, c.want) {
			t.Errorf("%q/%q: err = %v, want %v", c.user, c.pass, err, c.want)
		}
	}
	for _, f := range s.Searches {
		if strings.Contains(f, "(uid=*)") {
			t.Fatalf("filter injection reached the server: %s", f)
		}
	}
	if _, err := directory.Authenticate(cfg, "bad-service-password", "anna", "anna-pass-1"); err == nil || errors.Is(err, directory.ErrInvalidCredentials) {
		t.Fatalf("wrong service password must be a configuration error, got %v", err)
	}
}

func TestAmbiguousUser(t *testing.T) {
	s := directorytest.Start(t,
		directorytest.Entry{DN: base},
		directorytest.Entry{DN: svcDN, Password: svcPass},
		directorytest.Entry{DN: "uid=a1,dc=example,dc=org", Password: "p-1234567", Attrs: map[string][]string{"objectClass": {"inetOrgPerson"}, "uid": {"dup"}}},
		directorytest.Entry{DN: "uid=a2,dc=example,dc=org", Password: "p-1234567", Attrs: map[string][]string{"objectClass": {"inetOrgPerson"}, "uid": {"dup"}}},
	)
	cfg := directory.Config{Kind: directory.KindOpenLDAP, URL: s.URL, BindDN: svcDN, BaseDN: base}
	if _, err := directory.Authenticate(cfg, svcPass, "dup", "p-1234567"); !errors.Is(err, directory.ErrAmbiguousUser) {
		t.Fatalf("err = %v", err)
	}
}

func TestActiveDirectoryNestedAdmins(t *testing.T) {
	s := directorytest.Start(t,
		directorytest.Entry{DN: "dc=corp,dc=local"},
		directorytest.Entry{DN: "CN=svc-umbrella,OU=Service,DC=corp,DC=local", Password: svcPass},
		directorytest.Entry{DN: "CN=Ops Admins,OU=Groups,DC=corp,DC=local"},
		directorytest.Entry{DN: "CN=Olga,OU=Users,DC=corp,DC=local", Password: "olga-pass-1", Attrs: map[string][]string{
			"objectCategory": {"person"}, "objectClass": {"user"}, "sAMAccountName": {"olga"}, "displayName": {"Olga S."},
			"memberOf": {"CN=Ops Admins,OU=Groups,DC=corp,DC=local"}}},
	)
	cfg := directory.Config{URL: s.URL, BindDN: "CN=svc-umbrella,OU=Service,DC=corp,DC=local", BaseDN: "DC=corp,DC=local",
		AdminGroupDN: "CN=Ops Admins,OU=Groups,DC=corp,DC=local"}
	id, err := directory.Authenticate(cfg, svcPass, "olga", "olga-pass-1")
	if err != nil || !id.Admin || id.Name != "Olga S." {
		t.Fatalf("identity = %+v, %v", id, err)
	}
	var chain bool
	for _, f := range s.Searches {
		chain = chain || strings.Contains(f, "1.2.840.113556.1.4.1941")
	}
	if !chain {
		t.Fatal("AD group check must follow nested groups")
	}
}

func TestProbe(t *testing.T) {
	_, cfg := openLDAP(t)
	p, err := directory.Test(cfg, svcPass, "", "")
	if err != nil || p.BaseDN != base || !p.AdminGroup || p.TLS != "none" {
		t.Fatalf("probe = %+v, %v", p, err)
	}
	p, err = directory.Test(cfg, svcPass, "anna", "anna-pass-1")
	if err != nil || p.User == nil || !p.User.Admin || !p.UserAuthenticated {
		t.Fatalf("probe with user = %+v, %v", p, err)
	}
	bad := cfg
	bad.BaseDN = "dc=missing,dc=org"
	if _, err := directory.Test(bad, svcPass, "", ""); err == nil {
		t.Fatal("missing base DN must fail")
	}
}

func TestNormalize(t *testing.T) {
	c, err := directory.Config{URL: "ldaps://dc1.corp.local", BindDN: "svc@corp.local", BaseDN: "DC=corp,DC=local"}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != directory.KindAD || !strings.Contains(c.UserFilter, "sAMAccountName={username}") || c.TLSMode() != "ldaps" {
		t.Fatalf("defaults = %+v", c)
	}
	bad := []directory.Config{
		{URL: "http://dc1", BindDN: "x", BaseDN: "dc=a"},
		{URL: "ldaps://dc1", StartTLS: true, BindDN: "x", BaseDN: "dc=a"},
		{URL: "ldap://dc1", BaseDN: "dc=a"},
		{URL: "ldap://dc1", BindDN: "x", BaseDN: "not a dn"},
		{URL: "ldap://dc1", BindDN: "x", BaseDN: "dc=a", UserFilter: "(uid=fixed)"},
		{URL: "ldap://dc1", BindDN: "x", BaseDN: "dc=a", UserFilter: "(uid={username}"},
		{URL: "ldap://dc1", BindDN: "x", BaseDN: "dc=a", CACert: "not a pem"},
		{URL: "ldap://dc1", BindDN: "x", BaseDN: "dc=a", Kind: "novell"},
	}
	for i, b := range bad {
		if _, err := b.Normalize(); err == nil {
			t.Errorf("case %d must fail: %+v", i, b)
		}
	}
}
