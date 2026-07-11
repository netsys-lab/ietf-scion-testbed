package hev3

import (
	"reflect"
	"testing"
)

func labels(cands []Candidate) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.Label
	}
	return out
}

// worked example, brief step 2: {v4a,v6a,scion-p1,scion-p2,v6b}, nativeSCION=true,
// preferredFamilyCount=1 -> SCION,IPv6,IPv4 preference, round-robin interleave.
func TestSort_FamilyInterleave_NativeSCION(t *testing.T) {
	v4a := Candidate{Family: FamilyIPv4, Host: "203.0.113.1", Priority: 1, Label: "v4a"}
	v6a := Candidate{Family: FamilyIPv6, Host: "2001:db8::1", Priority: 1, Label: "v6a"}
	v6b := Candidate{Family: FamilyIPv6, Host: "2001:db8::2", Priority: 1, Label: "v6b"}
	scionP1 := Candidate{Family: FamilySCION, IA: "1-150", Priority: 1, Label: "scion-p1", Path: &SCIONPath{Fingerprint: "p1"}}
	scionP2 := Candidate{Family: FamilySCION, IA: "1-150", Priority: 1, Label: "scion-p2", Path: &SCIONPath{Fingerprint: "p2"}}

	in := []Candidate{v4a, v6a, scionP1, scionP2, v6b}
	got := Sort(in, true, 1)

	want := []string{"scion-p1", "v6a", "v4a", "scion-p2", "v6b"}
	if g := labels(got); !reflect.DeepEqual(g, want) {
		t.Fatalf("got %v, want %v", g, want)
	}
}

// §4.2/§5.1: candidates group by identical ALPN set (order-independent);
// a group's rank is its best (lowest) member Priority; groups sort ascending
// by rank; within a group members sort ascending by Priority.
func TestSort_ALPNGroupingAndPriorityOrder(t *testing.T) {
	// Group A: ALPN {h3} — best priority 1 (via a) -> group rank 1.
	a := Candidate{Family: FamilyIPv4, Host: "203.0.113.10", Priority: 1, ALPN: []string{"h3"}, Label: "a"}
	b := Candidate{Family: FamilyIPv4, Host: "203.0.113.11", Priority: 5, ALPN: []string{"h3"}, Label: "b"}
	// Group B: ALPN {h2,h3} (order-independent set, given here reversed vs a "canonical" order)
	// -> best priority 2, group rank 2, sorts after group A.
	c := Candidate{Family: FamilyIPv4, Host: "203.0.113.12", Priority: 2, ALPN: []string{"h3", "h2"}, Label: "c"}
	d := Candidate{Family: FamilyIPv4, Host: "203.0.113.13", Priority: 4, ALPN: []string{"h2", "h3"}, Label: "d"}

	// Feed in scrambled/interleaved order to prove grouping, not input order, drives result.
	in := []Candidate{d, b, a, c}
	got := Sort(in, false, 1)

	want := []string{"a", "b", "c", "d"}
	if g := labels(got); !reflect.DeepEqual(g, want) {
		t.Fatalf("got %v, want %v", g, want)
	}
}

// Multiple SCION path sub-candidates of the SAME address arrive pre-ranked
// (best path first); the sorter must preserve that relative order.
func TestSort_SCIONPathOrderPreserved(t *testing.T) {
	p1 := Candidate{Family: FamilySCION, IA: "1-150", Priority: 1, Label: "p1", Path: &SCIONPath{Fingerprint: "1"}}
	p2 := Candidate{Family: FamilySCION, IA: "1-150", Priority: 1, Label: "p2", Path: &SCIONPath{Fingerprint: "2"}}
	p3 := Candidate{Family: FamilySCION, IA: "1-150", Priority: 1, Label: "p3", Path: &SCIONPath{Fingerprint: "3"}}

	got := Sort([]Candidate{p1, p2, p3}, true, 1)
	want := []string{"p1", "p2", "p3"}
	if g := labels(got); !reflect.DeepEqual(g, want) {
		t.Fatalf("got %v, want %v", g, want)
	}
}

// nativeSCION=false: preference order is IPv6, IPv4 (no SCION-first).
func TestSort_NonNativeSCION_IPv6First(t *testing.T) {
	v4a := Candidate{Family: FamilyIPv4, Host: "203.0.113.1", Priority: 1, Label: "v4a"}
	v6a := Candidate{Family: FamilyIPv6, Host: "2001:db8::1", Priority: 1, Label: "v6a"}

	got := Sort([]Candidate{v4a, v6a}, false, 1)
	want := []string{"v6a", "v4a"}
	if g := labels(got); !reflect.DeepEqual(g, want) {
		t.Fatalf("got %v, want %v", g, want)
	}
}

// ViaScitra candidates dial as IPv6 (belong to FamilyIPv6 for interleaving)
// but must tie-break after real IPv6 candidates at equal priority.
func TestSort_ScitraTiebreakAfterRealIPv6(t *testing.T) {
	scitra := Candidate{Family: FamilyIPv6, Host: "fc00:961:9600::1", Priority: 1, ViaScitra: true, Label: "scitra"}
	realV6 := Candidate{Family: FamilyIPv6, Host: "2001:db8::1", Priority: 1, ViaScitra: false, Label: "realv6"}
	v4a := Candidate{Family: FamilyIPv4, Host: "203.0.113.1", Priority: 1, Label: "v4a"}

	// Feed scitra before realV6 to prove the tie-break reorders them.
	got := Sort([]Candidate{scitra, realV6, v4a}, false, 1)
	want := []string{"realv6", "v4a", "scitra"}
	if g := labels(got); !reflect.DeepEqual(g, want) {
		t.Fatalf("got %v, want %v", g, want)
	}
}

// nativeSCION=false with a stray SCION candidate present (should not happen
// in practice, but the sorter must not drop or crash on it): sorts last.
func TestSort_NonNativeSCION_StraySCIONSortsLast(t *testing.T) {
	v4a := Candidate{Family: FamilyIPv4, Host: "203.0.113.1", Priority: 1, Label: "v4a"}
	v6a := Candidate{Family: FamilyIPv6, Host: "2001:db8::1", Priority: 1, Label: "v6a"}
	stray := Candidate{Family: FamilySCION, IA: "1-150", Priority: 1, Label: "stray", Path: &SCIONPath{Fingerprint: "x"}}

	got := Sort([]Candidate{stray, v4a, v6a}, false, 1)
	want := []string{"v6a", "v4a", "stray"}
	if g := labels(got); !reflect.DeepEqual(g, want) {
		t.Fatalf("got %v, want %v", g, want)
	}
}

// Sort must be stable: candidates with identical group/priority/family/
// ViaScitra keys keep their relative input order.
func TestSort_StableForEqualKeys(t *testing.T) {
	a := Candidate{Family: FamilyIPv4, Host: "203.0.113.1", Priority: 3, Label: "a"}
	b := Candidate{Family: FamilyIPv4, Host: "203.0.113.2", Priority: 3, Label: "b"}
	c := Candidate{Family: FamilyIPv4, Host: "203.0.113.3", Priority: 3, Label: "c"}

	got := Sort([]Candidate{a, b, c}, true, 1)
	want := []string{"a", "b", "c"}
	if g := labels(got); !reflect.DeepEqual(g, want) {
		t.Fatalf("got %v, want %v", g, want)
	}
}

// preferredFamilyCount > 1: the preferred family gets N picks per round
// before the round robins through the remaining families once each.
func TestSort_PreferredFamilyCountAboveOne(t *testing.T) {
	s1 := Candidate{Family: FamilySCION, IA: "1-150", Priority: 1, Label: "s1", Path: &SCIONPath{Fingerprint: "1"}}
	s2 := Candidate{Family: FamilySCION, IA: "1-150", Priority: 1, Label: "s2", Path: &SCIONPath{Fingerprint: "2"}}
	s3 := Candidate{Family: FamilySCION, IA: "1-150", Priority: 1, Label: "s3", Path: &SCIONPath{Fingerprint: "3"}}
	v6a := Candidate{Family: FamilyIPv6, Host: "2001:db8::1", Priority: 1, Label: "v6a"}
	v4a := Candidate{Family: FamilyIPv4, Host: "203.0.113.1", Priority: 1, Label: "v4a"}

	got := Sort([]Candidate{v4a, v6a, s1, s2, s3}, true, 2)
	want := []string{"s1", "s2", "v6a", "v4a", "s3"}
	if g := labels(got); !reflect.DeepEqual(g, want) {
		t.Fatalf("got %v, want %v", g, want)
	}
}

// Empty and single-candidate inputs must not panic.
func TestSort_EmptyAndSingle(t *testing.T) {
	if got := Sort(nil, true, 1); len(got) != 0 {
		t.Fatalf("Sort(nil) = %v, want empty", got)
	}
	only := Candidate{Family: FamilyIPv4, Host: "203.0.113.1", Priority: 1, Label: "only"}
	got := Sort([]Candidate{only}, true, 1)
	if g := labels(got); !reflect.DeepEqual(g, []string{"only"}) {
		t.Fatalf("got %v, want [only]", g)
	}
}
