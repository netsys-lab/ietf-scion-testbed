package mock

import (
	"testing"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/derive"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/store"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// real155160Graph is a minimal topo.Graph containing exactly the real
// AS155<->AS160 link that cmd/fabricd/main.go preshapes at startup
// (demoShapedLink): same AS numbers and ifids topo.Load derives from
// config/AS155/topology.json and config/AS160/topology.json (per
// config/ifids.yml: "br1-155-1 36530: br1-160-1 39652"), so this test
// exercises the exact store keys the real generator/deriver pair would use
// for that link.
func real155160Graph() topo.Graph {
	return topo.Graph{
		ASes: []topo.AS{
			{IA: "1-155", Num: 155, Core: false, MgmtIP: "10.20.3.155"},
			{IA: "1-160", Num: 160, Core: false, MgmtIP: "10.20.3.160"},
		},
		Links: []topo.Link{{
			ID:     "155-160",
			Type:   "child",
			Subnet: "link 15",
			A:      topo.Endpoint{IA: "1-155", AS: 155, IfID: "36530", IP: "fd00:fade:15::155", LinkTo: "child"},
			B:      topo.Endpoint{IA: "1-160", AS: 160, IfID: "39652", IP: "fd00:fade:15::160", LinkTo: "parent"},
		}},
	}
}

// TestPreshapeAfterWarmupReachesElevatedBand is a regression test for the
// startup-preshape bug fixed in cmd/fabricd/main.go: derive.Deriver's
// per-side RTT baseline is the minimum ever observed for that key across the
// whole ring, so a link shaped from t=0 (as main.go's mock branch used to do)
// has no unshaped history -- its baseline becomes the shaped floor itself,
// and rttBand's `added`/`ratio` never cross the elevated threshold, so the
// link renders nominal with only a shaping chip (see
// .superpowers/sdd/dash-mockshaping-report.md's Fix Report).
//
// main.go now delays SetShaping(demoShapedLink, ...) via time.AfterFunc until
// the fabric has run unshaped for demoShapedLinkWarmup. This test reproduces
// that shape directly against a Generator + Deriver pair sharing one store,
// over the real 155-160 link's graph fixture: run a handful of unshaped
// Steps first (establishing real baseline history), confirm the band commits
// to nominal, then apply the same shaping main.go applies and confirm the
// band reaches "elevated" and holds there (no flap to degraded/nominal).
func TestPreshapeAfterWarmupReachesElevatedBand(t *testing.T) {
	g := real155160Graph()
	st := store.New(60)
	gen := New(g, st, 20260704) // deterministic seed
	d := derive.New(g, st)

	now := int64(10_000_000)

	// Unshaped warmup: 5 ticks of ordinary random-walk RTT, giving derive's
	// baseline real (sub-nominal-band) history to anchor on -- exactly what
	// main.go's delayed SetShaping now waits for before shaping the link.
	for i := 0; i < 5; i++ {
		now += 1000
		gen.Step(now)
	}

	// Two Frame calls over that unshaped history: the band should already be
	// (and, through hysteresis, remain) nominal.
	for i := 0; i < 2; i++ {
		if b := d.Frame(now).Links[0].Band; b != "nominal" {
			t.Fatalf("pre-shaping frame %d: band = %q, want nominal", i, b)
		}
	}

	gen.SetShaping("155-160", &derive.Shaping{DelayMs: f64(12), JitterMs: f64(2)})

	// hysteresis is now 3 frames (was 2), so six post-shaping frames gives it
	// room to commit and still leave three settled frames to check stability
	// over.
	var bands []string
	for i := 0; i < 6; i++ {
		now += 1000
		gen.Step(now)
		bands = append(bands, d.Frame(now).Links[0].Band)
	}

	if got := bands[len(bands)-1]; got != "elevated" {
		t.Fatalf("final band = %q, want elevated (bands after shaping: %v)", got, bands)
	}
	// Stability: once committed, the band must hold at elevated -- no flap
	// back to nominal or on to degraded/critical -- for the last 3 frames.
	for i, b := range bands[len(bands)-3:] {
		if b != "elevated" {
			t.Fatalf("frame %d after shaping: band = %q, want elevated to hold (bands: %v)", i, b, bands)
		}
	}
}
