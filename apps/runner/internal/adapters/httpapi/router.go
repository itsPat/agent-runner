// Package httpapi is the inbound HTTP adapter. It translates HTTP requests
// into use-case calls on the app layer and HTTP responses out of them.
// This package is the only place JSON encoding lives for client-facing
// traffic.
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/itsPat/agent-runner/apps/runner/internal/app"
	"github.com/itsPat/agent-runner/apps/runner/internal/ports"
)

// Router holds the dependencies the handlers need. Construct it with
// NewRouter, then call Register to attach its routes to a parent mux.
type Router struct {
	runs *app.RunService
}

func NewRouter(runs *app.RunService) *Router {
	return &Router{runs: runs}
}

// Register attaches this adapter's routes to the given mux. Letting the
// adapter choose its own path patterns keeps composition simple and keeps
// main.go free of route literals.
func (rt *Router) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /runs", rt.postRun)
	mux.HandleFunc("GET /runs/{id}", rt.getRun)
}

func (rt *Router) postRun(w http.ResponseWriter, r *http.Request) {
	var req submitRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	run, err := rt.runs.SubmitGoal(r.Context(), req.Goal)
	if err != nil {
		// Crude mapping for Phase 1. Later we will have typed app-level
		// errors and a switch that maps each to a status code.
		slog.Warn("submit goal failed", "err", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, runToDTO(run))
}

func (rt *Router) getRun(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run id")
		return
	}

	detail, err := rt.runs.GetRunDetail(r.Context(), id)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeError(w, http.StatusNotFound, "run not found")
		return
	case err != nil:
		slog.Error("get run detail failed", "err", err, "run_id", id)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, runDetailToDTO(detail))
}

// writeJSON is a small helper to keep each handler focused on its intent.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("encode response", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorDTO{Error: msg})
}
