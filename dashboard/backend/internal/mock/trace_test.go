package mock

import (
	"context"
	"testing"
	"time"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/derive"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/idint"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/store"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// traceTestGraph is the pinned 150-154-155-161 chain fixture -- same shape
// (AS numbers, link IDs, ifids) as internal/idint/idint_test.go's testGraph,
// so Case 4's expectations stay directly comparable with Task 4's
// hand-verified fixture.
func traceTestGraph() topo.Graph {
	return topo.Graph{
		ASes: []topo.AS{
			{IA: "1-150", Num: 150, MgmtIP: "10.20.3.150"},
			{IA: "1-154", Num: 154, MgmtIP: "10.20.3.154"},
			{IA: "1-155", Num: 155, MgmtIP: "10.20.3.155"},
			{IA: "1-161", Num: 161, MgmtIP: "10.20.3.161"},
		},
		Links: []topo.Link{
			{ID: "150-154", A: topo.Endpoint{AS: 150, IfID: "2"}, B: topo.Endpoint{AS: 154, IfID: "1"}},
			{ID: "154-155", A: topo.Endpoint{AS: 154, IfID: "3"}, B: topo.Endpoint{AS: 155, IfID: "1"}},
			{ID: "155-161", A: topo.Endpoint{AS: 155, IfID: "7"}, B: topo.Endpoint{AS: 161, IfID: "1"}},
		},
	}
}

// newTraceProber builds a TraceProber over traceTestGraph backed by a fresh
// seeded Generator, for tests that don't care about the Generator's own
// random-walk state beyond SetShaping/CurrentShaping.
func newTraceProber(t *testing.T) (*Generator, idint.Prober) {
	t.Helper()
	g := traceTestGraph()
	gen := New(g, store.New(10), 1)
	return gen, NewTraceProber(g, gen)
}

// --- Case 1: Paths finds the one simple path with correct interfaces/ifids ---

func TestTracePathsSimplePath(t *testing.T) {
	_, tp := newTraceProber(t)

	resp, err := tp.Paths(context.Background(), 150, 161)
	if err != nil {
		t.Fatalf("Paths: %v", err)
	}
	if resp.LocalIA != "1-150" {
		t.Errorf("LocalIA = %q, want 1-150", resp.LocalIA)
	}
	if len(resp.Paths) != 1 {
		t.Fatalf("len(Paths) = %d, want 1", len(resp.Paths))
	}

	p := resp.Paths[0]
	if p.Fingerprint != "mock-0" {
		t.Errorf("Fingerprint = %q, want mock-0", p.Fingerprint)
	}
	if p.MTU != 1472 {
		t.Errorf("MTU = %d, want 1472", p.MTU)
	}
	if _, err := time.Parse(time.RFC3339, p.Expiry); err != nil {
		t.Errorf("Expiry %q not RFC3339: %v", p.Expiry, err)
	}

	wantIfaces := []idint.IfaceJSON{
		{IA: "1-150", IfID: 2},
		{IA: "1-154", IfID: 1},
		{IA: "1-154", IfID: 3},
		{IA: "1-155", IfID: 1},
		{IA: "1-155", IfID: 7},
		{IA: "1-161", IfID: 1},
	}
	if len(p.Interfaces) != len(wantIfaces) {
		t.Fatalf("len(Interfaces) = %d, want %d", len(p.Interfaces), len(wantIfaces))
	}
	for i, want := range wantIfaces {
		if p.Interfaces[i] != want {
			t.Errorf("Interfaces[%d] = %+v, want %+v", i, p.Interfaces[i], want)
		}
	}

	// One latency entry per metadata gap: len(Interfaces)-1 == 5 for a
	// 3-link path (3 inter-AS gaps + 2 intra-AS gaps).
	if len(p.LatencyUs) != len(wantIfaces)-1 {
		t.Fatalf("len(LatencyUs) = %d, want %d", len(p.LatencyUs), len(wantIfaces)-1)
	}
	wantLat := []int64{
		linkBaseUs("150-154"), traceIntraASUs,
		linkBaseUs("154-155"), traceIntraASUs,
		linkBaseUs("155-161"),
	}
	for i, want := range wantLat {
		if p.LatencyUs[i] != want {
			t.Errorf("LatencyUs[%d] = %d, want %d", i, p.LatencyUs[i], want)
		}
	}
}

// --- Case 2: Probe unshaped -- 3 egress records, RTT = per-link base ---

func TestTraceProbeUnshaped(t *testing.T) {
	_, tp := newTraceProber(t)

	res, err := tp.Probe(context.Background(), 150, 161, "")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	wantLinks := []string{"150-154", "154-155", "155-161"}
	if len(res.Fwd) != len(wantLinks)+2 {
		t.Fatalf("len(Fwd) = %d, want %d (source + %d egress + ingress)", len(res.Fwd), len(wantLinks)+2, len(wantLinks))
	}
	if !res.Fwd[0].Source || res.Fwd[0].IA != "1-150" {
		t.Errorf("Fwd[0] = %+v, want source record at 1-150", res.Fwd[0])
	}
	last := res.Fwd[len(res.Fwd)-1]
	if !last.Ingress || last.IA != "1-161" {
		t.Errorf("last Fwd record = %+v, want ingress record at 1-161", last)
	}

	egress := res.Fwd[1 : len(res.Fwd)-1]
	if len(egress) != len(wantLinks) {
		t.Fatalf("egress records = %d, want %d", len(egress), len(wantLinks))
	}
	var wantRttSum int64
	for i, link := range wantLinks {
		rec := egress[i]
		if !rec.Egress {
			t.Errorf("egress[%d].Egress = false, want true", i)
		}
		base := linkBaseUs(link)
		wantRttSum += base
		if rec.RttNextBrUs == nil || *rec.RttNextBrUs != base {
			t.Errorf("egress[%d] (%s) RttNextBrUs = %v, want %d", i, link, rec.RttNextBrUs, base)
		}
	}

	wantRtt := float64(wantRttSum)/1000 + traceRttFixedMs
	if res.ProbeRttMs != wantRtt {
		t.Errorf("ProbeRttMs = %v, want %v", res.ProbeRttMs, wantRtt)
	}

	if len(res.Rev) != len(res.Fwd) {
		t.Errorf("len(Rev) = %d, want %d (mirrored)", len(res.Rev), len(res.Fwd))
	}
}

// --- Case 3: shaping a link raises just that hop's RTT ---

func TestTraceProbeShaped(t *testing.T) {
	gen, tp := newTraceProber(t)
	gen.SetShaping("154-155", &derive.Shaping{DelayMs: f64(50)})

	res, err := tp.Probe(context.Background(), 150, 161, "")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	egress := res.Fwd[1 : len(res.Fwd)-1]
	if len(egress) != 3 {
		t.Fatalf("egress records = %d, want 3", len(egress))
	}

	wantBase150154 := linkBaseUs("150-154")
	wantBase154155 := linkBaseUs("154-155") + 50000 // +50ms in us
	wantBase155161 := linkBaseUs("155-161")

	if r := egress[0].RttNextBrUs; r == nil || *r != wantBase150154 {
		t.Errorf("egress[0] (150-154) RttNextBrUs = %v, want %d (unshaped)", r, wantBase150154)
	}
	if r := egress[1].RttNextBrUs; r == nil || *r != wantBase154155 {
		t.Errorf("egress[1] (154-155) RttNextBrUs = %v, want %d (base+50000)", r, wantBase154155)
	}
	if r := egress[2].RttNextBrUs; r == nil || *r != wantBase155161 {
		t.Errorf("egress[2] (155-161) RttNextBrUs = %v, want %d (unshaped)", r, wantBase155161)
	}
}

// --- Case 4: wired through a real idint.Manager + TickOnce ---

func TestTraceIntegrationManager(t *testing.T) {
	g := traceTestGraph()
	gen := New(g, store.New(10), 1)
	tp := NewTraceProber(g, gen)

	gen.SetShaping("154-155", &derive.Shaping{DelayMs: f64(50)})

	m := idint.NewManager(g, tp, time.Second)
	if err := m.Set(150, 161, ""); err != nil {
		t.Fatalf("Set: %v", err)
	}
	m.TickOnce(context.Background())

	vm := m.VM()
	if vm == nil {
		t.Fatal("VM() nil after TickOnce")
	}
	if !vm.Ok {
		t.Fatalf("Ok = false, want true; Error = %q", vm.Error)
	}
	if len(vm.Hops) != 3 {
		t.Fatalf("len(Hops) = %d, want 3", len(vm.Hops))
	}
	want := linkBaseUs("154-155") + 50000
	if vm.Hops[1].RttNextBrUs == nil || *vm.Hops[1].RttNextBrUs != want {
		t.Errorf("Hops[1].RttNextBrUs = %v, want %d", vm.Hops[1].RttNextBrUs, want)
	}
	if vm.Hops[1].Link != "154-155" {
		t.Errorf("Hops[1].Link = %q, want 154-155", vm.Hops[1].Link)
	}
}
