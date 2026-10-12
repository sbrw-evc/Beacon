package setup

import (
	"context"
	"net/http"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/config"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/directory"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/httpx"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
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

func (m *Module) testLDAP(w http.ResponseWriter, r *http.Request) {
	var in directory.TestRequest
	if !httpx.Decode(w, r, &in) {
		return
	}
	httpx.JSON(w, http.StatusOK, directory.Report(directory.Test(in.Config, in.BindPassword, in.TestUsername, in.TestPassword)))
}
