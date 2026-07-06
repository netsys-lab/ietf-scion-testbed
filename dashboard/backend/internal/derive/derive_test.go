package derive

import (
	"math"
	"testing"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/store"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// oneCoreLink builds a minimal Graph: a single core link 150-151, with both
// ASes marked core. A is the lower AS (150), both use interface id "1".
func oneCoreLink() topo.Graph {
	return topo.Graph{
		ASes: []topo.AS{
			{IA: "1-150", Num: 150, Core: true, MgmtIP: "10.20.3.150"},
			{IA: "1-151", Num: 151, Core: true, MgmtIP: "10.20.3.151"},
		},
		Links: []topo.Link{{
			ID:     "150-151",
			Type:   "core",
			Subnet: "link 1",
			A:      topo.Endpoint{IA: "1-150", AS: 150, IfID: "1", IP: "fd00:fade:1::150", LinkTo: "core"},
			B:      topo.Endpoint{IA: "1-151", AS: 151, IfID: "1", IP: "fd00:fade:1::151", LinkTo: "core"},
		}},
	}
}

// health writes fresh service-health gauges (_up = 1) for both ASes so links
// are not considered stale. Call at the tick timestamp.
func health(st *store.Store, t int64) {
	for _, as := range []int{150, 151} {
		st.Put(sfmt(as, "br", "_up", ""), t, 1)
		st.Put(sfmt(as, "cs", "_up", ""), t, 1)
		st.Put(sfmt(as, "sd", "_up", ""), t, 1)
	}
}

// sfmt mirrors the store key scheme "<as>/<svc>/<metric>/<ifid>".
func sfmt(as int, svc, metric, ifid string) string {
	return itoa(as) + "/" + svc + "/" + metric + "/" + ifid
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// putRTT sets the RTT (ms) gauge for both sides of link 150-151.
func putRTT(st *store.Store, t int64, aMs, bMs float64) {
	st.Put(sfmt(150, "br", "rtt", "1"), t, aMs)
	st.Put(sfmt(151, "br", "rtt", "1"), t, bMs)
}

// putBytes advances the output/input byte counters on both sides so that the
// A->B and B->A directions carry the given per-second byte rates with matching
// (lossless) ingress counters.
func putBytes(st *store.Store, t int64, seq int, outABps, outBAps float64) {
	// A egress toward B, B ingress from A (matching -> loss 0)
	st.Put(sfmt(150, "br", "output_bytes", "1"), t, outABps*float64(seq))
	st.Put(sfmt(151, "br", "input_bytes", "1"), t, outABps*float64(seq))
	// B egress toward A, A ingress from B (matching -> loss 0)
	st.Put(sfmt(151, "br", "output_bytes", "1"), t, outBAps*float64(seq))
	st.Put(sfmt(150, "br", "input_bytes", "1"), t, outBAps*float64(seq))
}

const mbit = 1e6 / 8 // bytes/sec that equals 1 Mbit/s

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestNominalSteadyState(t *testing.T) {
	st := store.New(64)
	d := New(oneCoreLink(), st)

	// Steady 2ms RTT both sides, 10 Mbit A->B and 6 Mbit B->A, matching
	// ingress counters so wire loss is zero.
	var f Frame
	for i := 0; i <= 6; i++ {
		ti := int64(i * 1000)
		putRTT(st, ti, 2, 2)
		putBytes(st, ti, i, 10*mbit, 6*mbit)
		health(st, ti)
		f = d.Frame(ti)
	}

	l := f.Links[0]
	if l.Band != "nominal" {
		t.Fatalf("band = %q, want nominal", l.Band)
	}
	if !l.Up || l.Stale {
		t.Fatalf("up=%v stale=%v, want up && !stale", l.Up, l.Stale)
	}
	if !approx(l.RttMsA, 2, 0.001) || !approx(l.RttMsB, 2, 0.001) {
		t.Fatalf("rtt a=%v b=%v, want 2/2", l.RttMsA, l.RttMsB)
	}
	if !approx(l.RateABMbit, 10, 0.05) {
		t.Fatalf("rateAB = %v Mbit, want ~10", l.RateABMbit)
	}
	if !approx(l.RateBAMbit, 6, 0.05) {
		t.Fatalf("rateBA = %v Mbit, want ~6", l.RateBAMbit)
	}
	if !approx(l.LossPct, 0, 0.001) {
		t.Fatalf("loss = %v, want 0", l.LossPct)
	}
	// KPI checks.
	if f.KPI.LinksUp != 1 || f.KPI.LinksTotal != 1 {
		t.Fatalf("KPI links up=%d total=%d", f.KPI.LinksUp, f.KPI.LinksTotal)
	}
	if !approx(f.KPI.TotalMbit, 16, 0.1) {
		t.Fatalf("KPI total = %v Mbit, want ~16", f.KPI.TotalMbit)
	}
	if !approx(f.KPI.AvgCoreRttMs, 2, 0.001) {
		t.Fatalf("KPI avg core rtt = %v, want 2", f.KPI.AvgCoreRttMs)
	}
	if len(f.ASes) != 2 || !f.ASes[0].BRUp || !f.ASes[0].CSUp || !f.ASes[0].SDUp {
		t.Fatalf("ASVM = %+v", f.ASes)
	}
}

// baselineFrames pushes n frames of nominal 2ms/2ms traffic to establish a
// per-side baseline of 2ms and a committed nominal band.
func baselineFrames(t *testing.T, d *Deriver, st *store.Store, n int) int64 {
	t.Helper()
	var ti int64
	for i := 0; i < n; i++ {
		ti = int64(i * 1000)
		putRTT(st, ti, 2, 2)
		putBytes(st, ti, i, 1*mbit, 1*mbit)
		health(st, ti)
		f := d.Frame(ti)
		if f.Links[0].Band != "nominal" {
			t.Fatalf("baseline frame %d band = %q, want nominal", i, f.Links[0].Band)
		}
	}
	return ti
}

func TestRTTJumpHysteresisToDegraded(t *testing.T) {
	st := store.New(64)
	d := New(oneCoreLink(), st)
	ti := baselineFrames(t, d, st, 4)

	// First 20ms sample: 20/2 = 10x >= 8x -> raw degraded, but hysteresis
	// holds the committed band at nominal for one frame.
	ti += 1000
	putRTT(st, ti, 20, 20)
	putBytes(st, ti, 4, 1*mbit, 1*mbit)
	health(st, ti)
	if b := d.Frame(ti).Links[0].Band; b != "nominal" {
		t.Fatalf("after 1 jump frame band = %q, want nominal (hysteresis)", b)
	}

	// Second consecutive 20ms sample commits the change.
	ti += 1000
	putRTT(st, ti, 20, 20)
	putBytes(st, ti, 5, 1*mbit, 1*mbit)
	health(st, ti)
	if b := d.Frame(ti).Links[0].Band; b != "degraded" {
		t.Fatalf("after 2 jump frames band = %q, want degraded", b)
	}
}

func TestRTTElevatedAfterTwoFrames(t *testing.T) {
	st := store.New(64)
	d := New(oneCoreLink(), st)
	ti := baselineFrames(t, d, st, 4)

	// 7ms vs 2ms baseline: added 5 (<8) but ratio 3.5 (>=3) -> elevated.
	ti += 1000
	putRTT(st, ti, 7, 7)
	putBytes(st, ti, 4, 1*mbit, 1*mbit)
	health(st, ti)
	if b := d.Frame(ti).Links[0].Band; b != "nominal" {
		t.Fatalf("after 1 elevated frame band = %q, want nominal", b)
	}

	ti += 1000
	putRTT(st, ti, 7, 7)
	putBytes(st, ti, 5, 1*mbit, 1*mbit)
	health(st, ti)
	if b := d.Frame(ti).Links[0].Band; b != "elevated" {
		t.Fatalf("after 2 elevated frames band = %q, want elevated", b)
	}
}

func TestFlappingNeverLeavesNominal(t *testing.T) {
	st := store.New(64)
	d := New(oneCoreLink(), st)

	// Alternate 7ms (raw elevated) and 2ms (raw nominal). The band never
	// accumulates 2 consecutive off-nominal samples, so it stays nominal.
	for i := 0; i < 12; i++ {
		ti := int64(i * 1000)
		rtt := 2.0
		if i%2 == 1 {
			rtt = 7.0
		}
		putRTT(st, ti, rtt, rtt)
		putBytes(st, ti, i, 1*mbit, 1*mbit)
		health(st, ti)
		if b := d.Frame(ti).Links[0].Band; b != "nominal" {
			t.Fatalf("flap frame %d (rtt %v) band = %q, want nominal", i, rtt, b)
		}
	}
}

func TestUpZeroBecomesDown(t *testing.T) {
	st := store.New(64)
	d := New(oneCoreLink(), st)
	ti := baselineFrames(t, d, st, 4)

	// Interface up gauge drops to 0 on side A. Feed two frames so the band
	// commits through hysteresis.
	var l LinkVM
	for k := 0; k < 2; k++ {
		ti += 1000
		putRTT(st, ti, 2, 2)
		putBytes(st, ti, 4+k, 1*mbit, 1*mbit)
		health(st, ti)
		st.Put(sfmt(150, "br", "up", "1"), ti, 0)
		st.Put(sfmt(151, "br", "up", "1"), ti, 1)
		l = d.Frame(ti).Links[0]
	}
	if l.Band != "down" {
		t.Fatalf("band = %q, want down", l.Band)
	}
	if l.Up {
		t.Fatalf("up = true, want false when up gauge is 0")
	}
	// Down link is excluded from links-up KPI and avg core RTT.
	f := d.Frame(ti)
	if f.KPI.LinksUp != 0 {
		t.Fatalf("KPI links up = %d, want 0", f.KPI.LinksUp)
	}
}

func TestLossEstimate(t *testing.T) {
	st := store.New(64)
	d := New(oneCoreLink(), st)

	// A egress 10 Mbit, B ingress only 9 Mbit -> ~10% wire loss A->B.
	var f Frame
	for i := 0; i <= 6; i++ {
		ti := int64(i * 1000)
		putRTT(st, ti, 2, 2)
		st.Put(sfmt(150, "br", "output_bytes", "1"), ti, 10*mbit*float64(i))
		st.Put(sfmt(151, "br", "input_bytes", "1"), ti, 9*mbit*float64(i))
		// B->A direction lossless so the worst direction is A->B.
		st.Put(sfmt(151, "br", "output_bytes", "1"), ti, 1*mbit*float64(i))
		st.Put(sfmt(150, "br", "input_bytes", "1"), ti, 1*mbit*float64(i))
		health(st, ti)
		f = d.Frame(ti)
	}
	if !approx(f.Links[0].LossPct, 10, 0.5) {
		t.Fatalf("loss = %v, want ~10", f.Links[0].LossPct)
	}
}

func TestStaleWhenHealthZero(t *testing.T) {
	st := store.New(64)
	d := New(oneCoreLink(), st)
	ti := baselineFrames(t, d, st, 4)

	// Side B's border-router scrape fails (_up = 0). After two frames the
	// band commits to stale and the Stale flag is set. Note the interface-up
	// gauge (router_interface_up) is never zeroed here -- it just freezes at
	// its last reading, which is exactly how a dead AS looks in practice:
	// vm.Up stays true while vm.Stale becomes true.
	var l LinkVM
	var f Frame
	for k := 0; k < 2; k++ {
		ti += 1000
		putRTT(st, ti, 2, 2)
		putBytes(st, ti, 4+k, 1*mbit, 1*mbit)
		st.Put(sfmt(150, "br", "_up", ""), ti, 1)
		st.Put(sfmt(151, "br", "_up", ""), ti, 0)
		f = d.Frame(ti)
		l = f.Links[0]
	}
	if l.Band != "stale" {
		t.Fatalf("band = %q, want stale", l.Band)
	}
	if !l.Stale {
		t.Fatalf("stale = false, want true")
	}
	if !l.Up {
		t.Fatalf("up = false, want true (a frozen interface-up gauge stays truthy while stale)")
	}
	// A stale link must not count as up in the KPIs, even though vm.Up is
	// still true: its numbers (including the RTT that feeds AvgCoreRttMs)
	// are not trustworthy. Without the vm.Up && !vm.Stale gate, a dead AS
	// would freeze its up gauge and the KPI strip would keep reporting
	// links_up == links_total while the map shows the link greyed out.
	if f.KPI.LinksUp != 0 {
		t.Fatalf("KPI links up = %d, want 0 (stale link must not count as up)", f.KPI.LinksUp)
	}
	if f.KPI.AvgCoreRttMs != 0 {
		t.Fatalf("KPI avg core rtt = %v, want 0 (stale core link must not contribute)", f.KPI.AvgCoreRttMs)
	}
}

// TestBaselineSurvivesRingRollover reproduces the "held-shaped link decays to
// nominal" bug: baseline() used to be a pure min-over-the-ring, so once a
// link's original low-RTT samples aged out of the ring (storeCapacity, ~1h
// in production), the baseline silently became whatever the currently-shaped
// RTT is, and the band decayed back toward nominal even though the link is
// still just as shaped as ever. The fix is a running minimum
// (d.baselineMin) that survives ring rollover.
func TestBaselineSurvivesRingRollover(t *testing.T) {
	st := store.New(5) // tiny ring so it rolls over in a handful of frames
	d := New(oneCoreLink(), st)

	// Establish a true baseline of 2ms while the ring still holds it.
	ti := baselineFrames(t, d, st, 3)

	// Push far more 60ms samples than the ring's capacity, so the original
	// 2ms samples are fully evicted from the ring. 60/2 = 30x >= the 25x
	// critical ratio threshold, so as long as the true baseline is
	// remembered the band must stay (and remain) critical.
	var f Frame
	for k := 0; k < 8; k++ {
		ti += 1000
		putRTT(st, ti, 60, 60)
		putBytes(st, ti, 3+k, 1*mbit, 1*mbit)
		health(st, ti)
		f = d.Frame(ti)
	}
	if f.Links[0].Band != bandCritical {
		t.Fatalf("band after ring rollover = %q, want %q (baseline must survive ring rollover)", f.Links[0].Band, bandCritical)
	}
}

// TestSeedBaselinesMergeMin checks SeedBaselines' merge semantics directly:
// each key ends up with the smaller of its existing value and the seeded
// one, and Baselines() returns an independent copy.
func TestSeedBaselinesMergeMin(t *testing.T) {
	st := store.New(8)
	d := New(oneCoreLink(), st)

	d.SeedBaselines(map[string]float64{"150/br/rtt/1": 5, "151/br/rtt/1": 5})
	d.SeedBaselines(map[string]float64{"150/br/rtt/1": 3, "151/br/rtt/1": 20})

	got := d.Baselines()
	if got["150/br/rtt/1"] != 3 {
		t.Fatalf("150 baseline = %v, want 3 (merge-min must take the lower of the two seeds)", got["150/br/rtt/1"])
	}
	if got["151/br/rtt/1"] != 5 {
		t.Fatalf("151 baseline = %v, want 5 (merge-min must not raise an existing lower value)", got["151/br/rtt/1"])
	}

	// Baselines() must return a copy: mutating it must not affect the Deriver.
	got["150/br/rtt/1"] = 999
	if d.Baselines()["150/br/rtt/1"] != 3 {
		t.Fatalf("Baselines() leaked its internal map")
	}
}

// TestSeededBaselineAffectsBand checks that a seeded baseline (as would be
// restored from disk at startup via cmd/fabricd's baselines_path) actually
// feeds into deriveLink's RTT classification, not just the Baselines() map.
func TestSeededBaselineAffectsBand(t *testing.T) {
	st := store.New(64)
	d := New(oneCoreLink(), st)
	// Seed a baseline lower than anything the ring will ever observe, so the
	// classification can only be explained by the seed being honored.
	d.SeedBaselines(map[string]float64{"150/br/rtt/1": baselineFloor, "151/br/rtt/1": baselineFloor})

	var f Frame
	for i := 0; i < 2; i++ {
		ti := int64(i * 1000)
		putRTT(st, ti, 2, 2)
		putBytes(st, ti, i, 1*mbit, 1*mbit)
		health(st, ti)
		f = d.Frame(ti)
	}
	if f.Links[0].Band != bandElevated {
		t.Fatalf("band = %q, want %q (seeded baseline should be honored: 2ms/0.5ms = 4x ratio)", f.Links[0].Band, bandElevated)
	}
}

func TestSetShapingCountsShaped(t *testing.T) {
	st := store.New(64)
	d := New(oneCoreLink(), st)
	ti := baselineFrames(t, d, st, 2)

	delay := 5.0
	d.SetShaping(map[string]*Shaping{"150-151": {DelayMs: &delay}})

	ti += 1000
	putRTT(st, ti, 2, 2)
	putBytes(st, ti, 2, 1*mbit, 1*mbit)
	health(st, ti)
	f := d.Frame(ti)
	if f.KPI.Shaped != 1 {
		t.Fatalf("shaped = %d, want 1", f.KPI.Shaped)
	}
	if f.Links[0].Shaping == nil || f.Links[0].Shaping.DelayMs == nil || *f.Links[0].Shaping.DelayMs != 5 {
		t.Fatalf("shaping = %+v, want delay 5", f.Links[0].Shaping)
	}
}

// TestZeroRTTDoesNotPoisonBaseline verifies that RTT=0 samples (which border
// routers emit before their BFD sessions converge at startup) are excluded
// from the running-min baseline. Otherwise the baseline collapses to zero and
// a link at its normal (shaped) latency reads as a huge regression.
func TestZeroRTTDoesNotPoisonBaseline(t *testing.T) {
	st := store.New(64)
	d := New(oneCoreLink(), st)

	// Pre-convergence: the border router reports RTT=0 for a few frames.
	var ti int64
	for i := 0; i < 4; i++ {
		ti = int64(i * 1000)
		putRTT(st, ti, 0, 0)
		putBytes(st, ti, i, 1*mbit, 1*mbit)
		health(st, ti)
		d.Frame(ti)
	}
	// BFD converged: every sample is the link's real 12ms shaped latency.
	// With zeros excluded the baseline is ~12ms, so 12ms must read nominal.
	var f Frame
	for i := 4; i < 8; i++ {
		ti = int64(i * 1000)
		putRTT(st, ti, 12, 12)
		putBytes(st, ti, i, 1*mbit, 1*mbit)
		health(st, ti)
		f = d.Frame(ti)
	}
	if b := f.Links[0].Band; b != bandNominal {
		t.Fatalf("band = %q, want nominal (0-RTT must not become the baseline)", b)
	}
}

// TestDeclaredBaselineBandsAndExposes verifies that linkd's declared baseline
// (story) shape, when present, is used as the RTT band reference (2x one-way
// delay for the round trip) and is surfaced on the LinkVM for the frontend's
// shaping-slider bounds — so a link sitting at its normal shape reads nominal
// and one shaped worse than the story reads degraded.
func TestDeclaredBaselineBandsAndExposes(t *testing.T) {
	st := store.New(64)
	d := New(oneCoreLink(), st)
	fp := func(v float64) *float64 { return &v }
	d.SetBaselineShaping(map[string]*Shaping{
		"150-151": {DelayMs: fp(6), RateMbit: fp(100)}, // RTT baseline = 12ms
	})

	var f Frame
	for i := 0; i < 3; i++ {
		ti := int64(i * 1000)
		putRTT(st, ti, 12, 12) // at the declared baseline RTT
		putBytes(st, ti, i, 1*mbit, 1*mbit)
		health(st, ti)
		f = d.Frame(ti)
	}
	l := f.Links[0]
	if l.Band != bandNominal {
		t.Fatalf("at baseline RTT band = %q, want nominal", l.Band)
	}
	if l.BaselineDelayMs == nil || *l.BaselineDelayMs != 6 {
		t.Fatalf("BaselineDelayMs = %v, want 6", l.BaselineDelayMs)
	}
	if l.BaselineRateMbit == nil || *l.BaselineRateMbit != 100 {
		t.Fatalf("BaselineRateMbit = %v, want 100", l.BaselineRateMbit)
	}

	// Shaped worse than the story (60ms vs 12ms baseline: +48ms >= 40) -> degraded.
	var ti int64
	for i := 3; i < 7; i++ {
		ti = int64(i * 1000)
		putRTT(st, ti, 60, 60)
		putBytes(st, ti, i, 1*mbit, 1*mbit)
		health(st, ti)
		f = d.Frame(ti)
	}
	if f.Links[0].Band != bandDegraded {
		t.Fatalf("shaped-worse band = %q, want degraded", f.Links[0].Band)
	}
}
