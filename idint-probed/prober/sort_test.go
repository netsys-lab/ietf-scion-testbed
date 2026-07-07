package prober_test

import (
	"testing"
	"time"

	"github.com/netsys-lab/ietf-scion-testbed/idint-probed/prober"
	"github.com/scionproto/scion/pkg/addr"
	"github.com/scionproto/scion/pkg/snet"
	spath "github.com/scionproto/scion/pkg/snet/path"
)

// mkLatPath builds a synthetic path; mtu doubles as a marker for asserts.
func mkLatPath(mtu uint16, ifaces []snet.PathInterface, lat []time.Duration) snet.Path {
	return spath.Path{Meta: snet.PathMetadata{
		Interfaces: ifaces,
		MTU:        mtu,
		Latency:    lat,
	}}
}

func mtus(paths []snet.Path) []uint16 {
	out := make([]uint16, len(paths))
	for i, p := range paths {
		out[i] = p.Metadata().MTU
	}
	return out
}

func TestSortPathsByAdvertisedLatency(t *testing.T) {
	// sciond order: shaped (slow) path first — must sort after the fast ones.
	shaped := mkLatPath(1, []snet.PathInterface{
		{IA: addr.MustParseIA("1-150"), ID: 1},
		{IA: addr.MustParseIA("1-154"), ID: 2},
	}, []time.Duration{69800 * time.Microsecond})
	fast := mkLatPath(2, []snet.PathInterface{
		{IA: addr.MustParseIA("1-150"), ID: 3},
		{IA: addr.MustParseIA("1-155"), ID: 4},
		{IA: addr.MustParseIA("1-155"), ID: 5},
		{IA: addr.MustParseIA("1-154"), ID: 6},
	}, []time.Duration{6 * time.Millisecond, 400 * time.Microsecond, 6500 * time.Microsecond})
	mid := mkLatPath(3, []snet.PathInterface{
		{IA: addr.MustParseIA("1-150"), ID: 7},
		{IA: addr.MustParseIA("1-156"), ID: 8},
		{IA: addr.MustParseIA("1-156"), ID: 9},
		{IA: addr.MustParseIA("1-154"), ID: 10},
	}, []time.Duration{8 * time.Millisecond, 400 * time.Microsecond, 8500 * time.Microsecond})

	paths := []snet.Path{shaped, mid, fast}
	prober.SortPaths(paths)

	if got, want := mtus(paths), []uint16{2, 3, 1}; !equalU16(got, want) {
		t.Errorf("order (by marker MTU) = %v, want %v (fast, mid, shaped)", got, want)
	}
}

func TestSortPathsUnsetLatencySortsLast(t *testing.T) {
	// Shortest path, but one latency entry unset (-1): sorts after all
	// fully-annotated paths.
	unset := mkLatPath(1, []snet.PathInterface{
		{IA: addr.MustParseIA("1-150"), ID: 1},
		{IA: addr.MustParseIA("1-154"), ID: 2},
	}, []time.Duration{-1})
	slowButAnnotated := mkLatPath(2, []snet.PathInterface{
		{IA: addr.MustParseIA("1-150"), ID: 3},
		{IA: addr.MustParseIA("1-155"), ID: 4},
		{IA: addr.MustParseIA("1-155"), ID: 5},
		{IA: addr.MustParseIA("1-154"), ID: 6},
	}, []time.Duration{50 * time.Millisecond, time.Millisecond, 50 * time.Millisecond})

	paths := []snet.Path{unset, slowButAnnotated}
	prober.SortPaths(paths)

	if got, want := mtus(paths), []uint16{2, 1}; !equalU16(got, want) {
		t.Errorf("order (by marker MTU) = %v, want %v (annotated before unset)", got, want)
	}
}

func TestSortPathsTiebreakFewerInterfaces(t *testing.T) {
	// Equal totals (10ms): the 2-interface path beats the 4-interface path.
	long := mkLatPath(1, []snet.PathInterface{
		{IA: addr.MustParseIA("1-150"), ID: 1},
		{IA: addr.MustParseIA("1-155"), ID: 2},
		{IA: addr.MustParseIA("1-155"), ID: 3},
		{IA: addr.MustParseIA("1-154"), ID: 4},
	}, []time.Duration{5 * time.Millisecond, 0, 5 * time.Millisecond})
	short := mkLatPath(2, []snet.PathInterface{
		{IA: addr.MustParseIA("1-150"), ID: 5},
		{IA: addr.MustParseIA("1-154"), ID: 6},
	}, []time.Duration{10 * time.Millisecond})

	paths := []snet.Path{long, short}
	prober.SortPaths(paths)

	if got, want := mtus(paths), []uint16{2, 1}; !equalU16(got, want) {
		t.Errorf("order (by marker MTU) = %v, want %v (fewer interfaces first)", got, want)
	}
}

func equalU16(a, b []uint16) bool {
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
