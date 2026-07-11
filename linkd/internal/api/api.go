// Package api serves the scion-linkd REST control interface.
package api

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"

	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/bgpstatus"
	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/shape"
	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/topo"
)

type ManagedIface struct {
	topo.Interface
	Dev string
}

// Options carries the optional metadata-sync collaborators.
type Options struct {
	Baseline    map[string]shape.Params // ifid -> story profile
	OnChange    func()                  // called after successful Apply/Clear
	Status      func() (metadataOK, reloadOK bool)
	BGPSessions func() ([]bgpstatus.Session, error)
}

type linkJSON struct {
	IfID     string        `json:"ifid"`
	Neighbor string        `json:"neighbor_isd_as"`
	LinkTo   string        `json:"link_to"`
	Device   string        `json:"device"`
	Shaping  *shape.Params `json:"shaping"`
	Baseline *shape.Params `json:"baseline,omitempty"`
	Shaped   bool          `json:"shaped"`
}

type server struct {
	ifaces      map[string]ManagedIface
	order       []string
	shaper      shape.Shaper
	baseline    map[string]shape.Params
	onChange    func()
	status      func() (bool, bool)
	bgpSessions func() ([]bgpstatus.Session, error)
}

func New(ifaces []ManagedIface, s shape.Shaper, opts Options) http.Handler {
	sv := &server{
		ifaces:      map[string]ManagedIface{},
		shaper:      s,
		baseline:    opts.Baseline,
		onChange:    opts.OnChange,
		status:      opts.Status,
		bgpSessions: opts.BGPSessions,
	}
	for _, i := range ifaces {
		sv.ifaces[i.IfID] = i
		sv.order = append(sv.order, i.IfID)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/links", sv.list)
	mux.HandleFunc("PUT /api/v1/links/{ifid}", sv.put)
	mux.HandleFunc("DELETE /api/v1/links/{ifid}", sv.del)
	mux.HandleFunc("GET /healthz", sv.health)
	mux.HandleFunc("GET /api/v1/bgp", sv.bgp)
	return mux
}

func (s *server) changed() {
	if s.onChange != nil {
		s.onChange()
	}
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
		var cur shape.Params
		if p, err := s.shaper.Get(m.Dev); err == nil {
			cur = p
			if !p.Empty() {
				lj.Shaping = &p
			}
		}
		if b, ok := s.baseline[m.IfID]; ok {
			b := b
			lj.Baseline = &b
			lj.Shaped = !paramsEqual(cur, b)
		} else {
			lj.Shaped = !cur.Empty()
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
	s.changed()
	writeJSON(w, http.StatusOK, merged)
}

func (s *server) del(w http.ResponseWriter, r *http.Request) {
	m, ok := s.lookup(w, r)
	if !ok {
		return
	}
	if base, ok := s.baseline[m.IfID]; ok {
		if err := s.shaper.Apply(m.Dev, base); err != nil {
			writeErr(w, http.StatusBadGateway, "restore baseline %s: %v", m.Dev, err)
			return
		}
		s.changed()
		writeJSON(w, http.StatusOK, base)
		return
	}
	if err := s.shaper.Clear(m.Dev); err != nil {
		writeErr(w, http.StatusBadGateway, "clear %s: %v", m.Dev, err)
		return
	}
	s.changed()
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{"status": "ok", "interfaces": len(s.ifaces)}
	if s.status != nil {
		m, rl := s.status()
		body["metadata_ok"] = m
		body["reload_ok"] = rl
	}
	writeJSON(w, http.StatusOK, body)
}

// bgp serves BIRD session state. BGP is an optional add-on: BIRD absent or
// birdc failing yields 503 without affecting the shaping API.
func (s *server) bgp(w http.ResponseWriter, r *http.Request) {
	if s.bgpSessions == nil {
		writeErr(w, http.StatusServiceUnavailable, "bgp unavailable: not configured")
		return
	}
	ss, err := s.bgpSessions()
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "bgp unavailable: %v", err)
		return
	}
	if ss == nil {
		ss = []bgpstatus.Session{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": ss})
}

// approxEq compares tc-derived values with kernel-rounding tolerance: netem
// stores ticks, so Get returns approximations of what was Applied.
func approxEq(a, b *float64) bool {
	av, bv := 0.0, 0.0
	if a != nil {
		av = *a
	}
	if b != nil {
		bv = *b
	}
	diff := math.Abs(av - bv)
	return diff <= 0.1 || diff <= 0.01*math.Max(math.Abs(av), math.Abs(bv))
}

func paramsEqual(a, b shape.Params) bool {
	return approxEq(a.DelayMs, b.DelayMs) && approxEq(a.JitterMs, b.JitterMs) &&
		approxEq(a.LossPct, b.LossPct) && approxEq(a.RateMbit, b.RateMbit)
}
