// Package mock generates synthetic per-link telemetry directly into the
// dashboard Store, standing in for the Prometheus scraper when no live
// testbed is reachable (fabricd -config ... with mock=true). Every graph
// link gets: an RTT that random-walks around a 2-5ms per-link baseline;
// ambient traffic that random-walks in 0.3-2 Mbit/s each direction; a single
// "burst path" carrying an extra 20-40 Mbit/s that wanders across the link
// list over time; up=1 health, except link "150-154" which is pinned down
// (mirroring the real testbed's underlay-subnet-mismatch bug -- see
// CLAUDE.md's Known issues); and _up=1 service-health gauges for every AS's
// br/cs/sd targets.
//
// Byte counters (output_bytes/input_bytes) are Prometheus-style COUNTERs:
// the Store computes rates from consecutive deltas, so the generator keeps a
// running cumulative total per endpoint and Puts that total, never a rate.
//
// Any link can also be interactively shaped via SetShaping (normally reached
// through the Controller in controller.go, which the dashboard's shaping API
// drives in mock mode): a shaped link's RTT tracks baseline+delay+jitter
// instead of freely random-walking, its wire loss is synthesized by making
// the receiving side's counter advance slower than the sending side's, and
// its traffic is capped at rate_mbit -- so the demo's shaping controls work
// fully without a live testbed. A down link (150-154) ignores shaping.
package mock

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/derive"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/store"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// downLinkID is pinned down in every mock run, mirroring the real testbed's
// 150<->154 underlay-subnet mismatch (CLAUDE.md Known issues).
const downLinkID = "150-154"

// Sane bounds the generated values are walked within/clamped to.
const (
	rttMinMs = 0.5
	rttMaxMs = 500

	rttBaselineMin = 2.0
	rttBaselineMax = 5.0

	ambientMbitMin = 0.3
	ambientMbitMax = 2.0

	burstMbitMin = 20.0
	burstMbitMax = 40.0

	beaconsMinPerSec = 1.0
	beaconsMaxPerSec = 3.0

	// tickSeconds is the elapsed time each Step call is assumed to represent,
	// used to convert an Mbit/s rate into a byte-counter delta. Run's ticker
	// fires every second; Step does not derive the interval from `now` so
	// tests can call it back-to-back without needing real elapsed time.
	tickSeconds = 1.0

	// burstMoveProb is the per-step chance the burst path shifts to an
	// adjacent link index, making it wander slowly across the topology
	// rather than teleporting or staying fixed.
	burstMoveProb = 0.05

	// rttShapedNoiseMs is the small symmetric noise applied on top of a
	// shaped link's baseline+delay+jitter RTT, so a shaped sample still
	// wobbles slightly instead of reading as a dead-flat line.
	rttShapedNoiseMs = 0.15
)

// linkGen holds one link's random-walk state.
type linkGen struct {
	link topo.Link
	down bool

	rttBaselineA, rttBaselineB float64
	rttA, rttB                 float64

	ambientAB, ambientBA float64 // Mbit/s ambient traffic, A->B and B->A

	cumOutA, cumInA float64 // A's output_bytes / input_bytes counters
	cumOutB, cumInB float64 // B's output_bytes / input_bytes counters

	beaconA, beaconB float64 // A's/B's cs/beacons_recv counters
}

// Generator produces synthetic samples for a fixed graph into a Store. Build
// one with New (seeded, for deterministic tests) and either call Step
// directly or call Run to drive it off a 1s ticker.
//
// shaped/shapedMu let a mock Controller (internal/mock/controller.go) apply
// interactive shaping from HTTP handler goroutines while Step runs on Run's
// ticker goroutine: every access to shaped goes through shapedMu.
type Generator struct {
	g   topo.Graph
	st  *store.Store
	rng *rand.Rand

	links     []*linkGen
	burstIdx  int
	burstMbit float64 // current magnitude of the wandering burst path

	shapedMu sync.Mutex
	shaped   map[string]*derive.Shaping // link ID -> currently-applied shaping
}

// New builds a Generator for graph g writing into st, seeded deterministically
// from seed so tests can reproduce a run exactly.
func New(g topo.Graph, st *store.Store, seed int64) *Generator {
	rng := rand.New(rand.NewSource(seed))
	links := make([]*linkGen, len(g.Links))
	for i, l := range g.Links {
		baseA := rttBaselineMin + rng.Float64()*(rttBaselineMax-rttBaselineMin)
		baseB := rttBaselineMin + rng.Float64()*(rttBaselineMax-rttBaselineMin)
		links[i] = &linkGen{
			link:         l,
			down:         l.ID == downLinkID,
			rttBaselineA: baseA,
			rttBaselineB: baseB,
			rttA:         baseA,
			rttB:         baseB,
			ambientAB:    ambientMbitMin + rng.Float64()*(ambientMbitMax-ambientMbitMin),
			ambientBA:    ambientMbitMin + rng.Float64()*(ambientMbitMax-ambientMbitMin),
		}
	}
	burstIdx := 0
	if len(links) > 0 {
		burstIdx = rng.Intn(len(links))
	}
	return &Generator{
		g:         g,
		st:        st,
		rng:       rng,
		links:     links,
		burstIdx:  burstIdx,
		burstMbit: burstMbitMin + rng.Float64()*(burstMbitMax-burstMbitMin),
		shaped:    make(map[string]*derive.Shaping),
	}
}

// SetShaping sets the shaping applied to linkID's synthetic RTT/loss/traffic
// from the next Step onward, or clears it when p is nil. Safe to call
// concurrently with Step/Run: it is the method a mock Controller calls from
// an HTTP handler goroutine while Step runs on Run's ticker goroutine.
func (gen *Generator) SetShaping(linkID string, p *derive.Shaping) {
	gen.shapedMu.Lock()
	defer gen.shapedMu.Unlock()
	if p == nil {
		delete(gen.shaped, linkID)
		return
	}
	gen.shaped[linkID] = p
}

// CurrentShaping returns a copy of the linkID -> Shaping snapshot currently
// applied, safe for the caller to retain and read without racing Step.
func (gen *Generator) CurrentShaping() map[string]*derive.Shaping {
	gen.shapedMu.Lock()
	defer gen.shapedMu.Unlock()
	out := make(map[string]*derive.Shaping, len(gen.shaped))
	for k, v := range gen.shaped {
		out[k] = v
	}
	return out
}

// shapingFor returns the currently-applied shaping for linkID, or nil.
func (gen *Generator) shapingFor(linkID string) *derive.Shaping {
	gen.shapedMu.Lock()
	defer gen.shapedMu.Unlock()
	return gen.shaped[linkID]
}

// Step writes one synthetic sample set for every link (RTT, byte counters,
// up) and every AS's service-health gauges at timestamp now (unix millis),
// then advances the random-walk state for the next call.
func (gen *Generator) Step(now int64) {
	gen.maybeMoveBurst()
	gen.burstMbit = clamp(walk(gen.rng, gen.burstMbit, (burstMbitMin+burstMbitMax)/2, 3, 0.15), burstMbitMin, burstMbitMax)

	for i, lg := range gen.links {
		gen.stepLink(lg, i == gen.burstIdx, now)
	}
	for _, as := range gen.g.ASes {
		for _, svc := range []string{"br", "cs", "sd"} {
			gen.st.Put(healthKey(as.Num, svc), now, 1)
		}
	}
}

// Run drives Step off a 1-second ticker until ctx is cancelled, sampling
// immediately on entry (matching scrape.Scraper.Run's cadence).
func (gen *Generator) Run(ctx context.Context) {
	gen.Step(time.Now().UnixMilli())
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			gen.Step(t.UnixMilli())
		}
	}
}

// Run is the package-level entry point main wires up in mock mode: it builds
// a time-seeded Generator and runs it until ctx is done, in place of
// starting the Prometheus scraper.
func Run(ctx context.Context, g topo.Graph, st *store.Store) {
	New(g, st, time.Now().UnixNano()).Run(ctx)
}

// maybeMoveBurst lets the burst path drift to an adjacent link index with
// small probability each step, so the "hot" link wanders slowly across the
// topology rather than teleporting or staying fixed. Down links are skipped
// since traffic on them is always forced to zero, so a burst parked there
// would be invisible; if every link is down, the burst simply stays put.
func (gen *Generator) maybeMoveBurst() {
	n := len(gen.links)
	if n < 2 {
		return
	}
	if gen.rng.Float64() >= burstMoveProb {
		return
	}
	dir := 1
	if gen.rng.Intn(2) != 0 {
		dir = -1
	}
	idx := gen.burstIdx
	for i := 0; i < n; i++ {
		idx = (idx + dir + n) % n
		if !gen.links[idx].down {
			gen.burstIdx = idx
			return
		}
	}
	// every link is down: nothing to move to, leave burstIdx unchanged.
}

// stepLink advances one link's RTT/traffic random-walk state and writes its
// samples. burst is true when this link currently carries the wandering
// burst path. A down link ignores any shaping applied to it: its RTT still
// random-walks around baseline and its traffic is forced to zero regardless,
// matching CLAUDE.md's "no particles on a down link" behavior.
func (gen *Generator) stepLink(lg *linkGen, burst bool, now int64) {
	var p *derive.Shaping
	if !lg.down {
		p = gen.shapingFor(lg.link.ID)
	}

	if p != nil {
		lg.rttA = clamp(shapedRTT(gen.rng, lg.rttBaselineA, p), rttMinMs, rttMaxMs)
		lg.rttB = clamp(shapedRTT(gen.rng, lg.rttBaselineB, p), rttMinMs, rttMaxMs)
	} else {
		lg.rttA = clamp(walk(gen.rng, lg.rttA, lg.rttBaselineA, 0.25, 0.2), rttMinMs, rttMaxMs)
		lg.rttB = clamp(walk(gen.rng, lg.rttB, lg.rttBaselineB, 0.25, 0.2), rttMinMs, rttMaxMs)
	}

	ambientMid := (ambientMbitMin + ambientMbitMax) / 2
	lg.ambientAB = clamp(walk(gen.rng, lg.ambientAB, ambientMid, 0.15, 0.1), ambientMbitMin, ambientMbitMax)
	lg.ambientBA = clamp(walk(gen.rng, lg.ambientBA, ambientMid, 0.15, 0.1), ambientMbitMin, ambientMbitMax)

	mbitAB, mbitBA := lg.ambientAB, lg.ambientBA
	if burst {
		mbitAB += gen.burstMbit
		mbitBA += gen.burstMbit
	}
	if p != nil && p.RateMbit != nil {
		if mbitAB > *p.RateMbit {
			mbitAB = *p.RateMbit
		}
		if mbitBA > *p.RateMbit {
			mbitBA = *p.RateMbit
		}
	}

	// A down link carries no traffic: force both directions to zero before
	// accumulating so the cumulative counters freeze (still monotonic
	// non-decreasing) and store.Rate reports 0, matching up=0 -- no
	// particles on a down link in the dashboard.
	beaconRateA, beaconRateB := 0.0, 0.0
	if lg.down {
		mbitAB, mbitBA = 0, 0
	} else {
		beaconRateA = beaconsMinPerSec + gen.rng.Float64()*(beaconsMaxPerSec-beaconsMinPerSec)
		beaconRateB = beaconsMinPerSec + gen.rng.Float64()*(beaconsMaxPerSec-beaconsMinPerSec)
	}

	// Wire-loss synthesis: the offered rate is what leaves the sending side
	// (still credited to the sender's output_bytes in full), but only
	// rate*(1-loss/100) of it arrives at the receiver's input_bytes, so
	// derive's lossEstimate (out-in)/out recovers loss_pct from the two
	// counters. Unshaped links keep the previous 1:1 in/out (no loss).
	lossFactor := 1.0
	if p != nil && p.LossPct != nil {
		lossFactor = 1 - *p.LossPct/100
		if lossFactor < 0 {
			lossFactor = 0
		}
	}

	lg.cumOutA += mbitToBytes(mbitAB)
	lg.cumInB += mbitToBytes(mbitAB * lossFactor)
	lg.cumOutB += mbitToBytes(mbitBA)
	lg.cumInA += mbitToBytes(mbitBA * lossFactor)

	lg.beaconA += beaconRateA * tickSeconds
	lg.beaconB += beaconRateB * tickSeconds

	up := 1.0
	if lg.down {
		up = 0
	}

	a, b := lg.link.A, lg.link.B
	gen.st.Put(rttKey(a), now, lg.rttA)
	gen.st.Put(rttKey(b), now, lg.rttB)
	gen.st.Put(outKey(a), now, lg.cumOutA)
	gen.st.Put(inKey(a), now, lg.cumInA)
	gen.st.Put(outKey(b), now, lg.cumOutB)
	gen.st.Put(inKey(b), now, lg.cumInB)
	gen.st.Put(upKey(a), now, up)
	gen.st.Put(upKey(b), now, up)
	gen.st.Put(beaconsKey(a), now, lg.beaconA)
	gen.st.Put(beaconsKey(b), now, lg.beaconB)
}

// walk performs one step of a mean-reverting random walk: cur moves by a
// random step of size up to noise, then reverts a revert fraction of its
// remaining distance from target. Callers clamp the result into a sane
// range.
func walk(rng *rand.Rand, cur, target, noise, revert float64) float64 {
	cur += (rng.Float64()*2 - 1) * noise
	cur += (target - cur) * revert
	return cur
}

// shapedRTT computes one side's RTT sample while a link is shaped: the
// per-side baseline plus the configured delay, a jitter term drawn uniformly
// from [-jitter/2, +jitter/2), and a small independent noise term so the
// sample still wobbles slightly rather than reading as a dead-flat line.
// Callers clamp the result into [rttMinMs, rttMaxMs].
func shapedRTT(rng *rand.Rand, baseline float64, p *derive.Shaping) float64 {
	v := baseline
	if p.DelayMs != nil {
		v += *p.DelayMs
	}
	if p.JitterMs != nil {
		v += *p.JitterMs * (rng.Float64() - 0.5)
	}
	v += (rng.Float64()*2 - 1) * rttShapedNoiseMs
	return v
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// mbitToBytes converts a Mbit/s rate over one tick into a byte delta.
func mbitToBytes(mbit float64) float64 {
	return mbit * 1e6 / 8 * tickSeconds
}

// Store key helpers, mirroring the "<as>/br/<metric>/<ifid>" and
// "<as>/<svc>/_up/" scheme used by internal/scrape and internal/derive.
func rttKey(e topo.Endpoint) string       { return fmt.Sprintf("%d/br/rtt/%s", e.AS, e.IfID) }
func outKey(e topo.Endpoint) string       { return fmt.Sprintf("%d/br/output_bytes/%s", e.AS, e.IfID) }
func inKey(e topo.Endpoint) string        { return fmt.Sprintf("%d/br/input_bytes/%s", e.AS, e.IfID) }
func upKey(e topo.Endpoint) string        { return fmt.Sprintf("%d/br/up/%s", e.AS, e.IfID) }
func healthKey(as int, svc string) string { return fmt.Sprintf("%d/%s/_up/", as, svc) }
func beaconsKey(e topo.Endpoint) string   { return fmt.Sprintf("%d/cs/beacons_recv/%s", e.AS, e.IfID) }
