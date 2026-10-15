package app

import (
	"net/http"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

// sessionMaxAge is how long the session cookie is kept by the browser.
var sessionMaxAge = auth.MaxLifetime

// logout ends the session in Umbrella; the answer names SPA's sign-out address, which ends the
// SPA session too and comes back to Umbrella.
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	s := current(r)
	a.deps.Sessions.Delete(s.ss.ID)
	a.deps.Store.Write(func(d *store.Data) {
		d.AddAudit(store.AuditEntry{At: time.Now().UTC(), Actor: s.user.Username, Action: "auth.logout"})
	})
	a.setCookie(w, "", -1)
	httpx.JSON(w, http.StatusOK, map[string]string{"redirect": a.spaSignOut(r)})
}

func (a *App) me(w http.ResponseWriter, r *http.Request) {
	s := current(r)
	httpx.JSON(w, http.StatusOK, a.view(s.user, s.ss.CSRF))
}

func (a *App) setCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: value, Path: "/", HttpOnly: true, Secure: a.opt.SecureCookies,
		SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}
