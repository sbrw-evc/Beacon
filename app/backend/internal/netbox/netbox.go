// Package netbox reads configuration items and their contacts from NetBox and registers,
// changes and deletes objects there.
package netbox

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	KindDevice  = "device"
	KindVM      = "virtual-machine"
	KindService = "service"

	SecretPath = "netbox"
	SecretKey  = "token"

	pageSize = 500
)

var (
	Timeout = 30 * time.Second

	ErrNotFound = errors.New("the object does not exist in NetBox")
	ErrDefaults = errors.New("registration defaults are not set")
)

type kindInfo struct {
	api, ui, objectType string
}

var kinds = map[string]kindInfo{
	KindDevice:  {"dcim/devices", "dcim/devices", "dcim.device"},
	KindVM:      {"virtualization/virtual-machines", "virtualization/virtual-machines", "virtualization.virtualmachine"},
	KindService: {"ipam/services", "ipam/services", "ipam.service"},
}

// ValidKind reports whether kind names a NetBox object type Umbrella works with.
func ValidKind(kind string) bool { _, ok := kinds[kind]; return ok }

// Config is the NetBox connection kept in the settings. The token itself is in OpenBao.
type Config struct {
	Enabled        bool   `json:"enabled"`
	URL            string `json:"url"`
	TokenRef       string `json:"-"`
	SkipVerify     bool   `json:"skip_verify"`
	SyncMinutes    int    `json:"sync_minutes"`
	ImportDevices  bool   `json:"import_devices"`
	ImportVMs      bool   `json:"import_vms"`
	ImportServices bool   `json:"import_services"`
	SyncContacts   bool   `json:"sync_contacts"`
	// CreateUsers makes a user account for a contact no account matches by e-mail. Off: such
	// contacts are not responsible people of their items until the person has an account.
	CreateUsers   bool `json:"create_users"`
	SyncDirectory bool `json:"sync_directory"`
	SiteID        int  `json:"site_id"`
	DeviceRoleID  int  `json:"device_role_id"`
	DeviceTypeID  int  `json:"device_type_id"`
	ClusterID     int  `json:"cluster_id"`
}

// Defaults: import everything once a connection is set up, hourly.
func Defaults() Config {
	return Config{SyncMinutes: 60, ImportDevices: true, ImportVMs: true, ImportServices: true, SyncContacts: true, SyncDirectory: true}
}

const maxSyncMinutes = 7 * 24 * 60

func (c Config) Normalize() (Config, error) {
	c.URL = strings.TrimRight(strings.TrimSpace(c.URL), "/")
	c.URL = strings.TrimSuffix(c.URL, "/api")
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return c, errors.New("NetBox address must look like https://netbox.example.com")
	}
	if c.SyncMinutes < 0 || c.SyncMinutes > maxSyncMinutes {
		return c, fmt.Errorf("synchronization interval must be 0 to %d minutes", maxSyncMinutes)
	}
	for _, id := range []int{c.SiteID, c.DeviceRoleID, c.DeviceTypeID, c.ClusterID} {
		if id < 0 {
			return c, errors.New("NetBox object IDs must be positive")
		}
	}
	return c, nil
}

// Imports reports whether objects of the kind are loaded from NetBox.
func (c Config) Imports(kind string) bool {
	switch kind {
	case KindDevice:
		return c.ImportDevices
	case KindVM:
		return c.ImportVMs
	case KindService:
		return c.ImportServices
	}
	return false
}

// ObjectURL is the page of the object in the NetBox web interface.
func (c Config) ObjectURL(kind string, id int) string {
	k, ok := kinds[kind]
	if !ok || c.URL == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s/%d/", c.URL, k.ui, id)
}

type Client struct {
	cfg   Config
	token string
	http  *http.Client
}

func New(cfg Config, token string) (*Client, error) {
	cfg, err := cfg.Normalize()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("API token is empty")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.SkipVerify}
	return &Client{cfg: cfg, token: strings.TrimSpace(token), http: &http.Client{Timeout: Timeout, Transport: tr}}, nil
}

// Error is a request NetBox answered with an error status.
type Error struct {
	Status int
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("NetBox answered %d", e.Status)
	}
	return fmt.Sprintf("NetBox answered %d: %s", e.Status, e.Detail)
}

func (c *Client) authorization() string {
	// NetBox 4.5 v2 tokens start with nbt_ and use the Bearer scheme; older tokens use Token.
	if strings.HasPrefix(c.token, "nbt_") {
		return "Bearer " + c.token
	}
	return "Token " + c.token
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := c.cfg.URL + "/api/" + strings.TrimPrefix(path, "/")
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.authorization())
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("NetBox %s: %w", c.cfg.URL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("NetBox %s: %w", c.cfg.URL, err)
	}
	if resp.StatusCode == http.StatusNotFound && isObjectPath(path) {
		return ErrNotFound
	}
	if resp.StatusCode >= 300 {
		return &Error{Status: resp.StatusCode, Detail: errorDetail(raw)}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("NetBox answer is not JSON: %w", err)
	}
	return nil
}

// isObjectPath: the path ends in a numeric ID, such as dcim/devices/7/.
func isObjectPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	_, err := strconv.Atoi(parts[len(parts)-1])
	return err == nil
}

func errorDetail(raw []byte) string {
	var v map[string]any
	if json.Unmarshal(raw, &v) == nil {
		if d, ok := v["detail"].(string); ok {
			return d
		}
		parts := []string{}
		for k, x := range v {
			b, _ := json.Marshal(x)
			parts = append(parts, k+": "+string(b))
		}
		if len(parts) > 0 {
			return clip(strings.Join(parts, "; "))
		}
	}
	s := strings.TrimSpace(string(raw))
	if strings.HasPrefix(s, "<") {
		return ""
	}
	return clip(s)
}

func clip(s string) string {
	if r := []rune(s); len(r) > 300 {
		return string(r[:300]) + "…"
	}
	return s
}

type page struct {
	Count   int               `json:"count"`
	Results []json.RawMessage `json:"results"`
}

// list reads every page of a collection. Offsets are counted here rather than taken from
// the next link, which carries the address NetBox thinks it has (wrong behind a proxy).
func list[T any](ctx context.Context, c *Client, path string, query url.Values) ([]T, error) {
	out := []T{}
	for offset := 0; ; {
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		q.Set("limit", strconv.Itoa(pageSize))
		q.Set("offset", strconv.Itoa(offset))
		var p page
		if err := c.do(ctx, http.MethodGet, path+"/", q, nil, &p); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for _, r := range p.Results {
			var x T
			if err := json.Unmarshal(r, &x); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			out = append(out, x)
		}
		offset += len(p.Results)
		if len(p.Results) == 0 || offset >= p.Count {
			return out, nil
		}
	}
}

type ref struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Model   string `json:"model"`
	Display string `json:"display"`
}

// UnmarshalJSON also takes a bare ID, which some NetBox versions return for write-only fields.
func (r *ref) UnmarshalJSON(b []byte) error {
	if id, err := strconv.Atoi(string(b)); err == nil {
		*r = ref{ID: id}
		return nil
	}
	type plain ref
	return json.Unmarshal(b, (*plain)(r))
}

func (r *ref) label() string {
	switch {
	case r == nil:
		return ""
	case r.Name != "":
		return r.Name
	case r.Model != "":
		return r.Model
	}
	return r.Display
}

type choice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type ip struct {
	Address string `json:"address"`
}

type tag struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type object struct {
	ID               int     `json:"id"`
	Name             *string `json:"name"`
	Display          string  `json:"display"`
	Status           *choice `json:"status"`
	Description      string  `json:"description"`
	Serial           string  `json:"serial"`
	Role             *ref    `json:"role"`
	DeviceRole       *ref    `json:"device_role"`
	DeviceType       *ref    `json:"device_type"`
	Site             *ref    `json:"site"`
	Cluster          *ref    `json:"cluster"`
	Tenant           *ref    `json:"tenant"`
	Platform         *ref    `json:"platform"`
	PrimaryIP4       *ip     `json:"primary_ip4"`
	PrimaryIP6       *ip     `json:"primary_ip6"`
	Tags             []tag   `json:"tags"`
	Protocol         *choice `json:"protocol"`
	Ports            []int   `json:"ports"`
	Device           *ref    `json:"device"`
	VirtualMachine   *ref    `json:"virtual_machine"`
	Parent           *ref    `json:"parent"`
	ParentObjectType string  `json:"parent_object_type"`
}

// Object is a NetBox device, virtual machine or service in the shape Umbrella keeps.
type Object struct {
	Kind        string   `json:"kind"`
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	Description string   `json:"description"`
	Serial      string   `json:"serial,omitempty"`
	Site        string   `json:"site,omitempty"`
	Role        string   `json:"role,omitempty"`
	DeviceType  string   `json:"device_type,omitempty"`
	Cluster     string   `json:"cluster,omitempty"`
	Tenant      string   `json:"tenant,omitempty"`
	Platform    string   `json:"platform,omitempty"`
	Parent      string   `json:"parent,omitempty"`
	Ports       string   `json:"ports,omitempty"`
	IPs         []string `json:"ips"`
	Tags        []string `json:"tags"`
}

func (o object) normalize(kind string) Object {
	out := Object{Kind: kind, ID: o.ID, Name: o.Display, Description: o.Description, Serial: o.Serial, Site: o.Site.label(),
		DeviceType: o.DeviceType.label(), Cluster: o.Cluster.label(), Tenant: o.Tenant.label(), Platform: o.Platform.label(),
		IPs: []string{}, Tags: []string{}}
	if o.Name != nil && *o.Name != "" {
		out.Name = *o.Name
	}
	if o.Status != nil {
		out.Status = o.Status.Value
	}
	out.Role = o.Role.label()
	if out.Role == "" {
		out.Role = o.DeviceRole.label()
	}
	for _, a := range []*ip{o.PrimaryIP4, o.PrimaryIP6} {
		if a != nil && a.Address != "" {
			addr, _, _ := strings.Cut(a.Address, "/")
			out.IPs = append(out.IPs, addr)
		}
	}
	for _, t := range o.Tags {
		out.Tags = append(out.Tags, strings.ToLower(firstSet(t.Slug, t.Name)))
	}
	if kind == KindService {
		out.Parent = firstSet(o.Device.label(), o.VirtualMachine.label(), o.Parent.label())
		ports := make([]string, 0, len(o.Ports))
		for _, p := range o.Ports {
			ports = append(ports, strconv.Itoa(p))
		}
		out.Ports = strings.Join(ports, ", ")
		if o.Protocol != nil && out.Ports != "" {
			out.Ports = o.Protocol.Value + "/" + out.Ports
		}
		if out.Status == "" {
			out.Status = "active"
		}
	}
	return out
}

// Contact is a person assigned to NetBox objects.
type Contact struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Title string `json:"title"`
	Phone string `json:"phone"`
	Email string `json:"email"`
}

type assignment struct {
	ID          int    `json:"id"`
	ObjectType  string `json:"object_type"`
	ContentType string `json:"content_type"`
	ObjectID    int    `json:"object_id"`
	Contact     *ref   `json:"contact"`
	Role        *ref   `json:"role"`
}

// Assignment ties a contact to an object with a role, such as "Owner" or "On duty".
type Assignment struct {
	Kind      string `json:"kind"`
	ObjectID  int    `json:"object_id"`
	ContactID int    `json:"contact_id"`
	Role      string `json:"role"`
}

// Inventory is everything a synchronization reads from NetBox.
type Inventory struct {
	Objects     []Object
	Tags        []Tag
	Contacts    []Contact
	Assignments []Assignment
}

func (c *Client) Objects(ctx context.Context, kind string) ([]Object, error) {
	k, ok := kinds[kind]
	if !ok {
		return nil, fmt.Errorf("unknown kind %q", kind)
	}
	raw, err := list[object](ctx, c, k.api, nil)
	if err != nil {
		return nil, err
	}
	out := make([]Object, 0, len(raw))
	for _, o := range raw {
		out = append(out, o.normalize(kind))
	}
	return out, nil
}

// Fetch reads the objects the configuration imports and, when contacts are synchronized,
// the contacts assigned to them.
func (c *Client) Fetch(ctx context.Context) (Inventory, error) {
	var inv Inventory
	for _, kind := range []string{KindDevice, KindVM, KindService} {
		if !c.cfg.Imports(kind) {
			continue
		}
		objs, err := c.Objects(ctx, kind)
		if err != nil {
			return inv, err
		}
		inv.Objects = append(inv.Objects, objs...)
	}
	tags, err := c.Tags(ctx)
	if err != nil {
		return inv, err
	}
	inv.Tags = tags
	if !c.cfg.SyncContacts {
		return inv, nil
	}
	contacts, err := list[Contact](ctx, c, "tenancy/contacts", nil)
	if err != nil {
		return inv, err
	}
	inv.Contacts = contacts
	raw, err := list[assignment](ctx, c, "tenancy/contact-assignments", nil)
	if err != nil {
		return inv, err
	}
	types := map[string]string{}
	for kind, k := range kinds {
		types[k.objectType] = kind
	}
	for _, a := range raw {
		kind, ok := types[firstSet(a.ObjectType, a.ContentType)]
		if !ok || a.Contact == nil {
			continue
		}
		inv.Assignments = append(inv.Assignments, Assignment{Kind: kind, ObjectID: a.ObjectID, ContactID: a.Contact.ID, Role: a.Role.label()})
	}
	return inv, nil
}

// Probe is what a connection test found.
type Probe struct {
	Version  string `json:"version"`
	Devices  int    `json:"devices"`
	VMs      int    `json:"vms"`
	Services int    `json:"services"`
	Contacts int    `json:"contacts"`
}

func (c *Client) count(ctx context.Context, path string) (int, error) {
	var p page
	if err := c.do(ctx, http.MethodGet, path+"/", url.Values{"limit": {"1"}}, nil, &p); err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	return p.Count, nil
}

// Test checks the address and the token and counts what a synchronization would read.
func (c *Client) Test(ctx context.Context) (Probe, error) {
	var p Probe
	var st map[string]any
	if err := c.do(ctx, http.MethodGet, "status/", nil, nil, &st); err != nil {
		return p, err
	}
	p.Version, _ = st["netbox-version"].(string)
	var err error
	if p.Devices, err = c.count(ctx, kinds[KindDevice].api); err != nil {
		return p, err
	}
	if p.VMs, err = c.count(ctx, kinds[KindVM].api); err != nil {
		return p, err
	}
	if p.Services, err = c.count(ctx, kinds[KindService].api); err != nil {
		return p, err
	}
	if p.Contacts, err = c.count(ctx, "tenancy/contacts"); err != nil {
		return p, err
	}
	return p, nil
}

// Option is a NetBox object offered as a registration default.
type Option struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Choices struct {
	Sites       []Option `json:"sites"`
	DeviceRoles []Option `json:"device_roles"`
	DeviceTypes []Option `json:"device_types"`
	Clusters    []Option `json:"clusters"`
}

func options(ctx context.Context, c *Client, path string) ([]Option, error) {
	raw, err := list[ref](ctx, c, path, nil)
	if err != nil {
		return nil, err
	}
	out := make([]Option, 0, len(raw))
	for _, r := range raw {
		out = append(out, Option{ID: r.ID, Name: firstSet(r.label(), r.Display)})
	}
	return out, nil
}

func (c *Client) Choices(ctx context.Context) (Choices, error) {
	var out Choices
	var err error
	if out.Sites, err = options(ctx, c, "dcim/sites"); err != nil {
		return out, err
	}
	if out.DeviceRoles, err = options(ctx, c, "dcim/device-roles"); err != nil {
		return out, err
	}
	if out.DeviceTypes, err = options(ctx, c, "dcim/device-types"); err != nil {
		return out, err
	}
	if out.Clusters, err = options(ctx, c, "virtualization/clusters"); err != nil {
		return out, err
	}
	return out, nil
}

// Spec is a new object to register or the fields to change in an existing one.
type Spec struct {
	Name        string
	Status      string
	Description string
}

func (c *Client) body(kind string, s Spec, create bool) (map[string]any, error) {
	b := map[string]any{"name": s.Name, "description": s.Description}
	if s.Status != "" {
		b["status"] = s.Status
	}
	if !create {
		return b, nil
	}
	set := func(key string, id int) {
		if id > 0 {
			b[key] = id
		}
	}
	switch kind {
	case KindDevice:
		if c.cfg.SiteID == 0 || c.cfg.DeviceRoleID == 0 || c.cfg.DeviceTypeID == 0 {
			return nil, fmt.Errorf("%w: choose the site, device role and device type for new devices in the NetBox settings", ErrDefaults)
		}
		set("site", c.cfg.SiteID)
		set("device_type", c.cfg.DeviceTypeID)
		// NetBox 4 calls the field role, NetBox 3 device_role; each ignores the other.
		set("role", c.cfg.DeviceRoleID)
		set("device_role", c.cfg.DeviceRoleID)
	case KindVM:
		if c.cfg.SiteID == 0 && c.cfg.ClusterID == 0 {
			return nil, fmt.Errorf("%w: choose the site or the cluster for new virtual machines in the NetBox settings", ErrDefaults)
		}
		set("site", c.cfg.SiteID)
		set("cluster", c.cfg.ClusterID)
	default:
		return nil, fmt.Errorf("objects of kind %q cannot be registered in NetBox", kind)
	}
	return b, nil
}

// Create registers a new device or virtual machine with the registration defaults.
func (c *Client) Create(ctx context.Context, kind string, s Spec) (Object, error) {
	b, err := c.body(kind, s, true)
	if err != nil {
		return Object{}, err
	}
	var o object
	if err := c.do(ctx, http.MethodPost, kinds[kind].api+"/", nil, b, &o); err != nil {
		return Object{}, err
	}
	return o.normalize(kind), nil
}

func (c *Client) Update(ctx context.Context, kind string, id int, s Spec) error {
	k, ok := kinds[kind]
	if !ok {
		return fmt.Errorf("unknown kind %q", kind)
	}
	b, err := c.body(kind, s, false)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPatch, fmt.Sprintf("%s/%d/", k.api, id), nil, b, nil)
}

// Delete removes the object; ErrNotFound means it was already gone.
func (c *Client) Delete(ctx context.Context, kind string, id int) error {
	k, ok := kinds[kind]
	if !ok {
		return fmt.Errorf("unknown kind %q", kind)
	}
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("%s/%d/", k.api, id), nil, nil, nil)
}

func firstSet(v ...string) string {
	for _, x := range v {
		if x != "" {
			return x
		}
	}
	return ""
}

// Tag is a NetBox tag. Umbrella keeps one tag per business service.
type Tag struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

const tagsPath = "extras/tags"

// TagURL is the page of the tag in the NetBox web interface.
func (c Config) TagURL(id int) string {
	if c.URL == "" || id <= 0 {
		return ""
	}
	return fmt.Sprintf("%s/%s/%d/", c.URL, tagsPath, id)
}

// Tags reads every tag.
func (c *Client) Tags(ctx context.Context) ([]Tag, error) {
	return list[Tag](ctx, c, tagsPath, nil)
}

// EnsureTag changes the tag with the given ID or, when there is none (any more), the tag with
// the same slug, and creates the tag when neither exists.
func (c *Client) EnsureTag(ctx context.Context, id int, t Tag) (Tag, error) {
	body := map[string]any{"name": t.Name, "slug": t.Slug, "color": t.Color, "description": t.Description}
	if id <= 0 {
		found, err := list[Tag](ctx, c, tagsPath, url.Values{"slug": {t.Slug}})
		if err != nil {
			return Tag{}, err
		}
		for _, x := range found {
			if x.Slug == t.Slug {
				id = x.ID
			}
		}
	}
	var out Tag
	if id > 0 {
		err := c.do(ctx, http.MethodPatch, fmt.Sprintf("%s/%d/", tagsPath, id), nil, body, &out)
		if !errors.Is(err, ErrNotFound) {
			return out, err
		}
	}
	err := c.do(ctx, http.MethodPost, tagsPath+"/", nil, body, &out)
	var nbErr *Error
	if errors.As(err, &nbErr) && nbErr.Status == http.StatusBadRequest && strings.Contains(nbErr.Detail, "name") {
		// Another tag already has the name: keep the slug in the name to tell them apart.
		body["name"] = clipName(t.Name, 100-len(t.Slug)-3) + " (" + t.Slug + ")"
		err = c.do(ctx, http.MethodPost, tagsPath+"/", nil, body, &out)
	}
	return out, err
}

func clipName(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// DeleteTag removes the tag, which NetBox also takes off every object; a missing tag is fine.
func (c *Client) DeleteTag(ctx context.Context, id int) error {
	err := c.do(ctx, http.MethodDelete, fmt.Sprintf("%s/%d/", tagsPath, id), nil, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// SetTag puts the tag on the object or takes it off, keeping the other tags of the object.
func (c *Client) SetTag(ctx context.Context, kind string, objectID, tagID int, on bool) error {
	k, ok := kinds[kind]
	if !ok {
		return fmt.Errorf("unknown kind %q", kind)
	}
	path := fmt.Sprintf("%s/%d/", k.api, objectID)
	var o struct {
		Tags []Tag `json:"tags"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &o); err != nil {
		return err
	}
	ids := []int{}
	has := false
	for _, t := range o.Tags {
		if t.ID == tagID {
			has = true
			if !on {
				continue
			}
		}
		ids = append(ids, t.ID)
	}
	if has == on {
		return nil
	}
	if on {
		ids = append(ids, tagID)
	}
	return c.do(ctx, http.MethodPatch, path, nil, map[string]any{"tags": ids}, nil)
}
