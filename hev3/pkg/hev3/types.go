// Package hev3 implements a SCION-aware Happy Eyeballs v3 dialer, per
// draft-ietf-happy-happyeyeballs-v3-04 extended with SCION as a third
// address family (see docs/superpowers/specs/2026-07-10-scion-svcb-hev3-design.md).
package hev3

import "time"

// Family identifies the address/transport family a Candidate belongs to.
type Family int

const (
	FamilySCION Family = iota
	FamilyIPv6
	FamilyIPv4
)

// SCIONPath describes one ranked SCION path to a destination.
type SCIONPath struct {
	Fingerprint string
	Latency     time.Duration
	Hops        int
	SNET        any // holds snet.Path; typed as any so pure-logic packages need no scionproto import
}

// Candidate is one dialable destination: an IP address+port, or a SCION
// address+path. Multiple Candidates may share the same IA/Host when they
// represent distinct ranked SCION paths (see Path).
type Candidate struct {
	Family    Family
	Host      string // IP literal, or SCION host IP
	Port      uint16
	IA        string     // "1-150"; empty for IP families
	Path      *SCIONPath // nil for IP families and the scitra leg
	ViaScitra bool       // SCION-derived candidate dialed as mapped IPv6
	ALPN      []string   // from SVCB; empty = default (https ⇒ try h2/h1.1 TCP)
	Priority  uint16     // SvcPriority
	Label     string     // stable human id, e.g. "scion:1-150#p1", "v6:2001:db8::1", "v4+tcp:…"
}
