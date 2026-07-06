package mock

import (
	"math"
	"testing"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/derive"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/store"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// f64 returns a pointer to v, for building derive.Shaping literals in tests.
func f64(v float64) *float64 { return &v }

// linkGenByID returns the Generator's internal per-link state for linkID
// (white-box: this test file is in package mock), so tests can compare
// shaped RTT samples against the exact seeded per-side baseline rather than
// re-deriving it.
func linkGenByID(gen *Generator, linkID string) *linkGen {
	for _, lg := range gen.links {
		if lg.link.ID == linkID {
			return lg
		}
	}
	return nil
}

// testGraph is a small fixture graph, independent of the topo package's own
// testdata, that includes the "150-154" link mock must pin down (mirroring
// the real testbed's underlay-subnet-mismatch bug -- see CLAUDE.md's Known
// issues) alongside a couple of healthy links.
func testGraph() topo.Graph {
	return topo.Graph{
		ASes: []topo.AS{
			{IA: "1-150", Num: 150, Core: true, MgmtIP: "10.20.3.150"},
			{IA: "1-151", Num: 151, Core: true, MgmtIP: "10.20.3.151"},
			{IA: "1-154", Num: 154, Core: false, MgmtIP: "10.20.3.154"},
			{IA: "1-155", Num: 155, Core: false, MgmtIP: "10.20.3.155"},
		},
		Links: []topo.Link{
			{
				ID: "150-151", Type: "core", Subnet: "link 1",
				A: topo.Endpoint{IA: "1-150", AS: 150, IfID: "1"},
				B: topo.Endpoint{IA: "1-151", AS: 151, IfID: "1"},
			},
			{
				ID: "150-154", Type: "child", Subnet: "link 7",
				A: topo.Endpoint{IA: "1-150", AS: 150, IfID: "2"},
				B: topo.Endpoint{IA: "1-154", AS: 154, IfID: "1"},
			},
			{
				ID: "151-155", Type: "child", Subnet: "link 9",
				A: topo.Endpoint{IA: "1-151", AS: 151, IfID: "2"},
				B: topo.Endpoint{IA: "1-155", AS: 155, IfID: "1"},
			},
		},
	}
}

// TestStepWritesEveryLinkAndHealthKey ticks a seeded Generator twice and
// checks every link's rtt/output_bytes/input_bytes/up keys on both sides
// exist, the 150-154 link (A-side, AS150) reads down, an ordinary link reads
// up, and every AS's br/cs/sd health gauge is present and 1.
func TestStepWritesEveryLinkAndHealthKey(t *testing.T) {
	g := testGraph()
	st := store.New(60)
	gen := New(g, st, 42)

	now := int64(1_000_000)
	gen.Step(now)
	gen.Step(now + 1000)

	for _, l := range g.Links {
		for _, ep := range []topo.Endpoint{l.A, l.B} {
			for _, key := range []string{rttKey(ep), outKey(ep), inKey(ep), upKey(ep)} {
				if _, ok := st.Last(key); !ok {
					t.Fatalf("link %s: missing key %s", l.ID, key)
				}
			}
		}
	}

	down, ok := st.Last(upKey(topo.Endpoint{AS: 150, IfID: "2"}))
	if !ok || down.V != 0 {
		t.Fatalf("want 150-154 A-side (AS150 ifid 2) up==0, got %+v ok=%v", down, ok)
	}
	up, ok := st.Last(upKey(topo.Endpoint{AS: 150, IfID: "1"}))
	if !ok || up.V != 1 {
		t.Fatalf("want 150-151 A-side up==1, got %+v ok=%v", up, ok)
	}

	for _, as := range g.ASes {
		for _, svc := range []string{"br", "cs", "sd"} {
			s, ok := st.Last(healthKey(as.Num, svc))
			if !ok || s.V != 1 {
				t.Fatalf("want %s up==1, got %+v ok=%v", healthKey(as.Num, svc), s, ok)
			}
		}
	}
}

// TestStepValueRangesAndMonotonicCounters checks RTT stays within the sane
// 0.5-500ms band, byte counters are never negative, and (being COUNTERs) they
// never decrease from one tick to the next.
func TestStepValueRangesAndMonotonicCounters(t *testing.T) {
	g := testGraph()
	st := store.New(60)
	gen := New(g, st, 7)

	now := int64(2_000_000)
	gen.Step(now)

	prev := map[string]float64{}
	for _, l := range g.Links {
		for _, ep := range []topo.Endpoint{l.A, l.B} {
			for _, key := range []string{outKey(ep), inKey(ep)} {
				s, _ := st.Last(key)
				prev[key] = s.V
			}
		}
	}

	gen.Step(now + 1000)

	for _, l := range g.Links {
		for _, ep := range []topo.Endpoint{l.A, l.B} {
			rtt, ok := st.Last(rttKey(ep))
			if !ok || rtt.V < 0.5 || rtt.V > 500 {
				t.Fatalf("link %s AS%d: rtt out of [0.5,500] range: %+v", l.ID, ep.AS, rtt)
			}
			for _, key := range []string{outKey(ep), inKey(ep)} {
				cur, ok := st.Last(key)
				if !ok || cur.V < 0 {
					t.Fatalf("key %s: missing or negative: %+v", key, cur)
				}
				if cur.V < prev[key] {
					t.Fatalf("key %s: counter decreased: %v -> %v", key, prev[key], cur.V)
				}
			}
			upS, ok := st.Last(upKey(ep))
			if !ok || (upS.V != 0 && upS.V != 1) {
				t.Fatalf("key %s: up not 0/1: %+v", upKey(ep), upS)
			}
		}
	}
}

// TestDownLinkTrafficFrozen ticks a seeded Generator three times and checks
// that the down 150-154 link's A-side output_bytes counter never advances
// (so store.Rate reads exactly 0, matching what the dashboard shows: no
// particles on a down link), while a healthy link's counter strictly
// increases every tick.
func TestDownLinkTrafficFrozen(t *testing.T) {
	g := testGraph()
	st := store.New(60)
	gen := New(g, st, 42)

	downEP := topo.Endpoint{AS: 150, IfID: "2"}    // 150-154 A-side
	healthyEP := topo.Endpoint{AS: 150, IfID: "1"} // 150-151 A-side

	now := int64(3_000_000)
	var downVals, healthyVals []float64
	for i := 0; i < 3; i++ {
		gen.Step(now + int64(i)*1000)

		d, ok := st.Last(outKey(downEP))
		if !ok {
			t.Fatalf("tick %d: missing down-link output_bytes", i)
		}
		downVals = append(downVals, d.V)

		h, ok := st.Last(outKey(healthyEP))
		if !ok {
			t.Fatalf("tick %d: missing healthy-link output_bytes", i)
		}
		healthyVals = append(healthyVals, h.V)
	}

	for i := 1; i < len(downVals); i++ {
		if downVals[i] != downVals[0] {
			t.Fatalf("down-link output_bytes counter changed across ticks: %v", downVals)
		}
	}
	for i := 1; i < len(healthyVals); i++ {
		if healthyVals[i] <= healthyVals[i-1] {
			t.Fatalf("healthy-link output_bytes counter did not strictly increase: %v", healthyVals)
		}
	}

	if rate := st.Rate(outKey(downEP), 10); rate != 0 {
		t.Fatalf("want down-link rate 0, got %v", rate)
	}
}

// TestShapingElevatesRTT sets 50ms delay on a healthy link, steps three
// times, and checks the stored RTT lands near baseline+50 (well above the
// nominal 2-5ms range regardless of the exact baseline). It then clears the
// shaping and steps enough times for the mean-reverting walk to bring the
// RTT back close to baseline, confirming SetShaping(id, nil) actually clears.
func TestShapingElevatesRTT(t *testing.T) {
	g := testGraph()
	st := store.New(120)
	gen := New(g, st, 11)

	const linkID = "150-151"
	aEP := topo.Endpoint{AS: 150, IfID: "1"}
	lg := linkGenByID(gen, linkID)
	if lg == nil {
		t.Fatalf("fixture missing link %s", linkID)
	}
	baseline := lg.rttBaselineA

	gen.SetShaping(linkID, &derive.Shaping{DelayMs: f64(50)})

	now := int64(5_000_000)
	for i := 0; i < 3; i++ {
		gen.Step(now + int64(i)*1000)
	}

	s, ok := st.Last(rttKey(aEP))
	if !ok {
		t.Fatalf("missing rtt for %s", linkID)
	}
	if s.V < 40 {
		t.Fatalf("shaped rtt should be well above nominal range, got %.2f", s.V)
	}
	want := baseline + 50
	if diff := math.Abs(s.V - want); diff > 3 {
		t.Fatalf("want rtt ~= baseline(%.2f)+50, got %.2f (diff %.2f)", baseline, s.V, diff)
	}

	// Clearing lets the walk revert toward baseline over a handful of steps
	// (not asserting anything about derive's bands here -- this is raw store
	// state, derive's baseline/band logic is a separate package).
	gen.SetShaping(linkID, nil)
	for i := 0; i < 30; i++ {
		gen.Step(now + int64(3+i)*1000)
	}
	s2, ok := st.Last(rttKey(aEP))
	if !ok {
		t.Fatalf("missing rtt for %s after clear", linkID)
	}
	if diff := math.Abs(s2.V - baseline); diff > 3 {
		t.Fatalf("want rtt back near baseline(%.2f) after clearing, got %.2f", baseline, s2.V)
	}
}

// TestShapingSynthesizesWireLoss sets 10%% loss on a healthy link and checks
// that the A-side output_bytes rate and the B-side input_bytes rate (the
// counters derive.Deriver's lossEstimate compares for the A->B direction)
// diverge by ~10%%, i.e. the receiving side's counter advances at
// rate*(1-loss/100) while the sending side's advances at the full offered
// rate.
func TestShapingSynthesizesWireLoss(t *testing.T) {
	g := testGraph()
	st := store.New(60)
	gen := New(g, st, 3)

	const linkID = "150-151"
	aEP := topo.Endpoint{AS: 150, IfID: "1"} // A side: sends outKey(aEP)
	bEP := topo.Endpoint{AS: 151, IfID: "1"} // B side: receives inKey(bEP)

	gen.SetShaping(linkID, &derive.Shaping{LossPct: f64(10)})

	now := int64(6_000_000)
	for i := 0; i < 5; i++ {
		gen.Step(now + int64(i)*1000)
	}

	outRate := st.Rate(outKey(aEP), 10)
	inRate := st.Rate(inKey(bEP), 10)
	if outRate <= 0 {
		t.Fatalf("want positive outbound rate, got %v", outRate)
	}
	gotLossPct := (outRate - inRate) / outRate * 100
	if diff := math.Abs(gotLossPct - 10); diff > 0.5 {
		t.Fatalf("want ~10%% wire loss (out=%.4f in=%.4f), got %.2f%%", outRate, inRate, gotLossPct)
	}
}

// TestShapingCapsTrafficRate sets a 0.5 Mbit/s rate cap on a healthy link
// (below the 0.3-2 Mbit/s ambient range it would otherwise wander in) and
// checks the synthesized output rate never exceeds it.
func TestShapingCapsTrafficRate(t *testing.T) {
	g := testGraph()
	st := store.New(60)
	gen := New(g, st, 5)

	const linkID = "150-151"
	aEP := topo.Endpoint{AS: 150, IfID: "1"}

	gen.SetShaping(linkID, &derive.Shaping{RateMbit: f64(0.5)})

	now := int64(7_000_000)
	for i := 0; i < 5; i++ {
		gen.Step(now + int64(i)*1000)
	}

	mbit := st.Rate(outKey(aEP), 10) * 8 / 1e6
	if mbit > 0.5+0.01 {
		t.Fatalf("want capped rate <= 0.5 Mbit/s, got %.4f", mbit)
	}
}

// TestShapingIgnoredOnDownLink shapes the pinned-down 150-154 link with an
// extreme delay and checks its RTT stays in the ordinary unshaped
// random-walk range and its traffic stays frozen at zero, confirming a down
// link ignores shaping entirely.
func TestShapingIgnoredOnDownLink(t *testing.T) {
	g := testGraph()
	st := store.New(60)
	gen := New(g, st, 9)

	downEP := topo.Endpoint{AS: 150, IfID: "2"} // 150-154 A-side

	gen.SetShaping(downLinkID, &derive.Shaping{DelayMs: f64(999)})

	now := int64(8_000_000)
	for i := 0; i < 3; i++ {
		gen.Step(now + int64(i)*1000)
	}

	s, ok := st.Last(rttKey(downEP))
	if !ok {
		t.Fatalf("missing rtt for down link")
	}
	if s.V > rttBaselineMax+5 {
		t.Fatalf("down link rtt should ignore shaping, got %.2f", s.V)
	}
	if rate := st.Rate(outKey(downEP), 10); rate != 0 {
		t.Fatalf("down link traffic must stay frozen even when shaped, got rate %v", rate)
	}
}
