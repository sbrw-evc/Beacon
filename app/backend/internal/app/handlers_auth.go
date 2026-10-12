package app

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
)

type loginInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var in loginInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	ip := a.clientIP(r)
	if a.limiter.Locked(in.Username, ip) {
		writeError(w, ErrTooManyAttempts)
		return
	}
	u, err := a.auth.Authenticate(in.Username, in.Password)
	if errors.Is(err, ErrInvalidCredentials) {
		a.limiter.Fail(in.Username, ip)
		a.auth.Failed(strings.TrimSpace(in.Username), ip)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	a.limiter.Reset(in.Username, ip)
	ss := a.deps.Sessions.Create(u.ID)
	u = a.auth.Signed(u.ID, ip)
	a.setCookie(w, ss.ID, int(auth.MaxLifetime/time.Second))
	httpx.JSON(w, http.StatusOK, a.view(u, ss.CSRF))
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	s := current(r)
	a.deps.Sessions.Delete(s.ss.ID)
	a.auth.SignedOut(s.user.Username)
	a.setCookie(w, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) me(w http.ResponseWriter, r *http.Request) {
	s := current(r)
	httpx.JSON(w, http.StatusOK, a.view(s.user, s.ss.CSRF))
}

func (a *App) setCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: value, Path: "/", HttpOnly: true, Secure: a.opt.SecureCookies,
		SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}
