// Package derive turns the raw scraped time series in the Store into
// per-link and per-AS view models plus network-wide KPIs. It is the state
// machine that drives the dashboard visualization: it classifies each link
// into a color band (nominal/elevated/degraded/critical/down/stale) using
// RTT-vs-baseline and wire-loss thresholds, and debounces band changes with
// 2-sample hysteresis so transient blips do not flicker the map.
//
// The JSON tags on the exported types are the wire protocol shared with the
// frontend; do not change them without updating the client.
package derive

import (
	"fmt"
	"math"
	"sync"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/store"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// Shaping mirrors linkd's shape.Params: the netem parameters currently
// applied to a link. Fields are pointers so an unset parameter is omitted
// from the wire form rather than sent as a zero value.
type Shaping struct {
	DelayMs  *float64 `json:"delay_ms,omitempty"`
	JitterMs *float64 `json:"jitter_ms,omitempty"`
	LossPct  *float64 `json:"loss_pct,omitempty"`
	RateMbit *float64 `json:"rate_mbit,omitempty"`
}

// LinkVM is the derived view of one inter-AS link. RttMsA/RttMsB are the two
// border routers' current RTT readings (they may differ); RateAB/RateBA are
// per-direction throughput in Mbit/s; LossPct is the worst-direction wire
// loss estimate; Band is the debounced state used for coloring.
type LinkVM struct {
	ID         string   `json:"id"`
	Band       string   `json:"band"` // nominal|elevated|degraded|critical|down|stale
	RttMsA     float64  `json:"rtt_ms_a"`
	RttMsB     float64  `json:"rtt_ms_b"`
	RateABMbit float64  `json:"rate_ab_mbit"`
	RateBAMbit float64  `json:"rate_ba_mbit"`
	LossPct    float64  `json:"loss_pct"`
	Up         bool     `json:"up"`
	Stale      bool     `json:"stale"`
	Shaping    *Shaping `json:"shaping,omitempty"`
	// BaselineDelayMs / BaselineRateMbit are the link's declared story shape
	// (one-way delay and rate tier), the nominal state the RTT band and the
	// shaping sliders are measured against. Omitted when linkd reports no
	// baseline for the link.
	BaselineDelayMs  *float64 `json:"baseline_delay_ms,omitempty"`
	BaselineRateMbit *float64 `json:"baseline_rate_mbit,omitempty"`
}

// ASVM is the derived per-AS view: service-health LEDs and beaconing rate.
type ASVM struct {
	IA            string  `json:"ia"`
	BRUp          bool    `json:"br_up"`
	CSUp          bool    `json:"cs_up"`
	SDUp          bool    `json:"sd_up"`
	BeaconsPerSec float64 `json:"beacons_per_sec"`
}

// KPI is the network-wide summary strip.
type KPI struct {
	LinksUp       int     `json:"links_up"`
	LinksTotal    int     `json:"links_total"`
	Shaped        int     `json:"shaped"`
	TotalMbit     float64 `json:"total_mbit"`
	AvgCoreRttMs  float64 `json:"avg_core_rtt_ms"`
	BeaconsPerSec float64 `json:"beacons_per_sec"`
}

// TraceHop is one link's worth of collapsed ID-INT telemetry for the active
// trace session: the k-th egress record on the probed path, annotated with
// the dashboard link ID it crossed. Pointer fields mirror idint.HopRecord's
// optionality (a router may omit a requested slot).
type TraceHop struct {
	IA          string   `json:"ia"`
	Link        string   `json:"link"` // dashboard link ID "150-154"
	RttNextBrUs *int64   `json:"rtt_next_br_us,omitempty"`
	EgrTxPct    *float64 `json:"egr_tx_pct,omitempty"`
	QueueLen    *int64   `json:"queue_len,omitempty"`
	NodeId      *uint32  `json:"node_id,omitempty"`
	Verified    bool     `json:"verified"`
}

// TraceVM is the shared ID-INT trace session's view model: the src/dst pair
// under inspection, the path last (successfully or not) probed, and its
// per-link telemetry. See internal/idint.Manager, which owns the one shared
// session and produces this via VM().
type TraceVM struct {
	Src         string     `json:"src"` // "1-150"
	Dst         string     `json:"dst"`
	Fingerprint string     `json:"fingerprint"` // of the path actually probed
	Auto        bool       `json:"auto"`
	PathLinks   []string   `json:"path_links"`
	Ok          bool       `json:"ok"`
	Error       string     `json:"error,omitempty"`
	UpdatedAt   int64      `json:"updated_at"` // unix ms of last probe attempt
	ProbeRttMs  float64    `json:"probe_rtt_ms"`
	Hops        []TraceHop `json:"hops"`
}

// Frame is a full snapshot: every link, every AS, and the KPIs, stamped with
// the time it was produced.
type Frame struct {
	T     int64    `json:"t"`
	Links []LinkVM `json:"links"`
	ASes  []ASVM   `json:"ases"`
	KPI   KPI      `json:"kpi"`
	// Trace is the shared ID-INT trace session's latest per-hop readings,
	// attached by the api layer when a trace is active; nil (omitted)
	// otherwise. See internal/idint.
	Trace *TraceVM `json:"trace,omitempty"`
}

// Band names, ordered by increasing severity for the RTT/loss classification.
// down and stale are health overrides that sit outside this ordering.
const (
	bandNominal  = "nominal"
	bandElevated = "elevated"
	bandDegraded = "degraded"
	bandCritical = "critical"
	bandDown     = "down"
	bandStale    = "stale"
)

// severity orders only the RTT/loss bands; worse() uses it to pick the worst
// of two sides plus the loss band.
var severity = map[string]int{
	bandNominal:  0,
	bandElevated: 1,
	bandDegraded: 2,
	bandCritical: 3,
}

const (
	// rateWindow is the number of trailing samples fed to store.Rate for
	// throughput and beacon rates. At the 1s scrape interval this is a ~5s
	// smoothing window.
	rateWindow = 5
	// baselineFloor is the minimum per-side RTT baseline (ms); it keeps the
	// ratio threshold meaningful on sub-millisecond links.
	baselineFloor = 0.5
	// lossMinMbit gates the wire-loss estimate: below this egress rate the
	// counter deltas are too small to estimate loss reliably.
	lossMinMbit = 0.1
	// hysteresis is the number of consecutive frames a new band must persist
	// before it commits, mirroring the mockup's pend/pendBand/pendCount.
	hysteresis = 2
)

// linkState holds the per-link hysteresis state across Frame calls.
type linkState struct {
	band     string // currently committed band
	pendBand string // candidate band being counted toward a commit
	pendN    int    // consecutive frames pendBand has been seen
}

// Deriver is stateful: it remembers each link's committed band and the
// pending-change counter, and the latest shaping snapshot from the linkd
// poller. Frame and SetShaping are safe for concurrent use.
type Deriver struct {
	g  topo.Graph
	st *store.Store

	mu             sync.Mutex
	state          map[string]*linkState
	shaping        map[string]*Shaping
	baselineShaping map[string]*Shaping // linkID -> declared story (baseline) shape from linkd

	// baselineMin is the running-minimum RTT ever observed per rttKey(...)
	// side, surviving store ring rollover (see baseline). It can be seeded
	// from a prior run via SeedBaselines and read back via Baselines for
	// persistence (see cmd/fabricd's baselines_path config key).
	baselineMin map[string]float64
}

// New builds a Deriver for graph g reading from store st. Every link starts
// in the nominal band; real classifications commit through hysteresis on
// subsequent Frame calls.
func New(g topo.Graph, st *store.Store) *Deriver {
	d := &Deriver{
		g:           g,
		st:          st,
		state:           make(map[string]*linkState, len(g.Links)),
		shaping:         make(map[string]*Shaping),
		baselineShaping: make(map[string]*Shaping),
		baselineMin:     make(map[string]float64),
	}
	for _, l := range g.Links {
		d.state[l.ID] = &linkState{band: bandNominal}
	}
	return d
}

// SetShaping replaces the linkID -> Shaping snapshot used to annotate links
// and count shaped links. Called by the linkd poller (Task 5).
func (d *Deriver) SetShaping(m map[string]*Shaping) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if m == nil {
		m = make(map[string]*Shaping)
	}
	d.shaping = m
}

// SetBaselineShaping replaces the linkID -> declared baseline (story) shape
// snapshot, polled from linkd alongside the current shaping. The baseline is
// a link's nominal state: deriveLink bands RTT relative to 2x the baseline
// one-way delay (round trip on a symmetric link) and surfaces the baseline
// delay/rate to the frontend so the shaping sliders can bound to it.
func (d *Deriver) SetBaselineShaping(m map[string]*Shaping) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if m == nil {
		m = make(map[string]*Shaping)
	}
	d.baselineShaping = m
}

// Frame computes the view models and KPIs for the current store contents,
// advancing each link's hysteresis state. now is used as the frame timestamp.
func (d *Deriver) Frame(now int64) Frame {
	d.mu.Lock()
	defer d.mu.Unlock()

	links := make([]LinkVM, 0, len(d.g.Links))
	var totalMbit, coreRttSum float64
	var linksUp, shaped, coreCount int

	for _, l := range d.g.Links {
		vm := d.deriveLink(l)
		links = append(links, vm)
		totalMbit += vm.RateABMbit + vm.RateBAMbit
		// A dead AS freezes its interface-up gauge, so vm.Up can stay true
		// even while vm.Stale is true. Gate both KPI contributions on
		// !vm.Stale too, or a stale link silently keeps counting as up (KPI
		// strip says N/N while the map shows it greyed out) and its frozen
		// RTT keeps polluting the core-RTT average.
		if vm.Up && !vm.Stale {
			linksUp++
		}
		if vm.Shaping != nil {
			shaped++
		}
		if l.Type == "core" && vm.Up && !vm.Stale {
			coreRttSum += (vm.RttMsA + vm.RttMsB) / 2
			coreCount++
		}
	}

	ases := make([]ASVM, 0, len(d.g.ASes))
	var beaconsTotal float64
	for _, as := range d.g.ASes {
		b := d.beaconsRecv(as.Num)
		beaconsTotal += b
		ases = append(ases, ASVM{
			IA:            as.IA,
			BRUp:          d.serviceUp(as.Num, "br"),
			CSUp:          d.serviceUp(as.Num, "cs"),
			SDUp:          d.serviceUp(as.Num, "sd"),
			BeaconsPerSec: b,
		})
	}

	avgCore := 0.0
	if coreCount > 0 {
		avgCore = coreRttSum / float64(coreCount)
	}

	return Frame{
		T:     now,
		Links: links,
		ASes:  ases,
		KPI: KPI{
			LinksUp:       linksUp,
			LinksTotal:    len(d.g.Links),
			Shaped:        shaped,
			TotalMbit:     totalMbit,
			AvgCoreRttMs:  avgCore,
			BeaconsPerSec: beaconsTotal,
		},
	}
}

// deriveLink computes one link's view model and advances its hysteresis
// state. It must be called with d.mu held.
func (d *Deriver) deriveLink(l topo.Link) LinkVM {
	rttA := d.lastVal(rttKey(l.A))
	rttB := d.lastVal(rttKey(l.B))
	baseA := d.baseline(rttKey(l.A))
	baseB := d.baseline(rttKey(l.B))

	// Prefer linkd's declared baseline (the story shape) over the observed
	// running-min: it is the true nominal and is immune to the transient low
	// RTT samples that otherwise pin the running-min low and read a link at
	// its normal shape as degraded. On a symmetric link the round-trip
	// baseline is 2x the one-way baseline delay.
	bl := d.baselineShaping[l.ID]
	var blDelay, blRate *float64
	if bl != nil {
		blDelay, blRate = bl.DelayMs, bl.RateMbit
		if bl.DelayMs != nil {
			if declared := 2 * *bl.DelayMs; declared >= baselineFloor {
				baseA, baseB = declared, declared
			}
		}
	}

	rateAB := d.rateMbit(outKey(l.A))
	rateBA := d.rateMbit(outKey(l.B))
	// Wire loss per direction: what A sent toward B minus what B received,
	// and symmetrically for B->A. Worst direction wins.
	lossAB := lossEstimate(rateAB, d.rateMbit(inKey(l.B)))
	lossBA := lossEstimate(rateBA, d.rateMbit(inKey(l.A)))
	loss := math.Max(lossAB, lossBA)

	down := d.upZero(upKey(l.A)) || d.upZero(upKey(l.B))
	stale := !d.serviceUp(l.A.AS, "br") || !d.serviceUp(l.B.AS, "br")

	raw := worse(worse(rttBand(rttA, baseA), rttBand(rttB, baseB)), lossBand(loss))
	// Health overrides the RTT/loss classification; a down link reads down
	// even at nominal RTT, and a stale link's numbers are not trustworthy.
	if down {
		raw = bandDown
	} else if stale {
		raw = bandStale
	}

	band := d.commit(l.ID, raw)

	return LinkVM{
		ID:         l.ID,
		Band:       band,
		RttMsA:     rttA,
		RttMsB:     rttB,
		RateABMbit: rateAB,
		RateBAMbit: rateBA,
		LossPct:    loss,
		Up:               !down,
		Stale:            stale,
		Shaping:          d.shaping[l.ID],
		BaselineDelayMs:  blDelay,
		BaselineRateMbit: blRate,
	}
}

// commit runs the 2-sample hysteresis for one link and returns the committed
// band. It mirrors the mockup's stepMock: a candidate band must be seen on
// two consecutive frames before it replaces the committed band. Must be
// called with d.mu held.
func (d *Deriver) commit(linkID, raw string) string {
	ls := d.state[linkID]
	if ls == nil {
		ls = &linkState{band: bandNominal}
		d.state[linkID] = ls
	}
	switch {
	case raw == ls.band:
		ls.pendN = 0
		ls.pendBand = ""
	case ls.pendBand == raw:
		ls.pendN++
	default:
		ls.pendN = 1
		ls.pendBand = raw
	}
	if ls.pendN >= hysteresis {
		ls.band = raw
	}
	return ls.band
}

// rttBand classifies one side's current RTT against its baseline. added is
// the absolute increase (ms); ratio is the multiplicative increase.
func rttBand(rtt, baseline float64) string {
	added := rtt - baseline
	ratio := 0.0
	if baseline > 0 {
		ratio = rtt / baseline
	}
	switch {
	case added >= 150 || ratio >= 25:
		return bandCritical
	case added >= 40 || ratio >= 8:
		return bandDegraded
	case added >= 8 || ratio >= 3:
		return bandElevated
	default:
		return bandNominal
	}
}

// lossBand classifies wire loss (percent). Loss never yields the elevated
// band; the smallest loss step is degraded.
func lossBand(lossPct float64) string {
	switch {
	case lossPct >= 5:
		return bandCritical
	case lossPct >= 1:
		return bandDegraded
	default:
		return bandNominal
	}
}

// lossEstimate is max(0, out-in)/out*100, gated on a minimum egress rate so
// tiny counter deltas do not produce phantom loss. out and in are Mbit/s.
func lossEstimate(outMbit, inMbit float64) float64 {
	if outMbit <= lossMinMbit {
		return 0
	}
	l := (outMbit - inMbit) / outMbit * 100
	if l < 0 {
		return 0
	}
	return l
}

// worse returns the more-severe of two RTT/loss bands.
func worse(a, b string) string {
	if severity[a] >= severity[b] {
		return a
	}
	return b
}

// lastVal returns the most recent value for key, or 0 if the key is absent.
func (d *Deriver) lastVal(key string) float64 {
	if s, ok := d.st.Last(key); ok {
		return s.V
	}
	return 0
}

// rateMbit reads a counter's per-second rate over rateWindow samples and
// converts bytes/s to Mbit/s.
func (d *Deriver) rateMbit(key string) float64 {
	return d.st.Rate(key, rateWindow) * 8 / 1e6
}

// baseline is the reference RTT (ms) the RTT bands compare against for key,
// floored at baselineFloor. It is min(ringMin, baselineMin[key]): ringMin is
// the minimum over the samples currently held in the store's ring (bounded
// by storeCapacity, ~1h in production), and baselineMin[key] is a running
// minimum that survives ring rollover, so a link held shaped for longer than
// the ring's window does not decay back toward nominal once its original
// unshaped samples age out. baselineMin can be seeded at startup from a
// prior run via SeedBaselines and persisted via Baselines (see
// cmd/fabricd's baselines_path config key). Must be called with d.mu held.
func (d *Deriver) baseline(key string) float64 {
	ringMin := math.Inf(1)
	for _, s := range d.st.Series(key, math.MinInt64) {
		// A non-positive RTT means "no BFD measurement yet" (border routers
		// report 0 until their BFD sessions converge at startup), NOT a 0ms
		// link. Letting it into the running-min collapses the baseline to
		// zero, which then flags every shaped link as degraded. Skip it.
		if s.V <= 0 {
			continue
		}
		if s.V < ringMin {
			ringMin = s.V
		}
	}
	m := ringMin
	if prev, ok := d.baselineMin[key]; ok && prev > 0 && prev < m {
		m = prev
	}
	if !math.IsInf(m, 1) {
		d.baselineMin[key] = m
	}
	if math.IsInf(m, 1) || m < baselineFloor {
		return baselineFloor
	}
	return m
}

// Baselines returns a copy of the running per-key RTT baseline minimums
// (keyed identically to baseline's argument, i.e. rttKey(endpoint)), for a
// caller to persist to disk. Safe for concurrent use.
func (d *Deriver) Baselines() map[string]float64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]float64, len(d.baselineMin))
	for k, v := range d.baselineMin {
		out[k] = v
	}
	return out
}

// SeedBaselines merges m into the running baseline minimums: each key ends
// up holding the smaller of its current value (if any) and m[key]. Intended
// to restore baselines saved by a previous run at startup, so a fabricd
// restart does not cold-start an already-shaped link's baseline back up to
// its currently-shaped (not unshaped) RTT. Safe for concurrent use.
func (d *Deriver) SeedBaselines(m map[string]float64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, v := range m {
		// Never seed a non-positive baseline: a persisted 0 (from a prior run
		// that recorded pre-BFD-convergence RTT) would pin the running-min at
		// zero and read every shaped link as degraded.
		if v <= 0 {
			continue
		}
		if cur, ok := d.baselineMin[k]; !ok || v < cur {
			d.baselineMin[k] = v
		}
	}
}

// upZero reports whether key holds a present interface-up gauge reading 0
// (an explicit link-down signal). An absent gauge is not treated as down;
// unreachable targets surface as stale instead.
func (d *Deriver) upZero(key string) bool {
	s, ok := d.st.Last(key)
	return ok && s.V == 0
}

// serviceUp reports whether the AS's service-health gauge is present and
// non-zero. An absent gauge means not up (the scraper always writes it).
func (d *Deriver) serviceUp(as int, svc string) bool {
	s, ok := d.st.Last(fmt.Sprintf("%d/%s/_up/", as, svc))
	return ok && s.V != 0
}

// beaconsRecv sums the received-beacon rates across all of an AS's control
// service interfaces.
func (d *Deriver) beaconsRecv(as int) float64 {
	var sum float64
	for _, k := range d.st.Keys(fmt.Sprintf("%d/cs/beacons_recv/", as)) {
		sum += d.st.Rate(k, rateWindow)
	}
	return sum
}

func rttKey(e topo.Endpoint) string { return fmt.Sprintf("%d/br/rtt/%s", e.AS, e.IfID) }
func outKey(e topo.Endpoint) string { return fmt.Sprintf("%d/br/output_bytes/%s", e.AS, e.IfID) }
func inKey(e topo.Endpoint) string  { return fmt.Sprintf("%d/br/input_bytes/%s", e.AS, e.IfID) }
func upKey(e topo.Endpoint) string  { return fmt.Sprintf("%d/br/up/%s", e.AS, e.IfID) }
