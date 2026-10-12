package directorytest

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	ber "github.com/go-asn1-ber/asn1-ber"
	"github.com/go-ldap/ldap/v3"
	"github.com/jimlambrt/gldap"
)

type Entry struct {
	DN       string
	Password string
	Attrs    map[string][]string
}

type Server struct {
	URL string

	mu       sync.Mutex
	entries  []Entry
	bound    map[int]string
	Searches []string
}

func Start(t *testing.T, entries ...Entry) *Server {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	s := &Server{URL: "ldap://" + addr, entries: entries, bound: map[int]string{}}
	srv, err := gldap.NewServer(gldap.WithDisablePanicRecovery())
	if err != nil {
		t.Fatal(err)
	}
	mux, err := gldap.NewMux()
	if err != nil {
		t.Fatal(err)
	}
	if err := mux.Bind(s.bind); err != nil {
		t.Fatal(err)
	}
	if err := mux.Search(s.search); err != nil {
		t.Fatal(err)
	}
	if err := srv.Router(mux); err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Run(addr) }()
	for i := 0; i < 100 && !srv.Ready(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if !srv.Ready() {
		t.Fatal("test directory did not start")
	}
	t.Cleanup(func() { _ = srv.Stop() })
	return s
}

// Put adds an entry or replaces the one with the same DN.
func (s *Server) Put(e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if x := s.find(e.DN); x != nil {
		*x = e
		return
	}
	s.entries = append(s.entries, e)
}

func (s *Server) find(dn string) *Entry {
	for i := range s.entries {
		if strings.EqualFold(s.entries[i].DN, dn) {
			return &s.entries[i]
		}
	}
	return nil
}

func (s *Server) bind(w *gldap.ResponseWriter, r *gldap.Request) {
	resp := r.NewBindResponse(gldap.WithResponseCode(gldap.ResultInvalidCredentials))
	defer func() { _ = w.Write(resp) }()
	m, err := r.GetSimpleBindMessage()
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bound, r.ConnectionID())
	e := s.find(m.UserName)
	if e == nil || e.Password == "" || string(m.Password) != e.Password {
		return
	}
	s.bound[r.ConnectionID()] = e.DN
	resp.SetResultCode(gldap.ResultSuccess)
}

func (s *Server) search(w *gldap.ResponseWriter, r *gldap.Request) {
	done := r.NewSearchDoneResponse(gldap.WithResponseCode(gldap.ResultOperationsError))
	defer func() { _ = w.Write(done) }()
	m, err := r.GetSearchMessage()
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Searches = append(s.Searches, m.Filter)
	if s.bound[r.ConnectionID()] == "" {
		done.SetResultCode(gldap.ResultInsufficientAccessRights)
		return
	}
	f, err := ldap.CompileFilter(m.Filter)
	if err != nil {
		done.SetResultCode(gldap.ResultProtocolError)
		return
	}
	var hits []Entry
	if m.Scope == gldap.BaseObject {
		e := s.find(m.BaseDN)
		if e == nil {
			done.SetResultCode(gldap.ResultNoSuchObject)
			return
		}
		if match(f, *e) {
			hits = append(hits, *e)
		}
	} else {
		suffix := "," + strings.ToLower(m.BaseDN)
		for _, e := range s.entries {
			dn := strings.ToLower(e.DN)
			if (dn == strings.ToLower(m.BaseDN) || strings.HasSuffix(dn, suffix)) && match(f, e) {
				hits = append(hits, e)
			}
		}
	}
	code := gldap.ResultSuccess
	if m.SizeLimit > 0 && int64(len(hits)) > m.SizeLimit {
		hits, code = hits[:m.SizeLimit], gldap.ResultSizeLimitExceeded
	}
	for _, e := range hits {
		_ = w.Write(r.NewSearchResponseEntry(e.DN, gldap.WithAttributes(e.Attrs)))
	}
	done.SetResultCode(code)
}

func values(e Entry, attr string) []string {
	if strings.EqualFold(attr, "objectClass") && len(e.Attrs["objectClass"]) == 0 {
		return []string{"top"}
	}
	for k, v := range e.Attrs {
		if strings.EqualFold(k, attr) {
			return v
		}
	}
	return nil
}

func has(e Entry, attr, value string) bool {
	for _, v := range values(e, attr) {
		if strings.EqualFold(v, value) {
			return true
		}
	}
	return false
}

func match(p *ber.Packet, e Entry) bool {
	switch p.Tag {
	case ldap.FilterAnd:
		for _, c := range p.Children {
			if !match(c, e) {
				return false
			}
		}
		return true
	case ldap.FilterOr:
		for _, c := range p.Children {
			if match(c, e) {
				return true
			}
		}
		return false
	case ldap.FilterNot:
		return len(p.Children) == 1 && !match(p.Children[0], e)
	case ldap.FilterEqualityMatch:
		return has(e, fmt.Sprint(p.Children[0].Value), string(p.Children[1].Data.Bytes()))
	case ldap.FilterPresent:
		return len(values(e, p.Data.String())) > 0
	case ldap.FilterExtensibleMatch:
		var attr, value string
		for _, c := range p.Children {
			switch c.Tag {
			case 2:
				attr = c.Data.String()
			case 3:
				value = c.Data.String()
			}
		}
		return has(e, attr, value)
	}
	return false
}
