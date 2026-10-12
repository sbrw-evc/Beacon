// Package inventorydb reads devices from Inventory DB (github.com/sbrw-evc/Inventory-DB) and
// sends it the state of the alerts on them. Inventory DB is an optional source of
// configuration items next to NetBox; its side of the exchange is described in its
// docs/integrations.md.
package inventorydb

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
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
	SecretPath   = "inventory-db"
	TokenKey     = "token"
	SignatureKey = "signing_secret"

	// RefPrefix starts the source reference of every object Inventory DB exports, for example
	// inventory-db:dcim.device:12.
	RefPrefix = "inventory-db:"

	pageSize = 1000
	// maxBatch is the most alert events one request carries; Inventory DB takes up to 500.
	maxBatch = 200
)

var Timeout = 30 * time.Second

// Config is the Inventory DB connection kept in the settings. The API token and the signing
// secret are in OpenBao.
type Config struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
	// IntegrationID is the integration created in Inventory DB (POST /api/v1/integrations).
	IntegrationID string `json:"integration_id"`
	TokenRef      string `json:"-"`
	SecretRef     string `json:"-"`
	SkipVerify    bool   `json:"skip_verify"`
	SyncMinutes   int    `json:"sync_minutes"`
	ImportDevices bool   `json:"import_devices"`
	// PushStatus sends the state of alerts on imported devices back to Inventory DB.
	PushStatus bool `json:"push_status"`
}

// Defaults: once connected, read devices every 15 minutes and send alert states back.
func Defaults() Config { return Config{SyncMinutes: 15, ImportDevices: true, PushStatus: true} }

const maxSyncMinutes = 7 * 24 * 60

func (c Config) Normalize() (Config, error) {
	c.URL = strings.TrimRight(strings.TrimSpace(c.URL), "/")
	c.URL = strings.TrimSuffix(strings.TrimSuffix(c.URL, "/api/v1"), "/api")
	c.IntegrationID = strings.TrimSpace(c.IntegrationID)
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return c, errors.New("Inventory DB address must look like https://inventory.example.com")
	}
	if c.IntegrationID == "" || strings.ContainsAny(c.IntegrationID, "/?#% ") {
		return c, errors.New("integration ID must be the id Inventory DB returned, such as int_abc123")
	}
	if c.SyncMinutes < 0 || c.SyncMinutes > maxSyncMinutes {
		return c, fmt.Errorf("synchronization interval must be 0 to %d minutes", maxSyncMinutes)
	}
	return c, nil
}

// Relation is a link from one exported object to another.
type Relation struct {
	Type      string `json:"type"`
	Direction string `json:"direction"`
	TargetRef string `json:"target_ref"`
	Via       string `json:"via,omitempty"`
}

// Identities are the names and addresses events can call an object by.
type Identities struct {
	Hostname string   `json:"hostname,omitempty"`
	FQDN     []string `json:"fqdn,omitempty"`
	IP       []string `json:"ip,omitempty"`
	Serial   string   `json:"serial,omitempty"`
	AssetTag string   `json:"asset_tag,omitempty"`
}

// CI is one configuration item of the Inventory DB feed: a site, rack or device.
type CI struct {
	SourceRef    string         `json:"source_ref"`
	Type         string         `json:"type"`
	Name         string         `json:"name"`
	Identities   Identities     `json:"identities"`
	LogicalGroup string         `json:"logical_group"`
	Status       string         `json:"status"`
	Monitored    bool           `json:"monitored"`
	Attributes   map[string]any `json:"attributes"`
	Relations    []Relation     `json:"relations"`
	URL          string         `json:"url"`
}

// Attr is a text attribute of the item, or "".
func (c CI) Attr(name string) string {
	switch v := c.Attributes[name].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// Tags are the tag slugs of the item.
func (c CI) Tags() []string {
	raw, _ := c.Attributes["tags"].([]any)
	var out []string
	for _, t := range raw {
		if s, ok := t.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ID is the number in the source reference (inventory-db:dcim.device:12 → 12), or 0.
func (c CI) ID() int {
	i := strings.LastIndexByte(c.SourceRef, ':')
	n, _ := strconv.Atoi(c.SourceRef[i+1:])
	return n
}

// Ref is the source reference of a device.
func Ref(id int) string { return RefPrefix + "dcim.device:" + strconv.Itoa(id) }

type feedPage struct {
	Count      int  `json:"count"`
	NextOffset *int `json:"next_offset"`
	Items      []CI `json:"items"`
}

// AlertEvent is the state of an alert as Inventory DB takes it.
type AlertEvent struct {
	AlertID   string     `json:"alert_id"`
	Status    string     `json:"status"`
	Severity  string     `json:"severity"`
	Title     string     `json:"title"`
	Signal    string     `json:"signal,omitempty"`
	CI        AlertCI    `json:"ci"`
	Links     AlertLinks `json:"links,omitzero"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type AlertCI struct {
	SourceRefs []string `json:"source_refs,omitempty"`
	Name       string   `json:"name,omitempty"`
}

type AlertLinks struct {
	Incident string `json:"incident,omitempty"`
	Grafana  string `json:"grafana,omitempty"`
}

type Client struct {
	cfg    Config
	token  string
	secret string
	http   *http.Client
	now    func() time.Time
}

// New connects with the API token (read) and, when alert states are sent, the signing secret.
func New(cfg Config, token, secret string) (*Client, error) {
	cfg, err := cfg.Normalize()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("API token is empty")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.SkipVerify}
	return &Client{cfg: cfg, token: strings.TrimSpace(token), secret: strings.TrimSpace(secret),
		http: &http.Client{Timeout: Timeout, Transport: tr}, now: time.Now}, nil
}

// Error is a request Inventory DB answered with an error status.
type Error struct {
	Status int
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("Inventory DB answered %d", e.Status)
	}
	return fmt.Sprintf("Inventory DB answered %d: %s", e.Status, e.Detail)
}

func (c *Client) base() string {
	return c.cfg.URL + "/api/v1/integrations/" + url.PathEscape(c.cfg.IntegrationID) + "/umbrella"
}

func (c *Client) send(req *http.Request, out any) error {
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Inventory DB %s: %w", c.cfg.URL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("Inventory DB %s: %w", c.cfg.URL, err)
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		if len(e.Message) > 300 {
			e.Message = e.Message[:300]
		}
		return &Error{Status: resp.StatusCode, Detail: e.Message}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("Inventory DB answer is not JSON: %w", err)
	}
	return nil
}

func (c *Client) page(ctx context.Context, offset, limit int) (feedPage, error) {
	var p feedPage
	u := c.base() + "/ci?" + url.Values{"offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(limit)}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return p, err
	}
	req.Header.Set("xc-token", c.token)
	return p, c.send(req, &p)
}

// Feed reads every item of the feed.
func (c *Client) Feed(ctx context.Context) ([]CI, error) {
	var out []CI
	for offset := 0; ; {
		p, err := c.page(ctx, offset, pageSize)
		if err != nil {
			return nil, err
		}
		out = append(out, p.Items...)
		if p.NextOffset == nil || *p.NextOffset <= offset || len(p.Items) == 0 {
			return out, nil
		}
		offset = *p.NextOffset
	}
}

// Probe is what a connection test found.
type Probe struct {
	Items int `json:"items"`
}

// Test checks the address, the integration ID and the token.
func (c *Client) Test(ctx context.Context) (Probe, error) {
	p, err := c.page(ctx, 0, 1)
	return Probe{Items: p.Count}, err
}

// Sign is the signature Inventory DB checks: v1=hex(HMAC-SHA256(secret, "<timestamp>.<body>")).
func Sign(secret, timestamp string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(timestamp + "."))
	m.Write(body)
	return "v1=" + hex.EncodeToString(m.Sum(nil))
}

// Push sends alert states, in batches.
func (c *Client) Push(ctx context.Context, events []AlertEvent) error {
	if c.secret == "" {
		return errors.New("signing secret is empty")
	}
	for len(events) > 0 {
		n := min(len(events), maxBatch)
		body, err := json.Marshal(events[:n])
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/alerts", bytes.NewReader(body))
		if err != nil {
			return err
		}
		ts := strconv.FormatInt(c.now().Unix(), 10)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Umbrella-Timestamp", ts)
		req.Header.Set("X-Umbrella-Signature", Sign(c.secret, ts, body))
		if err := c.send(req, nil); err != nil {
			return err
		}
		events = events[n:]
	}
	return nil
}
