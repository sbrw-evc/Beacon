package monitoring

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

// zabbix talks to the JSON-RPC API. An API token or a session goes in the Authorization header
// from Zabbix 6.4 on and in the auth field before (Zabbix 7.2 and 8.0 no longer accept the field).
// The calls below are the same on 6.0 to 8.0; the version only picks the login field, the auth
// place and the host group selector.
type zabbix struct {
	endpoint string
	web      string
	http     *http.Client
	major    int
	minor    int
	token    string
	id       int
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

func (e *rpcError) Error() string {
	if e.Data != "" && e.Data != e.Message {
		return "Zabbix: " + e.Message + " " + e.Data
	}
	return "Zabbix: " + e.Message
}

// zabbixEndpoint: the address of the web interface or of api_jsonrpc.php itself.
func zabbixEndpoint(raw string) (endpoint, web string, err error) {
	u, err := baseURL(raw)
	if err != nil {
		return "", "", err
	}
	u.RawQuery, u.Fragment = "", ""
	p := strings.TrimRight(u.Path, "/")
	if strings.HasSuffix(p, ".php") {
		u.Path = p
		endpoint = u.String()
		u.Path = p[:strings.LastIndex(p, "/")+1]
		return endpoint, strings.TrimRight(u.String(), "/"), nil
	}
	u.Path = p
	web = u.String()
	return web + "/api_jsonrpc.php", web, nil
}

func (z *zabbix) headerAuth() bool { return z.major > 6 || (z.major == 6 && z.minor >= 4) }

func (z *zabbix) call(ctx context.Context, method string, params any, out any) error {
	z.id++
	body := map[string]any{"jsonrpc": "2.0", "method": method, "params": params, "id": z.id}
	anonymous := method == "apiinfo.version" || method == "user.login"
	if z.token != "" && !anonymous && !z.headerAuth() {
		body["auth"] = z.token
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, z.endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json-rpc")
	if z.token != "" && !anonymous && z.headerAuth() {
		req.Header.Set("Authorization", "Bearer "+z.token)
	}
	resp, err := z.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	var r struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		if resp.StatusCode/100 != 2 {
			return fmt.Errorf("Zabbix answered %d", resp.StatusCode)
		}
		return errors.New("the address does not answer with the Zabbix API; give the address of the Zabbix web interface")
	}
	if r.Error != nil {
		return r.Error
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(r.Result, out)
}

func (z *zabbix) version(ctx context.Context) (string, error) {
	var v string
	if err := z.call(ctx, "apiinfo.version", []any{}, &v); err != nil {
		return "", err
	}
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return "", fmt.Errorf("Zabbix reported an unknown version %q", v)
	}
	z.major, _ = strconv.Atoi(parts[0])
	z.minor, _ = strconv.Atoi(parts[1])
	return v, nil
}

// login signs in with the credential: an API token is used as it is, a user name and password
// open a session that logout closes.
func (z *zabbix) login(ctx context.Context, auth *Auth) (session bool, err error) {
	if auth == nil {
		return false, errors.New("Zabbix needs a credential: an API token or a user name and password")
	}
	switch auth.Type {
	case "bearer":
		z.token = auth.Secrets["token"]
		return false, nil
	case "basic":
		user := "username"
		if z.major < 5 || (z.major == 5 && z.minor < 4) {
			user = "user"
		}
		var sid string
		if err := z.call(ctx, "user.login", map[string]string{user: auth.Fields["username"], "password": auth.Secrets["password"]}, &sid); err != nil {
			return false, err
		}
		z.token = sid
		return true, nil
	}
	return false, fmt.Errorf("a %s credential cannot be used with Zabbix: use an API token or a user name and password", auth.Type)
}

// flexInt reads the numbers Zabbix sends as strings.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	*f = flexInt(n)
	return err
}

type zabbixHost struct {
	HostID     string  `json:"hostid"`
	Host       string  `json:"host"`
	Name       string  `json:"name"`
	Status     flexInt `json:"status"`
	Available  flexInt `json:"available"`
	Interfaces []struct {
		IP        string  `json:"ip"`
		DNS       string  `json:"dns"`
		Available flexInt `json:"available"`
	} `json:"interfaces"`
	HostGroups []struct {
		Name string `json:"name"`
	} `json:"hostgroups"`
	Groups []struct {
		Name string `json:"name"`
	} `json:"groups"`
}

// openZabbix learns the version and signs in; close ends the session it opened.
func openZabbix(ctx context.Context, src model.MonitoringSource, auth *Auth) (z *zabbix, ver string, closeFn func(), err error) {
	endpoint, web, err := zabbixEndpoint(src.URL)
	if err != nil {
		return nil, "", nil, err
	}
	z = &zabbix{endpoint: endpoint, web: web, http: clients[src.SkipVerify]}
	if ver, err = z.version(ctx); err != nil {
		return nil, "", nil, err
	}
	session, err := z.login(ctx, auth)
	if err != nil {
		return nil, "", nil, err
	}
	closeFn = func() {}
	if session {
		closeFn = func() { _ = z.call(context.WithoutCancel(ctx), "user.logout", []any{}, nil) }
	}
	return z, ver, closeFn, nil
}

func fetchZabbix(ctx context.Context, src model.MonitoringSource, auth *Auth) (Result, error) {
	z, ver, closeFn, err := openZabbix(ctx, src, auth)
	if err != nil {
		return Result{}, err
	}
	defer closeFn()
	web := z.web
	params := map[string]any{
		"output":           []string{"hostid", "host", "name", "status"},
		"selectInterfaces": []string{"ip", "dns", "available"},
	}
	// Host groups have their own selector from 6.2 on; availability moved to interfaces in 5.4.
	if z.major > 6 || (z.major == 6 && z.minor >= 2) {
		params["selectHostGroups"] = []string{"name"}
	} else {
		params["selectGroups"] = []string{"name"}
	}
	if z.major < 5 || (z.major == 5 && z.minor < 4) {
		params["output"] = []string{"hostid", "host", "name", "status", "available"}
	}
	var hosts []zabbixHost
	if err := z.call(ctx, "host.get", params, &hosts); err != nil {
		return Result{}, err
	}
	out := Result{Version: ver, Hosts: make([]model.MonitoringHost, 0, len(hosts))}
	for _, zh := range hosts {
		h := model.MonitoringHost{Key: zh.HostID, Host: zh.Host, Name: zh.Name}
		if h.Name == "" {
			h.Name = h.Host
		}
		var addrs []string
		up, down := 0, 0
		count := func(a flexInt) {
			switch a {
			case 1:
				up++
			case 2:
				down++
			}
		}
		for _, in := range zh.Interfaces {
			addrs = append(addrs, in.IP, in.DNS)
			count(in.Available)
		}
		count(zh.Available)
		ips, dns := addresses(addrs...)
		h.IPs, h.DNS = orEmpty(ips), orEmpty(dns)
		var groups []string
		for _, g := range zh.HostGroups {
			groups = append(groups, g.Name)
		}
		for _, g := range zh.Groups {
			groups = append(groups, g.Name)
		}
		h.Groups = sortedSet(groups)
		switch {
		case zh.Status == 1:
			h.State = model.HostDisabled
		case up > 0 && down == 0:
			h.State = model.HostUp
		case up == 0 && down > 0:
			h.State = model.HostDown
		case down > 0:
			h.State = model.HostPartial
		default:
			h.State = model.HostUnknown
		}
		h.URL = web + "/zabbix.php?action=host.view&filter_set=1&filter_name=" + url.QueryEscape(zh.Host)
		out.Hosts = append(out.Hosts, h)
	}
	return out, nil
}
