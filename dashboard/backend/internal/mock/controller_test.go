package mock

import (
	"context"
	"testing"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/derive"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/store"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// TestControllerApplyBoth checks that Apply with direction=both against a
// healthy link returns two OK results (one per endpoint, A then B) and that
// CurrentShaping reflects the applied shaping; then that clearing removes it
// and still returns two results.
func TestControllerApplyBoth(t *testing.T) {
	g := testGraph()
	st := store.New(60)
	gen := New(g, st, 1)
	c := NewController(g, gen)
	ctx := context.Background()

	link := g.Links[0] // "150-151"

	results := c.Apply(ctx, link, "both", derive.Shaping{DelayMs: f64(20)}, false)
	if len(results) != 2 {
		t.Fatalf("want 2 results for direction=both, got %d: %+v", len(results), results)
	}
	if !results[0].OK || !results[1].OK {
		t.Fatalf("want both results OK, got %+v", results)
	}
	if results[0].AS != link.A.AS || results[1].AS != link.B.AS {
		t.Fatalf("want results ordered A(%d) then B(%d), got %+v", link.A.AS, link.B.AS, results)
	}

	shaping, _ := c.Poll(ctx)
	p, ok := shaping[link.ID]
	if !ok || p == nil || p.DelayMs == nil || *p.DelayMs != 20 {
		t.Fatalf("want Poll/CurrentShaping to reflect applied shaping, got %+v", shaping)
	}
	// Poll must agree with the Generator's own view.
	if gp := gen.CurrentShaping()[link.ID]; gp == nil || gp.DelayMs == nil || *gp.DelayMs != 20 {
		t.Fatalf("gen.CurrentShaping disagrees with Controller.Poll: %+v", gen.CurrentShaping())
	}

	clearResults := c.Apply(ctx, link, "both", derive.Shaping{}, true)
	if len(clearResults) != 2 || !clearResults[0].OK || !clearResults[1].OK {
		t.Fatalf("want 2 OK results on clear, got %+v", clearResults)
	}
	shaping2, _ := c.Poll(ctx)
	if shaping2[link.ID] != nil {
		t.Fatalf("want shaping cleared after Apply(clear=true), got %+v", shaping2[link.ID])
	}
}

// TestControllerApplyDirectionSubset checks that a_to_b/b_to_a only produce a
// Result for the addressed endpoint, even though the shaping itself (per the
// mock's documented direction-agnostic behavior) still applies to the whole
// link.
func TestControllerApplyDirectionSubset(t *testing.T) {
	g := testGraph()
	st := store.New(60)
	gen := New(g, st, 1)
	c := NewController(g, gen)
	link := g.Links[0]

	results := c.Apply(context.Background(), link, "a_to_b", derive.Shaping{DelayMs: f64(5)}, false)
	if len(results) != 1 || results[0].AS != link.A.AS || !results[0].OK {
		t.Fatalf("want single OK result for AS%d, got %+v", link.A.AS, results)
	}

	results = c.Apply(context.Background(), link, "b_to_a", derive.Shaping{DelayMs: f64(5)}, false)
	if len(results) != 1 || results[0].AS != link.B.AS || !results[0].OK {
		t.Fatalf("want single OK result for AS%d, got %+v", link.B.AS, results)
	}
}

// TestControllerAllHealthAllTrue checks that AllHealth reports every AS in
// the graph as up, matching the doc comment: mock mode has no real linkd to
// fail to reach.
func TestControllerAllHealthAllTrue(t *testing.T) {
	g := testGraph()
	st := store.New(60)
	gen := New(g, st, 1)
	c := NewController(g, gen)

	health := c.AllHealth(context.Background())
	if len(health) != len(g.ASes) {
		t.Fatalf("want %d entries, got %d: %+v", len(g.ASes), len(health), health)
	}
	for _, as := range g.ASes {
		if up, ok := health[as.Num]; !ok || !up {
			t.Fatalf("want AS%d up=true, got %v (present=%v)", as.Num, up, ok)
		}
	}
}

// TestControllerPollBGP checks that shaping a link to 100% loss makes PollBGP
// report a non-Established session on that link (both sides), while every
// other link stays Established — the off-fleet failure-demo synthesis.
func TestControllerPollBGP(t *testing.T) {
	g := testGraph()
	st := store.New(60)
	gen := New(g, st, 1)
	c := NewController(g, gen)
	ctx := context.Background()

	dead := g.Links[0].ID // "150-151"
	c.Apply(ctx, g.Links[0], "both", derive.Shaping{LossPct: f64(100)}, false)

	got, _ := c.PollBGP(ctx)
	if len(got) != len(g.Links) {
		t.Fatalf("want %d links, got %d", len(g.Links), len(got))
	}
	bl := got[dead]
	if bl == nil || bl.A == nil || bl.B == nil {
		t.Fatalf("want both sides present for %s, got %+v", dead, bl)
	}
	if bl.A.State == "Established" || bl.B.State == "Established" {
		t.Fatalf("want non-Established both sides on 100%%-loss link, got %+v / %+v", bl.A, bl.B)
	}
	for _, l := range g.Links[1:] {
		other := got[l.ID]
		if other == nil || other.A == nil || other.B == nil ||
			other.A.State != "Established" || other.B.State != "Established" {
			t.Fatalf("want %s Established both sides, got %+v", l.ID, other)
		}
	}
}

// triangleGraph is a 3-AS fully-meshed fixture (links A-B, B-C, A-C), used by
// the routes-reroute test: testGraph() (this file's tree of 150-151,
// 150-154, 151-155) has no detour path for any single link, so a loss-forced
// link there simply strands its far side rather than exercising the
// BFS-synth reroute.
func triangleGraph() topo.Graph {
	return topo.Graph{
		ASes: []topo.AS{
			{IA: "1-150", Num: 150, Core: true, MgmtIP: "10.20.3.150"},
			{IA: "1-151", Num: 151, Core: true, MgmtIP: "10.20.3.151"},
			{IA: "1-152", Num: 152, Core: true, MgmtIP: "10.20.3.152"},
		},
		Links: []topo.Link{
			{
				ID: "150-151", Type: "core", Subnet: "link 1",
				A: topo.Endpoint{IA: "1-150", AS: 150, IfID: "1"},
				B: topo.Endpoint{IA: "1-151", AS: 151, IfID: "1"},
			},
			{
				ID: "151-152", Type: "core", Subnet: "link 2",
				A: topo.Endpoint{IA: "1-151", AS: 151, IfID: "2"},
				B: topo.Endpoint{IA: "1-152", AS: 152, IfID: "1"},
			},
			{
				ID: "150-152", Type: "core", Subnet: "link 3",
				A: topo.Endpoint{IA: "1-150", AS: 150, IfID: "2"},
				B: topo.Endpoint{IA: "1-152", AS: 152, IfID: "2"},
			},
		},
	}
}

// TestPollBGPRoutesRerouteOnLoss checks that PollBGP's synthesized routes
// reroute around a link forced to 100% mock loss, mirroring what live BFD
// convergence would do — the failure-demo overlay must not keep pointing the
// BGP path overlay at a dead link.
func TestPollBGPRoutesRerouteOnLoss(t *testing.T) {
	g := triangleGraph()
	st := store.New(60)
	gen := New(g, st, 1)
	c := NewController(g, gen)
	ctx := context.Background()

	_, routes := c.PollBGP(ctx)
	if len(routes) == 0 {
		t.Fatal("no synthesized routes")
	}

	// Pick any link L in the graph; force 100% loss.
	l := c.g.Links[0]
	loss := 100.0
	c.gen.SetShaping(l.ID, &derive.Shaping{LossPct: &loss})
	_, routes = c.PollBGP(ctx)
	// A's first hop toward B must no longer be the dead link's ifid (unless
	// the dead link was the ONLY path — not true on this triangle graph).
	if got := routes[l.A.AS][l.B.AS]; got == l.A.IfID {
		t.Fatalf("route still uses dead link: %s", got)
	}
}
