package api

// The ID-INT path-inspector endpoints. All four 404 when s.tr == nil — the
// feature reads as nonexistent while disabled, matching the join-flow
// convention (see JoinConfig's doc comment).

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/idint"
)

func (s *server) handleIdintPaths(w http.ResponseWriter, r *http.Request) {
	if s.tr == nil {
		http.NotFound(w, r)
		return
	}
	src, err1 := strconv.Atoi(r.URL.Query().Get("src"))
	dst, err2 := strconv.Atoi(r.URL.Query().Get("dst"))
	if err1 != nil || err2 != nil {
		http.Error(w, "bad src/dst", http.StatusBadRequest)
		return
	}
	opts, err := s.tr.PathOptions(r.Context(), src, dst)
	if err != nil {
		if errors.Is(err, idint.ErrBadSession) { // unknown AS / src==dst
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"src": s.tr.IA(src), "dst": s.tr.IA(dst), "paths": opts,
	})
}

type idintTraceRequest struct {
	Src         int    `json:"src"`
	Dst         int    `json:"dst"`
	Fingerprint string `json:"fingerprint"`
}

func (s *server) handleIdintTraceSet(w http.ResponseWriter, r *http.Request) {
	if s.tr == nil {
		http.NotFound(w, r)
		return
	}
	var req idintTraceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := s.tr.Set(req.Src, req.Dst, req.Fingerprint); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleIdintTraceClear(w http.ResponseWriter, r *http.Request) {
	if s.tr == nil {
		http.NotFound(w, r)
		return
	}
	s.tr.Clear()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleIdintTraceGet(w http.ResponseWriter, r *http.Request) {
	if s.tr == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vm": s.tr.VM(), "latest": s.tr.Latest()})
}
