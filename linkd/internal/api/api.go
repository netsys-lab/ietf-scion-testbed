// Package api serves the scion-linkd REST control interface.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/shape"
	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/topo"
)

type ManagedIface struct {
	topo.Interface
	Dev string
}

type linkJSON struct {
	IfID     string        `json:"ifid"`
	Neighbor string        `json:"neighbor_isd_as"`
	LinkTo   string        `json:"link_to"`
	Device   string        `json:"device"`
	Shaping  *shape.Params `json:"shaping"`
}

type server struct {
	ifaces map[string]ManagedIface
	order  []string
	shaper shape.Shaper
}

func New(ifaces []ManagedIface, s shape.Shaper) http.Handler {
	sv := &server{ifaces: map[string]ManagedIface{}, shaper: s}
	for _, i := range ifaces {
		sv.ifaces[i.IfID] = i
		sv.order = append(sv.order, i.IfID)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/links", sv.list)
	mux.HandleFunc("PUT /api/v1/links/{ifid}", sv.put)
	mux.HandleFunc("DELETE /api/v1/links/{ifid}", sv.del)
	mux.HandleFunc("GET /healthz", sv.health)
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, format string, a ...any) {
	writeJSON(w, code, map[string]string{"error": fmt.Sprintf(format, a...)})
}

func (s *server) lookup(w http.ResponseWriter, r *http.Request) (ManagedIface, bool) {
	ifid := r.PathValue("ifid")
	m, ok := s.ifaces[ifid]
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown interface %s", ifid)
	}
	return m, ok
}

func (s *server) list(w http.ResponseWriter, r *http.Request) {
	out := []linkJSON{}
	for _, ifid := range s.order {
		m := s.ifaces[ifid]
		lj := linkJSON{IfID: m.IfID, Neighbor: m.Neighbor, LinkTo: m.LinkTo, Device: m.Dev}
		if p, err := s.shaper.Get(m.Dev); err == nil && !p.Empty() {
			lj.Shaping = &p
		}
		out = append(out, lj)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) put(w http.ResponseWriter, r *http.Request) {
	m, ok := s.lookup(w, r)
	if !ok {
		return
	}
	var upd shape.Params
	if err := json.NewDecoder(r.Body).Decode(&upd); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body: %v", err)
		return
	}
	cur, err := s.shaper.Get(m.Dev)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "read %s: %v", m.Dev, err)
		return
	}
	merged := shape.Merge(cur, upd)
	if err := shape.Validate(merged); err != nil {
		writeErr(w, http.StatusBadRequest, "%v", err)
		return
	}
	if err := s.shaper.Apply(m.Dev, merged); err != nil {
		writeErr(w, http.StatusBadGateway, "apply %s: %v", m.Dev, err)
		return
	}
	writeJSON(w, http.StatusOK, merged)
}

func (s *server) del(w http.ResponseWriter, r *http.Request) {
	m, ok := s.lookup(w, r)
	if !ok {
		return
	}
	if err := s.shaper.Clear(m.Dev); err != nil {
		writeErr(w, http.StatusBadGateway, "clear %s: %v", m.Dev, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "interfaces": len(s.ifaces)})
}
