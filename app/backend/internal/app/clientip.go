package app

import (
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// TrustedProxies are the reverse proxies whose forwarding headers are believed when the
// client address is needed: TV wallboard and connector networks, sign-in limits, the audit.
type TrustedProxies []netip.Prefix

// ParseTrustedProxies reads a comma-separated list of CIDRs and addresses. Invalid entries are
// logged and skipped.
func ParseTrustedProxies(s string) TrustedProxies {
	var out TrustedProxies
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, err := parseNetwork(part)
		if err != nil {
			slog.Warn("UMBRELLA_TRUSTED_PROXIES: entry skipped", "entry", part, "err", err)
			continue
		}
		out = append(out, p)
	}
	return out
}

// parseNetwork reads a CIDR or a single address into a masked prefix.
func parseNetwork(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		if p.Addr().Is4In6() {
			bits := p.Bits() - 96
			if bits < 0 {
				return netip.Prefix{}, &net.ParseError{Type: "CIDR", Text: s}
			}
			p = netip.PrefixFrom(p.Addr().Unmap(), bits)
		}
		return p.Masked(), nil
	}
	ip, err := parseIP(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(ip, ip.BitLen()), nil
}

// parseIP reads an address, dropping a zone and unmapping IPv4-mapped IPv6 addresses.
func parseIP(s string) (netip.Addr, error) {
	ip, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return netip.Addr{}, err
	}
	return ip.WithZone("").Unmap(), nil
}

func (t TrustedProxies) contains(ip netip.Addr) bool {
	for _, p := range t {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// remoteIP is the address of the peer of the connection.
func remoteIP(r *http.Request) (netip.Addr, bool) {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip, err := parseIP(host)
	return ip, err == nil
}

// ClientIP is the address of the client: the peer of the connection, unless the peer is a
// trusted proxy; then X-Forwarded-For is read from the right, skipping trusted proxies, and the
// first other address wins, then X-Real-IP.
func (t TrustedProxies) ClientIP(r *http.Request) netip.Addr {
	peer, ok := remoteIP(r)
	if !ok || !t.contains(peer) {
		return peer
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		ip, err := parseIP(hops[i])
		if err != nil {
			// A hop that is not an address cannot be checked; nothing left of it is trusted.
			return peer
		}
		if !t.contains(ip) {
			return ip
		}
	}
	if ip, err := parseIP(r.Header.Get("X-Real-IP")); err == nil {
		return ip
	}
	return peer
}
