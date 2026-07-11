package hev3

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/scionproto/scion/pkg/addr"
	"github.com/scionproto/scion/pkg/segment/iface"
	"github.com/scionproto/scion/pkg/snet"
)

const ms = time.Millisecond

// fakePath is a minimal snet.Path carrying only the metadata ExpandSCION reads.
type fakePath struct {
	md *snet.PathMetadata
}

func (p fakePath) UnderlayNextHop() *net.UDPAddr { return nil }
func (p fakePath) Dataplane() snet.DataplanePath { return nil }
func (p fakePath) Source() addr.IA               { return 0 }
func (p fakePath) Destination() addr.IA          { return 0 }
func (p fakePath) Metadata() *snet.PathMetadata  { return p.md }

func makePath(latencies []time.Duration, ifaceIDs []uint64) snet.Path {
	ifs := make([]snet.PathInterface, len(ifaceIDs))
	for i, id := range ifaceIDs {
		ifs[i] = snet.PathInterface{ID: iface.ID(id), IA: addr.MustParseIA("1-150")}
	}
	return fakePath{md: &snet.PathMetadata{Interfaces: ifs, Latency: latencies}}
}

type fakeQuerier struct {
	paths  []snet.Path
	err    error
	gotDst addr.IA
}

func (q *fakeQuerier) Paths(_ context.Context, dst addr.IA) ([]snet.Path, error) {
	q.gotDst = dst
	return q.paths, q.err
}

func withQuerier(t *testing.T, q pathQuerier, err error) {
	t.Helper()
	orig := resolveQuerier
	resolveQuerier = func(context.Context, DialerOptions) (pathQuerier, error) { return q, err }
	t.Cleanup(func() { resolveQuerier = orig })
}

func protoCand() Candidate {
	return Candidate{
		Family: FamilySCION,
		IA:     "1-150",
		Host:   "10.20.3.216",
		Port:   443,
		ALPN:   []string{"h3"},
		Label:  "scion:1-150,10.20.3.216",
	}
}

func TestExpandSCIONNativeRankingAndCap(t *testing.T) {
	fast := makePath([]time.Duration{10 * ms}, []uint64{1, 2})             // 10ms, 2 hops
	mid := makePath([]time.Duration{5 * ms, 5 * ms}, []uint64{3, 4, 5, 6}) // 10ms, 4 hops
	slow := makePath([]time.Duration{30 * ms}, []uint64{7, 8})             // 30ms, 2 hops
	unset := makePath([]time.Duration{-1}, []uint64{9, 10})                // unset ⇒ last
	q := &fakeQuerier{paths: []snet.Path{slow, unset, fast, mid}}
	withQuerier(t, q, nil)

	ipc := Candidate{Family: FamilyIPv6, Host: "2001:db8::1", Port: 443, Label: "v6:2001:db8::1"}
	out := ExpandSCION(context.Background(), []Candidate{protoCand(), ipc}, DialerOptions{})

	if q.gotDst != addr.MustParseIA("1-150") {
		t.Fatalf("querier queried dst %s, want 1-150", q.gotDst)
	}
	// passthrough IP candidate first, then 3 ranked natives (unset dropped by K=3).
	if len(out) != 4 {
		t.Fatalf("got %d candidates, want 4: %+v", len(out), labels(out))
	}
	if out[0].Label != "v6:2001:db8::1" {
		t.Fatalf("out[0] = %q, want passthrough IP candidate", out[0].Label)
	}
	natives := out[1:]
	wantLabels := []string{
		"scion:1-150,10.20.3.216#p1",
		"scion:1-150,10.20.3.216#p2",
		"scion:1-150,10.20.3.216#p3",
	}
	// Ranking: fast(10ms,2h) < mid(10ms,4h) < slow(30ms).
	wantHops := []int{2, 4, 2}
	wantLat := []time.Duration{10 * ms, 10 * ms, 30 * ms}
	for i, c := range natives {
		if c.Label != wantLabels[i] {
			t.Errorf("native[%d] label = %q, want %q", i, c.Label, wantLabels[i])
		}
		if c.Family != FamilySCION || c.Path == nil {
			t.Fatalf("native[%d] = %+v, want SCION with pinned path", i, c)
		}
		if c.Path.Hops != wantHops[i] || c.Path.Latency != wantLat[i] {
			t.Errorf("native[%d] hops=%d lat=%v, want hops=%d lat=%v",
				i, c.Path.Hops, c.Path.Latency, wantHops[i], wantLat[i])
		}
		if _, ok := c.Path.SNET.(snet.Path); !ok {
			t.Errorf("native[%d] SNET is %T, want snet.Path", i, c.Path.SNET)
		}
	}
}

func TestExpandSCIONNoPathsDrops(t *testing.T) {
	q := &fakeQuerier{paths: nil}
	withQuerier(t, q, nil)
	tl := &Timeline{}
	out := ExpandSCION(context.Background(), []Candidate{protoCand()}, DialerOptions{Timeline: tl})
	if len(out) != 0 {
		t.Fatalf("got %d candidates, want 0", len(out))
	}
	if !hasFailNote(tl, "scion:1-150,10.20.3.216") {
		t.Fatal("expected a Timeline fail note for the dropped SCION candidate")
	}
}

func TestExpandSCIONScitraFallback(t *testing.T) {
	withQuerier(t, nil, errors.New("no daemon"))
	setRoute(t, true)

	ipc := Candidate{Family: FamilyIPv4, Host: "203.0.113.1", Port: 443, Label: "v4:203.0.113.1"}
	out := ExpandSCION(context.Background(), []Candidate{protoCand(), ipc}, DialerOptions{})

	if len(out) != 2 {
		t.Fatalf("got %d candidates, want 2 (passthrough + scitra): %v", len(out), labels(out))
	}
	if out[0].Label != "v4:203.0.113.1" {
		t.Fatalf("out[0] = %q, want passthrough", out[0].Label)
	}
	sc := out[1]
	if !sc.ViaScitra || sc.Family != FamilyIPv6 {
		t.Fatalf("scitra candidate = %+v, want ViaScitra IPv6", sc)
	}
	if sc.Host != "fc00:1000:9600::ffff:a14:3d8" {
		t.Fatalf("scitra host = %q, want mapped fc00:1000:9600::ffff:a14:3d8", sc.Host)
	}
	if sc.Port != 443 || len(sc.ALPN) != 1 || sc.ALPN[0] != "h3" {
		t.Fatalf("scitra candidate lost port/ALPN: %+v", sc)
	}
	if sc.Path != nil {
		t.Fatalf("scitra candidate must have no SCION path, got %+v", sc.Path)
	}
}

func TestExpandSCIONNoDaemonNoScitraDrops(t *testing.T) {
	withQuerier(t, nil, errors.New("no daemon"))
	setRoute(t, false)
	tl := &Timeline{}
	out := ExpandSCION(context.Background(), []Candidate{protoCand()}, DialerOptions{Timeline: tl})
	if len(out) != 0 {
		t.Fatalf("got %d candidates, want 0", len(out))
	}
	if !hasFailNote(tl, "scion:1-150,10.20.3.216") {
		t.Fatal("expected a Timeline fail note when no daemon and no scitra route")
	}
}

// setRoute points procNetIPv6Route at a fixture with or without an fc00::/8 entry.
func setRoute(t *testing.T, present bool) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ipv6_route")
	content := "00000000000000000000000000000000 00 00000000000000000000000000000000 00 " +
		"00000000000000000000000000000000 ffffffff 00000000 00000000 00000000 lo\n"
	if present {
		content += "fc000000000000000000000000000000 08 00000000000000000000000000000000 00 " +
			"00000000000000000000000000000000 00000064 00000001 00000000 00000001 sci01\n"
	}
	writeFile(t, path, content)
	orig := procNetIPv6Route
	procNetIPv6Route = path
	t.Cleanup(func() { procNetIPv6Route = orig })
}

func hasFailNote(tl *Timeline, label string) bool {
	for _, e := range tl.Events() {
		if e.Kind == "fail" && e.Label == label {
			return true
		}
	}
	return false
}
