package directory

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/go-ldap/ldap/v3"
)

const (
	KindAD       = "ad"
	KindOpenLDAP = "openldap"

	Placeholder = "{username}"

	adNestedMember = "1.2.840.113556.1.4.1941"
)

var Timeout = 10 * time.Second

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUserNotFound       = errors.New("user not found in the directory")
	ErrAmbiguousUser      = errors.New("several directory entries match the username")
)

type Config struct {
	Enabled         bool   `json:"enabled"`
	Kind            string `json:"kind"`
	URL             string `json:"url"`
	StartTLS        bool   `json:"start_tls"`
	SkipVerify      bool   `json:"skip_verify"`
	CACert          string `json:"ca_cert,omitempty"`
	BindDN          string `json:"bind_dn"`
	BindPasswordRef string `json:"bind_password_ref,omitempty"`
	BaseDN          string `json:"base_dn"`
	UserFilter      string `json:"user_filter"`
	UsernameAttr    string `json:"username_attr"`
	NameAttr        string `json:"name_attr"`
	EmailAttr       string `json:"email_attr"`
	FirstNameAttr   string `json:"first_name_attr"`
	LastNameAttr    string `json:"last_name_attr"`
	MiddleNameAttr  string `json:"middle_name_attr"`
	TitleAttr       string `json:"title_attr"`
	DepartmentAttr  string `json:"department_attr"`
	ManagerAttr     string `json:"manager_attr"`
	PhotoAttr       string `json:"photo_attr"`
	AdminGroupDN    string `json:"admin_group_dn,omitempty"`
}

func Defaults(kind string) Config {
	if kind == KindOpenLDAP {
		return Config{Kind: KindOpenLDAP, UserFilter: "(&(objectClass=inetOrgPerson)(uid={username}))",
			UsernameAttr: "uid", NameAttr: "cn", EmailAttr: "mail", FirstNameAttr: "givenName", LastNameAttr: "sn",
			TitleAttr: "title", DepartmentAttr: "departmentNumber", ManagerAttr: "manager", PhotoAttr: "jpegPhoto"}
	}
	return Config{Kind: KindAD, UserFilter: "(&(objectCategory=person)(objectClass=user)(sAMAccountName={username}))",
		UsernameAttr: "sAMAccountName", NameAttr: "displayName", EmailAttr: "mail", FirstNameAttr: "givenName", LastNameAttr: "sn",
		MiddleNameAttr: "middleName", TitleAttr: "title", DepartmentAttr: "department", ManagerAttr: "manager", PhotoAttr: "thumbnailPhoto"}
}

func (c Config) Normalize() (Config, error) {
	c.Kind = strings.ToLower(strings.TrimSpace(c.Kind))
	if c.Kind == "" {
		c.Kind = KindAD
	}
	if c.Kind != KindAD && c.Kind != KindOpenLDAP {
		return c, errors.New("directory type must be ad or openldap")
	}
	d := Defaults(c.Kind)
	c.URL = strings.TrimSpace(c.URL)
	c.BindDN = strings.TrimSpace(c.BindDN)
	c.BaseDN = strings.TrimSpace(c.BaseDN)
	c.UserFilter = strings.TrimSpace(c.UserFilter)
	c.AdminGroupDN = strings.TrimSpace(c.AdminGroupDN)
	c.CACert = strings.TrimSpace(c.CACert)
	c.UsernameAttr = firstSet(strings.TrimSpace(c.UsernameAttr), d.UsernameAttr)
	c.NameAttr = firstSet(strings.TrimSpace(c.NameAttr), d.NameAttr)
	c.EmailAttr = firstSet(strings.TrimSpace(c.EmailAttr), d.EmailAttr)
	for _, f := range []*string{&c.FirstNameAttr, &c.LastNameAttr, &c.MiddleNameAttr, &c.TitleAttr, &c.DepartmentAttr, &c.ManagerAttr, &c.PhotoAttr} {
		*f = strings.TrimSpace(*f)
	}
	for _, a := range c.attrs() {
		if !attrRe.MatchString(a) {
			return c, fmt.Errorf("attribute name %q is invalid", a)
		}
	}
	c.UserFilter = firstSet(c.UserFilter, d.UserFilter)

	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "ldap" && u.Scheme != "ldaps") || u.Hostname() == "" {
		return c, errors.New("server address must look like ldap://host:389 or ldaps://host:636")
	}
	if u.Scheme == "ldaps" && c.StartTLS {
		return c, errors.New("StartTLS works only with ldap://, ldaps:// is already encrypted")
	}
	if c.BindDN == "" {
		return c, errors.New("service account (bind DN) is required")
	}
	if _, err := ldap.ParseDN(c.BaseDN); err != nil || c.BaseDN == "" {
		return c, errors.New("search base DN is invalid")
	}
	if c.AdminGroupDN != "" {
		if _, err := ldap.ParseDN(c.AdminGroupDN); err != nil {
			return c, errors.New("administrators group DN is invalid")
		}
	}
	if !strings.Contains(c.UserFilter, Placeholder) {
		return c, fmt.Errorf("user filter must contain %s", Placeholder)
	}
	if _, err := ldap.CompileFilter(strings.ReplaceAll(c.UserFilter, Placeholder, "probe")); err != nil {
		return c, fmt.Errorf("user filter is invalid: %v", err)
	}
	if c.CACert != "" {
		if _, err := certPool(c.CACert); err != nil {
			return c, err
		}
	}
	return c, nil
}

func (c Config) TLSMode() string {
	switch {
	case strings.HasPrefix(c.URL, "ldaps://"):
		return "ldaps"
	case c.StartTLS:
		return "starttls"
	}
	return "none"
}

type Identity struct {
	DN         string `json:"dn"`
	Username   string `json:"username"`
	Name       string `json:"name"`
	Email      string `json:"email,omitempty"`
	FirstName  string `json:"first_name,omitempty"`
	LastName   string `json:"last_name,omitempty"`
	MiddleName string `json:"middle_name,omitempty"`
	Title      string `json:"title,omitempty"`
	Department string `json:"department,omitempty"`
	Manager    string `json:"manager,omitempty"`
	Photo      []byte `json:"-"`
	HasPhoto   bool   `json:"has_photo"`
	Admin      bool   `json:"admin"`
	// Groups are the normalized DNs (or Entra object IDs) of the groups the user belongs to,
	// nested ones included. GroupsKnown is false when they could not be read.
	Groups      []string `json:"groups,omitempty"`
	GroupsKnown bool     `json:"groups_known"`
	GroupsError string   `json:"groups_error,omitempty"`
}

type Probe struct {
	Server            string    `json:"server"`
	TLS               string    `json:"tls"`
	BaseDN            string    `json:"base_dn"`
	AdminGroup        bool      `json:"admin_group"`
	User              *Identity `json:"user,omitempty"`
	UserAuthenticated bool      `json:"user_authenticated"`
}

func Test(c Config, bindPassword, username, password string) (Probe, error) {
	c, err := c.Normalize()
	if err != nil {
		return Probe{}, err
	}
	p := Probe{Server: c.URL, TLS: c.TLSMode()}
	conn, err := connect(c, bindPassword)
	if err != nil {
		return p, err
	}
	defer conn.Close()
	if err := exists(conn, c.BaseDN); err != nil {
		return p, fmt.Errorf("search base %s: %w", c.BaseDN, err)
	}
	p.BaseDN = c.BaseDN
	if c.AdminGroupDN != "" {
		if err := exists(conn, c.AdminGroupDN); err != nil {
			return p, fmt.Errorf("administrators group %s: %w", c.AdminGroupDN, err)
		}
		p.AdminGroup = true
	}
	if strings.TrimSpace(username) == "" {
		return p, nil
	}
	if err := checkUsername(username); err != nil {
		return p, err
	}
	e, err := findUser(conn, c, username)
	if err != nil {
		return p, err
	}
	if password != "" {
		if err := conn.Bind(e.DN, password); err != nil {
			return p, bindError(err)
		}
		p.UserAuthenticated = true
		if err := conn.Bind(c.BindDN, bindPassword); err != nil {
			return p, fmt.Errorf("service account bind: %w", err)
		}
	}
	admin, err := isAdmin(conn, c, e, username)
	if err != nil {
		return p, err
	}
	id := identity(conn, c, e, username, admin)
	withGroups(conn, c, &id, e.DN, username)
	p.User = &id
	return p, nil
}

func Authenticate(c Config, bindPassword, username, password string) (Identity, error) {
	if password == "" || len(password) > 1024 {
		return Identity{}, ErrInvalidCredentials
	}
	if err := checkUsername(username); err != nil {
		return Identity{}, ErrInvalidCredentials
	}
	c, err := c.Normalize()
	if err != nil {
		return Identity{}, err
	}
	conn, err := connect(c, bindPassword)
	if err != nil {
		return Identity{}, err
	}
	defer conn.Close()
	e, err := findUser(conn, c, username)
	if err != nil {
		return Identity{}, err
	}
	if err := conn.Bind(e.DN, password); err != nil {
		return Identity{}, bindError(err)
	}
	if err := conn.Bind(c.BindDN, bindPassword); err != nil {
		return Identity{}, fmt.Errorf("service account bind: %w", err)
	}
	admin, err := isAdmin(conn, c, e, username)
	if err != nil {
		return Identity{}, err
	}
	id := identity(conn, c, e, username, admin)
	withGroups(conn, c, &id, e.DN, username)
	return id, nil
}

func connect(c Config, bindPassword string) (*ldap.Conn, error) {
	if bindPassword == "" {
		return nil, errors.New("service account password is empty")
	}
	u, err := url.Parse(c.URL)
	if err != nil {
		return nil, err
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname(), InsecureSkipVerify: c.SkipVerify}
	if c.CACert != "" {
		pool, err := certPool(c.CACert)
		if err != nil {
			return nil, err
		}
		tc.RootCAs = pool
	}
	conn, err := ldap.DialURL(c.URL, ldap.DialWithDialer(&net.Dialer{Timeout: Timeout}), ldap.DialWithTLSConfig(tc))
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", c.URL, err)
	}
	conn.SetTimeout(Timeout)
	if c.StartTLS {
		if err := conn.StartTLS(tc); err != nil {
			conn.Close()
			return nil, fmt.Errorf("StartTLS: %w", err)
		}
	}
	if err := conn.Bind(c.BindDN, bindPassword); err != nil {
		conn.Close()
		return nil, fmt.Errorf("service account bind: %w", err)
	}
	return conn, nil
}

func exists(conn *ldap.Conn, dn string) error {
	_, err := conn.Search(ldap.NewSearchRequest(dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, seconds(), false,
		"(objectClass=*)", []string{"1.1"}, nil))
	if ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
		return errors.New("entry does not exist")
	}
	return err
}

func findUser(conn *ldap.Conn, c Config, username string) (*ldap.Entry, error) {
	filter := strings.ReplaceAll(c.UserFilter, Placeholder, ldap.EscapeFilter(username))
	res, err := conn.Search(ldap.NewSearchRequest(c.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, seconds(), false,
		filter, c.attrs(), nil))
	if ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) {
		return nil, ErrAmbiguousUser
	}
	if err != nil {
		return nil, fmt.Errorf("user search: %w", err)
	}
	switch len(res.Entries) {
	case 0:
		return nil, ErrUserNotFound
	case 1:
		return res.Entries[0], nil
	}
	return nil, ErrAmbiguousUser
}

func isAdmin(conn *ldap.Conn, c Config, e *ldap.Entry, username string) (bool, error) {
	if c.AdminGroupDN == "" {
		return false, nil
	}
	var req *ldap.SearchRequest
	if c.Kind == KindAD {
		req = ldap.NewSearchRequest(e.DN, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, seconds(), false,
			"(memberOf:"+adNestedMember+":="+ldap.EscapeFilter(c.AdminGroupDN)+")", []string{"1.1"}, nil)
	} else {
		dn := ldap.EscapeFilter(e.DN)
		req = ldap.NewSearchRequest(c.AdminGroupDN, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, seconds(), false,
			"(|(member="+dn+")(uniqueMember="+dn+")(memberUid="+ldap.EscapeFilter(username)+"))", []string{"1.1"}, nil)
	}
	res, err := conn.Search(req)
	if ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("administrators group check: %w", err)
	}
	return len(res.Entries) > 0, nil
}

var attrRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,63}$`)

func (c Config) attrs() []string {
	var out []string
	for _, a := range []string{c.UsernameAttr, c.NameAttr, c.EmailAttr, c.FirstNameAttr, c.LastNameAttr, c.MiddleNameAttr,
		c.TitleAttr, c.DepartmentAttr, c.ManagerAttr, c.PhotoAttr} {
		if a != "" {
			out = append(out, a)
		}
	}
	return out
}

func value(e *ldap.Entry, attr string) string {
	if attr == "" {
		return ""
	}
	return strings.TrimSpace(e.GetAttributeValue(attr))
}

func managerName(conn *ldap.Conn, c Config, dn string) string {
	if dn == "" {
		return ""
	}
	parsed, err := ldap.ParseDN(dn)
	if err != nil {
		return dn
	}
	res, err := conn.Search(ldap.NewSearchRequest(dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, seconds(), false,
		"(objectClass=*)", []string{c.NameAttr}, nil))
	if err == nil && len(res.Entries) == 1 {
		if n := value(res.Entries[0], c.NameAttr); n != "" {
			return n
		}
	}
	if len(parsed.RDNs) > 0 && len(parsed.RDNs[0].Attributes) > 0 {
		return parsed.RDNs[0].Attributes[0].Value
	}
	return dn
}

const maxPhoto = 5 << 20

func identity(conn *ldap.Conn, c Config, e *ldap.Entry, username string, admin bool) Identity {
	id := Identity{DN: e.DN, Username: firstSet(value(e, c.UsernameAttr), username), Name: firstSet(value(e, c.NameAttr), username),
		Email: value(e, c.EmailAttr), FirstName: value(e, c.FirstNameAttr), LastName: value(e, c.LastNameAttr),
		MiddleName: value(e, c.MiddleNameAttr), Title: value(e, c.TitleAttr), Department: value(e, c.DepartmentAttr), Admin: admin}
	if c.ManagerAttr != "" {
		id.Manager = managerName(conn, c, value(e, c.ManagerAttr))
	}
	if c.PhotoAttr != "" {
		if raw := e.GetRawAttributeValue(c.PhotoAttr); len(raw) > 0 && len(raw) <= maxPhoto {
			id.Photo, id.HasPhoto = raw, true
		}
	}
	return id
}

func bindError(err error) error {
	if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
		return ErrInvalidCredentials
	}
	return fmt.Errorf("user bind: %w", err)
}

func checkUsername(u string) error {
	if strings.TrimSpace(u) == "" || len(u) > 256 {
		return errors.New("username is empty or too long")
	}
	for _, r := range u {
		if unicode.IsControl(r) {
			return errors.New("username contains control characters")
		}
	}
	return nil
}

func certPool(pem string) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(pem)) {
		return nil, errors.New("CA certificate: no PEM certificates found")
	}
	return pool, nil
}

func seconds() int { return int(Timeout / time.Second) }

func firstSet(v ...string) string {
	for _, x := range v {
		if x != "" {
			return x
		}
	}
	return ""
}
