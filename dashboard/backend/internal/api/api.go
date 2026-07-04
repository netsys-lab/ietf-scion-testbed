// Package api serves the dashboard backend's HTTP + WebSocket surface: the
// REST endpoints the frontend calls (topology, history, link shaping, health)
// and the /api/live WebSocket that streams derived frames. It owns no state
// beyond the set of live WebSocket connections; every reading endpoint is a
// thin adapter over topo/store/derive, and link-control endpoints fan out
// through a Controller (the linkd client).
//
// The JSON response shapes here are the wire protocol shared with the web
// client; keep them in sync with the frontend when changing them.
package api

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/derive"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/linkdclient"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/store"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// writeWait is the per-message WebSocket write deadline. A client that cannot
// absorb a frame within this window is dropped rather than allowed to stall
// the broadcast loop.
const writeWait = 2 * time.Second

// Controller is the subset of *linkdclient.Client the API depends on. Keeping
// it an interface lets tests inject a fake linkd without a real HTTP backend.
type Controller interface {
	Poll(ctx context.Context) map[string]*derive.Shaping
	Apply(ctx context.Context, link topo.Link, direction string, p derive.Shaping, clear bool) []linkdclient.Result
	AllHealth(ctx context.Context) map[int]bool
}

// The production Controller is the linkd client.
var _ Controller = (*linkdclient.Client)(nil)

// server is the concrete http.Handler returned by New. It also carries the
// dependencies RunBroadcast needs, reached via a type assertion on the handler.
type server struct {
	g         topo.Graph
	st        *store.Store
	d         *derive.Deriver
	lc        Controller
	linksByID map[string]topo.Link

	hub      *hub
	upgrader websocket.Upgrader
	mux      *http.ServeMux

	// lastFrame caches the most recent broadcast frame so handleLive can
	// build a WS-connect snapshot from it instead of calling d.Frame
	// directly: Deriver.Frame advances per-link hysteresis state as a side
	// effect, so calling it at connect time would inject off-cadence FSM
	// steps. Nil until the first broadcast tick runs.
	lastFrame atomic.Pointer[derive.Frame]
	// pollInFlight guards against overlapping Controller.Poll calls piling
	// up behind a slow HTTP round trip and starving frame broadcasting.
	pollInFlight atomic.Bool
}

// New builds the dashboard HTTP handler. static, when non-nil, is served at /
// with an index.html SPA fallback for unknown non-/api paths; pass nil to
// disable static serving (e.g. API-only deployments and tests).
func New(g topo.Graph, st *store.Store, d *derive.Deriver, lc Controller, static fs.FS) http.Handler {
	s := &server{
		g:         g,
		st:        st,
		d:         d,
		lc:        lc,
		linksByID: make(map[string]topo.Link, len(g.Links)),
		hub:       newHub(),
		upgrader: websocket.Upgrader{
			// The dashboard is served same-origin in production, but the dev
			// frontend runs on a different port; allow any origin for the demo.
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		mux: http.NewServeMux(),
	}
	for _, l := range g.Links {
		s.linksByID[l.ID] = l
	}

	s.mux.HandleFunc("GET /api/topology", s.handleTopology)
	s.mux.HandleFunc("GET /api/history", s.handleHistory)
	s.mux.HandleFunc("PUT /api/links/{id}/shaping", s.handleShaping)
	s.mux.HandleFunc("POST /api/links/{id}/reset", s.handleReset)
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/live", s.handleLive)
	if static != nil {
		s.mux.Handle("/", s.staticHandler(static))
	}
	return s
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// --- REST endpoints -------------------------------------------------------

func (s *server) handleTopology(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.g)
}

// handleHistory returns a key's samples over the last mins minutes (default
// 15, capped at 60) as [{"t":..,"v":..}].
func (s *server) handleHistory(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	mins := 15
	if v := r.URL.Query().Get("mins"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			mins = n
		}
	}
	if mins > 60 {
		mins = 60
	}
	sinceMs := time.Now().Add(-time.Duration(mins) * time.Minute).UnixMilli()
	samples := s.st.Series(key, sinceMs)
	if samples == nil {
		samples = []store.Sample{}
	}
	writeJSON(w, http.StatusOK, samples)
}

// shapingRequest is the PUT /shaping and POST /reset body. The embedded
// derive.Shaping promotes delay_ms/jitter_ms/loss_pct/rate_mbit inline (all
// ignored for reset, which only reads direction).
type shapingRequest struct {
	Direction string `json:"direction"`
	derive.Shaping
}

func (s *server) handleShaping(w http.ResponseWriter, r *http.Request) {
	s.applyShaping(w, r, false)
}

func (s *server) handleReset(w http.ResponseWriter, r *http.Request) {
	s.applyShaping(w, r, true)
}

// applyShaping validates the link id, body, and direction, then fans the
// request out through the Controller and writes {"results":[...]}: 502 when
// every endpoint failed, 200 otherwise.
func (s *server) applyShaping(w http.ResponseWriter, r *http.Request, clear bool) {
	link, ok := s.linksByID[r.PathValue("id")]
	if !ok {
		http.Error(w, "unknown link", http.StatusNotFound)
		return
	}
	var req shapingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if !validDirection(req.Direction) {
		http.Error(w, "invalid direction (want a_to_b, b_to_a, or both)", http.StatusBadRequest)
		return
	}

	p := req.Shaping
	if clear {
		p = derive.Shaping{}
	}
	results := s.lc.Apply(r.Context(), link, req.Direction, p, clear)
	if results == nil {
		results = []linkdclient.Result{}
	}

	status := http.StatusOK
	if allFailed(results) {
		status = http.StatusBadGateway
	}
	writeJSON(w, status, map[string]any{"results": results})
}

// handleHealth reports scrape-target liveness (from the store's "<as>/<svc>/
// _up/" gauges) and per-AS linkd reachability (from the Controller).
func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	targets := map[string]bool{}
	for _, k := range s.st.Keys("") {
		label, ok := strings.CutSuffix(k, "/_up/")
		if !ok {
			continue
		}
		if sm, ok := s.st.Last(k); ok {
			targets[label] = sm.V != 0
		}
	}
	linkd := map[string]bool{}
	for as, up := range s.lc.AllHealth(r.Context()) {
		linkd[strconv.Itoa(as)] = up
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": targets, "linkd": linkd})
}

// --- WebSocket ------------------------------------------------------------

// snapshotMsg is the first message every /api/live client receives: the full
// topology plus a fresh frame, so a newly-connected client can render without
// waiting for the next broadcast tick.
type snapshotMsg struct {
	Type     string       `json:"type"`
	Topology topo.Graph   `json:"topology"`
	Frame    derive.Frame `json:"frame"`
}

// frameMsg is a periodic derived frame pushed to every live client.
type frameMsg struct {
	Type  string       `json:"type"`
	Frame derive.Frame `json:"frame"`
}

func (s *server) handleLive(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote an error response.
	}
	client := &wsClient{conn: conn}

	// Send the snapshot before registering the client, so the connection's
	// first bytes are always the snapshot and no broadcast can interleave
	// ahead of it. Reuse the last broadcast frame rather than calling
	// d.Frame here: that call advances per-link hysteresis state, so a
	// client connect must not itself step the FSM off the broadcast cadence.
	frame := s.lastFrame.Load()
	if frame == nil {
		// No broadcast tick has run yet (RunBroadcast not started, or this
		// connect raced the very first tick); fall back to a direct call.
		f := s.d.Frame(time.Now().UnixMilli())
		frame = &f
	}
	snap := snapshotMsg{Type: "snapshot", Topology: s.g, Frame: *frame}
	if data, err := json.Marshal(snap); err != nil || client.write(data) != nil {
		conn.Close()
		return
	}

	s.hub.add(client)
	defer func() {
		s.hub.remove(client)
		conn.Close()
	}()

	// Drain incoming messages so control frames (ping/pong/close) are handled
	// and a client disconnect is detected; this server expects no client data.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

// RunBroadcast drives the /api/live fan-out until ctx is cancelled. Every
// frameInterval it computes one derive.Frame, marshals it once, and writes it
// to every connected client (dropping any that cannot keep up). Every
// pollInterval it refreshes the Deriver's shaping snapshot from the
// Controller. h must be the raw handler returned by New — passing anything
// else (e.g. a middleware-wrapped handler) panics at startup.
func RunBroadcast(ctx context.Context, h http.Handler, frameInterval, pollInterval time.Duration) {
	s, ok := h.(*server)
	if !ok {
		panic("api.RunBroadcast: handler was not created by api.New (middleware-wrapped?)")
	}
	frameTick := time.NewTicker(frameInterval)
	defer frameTick.Stop()
	pollTick := time.NewTicker(pollInterval)
	defer pollTick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-frameTick.C:
			f := s.d.Frame(time.Now().UnixMilli())
			s.lastFrame.Store(&f)
			msg := frameMsg{Type: "frame", Frame: f}
			data, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			s.hub.broadcast(data)
		case <-pollTick.C:
			if s.pollInFlight.CompareAndSwap(false, true) {
				go func() {
					defer s.pollInFlight.Store(false)
					s.d.SetShaping(s.lc.Poll(ctx))
				}()
			}
		}
	}
}

// --- static (SPA) ---------------------------------------------------------

// staticHandler serves files from static, falling back to index.html for
// unknown non-/api paths so client-side routes resolve. /api/* never falls
// back (a stray /api request 404s instead of returning the SPA shell).
func (s *server) staticHandler(static fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if f, err := static.Open(name); err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback: serve the index shell for unknown routes.
		data, err := fs.ReadFile(static, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
}

// --- WebSocket hub --------------------------------------------------------

// wsClient wraps a connection with a write mutex. gorilla permits one
// concurrent reader and one concurrent writer; the read loop is the sole
// reader, and writeMu serializes the snapshot write against broadcast writes.
type wsClient struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func (c *wsClient) write(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// hub is the mutex-guarded set of live clients.
type hub struct {
	mu    sync.Mutex
	conns map[*wsClient]struct{}
}

func newHub() *hub { return &hub{conns: make(map[*wsClient]struct{})} }

func (h *hub) add(c *wsClient) {
	h.mu.Lock()
	h.conns[c] = struct{}{}
	h.mu.Unlock()
}

func (h *hub) remove(c *wsClient) {
	h.mu.Lock()
	delete(h.conns, c)
	h.mu.Unlock()
}

// broadcast writes data to every client, dropping (and closing) any whose
// write fails or exceeds the deadline so one slow client cannot stall the rest.
func (h *hub) broadcast(data []byte) {
	h.mu.Lock()
	clients := make([]*wsClient, 0, len(h.conns))
	for c := range h.conns {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	for _, c := range clients {
		if err := c.write(data); err != nil {
			h.remove(c)
			c.conn.Close()
		}
	}
}

// --- helpers --------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func validDirection(d string) bool {
	switch d {
	case "a_to_b", "b_to_a", "both":
		return true
	default:
		return false
	}
}

// allFailed reports whether results is non-empty and every result failed.
func allFailed(results []linkdclient.Result) bool {
	if len(results) == 0 {
		return false
	}
	for _, r := range results {
		if r.OK {
			return false
		}
	}
	return true
}
