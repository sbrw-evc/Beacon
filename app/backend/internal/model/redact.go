package model

import (
	"net/url"
	"strings"
)

// RedactURL is a URL that carries a secret (a webhook URL) as it may be shown in records, logs
// and to people who cannot edit it: the scheme, the host and the last four characters,
// https://host/…abcd. A format parameter (not a secret) is left out of the tail.
func RedactURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "…"
	}
	tail := strings.TrimRight(u.EscapedPath(), "/")
	var params []string
	for _, p := range strings.Split(u.RawQuery, "&") {
		if p != "" && !strings.HasPrefix(p, "format=") {
			params = append(params, p)
		}
	}
	if len(params) > 0 {
		tail += "?" + strings.Join(params, "&")
	}
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	} else {
		tail = ""
	}
	return u.Scheme + "://" + u.Host + "/…" + tail
}
