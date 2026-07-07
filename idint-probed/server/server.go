// Package server exposes the prober over HTTP for fabricd.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/netsys-lab/ietf-scion-testbed/idint-probed/prober"
	"github.com/scionproto/scion/pkg/addr"
	"github.com/scionproto/scion/pkg/snet"
)

type Server struct {
	Engine  prober.Engine
	probeMu sync.Mutex // serialize probes; fabricd is the only 1 Hz caller
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/paths", s.handlePaths)
	mux.HandleFunc("POST /api/v1/probe", s.handleProbe)
	return mux
}

func (s *Server) handlePaths(w http.ResponseWriter, r *http.Request) {
	dst, err := addr.ParseIA(r.URL.Query().Get("dst"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad dst: "+err.Error())
		return
	}
	resp, err := s.Engine.Paths(r.Context(), dst)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

type probeRequest struct {
	Remote      string `json:"remote"`
	Fingerprint string `json:"fingerprint"`
}

func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	var req probeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	remote, err := snet.ParseUDPAddr(req.Remote)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad remote: "+err.Error())
		return
	}
	if !s.probeMu.TryLock() {
		writeErr(w, http.StatusConflict, "probe in flight")
		return
	}
	defer s.probeMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	res, err := s.Engine.Probe(ctx, remote, req.Fingerprint)
	switch {
	case errors.Is(err, prober.ErrFingerprintNotFound):
		writeErr(w, http.StatusNotFound, "fingerprint not found")
	case errors.Is(err, prober.ErrTimeout):
		writeErr(w, http.StatusGatewayTimeout, "probe timeout")
	case err != nil:
		writeErr(w, http.StatusBadGateway, err.Error())
	default:
		writeJSON(w, http.StatusOK, res)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
