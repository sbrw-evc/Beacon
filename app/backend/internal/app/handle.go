package app

import (
	"context"
	"net/http"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
)

// Handler templates: most endpoints decode a JSON body, call one service method and answer
// with its result, a status or nothing. The templates keep that shape in one place, so a route
// is its pattern, its permission and the call.

// route registers h for pattern, for signed-in users who have perm.
func (a *App) route(mux *http.ServeMux, pattern, perm string, h http.HandlerFunc) {
	mux.HandleFunc(pattern, a.authed(a.can(perm, h)))
}

// show answers 200 with what f returns.
func show[T any](f func() T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, f())
	}
}

// check answers 200 with what f returns, given checkTimeout to finish: the overviews and the
// connection checks of the settings pages.
func check[T any](f func(ctx context.Context) T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
		defer cancel()
		httpx.JSON(w, http.StatusOK, f(ctx))
	}
}

// fetch answers 200 with what f returns for the request, or with its error.
func fetch[T any](f func(r *http.Request) (T, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := f(r)
		reply(w, http.StatusOK, v, err)
	}
}

// submit decodes the JSON body into In, calls f and answers status with its result, or with
// its error.
func submit[In, Out any](status int, f func(r *http.Request, in In) (Out, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in In
		if !httpx.Decode(w, r, &in) {
			return
		}
		v, err := f(r, in)
		reply(w, status, v, err)
	}
}

// remove calls f and answers 204 No Content, or with its error.
func remove(f func(r *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := f(r); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// actorName is the username of the signed-in user, as services record it.
func actorName(r *http.Request) string { return current(r).user.Username }
