// Package idint holds the ID-INT path-inspector's trace session manager.
// Manager owns the single shared trace session (there is one dashboard, one
// inspector at a time): it ticks a Prober against the session's src/dst AS
// pair at a fixed interval, collapses the raw per-hop ID-INT telemetry onto
// the dashboard's link IDs, and exposes the result as a derive.TraceVM ready
// to attach to the WS frame.
package idint

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/derive"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// ErrBadSession is the sentinel wrapped into Set/PathOptions validation
// errors (unknown AS number, or src == dst) so the api layer can
// errors.Is() them into 400s without string-matching.
var ErrBadSession = errors.New("bad trace session")

// Prober is the ID-INT probing backend a Manager drives: either the real
// HTTP client (NewHTTPProber) talking to idint-probed sidecars, or a mock
// (internal/mock) for offline demos.
type Prober interface {
	Paths(ctx context.Context, src, dst int) (*PathsResponse, error)
	Probe(ctx context.Context, src, dst int, fingerprint string) (*ProbeResult, error)
}

// session is the trace session under inspection: an AS pair and an optional
// pinned path fingerprint ("" means follow whatever sciond currently
// considers best, i.e. Auto).
type session struct {
	src, dst    int
	fingerprint string
}

// Manager is stateful: it holds the one shared trace session, the latest
// probe result, and the derived view model. All exported methods are safe
// for concurrent use.
type Manager struct {
	g        topo.Graph
	p        Prober
	interval time.Duration
	iaByNum  map[int]string    // 150 -> "1-150"
	linkByIf map[string]string // "150:2" -> "150-154"
	asSet    map[int]bool

	mu       sync.Mutex
	sess     *session
	vm       *derive.TraceVM
	latest   *ProbeResult
	inFlight atomic.Bool
}

// NewManager builds a Manager for graph g, polling p every interval while a
// session is set. No session is active until Set is called.
func NewManager(g topo.Graph, p Prober, interval time.Duration) *Manager {
	m := &Manager{
		g:        g,
		p:        p,
		interval: interval,
		iaByNum:  make(map[int]string, len(g.ASes)),
		linkByIf: make(map[string]string, len(g.Links)*2),
		asSet:    make(map[int]bool, len(g.ASes)),
	}
	for _, as := range g.ASes {
		m.iaByNum[as.Num] = as.IA
		m.asSet[as.Num] = true
	}
	for _, l := range g.Links {
		m.linkByIf[linkKey(l.A.AS, l.A.IfID)] = l.ID
		m.linkByIf[linkKey(l.B.AS, l.B.IfID)] = l.ID
	}
	return m
}

// linkKey is the linkByIf map key for an AS number and its (decimal,
// string-form) interface id, e.g. linkKey(150, "2") == "150:2".
func linkKey(as int, ifid string) string {
	return fmt.Sprintf("%d:%s", as, ifid)
}

// IA looks up an AS number's ISD-AS string ("1-150"), or "" if unknown.
func (m *Manager) IA(as int) string {
	return m.iaByNum[as]
}

// validate reports a wrapped ErrBadSession error if src/dst is not a
// same-graph, distinct AS pair.
func (m *Manager) validate(src, dst int) error {
	if !m.asSet[src] || !m.asSet[dst] {
		return fmt.Errorf("unknown AS %d/%d: %w", src, dst, ErrBadSession)
	}
	if src == dst {
		return fmt.Errorf("src == dst (%d): %w", src, ErrBadSession)
	}
	return nil
}

// Set starts (or replaces) the shared trace session. fingerprint == "" means
// auto-follow sciond's current best path. The view model resets to a
// pending state (no hops yet) until the first successful tick.
func (m *Manager) Set(src, dst int, fingerprint string) error {
	if err := m.validate(src, dst); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sess = &session{src: src, dst: dst, fingerprint: fingerprint}
	m.vm = &derive.TraceVM{
		Src:       m.iaByNum[src],
		Dst:       m.iaByNum[dst],
		Auto:      fingerprint == "",
		Ok:        true,
		PathLinks: []string{},
		Hops:      []derive.TraceHop{},
	}
	m.latest = nil
	return nil
}

// Clear stops the session: VM/Latest report nothing until Set is called
// again.
func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sess = nil
	m.vm = nil
	m.latest = nil
}

// VM returns a copy of the current view model, safe to hand to a concurrent
// reader (e.g. the WS broadcaster). nil when no session is active.
func (m *Manager) VM() *derive.TraceVM {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.vm == nil {
		return nil
	}
	cp := *m.vm
	cp.PathLinks = append([]string{}, m.vm.PathLinks...)
	cp.Hops = append([]derive.TraceHop{}, m.vm.Hops...)
	return &cp
}

// Latest returns a copy of the raw last probe result, or nil if none has
// landed yet (or the session was cleared).
func (m *Manager) Latest() *ProbeResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.latest == nil {
		return nil
	}
	cp := *m.latest
	cp.Path.Interfaces = append([]IfaceJSON(nil), m.latest.Path.Interfaces...)
	cp.Path.LatencyUs = append([]int64(nil), m.latest.Path.LatencyUs...)
	cp.Fwd = append([]HopRecord(nil), m.latest.Fwd...)
	cp.Rev = append([]HopRecord(nil), m.latest.Rev...)
	return &cp
}

// Run ticks the active session at m.interval until ctx is done. Call it in
// a goroutine. A tick that is still in flight when the next one fires is
// skipped (overlap guard via inFlight), not queued.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.mu.Lock()
			active := m.sess != nil
			m.mu.Unlock()
			if !active {
				continue
			}
			if !m.inFlight.CompareAndSwap(false, true) {
				continue // previous tick still running
			}
			go func() {
				defer m.inFlight.Store(false)
				m.TickOnce(ctx)
			}()
		}
	}
}

// TickOnce runs one probe cycle for the current session. Exported so tests
// can drive it deterministically instead of waiting on Run's ticker. A
// no-op if no session is set.
//
// The probe itself runs outside the lock (it is a network call); the result
// is discarded if the session changed while the probe was in flight, so a
// slow response for a stale session can never clobber a newer one.
func (m *Manager) TickOnce(ctx context.Context) {
	m.mu.Lock()
	sessPtr := m.sess
	m.mu.Unlock()
	if sessPtr == nil {
		return
	}
	sess := *sessPtr
	res, err := m.p.Probe(ctx, sess.src, sess.dst, sess.fingerprint)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sess == nil || *m.sess != sess {
		return // session changed meanwhile; this result is stale
	}

	var vm *derive.TraceVM
	if err == nil {
		vm, err = m.buildVM(sess, res)
	}
	if err != nil {
		// Keep whatever hops/path/RTT the view model already has; only flip
		// the health fields. On the very first tick m.vm always exists
		// (Set seeds a pending one), so this is never nil.
		if m.vm == nil {
			m.vm = &derive.TraceVM{
				Src:       m.iaByNum[sess.src],
				Dst:       m.iaByNum[sess.dst],
				Auto:      sess.fingerprint == "",
				PathLinks: []string{},
				Hops:      []derive.TraceHop{},
			}
		}
		m.vm.Ok = false
		m.vm.Error = err.Error()
		m.vm.UpdatedAt = time.Now().UnixMilli()
		return
	}
	m.vm = vm
	m.latest = res
}

// buildVM turns one successful ProbeResult into a fresh derive.TraceVM: the
// probed path's links, and its per-link ID-INT telemetry collapsed from the
// forward egress records — or, when the forward stack carries no usable BR
// entries, from the reverse egress records mapped back dst->src.
func (m *Manager) buildVM(sess session, res *ProbeResult) (*derive.TraceVM, error) {
	pathLinks, err := m.pairLinks(res.Path.Interfaces)
	if err != nil {
		return nil, err
	}

	fwdEgress := egressRecords(res.Fwd)
	var egress []HopRecord
	if len(fwdEgress) == len(pathLinks) {
		// k-th forward egress record crossed the k-th link.
		egress = fwdEgress
	} else {
		// The deployed fork populates only the reverse ID-INT stack: the
		// forward report carries just the source record and the per-hop
		// telemetry rides the reverse stack, dst->src (verified live
		// 2026-07-07, probe 150->161). Rev egress record k maps onto
		// pathLinks[len-1-k]; the hop keeps the record's own IA — the
		// far-side BR that actually reported it (honest attribution).
		revEgress := egressRecords(res.Rev)
		if len(revEgress) != len(pathLinks) {
			return nil, fmt.Errorf("fwd/rev egress records (%d/%d) != path links (%d)",
				len(fwdEgress), len(revEgress), len(pathLinks))
		}
		egress = make([]HopRecord, len(revEgress))
		for k := range revEgress {
			egress[k] = revEgress[len(revEgress)-1-k]
		}
	}

	hops := make([]derive.TraceHop, len(egress))
	for i, rec := range egress {
		hops[i] = derive.TraceHop{
			IA:          rec.IA,
			Link:        pathLinks[i],
			RttNextBrUs: rec.RttNextBrUs,
			EgrTxPct:    rec.EgrLinkTxPct,
			QueueLen:    rec.QueueLen,
			NodeId:      rec.NodeId,
			Verified:    rec.Verified,
		}
	}

	return &derive.TraceVM{
		Src:         m.iaByNum[sess.src],
		Dst:         m.iaByNum[sess.dst],
		Fingerprint: res.Path.Fingerprint,
		Auto:        sess.fingerprint == "",
		PathLinks:   pathLinks,
		Ok:          true,
		UpdatedAt:   time.Now().UnixMilli(),
		ProbeRttMs:  res.ProbeRttMs,
		Hops:        hops,
	}, nil
}

// egressRecords filters one direction's hop records down to its egress
// entries, preserving order.
func egressRecords(recs []HopRecord) []HopRecord {
	out := make([]HopRecord, 0, len(recs))
	for _, rec := range recs {
		if rec.Egress {
			out = append(out, rec)
		}
	}
	return out
}

// pairLinks maps a flattened path interface list onto dashboard link IDs,
// pairing entries (0,1),(2,3),.... Each pair's first element resolves the
// link via linkByIf; the second element is cross-checked to resolve to the
// same link (a mismatch is logged, not fatal — the first element wins).
func (m *Manager) pairLinks(ifaces []IfaceJSON) ([]string, error) {
	if len(ifaces)%2 != 0 {
		return nil, fmt.Errorf("odd interface count %d", len(ifaces))
	}
	links := make([]string, 0, len(ifaces)/2)
	for i := 0; i+1 < len(ifaces); i += 2 {
		a, b := ifaces[i], ifaces[i+1]
		aNum, err := asNumOf(a.IA)
		if err != nil {
			return nil, err
		}
		key := linkKey(aNum, strconv.FormatUint(a.IfID, 10))
		link, ok := m.linkByIf[key]
		if !ok {
			return nil, fmt.Errorf("interface not in topology: %s", key)
		}
		if bNum, err := asNumOf(b.IA); err == nil {
			bKey := linkKey(bNum, strconv.FormatUint(b.IfID, 10))
			if bl, ok := m.linkByIf[bKey]; ok && bl != link {
				log.Printf("idint: pair (%s,%s) resolves to different links (%s vs %s)", key, bKey, link, bl)
			}
		}
		links = append(links, link)
	}
	return links, nil
}

// PathOptions lists the src->dst paths sciond currently offers, annotated
// with dashboard hop/link IDs and a total-latency estimate, for the trace
// session picker UI.
func (m *Manager) PathOptions(ctx context.Context, src, dst int) ([]PathOption, error) {
	if err := m.validate(src, dst); err != nil {
		return nil, err
	}
	resp, err := m.p.Paths(ctx, src, dst)
	if err != nil {
		return nil, err
	}
	opts := make([]PathOption, len(resp.Paths))
	for i, p := range resp.Paths {
		links, err := m.pairLinks(p.Interfaces)
		if err != nil {
			// Best-effort: still offer the path (hops/latency are still
			// meaningful) even if a link can't be resolved.
			links = nil
		}
		opts[i] = PathOption{
			Fingerprint:    p.Fingerprint,
			Hops:           hopsOf(p.Interfaces),
			Links:          links,
			LatencyUsTotal: latencyTotal(p.LatencyUs),
			MTU:            p.MTU,
			Expiry:         p.Expiry,
			CurrentBest:    i == 0,
		}
	}
	return opts, nil
}

// hopsOf returns the AS numbers a path's interfaces traverse, deduped in
// order: the first interface's AS, then every (0,1),(2,3),... pair's second
// element's AS (the ingress AS at each subsequent hop).
func hopsOf(ifaces []IfaceJSON) []int {
	if len(ifaces) == 0 {
		return nil
	}
	hops := make([]int, 0, len(ifaces)/2+1)
	if n, err := asNumOf(ifaces[0].IA); err == nil {
		hops = append(hops, n)
	}
	for i := 1; i < len(ifaces); i += 2 {
		if n, err := asNumOf(ifaces[i].IA); err == nil {
			hops = append(hops, n)
		}
	}
	return hops
}

// latencyTotal sums per-hop latencies, or -1 if the list is empty or any
// entry is unset (-1).
func latencyTotal(us []int64) int64 {
	if len(us) == 0 {
		return -1
	}
	var total int64
	for _, v := range us {
		if v < 0 {
			return -1
		}
		total += v
	}
	return total
}

// asNumOf parses the AS number out of an ISD-AS string like "1-150".
func asNumOf(ia string) (int, error) {
	i := strings.LastIndexByte(ia, '-')
	if i < 0 {
		return 0, fmt.Errorf("malformed ia %q", ia)
	}
	return strconv.Atoi(ia[i+1:])
}
