// Package ledgertest is a fake Ledger: its CMDB feed and alert webhook.
package ledgertest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/sbrw-evc/beacon/app/backend/internal/ledger"
)

const (
	Integration = "int_test01"
	Token       = "nc_test-token"
	Secret      = "whsec_test-secret"
)

type Server struct {
	*httptest.Server
	mu     sync.Mutex
	items  []map[string]any
	events []ledger.AlertEvent
	// Fail answers every request with this status when set.
	Fail int
}

func Start(t *testing.T) *Server {
	s := &Server{}
	mux := http.NewServeMux()
	base := "/api/v1/integrations/" + Integration + "/umbrella"
	mux.HandleFunc("GET "+base+"/ci", s.feed)
	mux.HandleFunc("POST "+base+"/alerts", s.alerts)
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// Device adds a device to the feed.
func Device(id int, name, ip string) map[string]any {
	return map[string]any{"source_ref": ledger.Ref(id), "type": "device", "name": name, "status": "active", "monitored": true,
		"identities": map[string]any{"hostname": name, "ip": []any{ip}, "serial": "SN" + strconv.Itoa(id)},
		"attributes": map[string]any{"site": "DC1", "rack": "R01", "role": "Server", "manufacturer": "Dell", "model": "R650", "tags": []any{"prod"}},
		"relations":  []any{}, "url": "https://inventory.example/dcim/devices/" + strconv.Itoa(id)}
}

// Set replaces the feed.
func (s *Server) Set(items ...map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = items
}

// Events are the alert states received so far.
func (s *Server) Events() []ledger.AlertEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ledger.AlertEvent(nil), s.events...)
}

func (s *Server) feed(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Fail != 0 {
		w.WriteHeader(s.Fail)
		return
	}
	if r.Header.Get("xc-token") != Token {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"UNAUTHORIZED","message":"Authentication required"}`)
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	end := min(offset+limit, len(s.items))
	page := []map[string]any{}
	if offset < end {
		page = s.items[offset:end]
	}
	var next any
	if end < len(s.items) {
		next = end
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"source": "inventory-db", "integration_id": Integration, "count": len(s.items),
		"offset": offset, "next_offset": next, "items": page})
}

func (s *Server) alerts(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	ts := r.Header.Get("X-Umbrella-Timestamp")
	if r.Header.Get("X-Umbrella-Signature") != ledger.Sign(Secret, ts, body) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var in []ledger.AlertEvent
	if err := json.Unmarshal(body, &in); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.events = append(s.events, in...)
	s.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
	_, _ = io.WriteString(w, `{"results":[]}`)
}
