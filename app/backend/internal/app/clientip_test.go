package app_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/app"
)

func TestParseTrustedProxies(t *testing.T) {
	got := app.ParseTrustedProxies(" 10.0.0.1, 192.168.0.0/16 ,bogus,10.1.2.3/8,,2001:db8::/32,::ffff:172.16.0.0/108, fe80::1%eth0 ,1.2.3.4/40")
	want := []string{"10.0.0.1/32", "192.168.0.0/16", "10.0.0.0/8", "2001:db8::/32", "172.16.0.0/12", "fe80::1/128"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Errorf("entry %d = %s, want %s", i, got[i], want[i])
		}
	}
	if len(app.ParseTrustedProxies("")) != 0 {
		t.Error("empty trusts nothing")
	}
}

func TestClientIP(t *testing.T) {
	trusted := app.ParseTrustedProxies("10.0.0.0/8, 2001:db8::1")
	cases := []struct {
		name, remote string
		xff          []string
		realIP       string
		proxies      app.TrustedProxies
		want         string
	}{
		{name: "no proxies: headers ignored", remote: "10.0.0.5:1234", xff: []string{"1.1.1.1"}, realIP: "2.2.2.2", want: "10.0.0.5"},
		{name: "untrusted peer cannot spoof", remote: "203.0.113.9:5555", xff: []string{"10.20.0.15"}, realIP: "10.20.0.15", proxies: trusted, want: "203.0.113.9"},
		{name: "trusted peer, single hop", remote: "10.0.0.5:1234", xff: []string{"198.51.100.7"}, proxies: trusted, want: "198.51.100.7"},
		{name: "chain: rightmost untrusted wins", remote: "10.0.0.5:1234", xff: []string{"6.6.6.6, 198.51.100.7, 10.1.1.1"}, proxies: trusted, want: "198.51.100.7"},
		{name: "chain across headers", remote: "10.0.0.5:1234", xff: []string{"6.6.6.6", "198.51.100.7", "10.1.1.1"}, proxies: trusted, want: "198.51.100.7"},
		{name: "all hops trusted: X-Real-IP", remote: "10.0.0.5:1234", xff: []string{"10.1.1.1"}, realIP: "198.51.100.8", proxies: trusted, want: "198.51.100.8"},
		{name: "no headers: peer", remote: "10.0.0.5:1234", proxies: trusted, want: "10.0.0.5"},
		{name: "garbage hop stops the walk", remote: "10.0.0.5:1234", xff: []string{"198.51.100.7, junk"}, proxies: trusted, want: "10.0.0.5"},
		{name: "mapped IPv4 peer is unmapped", remote: "[::ffff:10.0.0.5]:1234", xff: []string{"::ffff:198.51.100.7"}, proxies: trusted, want: "198.51.100.7"},
		{name: "IPv6 peer with zone", remote: "[fe80::1%eth0]:80", want: "fe80::1"},
		{name: "trusted IPv6 proxy", remote: "[2001:db8::1]:443", xff: []string{"2001:db8::99"}, proxies: trusted, want: "2001:db8::99"},
		{name: "peer without port", remote: "192.0.2.1", want: "192.0.2.1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = c.remote
		for _, v := range c.xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		if c.realIP != "" {
			r.Header.Set("X-Real-IP", c.realIP)
		}
		if got := c.proxies.ClientIP(r).String(); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}
