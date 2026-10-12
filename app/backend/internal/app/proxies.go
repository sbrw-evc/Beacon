package app

import (
	"net/http"
	"slices"
	"strings"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const maxTrustedProxies = 50

// trustedProxies are the proxies of UMBRELLA_TRUSTED_PROXIES and those set in the interface.
func (a *App) trustedProxies() TrustedProxies {
	var set []string
	a.deps.Store.Read(func(d *store.Data) { set = d.Settings.TrustedProxies })
	if len(set) == 0 {
		return a.proxies
	}
	out := slices.Clone(a.proxies)
	for _, n := range set {
		if p, err := parseNetwork(n); err == nil {
			out = append(out, p)
		}
	}
	return out
}

type proxySettings struct {
	// Set are the proxies set in the interface; Env those of UMBRELLA_TRUSTED_PROXIES, read-only.
	Set      []string `json:"trusted_proxies"`
	Env      []string `json:"env"`
	ClientIP string   `json:"client_ip"`
}

func (a *App) proxyView(r *http.Request) proxySettings {
	out := proxySettings{Set: []string{}, Env: []string{}, ClientIP: addrString(a.trustedProxies().ClientIP(r))}
	a.deps.Store.Read(func(d *store.Data) { out.Set = append(out.Set, d.Settings.TrustedProxies...) })
	for _, p := range a.proxies {
		out.Env = append(out.Env, p.String())
	}
	return out
}

func (a *App) proxySettings(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, a.proxyView(r))
}

type proxyInput struct {
	TrustedProxies []string `json:"trusted_proxies"`
}

func (a *App) saveProxySettings(w http.ResponseWriter, r *http.Request) {
	var in proxyInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	list := []string{}
	for _, n := range in.TrustedProxies {
		if n = strings.TrimSpace(n); n == "" {
			continue
		}
		p, err := parseNetwork(n)
		if err != nil || strings.Contains(n, "%") {
			writeError(w, invalid("proxy_invalid", err))
			return
		}
		if strings.Contains(n, "/") {
			n = p.String()
		} else {
			n = p.Addr().String()
		}
		if !slices.Contains(list, n) {
			list = append(list, n)
		}
	}
	if len(list) > maxTrustedProxies {
		writeError(w, invalid("too_many_proxies", nil))
		return
	}
	a.deps.Store.Write(func(d *store.Data) {
		d.Settings.TrustedProxies = list
		d.AddAudit(store.AuditEntry{Actor: current(r).user.Username, Action: "settings.trusted_proxies", Detail: strings.Join(list, ", ")})
	})
	httpx.JSON(w, http.StatusOK, a.proxyView(r))
}
