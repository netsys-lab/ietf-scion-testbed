package mock

import (
	"testing"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/store"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

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
				ID: "150-151", Type: "core", Subnet: "fade:1",
				A: topo.Endpoint{IA: "1-150", AS: 150, IfID: "1"},
				B: topo.Endpoint{IA: "1-151", AS: 151, IfID: "1"},
			},
			{
				ID: "150-154", Type: "child", Subnet: "fade:7",
				A: topo.Endpoint{IA: "1-150", AS: 150, IfID: "2"},
				B: topo.Endpoint{IA: "1-154", AS: 154, IfID: "1"},
			},
			{
				ID: "151-155", Type: "child", Subnet: "fade:9",
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
