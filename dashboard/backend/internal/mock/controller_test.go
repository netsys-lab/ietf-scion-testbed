package mock

import (
	"context"
	"testing"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/derive"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/store"
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

	shaping := c.Poll(ctx)
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
	shaping2 := c.Poll(ctx)
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
