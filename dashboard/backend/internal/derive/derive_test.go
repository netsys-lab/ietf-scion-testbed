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
			Subnet: "fade:1",
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
	// band commits to stale and the Stale flag is set.
	var l LinkVM
	for k := 0; k < 2; k++ {
		ti += 1000
		putRTT(st, ti, 2, 2)
		putBytes(st, ti, 4+k, 1*mbit, 1*mbit)
		st.Put(sfmt(150, "br", "_up", ""), ti, 1)
		st.Put(sfmt(151, "br", "_up", ""), ti, 0)
		l = d.Frame(ti).Links[0]
	}
	if l.Band != "stale" {
		t.Fatalf("band = %q, want stale", l.Band)
	}
	if !l.Stale {
		t.Fatalf("stale = false, want true")
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
