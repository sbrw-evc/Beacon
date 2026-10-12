package directory

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// Computer is a computer object of the domain controller.
type Computer struct {
	DN          string     `json:"dn"`
	Name        string     `json:"name"`
	DNSName     string     `json:"dns_name,omitempty"`
	OS          string     `json:"os,omitempty"`
	OSVersion   string     `json:"os_version,omitempty"`
	Description string     `json:"description,omitempty"`
	LastLogon   *time.Time `json:"last_logon,omitempty"`
	Disabled    bool       `json:"disabled"`
}

const (
	computerFilter = "(objectClass=computer)"
	uacDisabled    = 0x2
	computerPage   = 500
	fileTimeEpoch  = 11644473600
)

var computerAttrs = []string{"cn", "dNSHostName", "operatingSystem", "operatingSystemVersion", "description", "lastLogonTimestamp", "userAccountControl"}

// Computers lists the computer objects under the search base. Only Active Directory has them.
func Computers(c Config, bindPassword string) ([]Computer, error) {
	c, err := c.Normalize()
	if err != nil {
		return nil, err
	}
	if c.Kind != KindAD {
		return nil, errors.New("computer objects are read only from Active Directory")
	}
	conn, err := connect(c, bindPassword)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	res, err := conn.SearchWithPaging(ldap.NewSearchRequest(c.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, seconds()*6, false,
		computerFilter, computerAttrs, nil), computerPage)
	if err != nil {
		return nil, fmt.Errorf("computer search: %w", err)
	}
	out := make([]Computer, 0, len(res.Entries))
	for _, e := range res.Entries {
		pc := Computer{DN: e.DN, Name: value(e, "cn"), DNSName: strings.ToLower(value(e, "dNSHostName")), OS: value(e, "operatingSystem"),
			OSVersion: value(e, "operatingSystemVersion"), Description: value(e, "description")}
		if uac, err := strconv.ParseInt(value(e, "userAccountControl"), 10, 64); err == nil {
			pc.Disabled = uac&uacDisabled != 0
		}
		if ft, err := strconv.ParseInt(value(e, "lastLogonTimestamp"), 10, 64); err == nil && ft > 0 {
			t := time.Unix(ft/1e7-fileTimeEpoch, (ft%1e7)*100).UTC()
			pc.LastLogon = &t
		}
		if pc.Name != "" {
			out = append(out, pc)
		}
	}
	return out, nil
}
