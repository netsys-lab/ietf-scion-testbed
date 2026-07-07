package idint

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// testGraph is the pinned fixture: a 150-154-155-161 chain, real-shaped
// ifids (see task-4-brief.md).
func testGraph() topo.Graph {
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

// fakeProber is a settable-func Prober for tests.
type fakeProber struct {
	pathsFn func(ctx context.Context, src, dst int) (*PathsResponse, error)
	probeFn func(ctx context.Context, src, dst int, fingerprint string) (*ProbeResult, error)
}

func (f *fakeProber) Paths(ctx context.Context, src, dst int) (*PathsResponse, error) {
	return f.pathsFn(ctx, src, dst)
}

func (f *fakeProber) Probe(ctx context.Context, src, dst int, fingerprint string) (*ProbeResult, error) {
	return f.probeFn(ctx, src, dst, fingerprint)
}

func i64p(v int64) *int64 { return &v }

// goodProbeResult is the 150->161 result over 150-154-155-161, matching
// case 2 of the brief exactly: Fwd = source + 3 egress records (RTT
// 5600/55400/7800us) + final ingress record.
func goodProbeResult() *ProbeResult {
	return &ProbeResult{
		Path: PathJSON{
			Fingerprint: "fp-good",
			Interfaces: []IfaceJSON{
				{IA: "1-150", IfID: 2},
				{IA: "1-154", IfID: 1},
				{IA: "1-154", IfID: 3},
				{IA: "1-155", IfID: 1},
				{IA: "1-155", IfID: 7},
				{IA: "1-161", IfID: 1},
			},
			LatencyUs: []int64{1000, 1000, 1000},
		},
		ProbeRttMs: 12.5,
		Fwd: []HopRecord{
			{Hop: 0, IA: "1-150", Source: true},
			{Hop: 1, IA: "1-154", Egress: true, RttNextBrUs: i64p(5600)},
			{Hop: 2, IA: "1-155", Egress: true, RttNextBrUs: i64p(55400)},
			{Hop: 3, IA: "1-161", Egress: true, RttNextBrUs: i64p(7800)},
			{Hop: 4, IA: "1-161", Ingress: true},
		},
	}
}

// --- Case 1: Set validation + pending VM ---

func TestSetValidation(t *testing.T) {
	m := NewManager(testGraph(), &fakeProber{}, time.Second)

	if err := m.Set(999, 161, ""); !errors.Is(err, ErrBadSession) {
		t.Fatalf("unknown src AS: got err %v, want ErrBadSession", err)
	}
	if err := m.Set(150, 999, ""); !errors.Is(err, ErrBadSession) {
		t.Fatalf("unknown dst AS: got err %v, want ErrBadSession", err)
	}
	if err := m.Set(150, 150, ""); !errors.Is(err, ErrBadSession) {
		t.Fatalf("src == dst: got err %v, want ErrBadSession", err)
	}

	if err := m.Set(150, 161, ""); err != nil {
		t.Fatalf("valid Set: unexpected error %v", err)
	}
	vm := m.VM()
	if vm == nil {
		t.Fatal("VM() nil after valid Set")
	}
	if !vm.Ok {
		t.Errorf("pending VM: Ok = false, want true")
	}
	if len(vm.Hops) != 0 {
		t.Errorf("pending VM: Hops = %v, want empty", vm.Hops)
	}
	if vm.UpdatedAt != 0 {
		t.Errorf("pending VM: UpdatedAt = %d, want 0", vm.UpdatedAt)
	}
	if vm.Src != "1-150" || vm.Dst != "1-161" {
		t.Errorf("pending VM: Src/Dst = %q/%q, want 1-150/1-161", vm.Src, vm.Dst)
	}
	if !vm.Auto {
		t.Errorf("pending VM: Auto = false, want true (fingerprint == \"\")")
	}

	if err := m.Set(150, 161, "pinned-fp"); err != nil {
		t.Fatalf("valid Set with fingerprint: unexpected error %v", err)
	}
	if vm := m.VM(); vm.Auto {
		t.Errorf("pinned Set: Auto = true, want false")
	}
}

// --- Case 2: TickOnce success collapses hops correctly ---

func TestTickOnceSuccess(t *testing.T) {
	fp := &fakeProber{probeFn: func(ctx context.Context, src, dst int, fingerprint string) (*ProbeResult, error) {
		return goodProbeResult(), nil
	}}
	m := NewManager(testGraph(), fp, time.Second)
	if err := m.Set(150, 161, ""); err != nil {
		t.Fatal(err)
	}
	m.TickOnce(context.Background())

	vm := m.VM()
	if vm == nil {
		t.Fatal("VM() nil after TickOnce")
	}
	if !vm.Ok {
		t.Errorf("Ok = false, want true; Error = %q", vm.Error)
	}
	wantLinks := []string{"150-154", "154-155", "155-161"}
	if !equalStrings(vm.PathLinks, wantLinks) {
		t.Errorf("PathLinks = %v, want %v", vm.PathLinks, wantLinks)
	}
	if len(vm.Hops) != 3 {
		t.Fatalf("len(Hops) = %d, want 3", len(vm.Hops))
	}
	for i, link := range wantLinks {
		if vm.Hops[i].Link != link {
			t.Errorf("Hops[%d].Link = %q, want %q", i, vm.Hops[i].Link, link)
		}
	}
	if vm.Hops[1].RttNextBrUs == nil || *vm.Hops[1].RttNextBrUs != 55400 {
		t.Errorf("Hops[1].RttNextBrUs = %v, want 55400", vm.Hops[1].RttNextBrUs)
	}
	if vm.Fingerprint != "fp-good" {
		t.Errorf("Fingerprint = %q, want fp-good", vm.Fingerprint)
	}
}

// --- Case 2b: reverse-stack collapse (live fork behavior) ---

// revOnlyProbeResult mirrors the live 2026-07-07 probe 150->161: the fork
// populates only the reverse ID-INT stack, so Fwd carries just the source
// record and the per-hop egress records arrive dst->src in Rev.
func revOnlyProbeResult() *ProbeResult {
	res := goodProbeResult()
	res.Fwd = []HopRecord{{Hop: 0, IA: "1-150", Source: true}}
	res.Rev = []HopRecord{
		{Hop: 0, IA: "1-161", Source: true},
		{Hop: 1, IA: "1-161", Egress: true, RttNextBrUs: i64p(22062)},
		{Hop: 2, IA: "1-155", Egress: true, RttNextBrUs: i64p(14955)},
		{Hop: 3, IA: "1-154", Egress: true, RttNextBrUs: i64p(18412)},
		{Hop: 4, IA: "1-150", Ingress: true},
	}
	return res
}

func TestTickOnceRevStackCollapse(t *testing.T) {
	fp := &fakeProber{probeFn: func(ctx context.Context, src, dst int, fingerprint string) (*ProbeResult, error) {
		return revOnlyProbeResult(), nil
	}}
	m := NewManager(testGraph(), fp, time.Second)
	if err := m.Set(150, 161, ""); err != nil {
		t.Fatal(err)
	}
	m.TickOnce(context.Background())

	vm := m.VM()
	if vm == nil {
		t.Fatal("VM() nil after TickOnce")
	}
	if !vm.Ok {
		t.Fatalf("Ok = false, want true; Error = %q", vm.Error)
	}
	wantLinks := []string{"150-154", "154-155", "155-161"}
	if !equalStrings(vm.PathLinks, wantLinks) {
		t.Errorf("PathLinks = %v, want %v", vm.PathLinks, wantLinks)
	}
	if len(vm.Hops) != 3 {
		t.Fatalf("len(Hops) = %d, want 3", len(vm.Hops))
	}
	// Rev egress record k maps onto pathLinks[len-1-k]; the hop keeps the
	// record's own IA (the far-side BR that reported it).
	want := []struct {
		link string
		ia   string
		rtt  int64
	}{
		{"150-154", "1-154", 18412},
		{"154-155", "1-155", 14955},
		{"155-161", "1-161", 22062},
	}
	for i, w := range want {
		h := vm.Hops[i]
		if h.Link != w.link {
			t.Errorf("Hops[%d].Link = %q, want %q", i, h.Link, w.link)
		}
		if h.IA != w.ia {
			t.Errorf("Hops[%d].IA = %q, want %q", i, h.IA, w.ia)
		}
		if h.RttNextBrUs == nil || *h.RttNextBrUs != w.rtt {
			t.Errorf("Hops[%d].RttNextBrUs = %v, want %d", i, h.RttNextBrUs, w.rtt)
		}
	}
}

func TestTickOnceBothStacksEmpty(t *testing.T) {
	res := goodProbeResult()
	res.Fwd = []HopRecord{{Hop: 0, IA: "1-150", Source: true}}
	res.Rev = nil
	fp := &fakeProber{probeFn: func(ctx context.Context, src, dst int, fingerprint string) (*ProbeResult, error) {
		return res, nil
	}}
	m := NewManager(testGraph(), fp, time.Second)
	if err := m.Set(150, 161, ""); err != nil {
		t.Fatal(err)
	}
	m.TickOnce(context.Background())

	vm := m.VM()
	if vm.Ok {
		t.Fatal("Ok = true with no egress records in either stack, want false")
	}
	if !strings.Contains(vm.Error, "fwd/rev egress records (0/0) != path links (3)") {
		t.Errorf("Error = %q, want it to name both counts and the link count", vm.Error)
	}
}

// --- Case 3: egress-count mismatch keeps previous hops ---

func TestTickOnceEgressMismatch(t *testing.T) {
	good := goodProbeResult()
	bad := goodProbeResult()
	bad.Fwd = bad.Fwd[:len(bad.Fwd)-2] // drop the last egress record + trailing ingress

	calls := 0
	fp := &fakeProber{probeFn: func(ctx context.Context, src, dst int, fingerprint string) (*ProbeResult, error) {
		calls++
		if calls == 1 {
			return good, nil
		}
		return bad, nil
	}}
	m := NewManager(testGraph(), fp, time.Second)
	if err := m.Set(150, 161, ""); err != nil {
		t.Fatal(err)
	}
	m.TickOnce(context.Background()) // good
	prevHops := m.VM().Hops

	m.TickOnce(context.Background()) // bad: egress count now 2 != 3 links
	vm := m.VM()
	if vm.Ok {
		t.Fatal("Ok = true after egress-count mismatch, want false")
	}
	if !strings.Contains(vm.Error, "egress records") {
		t.Errorf("Error = %q, want it to mention \"egress records\"", vm.Error)
	}
	if !reflect.DeepEqual(vm.Hops, prevHops) {
		t.Errorf("Hops changed on error: got %v, want retained %v", vm.Hops, prevHops)
	}
}

// --- Case 4: probe error keeps previous good hops, advances UpdatedAt ---

func TestTickOnceProbeError(t *testing.T) {
	calls := 0
	fp := &fakeProber{probeFn: func(ctx context.Context, src, dst int, fingerprint string) (*ProbeResult, error) {
		calls++
		if calls == 1 {
			return goodProbeResult(), nil
		}
		return nil, errors.New("probe timeout")
	}}
	m := NewManager(testGraph(), fp, time.Second)
	if err := m.Set(150, 161, ""); err != nil {
		t.Fatal(err)
	}
	m.TickOnce(context.Background())
	prev := m.VM()
	if !prev.Ok {
		t.Fatalf("first tick not Ok: %+v", prev)
	}

	time.Sleep(2 * time.Millisecond) // ensure a distinguishable UnixMilli
	m.TickOnce(context.Background())
	vm := m.VM()
	if vm.Ok {
		t.Fatal("Ok = true after probe error, want false")
	}
	if vm.Error == "" {
		t.Error("Error empty after probe error")
	}
	if !reflect.DeepEqual(vm.Hops, prev.Hops) {
		t.Errorf("Hops changed on probe error: got %v, want retained %v", vm.Hops, prev.Hops)
	}
	if vm.UpdatedAt <= prev.UpdatedAt {
		t.Errorf("UpdatedAt did not advance: prev %d, got %d", prev.UpdatedAt, vm.UpdatedAt)
	}
}

// --- Case 5: PathOptions ---

func TestPathOptions(t *testing.T) {
	fp := &fakeProber{pathsFn: func(ctx context.Context, src, dst int) (*PathsResponse, error) {
		return &PathsResponse{
			LocalIA: "1-150",
			Paths: []PathJSON{
				{
					Fingerprint: "fp-1",
					MTU:         1472,
					Expiry:      "2026-07-08T00:00:00Z",
					Interfaces: []IfaceJSON{
						{IA: "1-150", IfID: 2},
						{IA: "1-154", IfID: 1},
						{IA: "1-154", IfID: 3},
						{IA: "1-155", IfID: 1},
						{IA: "1-155", IfID: 7},
						{IA: "1-161", IfID: 1},
					},
					LatencyUs: []int64{5000, 100, 3000},
				},
				{
					Fingerprint: "fp-2",
					Interfaces: []IfaceJSON{
						{IA: "1-150", IfID: 2},
						{IA: "1-154", IfID: 1},
					},
					LatencyUs: []int64{5000, -1},
				},
			},
		}, nil
	}}
	m := NewManager(testGraph(), fp, time.Second)

	if _, err := m.PathOptions(context.Background(), 999, 161); !errors.Is(err, ErrBadSession) {
		t.Fatalf("unknown src: got err %v, want ErrBadSession", err)
	}

	opts, err := m.PathOptions(context.Background(), 150, 161)
	if err != nil {
		t.Fatalf("PathOptions: %v", err)
	}
	if len(opts) != 2 {
		t.Fatalf("len(opts) = %d, want 2", len(opts))
	}

	o0 := opts[0]
	if !equalInts(o0.Hops, []int{150, 154, 155, 161}) {
		t.Errorf("opts[0].Hops = %v, want [150 154 155 161]", o0.Hops)
	}
	if !equalStrings(o0.Links, []string{"150-154", "154-155", "155-161"}) {
		t.Errorf("opts[0].Links = %v, want [150-154 154-155 155-161]", o0.Links)
	}
	if o0.LatencyUsTotal != 8100 {
		t.Errorf("opts[0].LatencyUsTotal = %d, want 8100", o0.LatencyUsTotal)
	}
	if !o0.CurrentBest {
		t.Errorf("opts[0].CurrentBest = false, want true")
	}

	o1 := opts[1]
	if o1.LatencyUsTotal != -1 {
		t.Errorf("opts[1].LatencyUsTotal = %d, want -1 (has an unset entry)", o1.LatencyUsTotal)
	}
	if o1.CurrentBest {
		t.Errorf("opts[1].CurrentBest = true, want false")
	}
}

// --- Case 6: Clear ---

func TestClear(t *testing.T) {
	fp := &fakeProber{probeFn: func(ctx context.Context, src, dst int, fingerprint string) (*ProbeResult, error) {
		return goodProbeResult(), nil
	}}
	m := NewManager(testGraph(), fp, time.Second)
	if err := m.Set(150, 161, ""); err != nil {
		t.Fatal(err)
	}
	m.TickOnce(context.Background())
	if m.VM() == nil {
		t.Fatal("VM() nil before Clear, test setup broken")
	}
	m.Clear()
	if vm := m.VM(); vm != nil {
		t.Errorf("VM() = %+v after Clear, want nil", vm)
	}
	if l := m.Latest(); l != nil {
		t.Errorf("Latest() = %+v after Clear, want nil", l)
	}
}

// --- Case 7: NewHTTPProber against httptest servers ---

func TestHTTPProber(t *testing.T) {
	var gotPathsURL string
	var gotProbeBody map[string]string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/paths", func(w http.ResponseWriter, r *http.Request) {
		gotPathsURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(PathsResponse{LocalIA: "1-150", Paths: []PathJSON{{Fingerprint: "fp"}}})
	})
	mux.HandleFunc("POST /api/v1/probe", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotProbeBody)
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "fingerprint not found"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	g := topo.Graph{
		ASes: []topo.AS{
			{IA: "1-150", Num: 150, MgmtIP: host},
			{IA: "1-161", Num: 161, MgmtIP: host},
		},
	}
	p := NewHTTPProber(g, port, 32001, srv.Client())

	resp, err := p.Paths(context.Background(), 150, 161)
	if err != nil {
		t.Fatalf("Paths: unexpected error %v", err)
	}
	if resp.LocalIA != "1-150" || len(resp.Paths) != 1 || resp.Paths[0].Fingerprint != "fp" {
		t.Errorf("Paths response mismatch: %+v", resp)
	}
	wantPathsURL := "/api/v1/paths?dst=1-161"
	if gotPathsURL != wantPathsURL {
		t.Errorf("paths request URL = %q, want %q", gotPathsURL, wantPathsURL)
	}

	_, err = p.Probe(context.Background(), 150, 161, "")
	if err == nil {
		t.Fatal("Probe: expected error from 404 response, got nil")
	}
	if !strings.Contains(err.Error(), "fingerprint not found") {
		t.Errorf("Probe error = %q, want it to contain the body message", err.Error())
	}
	wantRemote := "1-161," + net.JoinHostPort(host, "32001")
	if gotProbeBody["remote"] != wantRemote {
		t.Errorf("probe body remote = %q, want %q", gotProbeBody["remote"], wantRemote)
	}
}

// --- test helpers ---

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
