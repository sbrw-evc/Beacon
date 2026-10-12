package app

import (
	"context"
	"net/http"
	"strings"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/auth"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/flow"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
)

// generatedTokenBytes: 32 random bytes, 43 characters in base64url.
const generatedTokenBytes = 32

// GenerateBearer creates a Bearer credential with a random token and returns the token: this is
// the only time it can be read, it is kept in OpenBao.
func (s *CredentialsService) GenerateBearer(ctx context.Context, actor, name, description string) (CredentialView, string, error) {
	token := auth.RandomToken("umb_", generatedTokenBytes)
	v, err := s.Create(ctx, actor, CredentialInput{Name: name, Type: flow.CredBearer, Description: description, Secrets: map[string]string{"token": token}})
	if err != nil {
		return CredentialView{}, "", err
	}
	return v, token, nil
}

type tokenInput struct {
	Name string `json:"name"`
}

type tokenCreated struct {
	Credential credentialChoice `json:"credential"`
	Token      string           `json:"token"`
}

// createConnectorToken is «Create token» in the credential slot of a connector: a Bearer
// credential for the source to send with, made without leaving the connector dialog.
func (a *App) createConnectorToken(w http.ResponseWriter, r *http.Request) {
	var in tokenInput
	if r.ContentLength != 0 && !httpx.Decode(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "Token"
	}
	v, token, err := a.creds.GenerateBearer(r.Context(), current(r).user.Username, name, "")
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, tokenCreated{Credential: credentialChoice{ID: v.ID, Name: v.Name, Type: v.Type}, Token: token})
}
