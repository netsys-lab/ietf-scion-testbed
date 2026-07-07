// TraceProber synthesizes idint-probed responses from the topology graph so
// the path-inspector UI is fully drivable in mock mode. Paths are simple
// shortest routes on the link graph; per-hop RTT derives from a stable
// per-link base (2-8 ms, hashed from the link ID) plus the generator's
// currently-applied shaping delay, so "shape a link -> watch the hop spike"
// works offline.
package mock

import (
	"context"
	"fmt"
	"hash/fnv"
	"strconv"
	"time"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/idint"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// maxTracePaths/maxTraceDepth bound the simple-path search in candidates: up
// to 3 shortest paths, each at most 6 links long -- generous for this
// testbed's small (12-AS) graph and enough to keep worst-case enumeration
// cheap.
const (
	maxTracePaths = 3
	maxTraceDepth = 6
)

// traceIntraASUs is the synthetic forwarding delay charged for the
// ingress-interface-to-egress-interface gap within one AS, as opposed to the
// wire-propagation delay (linkBaseUs) charged for crossing a link.
const traceIntraASUs = 100

// traceRttFixedMs is a small fixed overhead folded into ProbeRttMs on top of
// the summed per-hop RTTs, standing in for reflector/serialization time a
// real idint-probed round trip would also pay.
const traceRttFixedMs = 1.0

// traceEgrLinkTxPct is the fixed synthetic egress-link utilization every
// egress HopRecord reports; the mock has no real per-BR interface counter to
// draw from.
const traceEgrLinkTxPct = 0.4

// edge is one directed traversal of a topology link: from the AS being left
// (using its ifid on this link) to the AS being entered.
type edge struct {
	link topo.Link
	from topo.Endpoint
	to   topo.Endpoint
}

// tracePath is one synthesized path candidate: its wire-shape PathJSON plus
// the ordered edges it traverses, kept alongside so Probe can recompute
// per-link RTTs against the *current* shaping without re-parsing Interfaces.
type tracePath struct {
	json  idint.PathJSON
	edges []edge
}

// TraceProber implements idint.Prober purely from the topology graph and a
// mock Generator's shaping state -- it never dials out, so mock mode never
// depends on real idint-probed sidecars being reachable.
type TraceProber struct {
	gen     *Generator
	iaByNum map[int]string
	adj     map[int][]edge
}

// NewTraceProber builds a TraceProber for graph g, reading gen.CurrentShaping
// on every Probe call so shaping a link is immediately visible on the next
// trace tick.
func NewTraceProber(g topo.Graph, gen *Generator) idint.Prober {
	tp := &TraceProber{
		gen:     gen,
		iaByNum: make(map[int]string, len(g.ASes)),
		adj:     make(map[int][]edge, len(g.ASes)),
	}
	for _, as := range g.ASes {
		tp.iaByNum[as.Num] = as.IA
	}
	for _, l := range g.Links {
		tp.adj[l.A.AS] = append(tp.adj[l.A.AS], edge{link: l, from: l.A, to: l.B})
		tp.adj[l.B.AS] = append(tp.adj[l.B.AS], edge{link: l, from: l.B, to: l.A})
	}
	return tp
}

// candidates enumerates up to maxTracePaths shortest simple src->dst paths
// via a plain iterative BFS over partial paths (FIFO queue, so complete
// paths pop off in non-decreasing length order), capped at maxTraceDepth
// links.
func (tp *TraceProber) candidates(src, dst int) []tracePath {
	type partial struct {
		as    int
		edges []edge
	}
	queue := []partial{{as: src}}
	var found []tracePath

	for len(queue) > 0 && len(found) < maxTracePaths {
		cur := queue[0]
		queue = queue[1:]

		if cur.as == dst && len(cur.edges) > 0 {
			found = append(found, tp.buildPath(len(found), cur.edges))
			continue
		}
		if len(cur.edges) >= maxTraceDepth {
			continue
		}

		visited := map[int]bool{src: true}
		for _, e := range cur.edges {
			visited[e.to.AS] = true
		}
		for _, e := range tp.adj[cur.as] {
			if visited[e.to.AS] {
				continue
			}
			nedges := append(append([]edge{}, cur.edges...), e)
			queue = append(queue, partial{as: e.to.AS, edges: nedges})
		}
	}
	return found
}

// buildPath synthesizes candidate i's PathJSON from its traversed edges: the
// flattened Interfaces list (departure ifid, arrival ifid per edge) and one
// LatencyUs entry per metadata gap -- the link's base RTT for the inter-AS
// gap within an edge, traceIntraASUs for the intra-AS gap between one edge's
// arrival AS and the next edge's departure AS.
func (tp *TraceProber) buildPath(i int, edges []edge) tracePath {
	ifaces := make([]idint.IfaceJSON, 0, 2*len(edges))
	lat := make([]int64, 0, 2*len(edges)-1)
	for idx, e := range edges {
		fromIfid, _ := strconv.ParseUint(e.from.IfID, 10, 64)
		toIfid, _ := strconv.ParseUint(e.to.IfID, 10, 64)
		ifaces = append(ifaces,
			idint.IfaceJSON{IA: tp.iaByNum[e.from.AS], IfID: fromIfid},
			idint.IfaceJSON{IA: tp.iaByNum[e.to.AS], IfID: toIfid},
		)
		lat = append(lat, linkBaseUs(e.link.ID))
		if idx < len(edges)-1 {
			lat = append(lat, traceIntraASUs)
		}
	}
	return tracePath{
		edges: edges,
		json: idint.PathJSON{
			Fingerprint: fmt.Sprintf("mock-%d", i),
			MTU:         1472,
			Expiry:      time.Now().Add(6 * time.Hour).UTC().Format(time.RFC3339),
			Interfaces:  ifaces,
			LatencyUs:   lat,
		},
	}
}

// Paths implements idint.Prober.
func (tp *TraceProber) Paths(ctx context.Context, src, dst int) (*idint.PathsResponse, error) {
	cands := tp.candidates(src, dst)
	paths := make([]idint.PathJSON, len(cands))
	for i, c := range cands {
		paths[i] = c.json
	}
	return &idint.PathsResponse{LocalIA: tp.iaByNum[src], Paths: paths}, nil
}

// Probe implements idint.Prober: it re-derives the same candidate paths
// Paths would (recomputing rather than caching keeps this consistent with a
// real prober's statelessness across separate calls), selects the one
// matching fingerprint ("" = the first/best, unknown = error), and
// synthesizes its forward/reverse ID-INT hop records from each traversed
// link's base RTT plus whatever shaping is currently applied to it.
func (tp *TraceProber) Probe(ctx context.Context, src, dst int, fingerprint string) (*idint.ProbeResult, error) {
	cands := tp.candidates(src, dst)
	if len(cands) == 0 {
		return nil, fmt.Errorf("mock trace: no path %d->%d", src, dst)
	}
	c := cands[0]
	if fingerprint != "" {
		found := false
		for _, cc := range cands {
			if cc.json.Fingerprint == fingerprint {
				c, found = cc, true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("fingerprint not found")
		}
	}

	shaping := tp.gen.CurrentShaping()
	srcIA, dstIA := tp.iaByNum[src], tp.iaByNum[dst]

	fwd := make([]idint.HopRecord, 0, len(c.edges)+2)
	fwd = append(fwd, idint.HopRecord{Hop: 0, IA: srcIA, Source: true, Verified: true})

	var rttSumUs int64
	for i, e := range c.edges {
		rtt := linkBaseUs(e.link.ID)
		if s, ok := shaping[e.link.ID]; ok && s.DelayMs != nil {
			rtt += int64(*s.DelayMs * 1000)
		}
		rttSumUs += rtt
		fwd = append(fwd, idint.HopRecord{
			Hop:          i + 1,
			IA:           tp.iaByNum[e.to.AS],
			Egress:       true,
			Verified:     true,
			NodeId:       u32p(1),
			RttNextBrUs:  i64p(rtt),
			EgrLinkTxPct: f64p(traceEgrLinkTxPct),
			QueueLen:     i64p(0),
		})
	}
	fwd = append(fwd, idint.HopRecord{Hop: len(c.edges) + 1, IA: dstIA, Ingress: true, Verified: true})

	rev := make([]idint.HopRecord, len(fwd))
	for i, r := range fwd {
		rev[len(fwd)-1-i] = r
	}

	return &idint.ProbeResult{
		Path:       c.json,
		ProbeRttMs: float64(rttSumUs)/1000 + traceRttFixedMs,
		Fwd:        fwd,
		Rev:        rev,
	}, nil
}

// linkBaseUs is the stable (no math/rand) per-link base RTT, 2-8ms hashed
// from the link ID, so tests and demos are deterministic and a given link's
// unshaped RTT never changes between mock runs.
func linkBaseUs(linkID string) int64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(linkID))
	return 2000 + int64(h.Sum32()%6000)
}

// u32p/i64p/f64p are tiny pointer-to-literal helpers for HopRecord's
// optional fields.
func u32p(v uint32) *uint32   { return &v }
func i64p(v int64) *int64     { return &v }
func f64p(v float64) *float64 { return &v }
