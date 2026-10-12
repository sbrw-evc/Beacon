package app

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/credentials"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/ingest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/netbox"
)

var (
	ErrInvalidCredentials   = errors.New("invalid username or password")
	ErrDirectoryUnavailable = errors.New("directory is unavailable")
	ErrManagedByDirectory   = errors.New("the account is managed by the directory")
	ErrWrongPassword        = errors.New("the current password is wrong")
	ErrSamePassword         = errors.New("the new password equals the current one")
	ErrAvatarInvalid        = errors.New("the file is not a supported image")
	ErrAvatarTooLarge       = errors.New("the image is too large")
	ErrTooManyAttempts      = errors.New("too many failed attempts")
	ErrNotFound             = errors.New("not found")
	ErrUnauthenticated      = errors.New("unauthenticated")
	ErrPasswordExpired      = errors.New("the password has expired")
	ErrNoLocalAdmin         = errors.New("no active local administrator would remain")

	invalidCSRF = errors.New("csrf")
	forbidden   = errors.New("forbidden")
)

type InputError struct {
	Code string
	Err  error
}

func (e *InputError) Error() string {
	if e.Err == nil {
		return e.Code
	}
	return e.Code + ": " + e.Err.Error()
}

func invalid(code string, err error) error { return &InputError{Code: code, Err: err} }

// errStatus answers a sentinel error (matched with errors.Is) with a status and a code.
type errStatus = struct {
	err    error
	status int
	code   string
}

// statuses answers the sentinel errors. Files of the package add their own in init.
var statuses = []errStatus{
	{ErrInvalidCredentials, http.StatusUnauthorized, "invalid_credentials"},
	{ErrUnauthenticated, http.StatusUnauthorized, "unauthenticated"},
	{ErrDirectoryUnavailable, http.StatusServiceUnavailable, "directory_unavailable"},
	{credentials.ErrUnavailable, http.StatusServiceUnavailable, "secrets_unavailable"},
	{ErrManagedByDirectory, http.StatusConflict, "managed_by_directory"},
	{ErrWrongPassword, http.StatusForbidden, "wrong_password"},
	{ErrSamePassword, http.StatusBadRequest, "same_password"},
	{ErrAvatarInvalid, http.StatusBadRequest, "avatar_invalid"},
	{ErrAvatarTooLarge, http.StatusRequestEntityTooLarge, "avatar_too_large"},
	{ErrTooManyAttempts, http.StatusTooManyRequests, "too_many_attempts"},
	{ErrNotFound, http.StatusNotFound, "not_found"},
	{ingest.ErrNotFound, http.StatusNotFound, "not_found"},
	{invalidCSRF, http.StatusForbidden, "csrf"},
	{forbidden, http.StatusForbidden, "forbidden"},
	{ErrPasswordExpired, http.StatusForbidden, "password_expired"},
	{ErrNoLocalAdmin, http.StatusConflict, "no_local_admin"},
	{ErrSlugTaken, http.StatusConflict, "connector_slug_taken"},
	{ErrDraftConflict, http.StatusConflict, "draft_conflict"},
	{ErrServiceNameTaken, http.StatusConflict, "service_name_taken"},
	{ErrCredentialInUse, http.StatusConflict, "credential_in_use"},
}

// alertStatuses answers the errors of the alert store; bulk actions report the same codes.
var alertStatuses = []errStatus{
	{alert.ErrNotFound, http.StatusNotFound, "not_found"},
	{alert.ErrNotOpen, http.StatusConflict, "not_open"},
	{alert.ErrNotActive, http.StatusConflict, "not_active"},
	{alert.ErrEmptyComment, http.StatusBadRequest, "empty_comment"},
	{alert.ErrBadAction, http.StatusBadRequest, "bad_action"},
}

func init() { statuses = append(statuses, alertStatuses...) }

// alertCode is the code of an error of the alert store, "internal" for any other.
func alertCode(err error) string {
	for _, s := range alertStatuses {
		if errors.Is(err, s.err) {
			return s.code
		}
	}
	return "internal"
}

// answer is the response to an error: the problem with code and detail, or body instead when
// the error carries more than a detail.
type answer struct {
	status int
	code   string
	detail error
	body   any
}

// structured answers the errors that carry data or whose text is worth showing. They are tried
// in order before the statuses table.
var structured = []func(error) (answer, bool){
	as(func(e *PublishError) answer {
		return answer{status: http.StatusBadRequest, code: "publish_failed", body: map[string]any{"error": "publish_failed", "issues": e.Issues}}
	}),
	as(func(e *LockError) answer {
		return answer{status: http.StatusConflict, code: "locked", body: map[string]any{"error": "locked", "lock": e.Lock}}
	}),
	as(func(e *CredentialInUseError) answer {
		return answer{status: http.StatusConflict, code: "credential_in_use", detail: e, body: map[string]any{"error": "credential_in_use", "detail": e.Error(), "used_by": e.Uses}}
	}),
	as(func(e *AliasTakenError) answer {
		return answer{status: http.StatusConflict, code: "alias_taken", detail: e}
	}),
	shown(ErrSecretsDown, http.StatusServiceUnavailable, "secrets_unavailable"),
	shown(netbox.ErrDefaults, http.StatusBadRequest, "netbox_defaults"),
	as(func(e netboxFailure) answer {
		return answer{status: http.StatusBadGateway, code: "netbox_failed", detail: e.err}
	}),
	as(func(e *model.PolicyError) answer {
		return answer{status: http.StatusBadRequest, code: "weak_password", detail: e, body: httpx.Problem{Code: "weak_password", Detail: e.Error(), Violations: e.Violations}}
	}),
	as(func(e *InputError) answer { return answer{status: http.StatusBadRequest, code: e.Code, detail: e.Err} }),
}

// as answers the errors of type E (matched with errors.As).
func as[E error](f func(E) answer) func(error) (answer, bool) {
	return func(err error) (answer, bool) {
		var e E
		if !errors.As(err, &e) {
			return answer{}, false
		}
		return f(e), true
	}
}

// shown answers target like the statuses table, with the error text as the detail.
func shown(target error, status int, code string) func(error) (answer, bool) {
	return func(err error) (answer, bool) {
		if !errors.Is(err, target) {
			return answer{}, false
		}
		return answer{status: status, code: code, detail: err}, true
	}
}

func answerOf(err error) answer {
	for _, f := range structured {
		if a, ok := f(err); ok {
			return a
		}
	}
	for _, s := range statuses {
		if errors.Is(err, s.err) {
			return answer{status: s.status, code: s.code}
		}
	}
	return answer{status: http.StatusInternalServerError, code: "internal"}
}

func writeError(w http.ResponseWriter, err error) {
	a := answerOf(err)
	switch {
	case a.status == http.StatusInternalServerError:
		slog.Error("request failed", "err", err)
		httpx.Error(w, a.status, a.code, nil)
	case a.body != nil:
		httpx.JSON(w, a.status, a.body)
	default:
		httpx.Error(w, a.status, a.code, a.detail)
	}
}

// reply answers with out as JSON, or with the error.
func reply(w http.ResponseWriter, status int, out any, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, status, out)
}
