package app

import (
	"io"
	"net/http"
	"strconv"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/avatar"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

func (a *App) respondUser(w http.ResponseWriter, r *http.Request, u model.User, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, a.view(u, current(r).ss.CSRF))
}

type preferencesInput struct {
	Timezone *string `json:"timezone"`
	Telegram *string `json:"telegram"`
	Locale   *string `json:"locale"`
}

func (a *App) preferences(w http.ResponseWriter, r *http.Request) {
	var in preferencesInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	id := current(r).user.ID
	u, err := a.users.Get(id)
	if err == nil && in.Telegram != nil {
		u, err = a.users.SetTelegram(id, *in.Telegram)
	}
	if err == nil && in.Locale != nil {
		u, err = a.users.SetLocale(id, *in.Locale)
	}
	if err == nil && in.Timezone != nil {
		u, err = a.users.SetTimezone(id, *in.Timezone)
	}
	a.respondUser(w, r, u, err)
}

func (a *App) uploadAvatar(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, avatar.MaxInput))
	if err != nil {
		writeError(w, ErrAvatarTooLarge)
		return
	}
	u, err := a.users.SetAvatar(current(r).user.ID, data)
	a.respondUser(w, r, u, err)
}

func (a *App) deleteAvatar(w http.ResponseWriter, r *http.Request) {
	u, err := a.users.RemoveAvatar(current(r).user.ID)
	a.respondUser(w, r, u, err)
}

func (a *App) avatar(w http.ResponseWriter, r *http.Request) {
	img, at, err := a.users.Avatar(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	version := strconv.FormatInt(at.UnixNano(), 10)
	etag := `"` + strconv.FormatInt(at.UnixNano(), 36) + `"`
	h := w.Header()
	h.Set("ETag", etag)
	h.Set("Last-Modified", at.UTC().Format(http.TimeFormat))
	h.Set("Vary", "Cookie")
	if r.URL.Query().Get("v") == version {
		h.Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "private, no-cache")
	}
	if httpx.NotModified(r, etag, at) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", avatar.MIME)
	_, _ = w.Write(img)
}
