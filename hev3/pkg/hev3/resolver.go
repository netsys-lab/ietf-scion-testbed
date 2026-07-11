package hev3

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
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
// late-arriving SVCB) complete — draft §4.3 mid-race merge. Updates is
// closed once every outstanding query has resolved one way or another.
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

	// release checks the §4.2 gate — (SVCB done or delay elapsed) AND both
	// AAAA and A done — and, once cleared, emits Initial on first clearance
	// or an Update whenever the merged set subsequently changes (§4.3).
	release := func() {
		if !aaaaFinal || !aFinal {
			return
		}
		if !svcbFinal && !delayElapsed {
			return
		}
		cands := r.buildCandidates(svc, aaaaIPs, aIPs)
		if !sent {
			sent = true
			r.emitNewCandidates(nil, cands)
			prev = cands
			r.initial <- cands
			return
		}
		if !reflect.DeepEqual(prev, cands) {
			r.emitNewCandidates(prev, cands)
			prev = cands
			r.updates <- cands
		}
	}

	for !(sent && svcbFinal) {
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
	visited := map[string]bool{}
	for step := 0; step < maxAliasChase; step++ {
		if visited[name] {
			return nil, fmt.Errorf("hev3: resolve: SVCB alias loop at %s", name)
		}
		visited[name] = true

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
			if target == name {
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
// answers (RFC 9460 §7.3: hints are a fallback, not a primary answer).
func (r *resolveRun) buildCandidates(svc *dns.SVCB, aaaaIPs, aIPs []net.IP) []Candidate {
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
	if len(aaaaIPs) == 0 {
		for _, ip := range hint6 {
			out = append(out, ipCandidate(FamilyIPv6, "v6hint:", ip, port, alpn, priority))
		}
	}

	for _, ip := range aIPs {
		out = append(out, ipCandidate(FamilyIPv4, "v4:", ip, port, alpn, priority))
	}
	if len(aIPs) == 0 {
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

// scionIA derives the "ISD-ASN" Candidate.IA from a SCIONAddr's canonical
// "ISD-ASN,host" presentation form.
func scionIA(addr dns.SCIONAddr) string {
	ia, _, _ := strings.Cut(addr.String(), ",")
	return ia
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
