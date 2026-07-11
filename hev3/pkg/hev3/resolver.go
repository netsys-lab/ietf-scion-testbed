package hev3

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"time"

	"github.com/miekg/dns"
)

// defaultResolutionDelay is how long ResolveHost waits for the SVCB answer
// before releasing Initial candidates built from AAAA/A alone — draft
// -happy-happyeyeballs-v3 §4.2 "Resolution Delay". SVCB is one extra round
// trip beyond the mandatory AAAA/A lookups, so it alone is time-boxed; AAAA
// and A are always awaited to completion (bounded only by queryTimeout).
const defaultResolutionDelay = 50 * time.Millisecond

// queryTimeout bounds a single DNS exchange (SVCB, AAAA, A, or one alias
// chase step). A query that times out or errors is non-fatal: it simply
// yields no candidates for that RR type/step (draft §4.1 fallback intent).
const queryTimeout = 2 * time.Second

// maxAliasChase caps SVCB AliasMode (SvcPriority 0, RFC 9460 §2.4.2) target
// following, guarding against loops or unreasonably long alias chains.
const maxAliasChase = 4

// ResolveOptions tunes ResolveHost. The zero value is valid.
type ResolveOptions struct {
	Resolver        string        // "ip:53"; empty ⇒ system resolver via /etc/resolv.conf
	ResolutionDelay time.Duration // 0 ⇒ defaultResolutionDelay (50ms)
	Timeline        *Timeline     // optional; records query/answer/candidate events
}

// Resolved is the outcome of ResolveHost: an Initial candidate set (released
// once the §4.2 Resolution Delay gate clears) plus an Updates channel that
// delivers later, merged candidate sets as slower answers (chiefly a
// late-arriving SVCB or the non-preferred address family) complete — draft
// §4.3 mid-race merge. Updates is closed once every outstanding query has
// resolved one way or another.
//
// A caller must keep draining Updates until it closes, or cancel ctx: the
// internal goroutine that resolved Initial holds a query outstanding (SVCB,
// AAAA, or A) until it completes and then blocks trying to send the merged
// result on Updates. That send only has a ctx.Done() escape, not a
// reader-dropped one, so an abandoned Updates channel whose ctx is never
// cancelled delays that goroutine's cleanup until ctx ends on its own (e.g.
// a caller-supplied deadline), not until the caller stops reading.
type Resolved struct {
	Initial []Candidate
	Updates <-chan []Candidate
}

// ResolveHost issues SVCB, AAAA and A queries for host in parallel against a
// single resolver and turns the answers into Candidates. SCION candidates
// come from a ServiceMode SVCB record's "scion" SvcParam (one Candidate per
// SCIONAddr, Path left nil — path expansion is a later stage); ALPN/port/
// priority on every Candidate the record covers come from that SVCB record.
// AliasMode SVCB records (SvcPriority 0) are followed by re-querying the
// TargetName (RFC 9460 §2.4.2), up to maxAliasChase steps.
func ResolveHost(ctx context.Context, host string, port uint16, o ResolveOptions) (Resolved, error) {
	server, err := resolverAddress(o.Resolver)
	if err != nil {
		return Resolved{}, err
	}
	delay := o.ResolutionDelay
	if delay <= 0 {
		delay = defaultResolutionDelay
	}

	r := &resolveRun{
		ctx:     ctx,
		server:  server,
		host:    dns.Fqdn(host),
		port:    port,
		tl:      o.Timeline,
		initial: make(chan []Candidate, 1),
		updates: make(chan []Candidate),
	}
	go r.run(delay)

	select {
	case cands := <-r.initial:
		return Resolved{Initial: cands, Updates: r.updates}, nil
	case <-ctx.Done():
		return Resolved{}, ctx.Err()
	}
}

// resolverAddress resolves ResolveOptions.Resolver to a "host:port" dial
// target, falling back to the first nameserver in /etc/resolv.conf when
// unset.
func resolverAddress(resolver string) (string, error) {
	if resolver != "" {
		return resolver, nil
	}
	cc, err := dns.ClientConfigFromFile("/etc/resolv.conf")
	if err != nil {
		return "", fmt.Errorf("hev3: resolve: reading /etc/resolv.conf: %w", err)
	}
	if len(cc.Servers) == 0 {
		return "", errors.New("hev3: resolve: /etc/resolv.conf has no nameserver")
	}
	return net.JoinHostPort(cc.Servers[0], cc.Port), nil
}

// resolveRun holds the state of one ResolveHost call across its SVCB, AAAA
// and A queries and any SVCB alias chase.
type resolveRun struct {
	ctx    context.Context
	server string
	host   string
	port   uint16
	tl     *Timeline

	initial chan []Candidate
	updates chan []Candidate
}

type svcbResult struct {
	svc *dns.SVCB
	err error
}

type ipResult struct {
	ips []net.IP
	err error
}

// run drives the three parallel queries to completion, releases Initial as
// soon as the §4.2 gate clears, and streams any subsequent change to the
// merged candidate set on Updates until nothing is left outstanding.
func (r *resolveRun) run(delay time.Duration) {
	defer close(r.updates)

	svcbCh := make(chan svcbResult, 1)
	aaaaCh := make(chan ipResult, 1)
	aCh := make(chan ipResult, 1)

	go func() {
		svc, err := r.resolveSVCB()
		svcbCh <- svcbResult{svc, err}
	}()
	go func() {
		ips, err := r.resolveIPs(dns.TypeAAAA)
		aaaaCh <- ipResult{ips, err}
	}()
	go func() {
		ips, err := r.resolveIPs(dns.TypeA)
		aCh <- ipResult{ips, err}
	}()

	var (
		svc                          *dns.SVCB
		aaaaIPs, aIPs                []net.IP
		svcbFinal, aaaaFinal, aFinal bool
		delayElapsed                 bool
		sent                         bool
		prev                         []Candidate
	)

	timer := time.NewTimer(delay)
	defer timer.Stop()

	// release checks the §4.2 gate: proceed once at least one positive
	// address answer has been received AND EITHER (a) the preferred family
	// (AAAA) has a final answer and SVCB service info is complete, OR (b)
	// the Resolution Delay has elapsed — draft-ietf-happy-happyeyeballs-v3
	// -04 §4.2. Notably A is never itself a gate requirement: it is only a
	// possible source of the "at least one positive address answer" (path
	// (b)), never required to be final. Once cleared, release emits Initial
	// on first clearance or an Update whenever the merged set subsequently
	// changes (§4.3). The Updates send escapes via ctx.Done() so a caller
	// that cancels ctx is never blocked on it (see the Resolved doc
	// comment for the abandon-without-cancel case, which is intentionally
	// not covered by this escape).
	release := func() {
		hasPositiveAddrs := (aaaaFinal && len(aaaaIPs) > 0) || (aFinal && len(aIPs) > 0)
		if !hasPositiveAddrs {
			return
		}
		if !(aaaaFinal && svcbFinal) && !delayElapsed {
			return
		}
		cands := r.buildCandidates(svc, aaaaIPs, aIPs, aaaaFinal, aFinal)
		if !sent {
			sent = true
			r.emitNewCandidates(nil, cands)
			prev = cands
			r.initial <- cands // buffered cap 1; never blocks
			return
		}
		if reflect.DeepEqual(prev, cands) {
			return
		}
		r.emitNewCandidates(prev, cands)
		prev = cands
		select {
		case r.updates <- cands:
		case <-r.ctx.Done():
		}
	}

	// The loop runs until every query (SVCB, AAAA, A) has settled, not
	// merely until Initial has been sent: under the §4.2 gate above,
	// Initial can release before A (or, via the delay path, before AAAA)
	// is final, and the still-outstanding query's eventual answer must
	// still reach Updates as a merge (§4.3) rather than being silently
	// dropped by an early exit.
	for !(svcbFinal && aaaaFinal && aFinal) {
		select {
		case res := <-svcbCh:
			svc, svcbFinal = res.svc, true
			release()
		case res := <-aaaaCh:
			aaaaIPs, aaaaFinal = res.ips, true
			release()
		case res := <-aCh:
			aIPs, aFinal = res.ips, true
			release()
		case <-timer.C:
			delayElapsed = true
			release()
		case <-r.ctx.Done():
			return
		}
	}
}

// resolveSVCB queries the SVCB RRset for r.host, following AliasMode
// (SvcPriority 0) records up to maxAliasChase steps (RFC 9460 §2.4.2). It
// returns (nil, nil) when there is no SVCB record at all, and a non-nil
// error only for transport failures/timeouts or a malformed chase (self-
// alias, loop, chase exhausted) — all non-fatal to the caller, which simply
// proceeds without SCION/hint candidates.
func (r *resolveRun) resolveSVCB() (*dns.SVCB, error) {
	name := r.host
	// visited and the self-alias check below key on dns.CanonicalName
	// (lowercase + Fqdn), not the raw literal name: DNS names are
	// case-insensitive (RFC 4343), so an AliasMode chain that revisits an
	// earlier name in different letter case is still a loop and must be
	// caught, not requeried as if it were a distinct name.
	visited := map[string]bool{}
	for step := 0; step < maxAliasChase; step++ {
		key := dns.CanonicalName(name)
		if visited[key] {
			return nil, fmt.Errorf("hev3: resolve: SVCB alias loop at %s", name)
		}
		visited[key] = true

		msg, err := r.exchange(name, dns.TypeSVCB)
		if err != nil {
			return nil, err
		}
		rr := firstSVCB(msg)
		if rr == nil {
			return nil, nil
		}
		if rr.Priority == 0 { // AliasMode
			target := dns.Fqdn(rr.Target)
			if dns.CanonicalName(target) == key {
				return nil, fmt.Errorf("hev3: resolve: SVCB alias to self at %s", name)
			}
			name = target
			continue
		}
		return rr, nil // ServiceMode
	}
	return nil, fmt.Errorf("hev3: resolve: SVCB alias chase exceeded %d steps", maxAliasChase)
}

// resolveIPs queries qtype (AAAA or A) for r.host and extracts the answer
// addresses.
func (r *resolveRun) resolveIPs(qtype uint16) ([]net.IP, error) {
	msg, err := r.exchange(r.host, qtype)
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	for _, rr := range msg.Answer {
		switch v := rr.(type) {
		case *dns.AAAA:
			ips = append(ips, v.AAAA)
		case *dns.A:
			ips = append(ips, v.A)
		}
	}
	return ips, nil
}

// exchange sends one query and waits for the reply (or queryTimeout/ctx
// cancellation), recording "query"/"answer" Timeline events around it.
func (r *resolveRun) exchange(name string, qtype uint16) (*dns.Msg, error) {
	m := new(dns.Msg)
	m.SetQuestion(name, qtype)

	label := queryLabel(name, qtype)
	if r.tl != nil {
		r.tl.Add("query", label, "")
	}

	c := &dns.Client{Net: "udp", Timeout: queryTimeout}
	resp, _, err := c.ExchangeContext(r.ctx, m, r.server)

	if r.tl != nil {
		detail := ""
		if err != nil {
			detail = err.Error()
		}
		r.tl.Add("answer", label, detail)
	}
	return resp, err
}

// buildCandidates turns the current SVCB record and AAAA/A answers into
// Candidates. ALPN/port/priority come from svc (nil ⇒ caller's default port,
// empty ALPN, priority 0) and apply uniformly to every Candidate it covers.
// ipv4hint/ipv6hint only contribute Candidates for a family with zero real
// answers (RFC 9460 §7.3: hints are a fallback, not a primary answer) AND
// whose query has actually completed (aaaaFinal/aFinal): since the §4.2
// gate can now release before one family is final (it is simply not yet
// known, not negatively answered), hints must not be used as a stand-in for
// "haven't heard back yet" — only for a family confirmed to have no real
// answer.
func (r *resolveRun) buildCandidates(svc *dns.SVCB, aaaaIPs, aIPs []net.IP, aaaaFinal, aFinal bool) []Candidate {
	port := r.port
	var alpn []string
	var priority uint16
	var scionAddrs []dns.SCIONAddr
	var hint4, hint6 []net.IP

	if svc != nil {
		priority = svc.Priority
		for _, v := range svc.Value {
			switch kv := v.(type) {
			case *dns.SVCBPort:
				port = kv.Port
			case *dns.SVCBAlpn:
				alpn = kv.Alpn
			case *dns.SVCBScion:
				scionAddrs = kv.Addrs
			case *dns.SVCBIPv4Hint:
				hint4 = kv.Hint
			case *dns.SVCBIPv6Hint:
				hint6 = kv.Hint
			}
		}
	}

	var out []Candidate
	for _, addr := range scionAddrs {
		out = append(out, Candidate{
			Family:   FamilySCION,
			Host:     addr.Host.String(),
			Port:     port,
			IA:       scionIA(addr),
			ALPN:     alpn,
			Priority: priority,
			Label:    "scion:" + addr.String(),
		})
	}

	for _, ip := range aaaaIPs {
		out = append(out, ipCandidate(FamilyIPv6, "v6:", ip, port, alpn, priority))
	}
	if aaaaFinal && len(aaaaIPs) == 0 {
		for _, ip := range hint6 {
			out = append(out, ipCandidate(FamilyIPv6, "v6hint:", ip, port, alpn, priority))
		}
	}

	for _, ip := range aIPs {
		out = append(out, ipCandidate(FamilyIPv4, "v4:", ip, port, alpn, priority))
	}
	if aFinal && len(aIPs) == 0 {
		for _, ip := range hint4 {
			out = append(out, ipCandidate(FamilyIPv4, "v4hint:", ip, port, alpn, priority))
		}
	}

	return out
}

func ipCandidate(fam Family, labelPrefix string, ip net.IP, port uint16, alpn []string, priority uint16) Candidate {
	return Candidate{
		Family:   fam,
		Host:     ip.String(),
		Port:     port,
		ALPN:     alpn,
		Priority: priority,
		Label:    labelPrefix + ip.String(),
	}
}

// maxBGPAS is the largest AS number in the classic 32-bit BGP AS-number
// space (scionproto/scion pkg/addr.MaxBGPAS). SCION's canonical ISD-ASN
// presentation format prints AS numbers at or below it in decimal, and
// larger ("SCION-only") AS numbers as three colon-separated 16-bit hex
// groups, e.g. "71-2:0:4a".
const maxBGPAS = (1 << 32) - 1

// scionIA derives the "ISD-ASN" Candidate.IA from a SCIONAddr, formatting
// the AS number per the maxBGPAS rule above. This is computed from
// addr.ISD/addr.ASN directly rather than via addr.String(): SCIONAddr
// remembers whether its AS was written in hex-group form in the zone file,
// but SVCBScion's wire pack/unpack (used for every real DNS answer, not
// just an in-process shortcut) carries only the numeric ASN — the
// hex-vs-decimal preference bit does not survive a real query, so
// addr.String() prints a large hex-range ASN like 0x2_0000_004a as the
// plain decimal "8589934666" once the answer has round-tripped over the
// wire. Recomputing the presentation form here keeps it correct regardless
// of how the answer arrived.
func scionIA(addr dns.SCIONAddr) string {
	if addr.ASN <= maxBGPAS {
		return fmt.Sprintf("%d-%d", addr.ISD, addr.ASN)
	}
	return fmt.Sprintf("%d-%x:%x:%x", addr.ISD, (addr.ASN>>32)&0xffff, (addr.ASN>>16)&0xffff, addr.ASN&0xffff)
}

// emitNewCandidates records a "candidate" Timeline event for every Candidate
// in next that was not already in prev (by Label), so a rebuild that repeats
// already-announced candidates (e.g. AAAA/A carried over into a post-SVCB
// merge) doesn't re-log them.
func (r *resolveRun) emitNewCandidates(prev, next []Candidate) {
	if r.tl == nil {
		return
	}
	seen := make(map[string]bool, len(prev))
	for _, c := range prev {
		seen[c.Label] = true
	}
	for _, c := range next {
		if seen[c.Label] {
			continue
		}
		r.tl.Add("candidate", c.Label, c.IA)
	}
}

func firstSVCB(msg *dns.Msg) *dns.SVCB {
	if msg == nil {
		return nil
	}
	for _, rr := range msg.Answer {
		if svc, ok := rr.(*dns.SVCB); ok {
			return svc
		}
	}
	return nil
}

func queryLabel(name string, qtype uint16) string {
	return dns.TypeToString[qtype] + " " + name
}
