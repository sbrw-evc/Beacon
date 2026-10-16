package setup

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/sbrw-evc/beacon/app/backend/internal/config"
	"github.com/sbrw-evc/beacon/app/backend/internal/httpx"
	"github.com/sbrw-evc/beacon/app/backend/internal/keystone"
	"github.com/sbrw-evc/beacon/app/backend/internal/store"
)

func (m *Module) testOpenBao(w http.ResponseWriter, r *http.Request) {
	var in config.OpenBao
	if !httpx.Decode(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	_, rep := in.Check(ctx)
	httpx.JSON(w, http.StatusOK, rep)
}

type pgInput struct {
	config.PostgresTarget
	ReuseExisting bool `json:"reuse_existing"`
}

type pgReply struct {
	OK    bool          `json:"ok"`
	Probe store.PGProbe `json:"probe"`
	Error string        `json:"error,omitempty"`
}

func (m *Module) testPostgres(w http.ResponseWriter, r *http.Request) {
	var in pgInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	probe, err := store.ProbePostgres(r.Context(), in.Config())
	rep := pgReply{OK: err == nil, Probe: probe}
	if err != nil {
		rep.Error = err.Error()
	}
	httpx.JSON(w, http.StatusOK, rep)
}

type spaInput struct {
	Key string `json:"spa_connection_key"`
	// PublicURL is the address of Beacon: the redirect address is <it>/api/auth/spa/callback.
	PublicURL string `json:"public_url"`
}

type spaReply struct {
	OK          bool   `json:"ok"`
	Issuer      string `json:"issuer,omitempty"`
	Application string `json:"application,omitempty"`
	Error       string `json:"error,omitempty"`
}

// testSPA checks a connection key against Keystone: its discovery, the federation token and the
// client secret.
func (m *Module) testSPA(w http.ResponseWriter, r *http.Request) {
	var in spaInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	k, err := keystone.ParseKey(in.Key)
	if err != nil {
		httpx.JSON(w, http.StatusOK, spaReply{Error: err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	cfg, err := k.Check(ctx, strings.TrimRight(in.PublicURL, "/")+keystone.CallbackPath)
	rep := spaReply{OK: err == nil, Issuer: k.Issuer, Application: cfg.Application.Name}
	if err != nil {
		rep.Error = err.Error()
	}
	httpx.JSON(w, http.StatusOK, rep)
}
