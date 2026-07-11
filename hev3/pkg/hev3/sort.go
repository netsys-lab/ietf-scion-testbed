package hev3

import "sort"

// Sort orders cands per draft-ietf-happy-happyeyeballs-v3-04 §5, extended
// with SCION as a third address family (companion spec
// docs/superpowers/specs/2026-07-10-scion-svcb-hev3-design.md).
//
// preferredFamilyCount is how many candidates the preferred family gets
// per interleave round before ceding a turn to each other family (§5.2,
// generalizing RFC 8305 §4's IPv6-preference interleave).
func Sort(cands []Candidate, nativeSCION bool, preferredFamilyCount int) []Candidate {
	if len(cands) == 0 {
		return nil
	}
	if preferredFamilyCount < 1 {
		preferredFamilyCount = 1
	}

	groups := groupByALPN(cands)

	// §5.1: groups sort ascending by their best (lowest) member Priority.
	sort.SliceStable(groups, func(i, j int) bool {
		return groups[i].rank < groups[j].rank
	})

	out := make([]Candidate, 0, len(cands))
	for _, g := range groups {
		out = append(out, sortGroup(g.members, nativeSCION, preferredFamilyCount)...)
	}
	return out
}

type alpnGroup struct {
	members []Candidate
	rank    uint16 // best (lowest) member Priority
}

// groupByALPN buckets candidates whose ALPN slice is set-equal (order and
// duplicates ignored — §4.1 SVCB ALPN is a set), preserving first-seen
// group order so equal-rank groups tie-break by input order (§5.1).
func groupByALPN(cands []Candidate) []*alpnGroup {
	index := make(map[string]*alpnGroup, len(cands))
	var order []*alpnGroup
	for _, c := range cands {
		key := alpnKey(c.ALPN)
		g, ok := index[key]
		if !ok {
			g = &alpnGroup{rank: c.Priority}
			index[key] = g
			order = append(order, g)
		} else if c.Priority < g.rank {
			g.rank = c.Priority
		}
		g.members = append(g.members, c)
	}
	return order
}

func alpnKey(alpn []string) string {
	if len(alpn) == 0 {
		return ""
	}
	sorted := append([]string(nil), alpn...)
	sort.Strings(sorted)
	key := sorted[0]
	for _, p := range sorted[1:] {
		key += "," + p
	}
	return key
}

// sortGroup orders one ALPN group: §5.1 ascending Priority (with a
// companion-spec ViaScitra tie-break), then the §5.2 family interleave.
func sortGroup(members []Candidate, nativeSCION bool, preferredFamilyCount int) []Candidate {
	sorted := append([]Candidate(nil), members...)

	// §5.1 ascending Priority. Secondary key: ViaScitra false before true,
	// so scitra-mapped IPv6 legs tie-break after real IPv6 candidates
	// (companion spec — not in the base draft, which has no scitra
	// concept). Equal-key pairs fall through unordered, so SliceStable
	// preserves their original relative order — this also implements the
	// requirement that same-address SCION path sub-candidates keep their
	// pre-ranked order, since they share Priority and ViaScitra=false.
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Priority != sorted[j].Priority {
			return sorted[i].Priority < sorted[j].Priority
		}
		return !sorted[i].ViaScitra && sorted[j].ViaScitra
	})

	buckets := make(map[Family][]Candidate, 3)
	for _, c := range sorted {
		buckets[c.Family] = append(buckets[c.Family], c)
	}

	// §5.2: family preference order. A native SCION stack tries SCION
	// first, then IPv6, then IPv4. Without native SCION, IPv6 then IPv4
	// (ViaScitra candidates are FamilyIPv6, so they interleave normally
	// here); any FamilySCION candidate that shouldn't exist in this case
	// is appended last rather than interleaved.
	var order, trailing []Family
	if nativeSCION {
		order = []Family{FamilySCION, FamilyIPv6, FamilyIPv4}
	} else {
		order = []Family{FamilyIPv6, FamilyIPv4}
		trailing = []Family{FamilySCION}
	}

	out := make([]Candidate, 0, len(sorted))
	for hasRemaining(buckets, order) {
		// Classic HEv3 interleave (RFC 8305 §4), generalized to N
		// families: the preferred family gets preferredFamilyCount picks
		// per round, every other family gets exactly one.
		out = append(out, take(buckets, order[0], preferredFamilyCount)...)
		for _, f := range order[1:] {
			out = append(out, take(buckets, f, 1)...)
		}
	}
	for _, f := range trailing {
		out = append(out, buckets[f]...)
	}
	return out
}

func hasRemaining(buckets map[Family][]Candidate, order []Family) bool {
	for _, f := range order {
		if len(buckets[f]) > 0 {
			return true
		}
	}
	return false
}

// take removes and returns up to n candidates from the front of buckets[f].
func take(buckets map[Family][]Candidate, f Family, n int) []Candidate {
	b := buckets[f]
	if n > len(b) {
		n = len(b)
	}
	taken := b[:n]
	buckets[f] = b[n:]
	return taken
}
