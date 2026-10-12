package app

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/ingest"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/netbox"
)

// TestWriteErrorAnswers pins the status and the exact body of the errors the handlers answer.
func TestWriteErrorAnswers(t *testing.T) {
	until := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, c := range []struct {
		err    error
		status int
		body   string
	}{
		{&PublishError{Issues: []flow.Issue{{Level: "error", Code: "c", Message: "m"}}}, http.StatusBadRequest,
			`{"error":"publish_failed","issues":[{"level":"error","code":"c","message":"m"}]}`},
		{fmt.Errorf("wrap: %w", &LockError{Lock: LockView{UserID: "u1", Username: "bob", Name: "Bob", Until: until}}), http.StatusConflict,
			`{"error":"locked","lock":{"user_id":"u1","username":"bob","name":"Bob","until":"2026-01-02T03:04:05Z","mine":false}}`},
		{&CredentialInUseError{Uses: []CredentialUse{{Kind: UseConnector, ID: "c1", Name: "A"}}}, http.StatusConflict,
			`{"detail":"the credential is used by A","error":"credential_in_use","used_by":[{"kind":"connector","id":"c1","name":"A"}]}`},
		{ErrCredentialInUse, http.StatusConflict, `{"error":"credential_in_use"}`},
		{fmt.Errorf("%w: boom", ErrSecretsDown), http.StatusServiceUnavailable, `{"error":"secrets_unavailable","detail":"OpenBao is unavailable: boom"}`},
		{&AliasTakenError{Items: []string{"a", "b"}}, http.StatusConflict, `{"error":"alias_taken","detail":"a, b"}`},
		{netbox.ErrDefaults, http.StatusBadRequest, `{"error":"netbox_defaults","detail":"` + netbox.ErrDefaults.Error() + `"}`},
		{netboxFailure{errors.New("down")}, http.StatusBadGateway, `{"error":"netbox_failed","detail":"down"}`},
		{ErrSlugTaken, http.StatusConflict, `{"error":"connector_slug_taken"}`},
		{ErrDraftConflict, http.StatusConflict, `{"error":"draft_conflict"}`},
		{ingest.ErrNotFound, http.StatusNotFound, `{"error":"not_found"}`},
		{ErrServiceNameTaken, http.StatusConflict, `{"error":"service_name_taken"}`},
		{ErrSourceInUse, http.StatusConflict, `{"error":"source_in_use"}`},
		{alert.ErrNotOpen, http.StatusConflict, `{"error":"not_open"}`},
		{alert.ErrEmptyComment, http.StatusBadRequest, `{"error":"empty_comment"}`},
		{invalid("bad_x", errors.New("why")), http.StatusBadRequest, `{"error":"bad_x","detail":"why"}`},
		{errors.New("other"), http.StatusInternalServerError, `{"error":"internal"}`},
	} {
		w := httptest.NewRecorder()
		writeError(w, c.err)
		if got := w.Body.String(); w.Code != c.status || got != c.body+"\n" {
			t.Errorf("%v: %d %s, want %d %s", c.err, w.Code, got, c.status, c.body)
		}
	}
}

func TestErrorCodeOfBulkItems(t *testing.T) {
	for _, c := range []struct {
		err          error
		code, detail string
	}{
		{netboxFailure{errors.New("down")}, "netbox_failed", "down"},
		{invalid("bad_x", nil), "bad_x", ""},
		{ErrNotFound, "not_found", ""},
		{errors.New("other"), "internal", ""},
	} {
		if code, detail := errorCode(c.err); code != c.code || detail != c.detail {
			t.Errorf("%v: %s %q, want %s %q", c.err, code, detail, c.code, c.detail)
		}
	}
	if alertCode(alert.ErrNotFound) != "not_found" || alertCode(ErrOutOfScope) != "internal" {
		t.Fatal("alert codes")
	}
}
