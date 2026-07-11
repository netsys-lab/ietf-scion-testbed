package hev3

import (
	"context"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// fakeDNS is a minimal in-process nameserver: a per-(qtype,qname) map of
// canned RRs, with an optional injectable per-question response delay for
// exercising Resolution Delay.
type fakeDNS struct {
	addr string

	mu      sync.Mutex
	records map[string][]dns.RR
	delays  map[string]time.Duration
}

func newFakeDNS(t *testing.T) *fakeDNS {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeDNS{records: map[string][]dns.RR{}, delays: map[string]time.Duration{}, addr: pc.LocalAddr().String()}

	mux := dns.NewServeMux()
	mux.HandleFunc(".", f.handle)
	srv := &dns.Server{PacketConn: pc, Handler: mux}
	started := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(started) }
	go srv.ActivateAndServe()
	<-started
	t.Cleanup(func() { srv.Shutdown() })
	return f
}

func fakeKey(name string, qtype uint16) string {
	return dns.TypeToString[qtype] + " " + dns.Fqdn(name)
}

func (f *fakeDNS) set(name string, qtype uint16, rrs ...dns.RR) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[fakeKey(name, qtype)] = rrs
}

func (f *fakeDNS) setDelay(name string, qtype uint16, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delays[fakeKey(name, qtype)] = d
}

func (f *fakeDNS) handle(w dns.ResponseWriter, req *dns.Msg) {
	q := req.Question[0]
	k := fakeKey(q.Name, q.Qtype)
	f.mu.Lock()
	rrs := f.records[k]
	d := f.delays[k]
	f.mu.Unlock()
	if d > 0 {
		time.Sleep(d)
	}
	m := new(dns.Msg)
	m.SetReply(req)
	m.Answer = rrs
	_ = w.WriteMsg(m)
}

func mustRR(t *testing.T, s string) dns.RR {
	t.Helper()
	rr, err := dns.NewRR(s)
	if err != nil {
		t.Fatalf("dns.NewRR(%q): %v", s, err)
	}
	return rr
}

func drainUpdates(ch <-chan []Candidate, timeout time.Duration) ([][]Candidate, bool) {
	var got [][]Candidate
	deadline := time.After(timeout)
	for {
		select {
		case cands, ok := <-ch:
			if !ok {
				return got, true
			}
			got = append(got, cands)
		case <-deadline:
			return got, false
		}
	}
}

// (a) SVCB with scion param + ipv6hint + AAAA + A, all answered promptly:
// candidates carry ALPN/port/priority from SVCB; real AAAA/A answers win
// over the ipv6hint; SCION address is parsed via the typed *dns.SVCBScion.
func TestResolveHost_SVCBWithScionAndRealAnswers(t *testing.T) {
	f := newFakeDNS(t)
	f.set("web.scion.", dns.TypeSVCB, mustRR(t,
		`web.scion. 300 IN SVCB 1 . alpn=h3 port=8443 ipv6hint=2001:db8::9 scion=1-150\,10.20.3.215`))
	f.set("web.scion.", dns.TypeAAAA, mustRR(t, `web.scion. 300 IN AAAA 2001:db8::215`))
	f.set("web.scion.", dns.TypeA, mustRR(t, `web.scion. 300 IN A 10.20.3.215`))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := ResolveHost(ctx, "web.scion", 443, ResolveOptions{Resolver: f.addr})
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}

	if len(res.Initial) == 0 {
		t.Fatalf("Initial = %v, want at least one candidate", labels(res.Initial))
	}

	// SVCB, AAAA and A are all answered promptly with no injected delay, so
	// which pair clears the §4.2 gate first (AAAA+SVCB vs. also having A)
	// is a genuine race — the gate no longer waits on A once AAAA and SVCB
	// are both final (see the dedicated gate tests). Whatever didn't make
	// Initial must arrive via exactly one merged Update.
	updates, closed := drainUpdates(res.Updates, time.Second)
	if !closed {
		t.Fatal("Updates did not close")
	}
	if len(updates) > 1 {
		t.Fatalf("got %d updates, want at most 1 (the fully-merged set)", len(updates))
	}
	final := res.Initial
	if len(updates) == 1 {
		final = updates[0]
	}

	if len(final) != 3 {
		t.Fatalf("final candidate set = %v, want 3 candidates", labels(final))
	}

	want := map[string]Candidate{
		"scion:1-150,10.20.3.215": {Family: FamilySCION, Host: "10.20.3.215", Port: 8443, IA: "1-150", ALPN: []string{"h3"}, Priority: 1, Label: "scion:1-150,10.20.3.215"},
		"v6:2001:db8::215":        {Family: FamilyIPv6, Host: "2001:db8::215", Port: 8443, ALPN: []string{"h3"}, Priority: 1, Label: "v6:2001:db8::215"},
		"v4:10.20.3.215":          {Family: FamilyIPv4, Host: "10.20.3.215", Port: 8443, ALPN: []string{"h3"}, Priority: 1, Label: "v4:10.20.3.215"},
	}
	for _, c := range final {
		w, ok := want[c.Label]
		if !ok {
			t.Errorf("unexpected candidate %+v", c)
			continue
		}
		if !reflect.DeepEqual(c, w) {
			t.Errorf("candidate %s = %+v, want %+v", c.Label, c, w)
		}
		if c.Path != nil {
			t.Errorf("candidate %s: Path = %+v, want nil (path expansion is a later task)", c.Label, c.Path)
		}
		if c.ViaScitra {
			t.Errorf("candidate %s: ViaScitra = true, want false", c.Label)
		}
	}
}

// (b) SVCB answer delayed 80ms with the default 50ms Resolution Delay:
// Initial carries only the AAAA/A candidates, and the SCION candidate
// (from the SVCB "scion" param) arrives later on Updates.
func TestResolveHost_ResolutionDelayGatesSVCB(t *testing.T) {
	f := newFakeDNS(t)
	f.set("web.scion.", dns.TypeSVCB, mustRR(t, `web.scion. 300 IN SVCB 1 . scion=1-150\,10.20.3.215`))
	f.setDelay("web.scion.", dns.TypeSVCB, 80*time.Millisecond)
	f.set("web.scion.", dns.TypeAAAA, mustRR(t, `web.scion. 300 IN AAAA 2001:db8::215`))
	f.set("web.scion.", dns.TypeA, mustRR(t, `web.scion. 300 IN A 10.20.3.215`))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	res, err := ResolveHost(ctx, "web.scion", 443, ResolveOptions{Resolver: f.addr})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}

	if elapsed < 30*time.Millisecond || elapsed > 79*time.Millisecond {
		t.Fatalf("Initial released after %v, want ~50ms (before the 80ms SVCB answer)", elapsed)
	}
	if got := labels(res.Initial); len(got) != 2 {
		t.Fatalf("Initial = %v, want exactly the AAAA/A candidates", got)
	}
	for _, c := range res.Initial {
		if c.Family == FamilySCION {
			t.Fatalf("Initial contains a SCION candidate before the SVCB answer arrived: %+v", c)
		}
	}

	updates, closed := drainUpdates(res.Updates, 2*time.Second)
	if !closed {
		t.Fatal("Updates did not close")
	}
	if len(updates) != 1 {
		t.Fatalf("got %d updates, want exactly 1 (the SVCB-merged set)", len(updates))
	}
	final := updates[0]
	if len(final) != 3 {
		t.Fatalf("merged update = %v, want 3 candidates", labels(final))
	}
	var sawSCION bool
	for _, c := range final {
		if c.Family == FamilySCION {
			sawSCION = true
			if c.IA != "1-150" || c.Host != "10.20.3.215" {
				t.Errorf("SCION candidate = %+v, want IA=1-150 Host=10.20.3.215", c)
			}
		}
	}
	if !sawSCION {
		t.Fatalf("merged update has no SCION candidate: %v", labels(final))
	}
}

// (c) No SVCB record at all: plain AAAA/A candidates with default ALPN
// (empty) and the caller's default port.
func TestResolveHost_NoSVCB(t *testing.T) {
	f := newFakeDNS(t)
	f.set("plain.scion.", dns.TypeAAAA, mustRR(t, `plain.scion. 300 IN AAAA 2001:db8::9`))
	f.set("plain.scion.", dns.TypeA, mustRR(t, `plain.scion. 300 IN A 10.20.3.9`))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := ResolveHost(ctx, "plain.scion", 8080, ResolveOptions{Resolver: f.addr, ResolutionDelay: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}

	// With no SVCB record, SVCB and A each independently race AAAA to
	// clear the §4.2 gate (path (a): AAAA+SVCB final; path (b): the 10ms
	// delay). Which of AAAA/A lands in Initial vs. arrives right after via
	// one merged Update is no longer guaranteed — only that both end up in
	// the final candidate set.
	updates, closed := drainUpdates(res.Updates, time.Second)
	if !closed {
		t.Fatal("Updates did not close")
	}
	if len(updates) > 1 {
		t.Fatalf("got %d updates, want at most 1 (the fully-merged set)", len(updates))
	}
	final := res.Initial
	if len(updates) == 1 {
		final = updates[0]
	}

	if len(final) != 2 {
		t.Fatalf("final candidate set = %v, want 2 candidates", labels(final))
	}
	for _, c := range final {
		if len(c.ALPN) != 0 {
			t.Errorf("candidate %s: ALPN = %v, want empty (no SVCB)", c.Label, c.ALPN)
		}
		if c.Port != 8080 {
			t.Errorf("candidate %s: Port = %d, want caller default 8080", c.Label, c.Port)
		}
		if c.Family == FamilySCION {
			t.Errorf("unexpected SCION candidate with no SVCB record: %+v", c)
		}
	}
}

// (d) AliasMode chase: SVCB "0 target.scion." at the queried name is
// followed to target.scion., whose ServiceMode record supplies the SCION
// candidate.
func TestResolveHost_AliasModeChase(t *testing.T) {
	f := newFakeDNS(t)
	f.set("alias.scion.", dns.TypeSVCB, mustRR(t, `alias.scion. 300 IN SVCB 0 target.scion.`))
	f.set("target.scion.", dns.TypeSVCB, mustRR(t, `target.scion. 300 IN SVCB 1 . alpn=h3 scion=1-161\,10.20.3.161`))
	f.set("alias.scion.", dns.TypeAAAA, mustRR(t, `alias.scion. 300 IN AAAA 2001:db8::161`))
	f.set("alias.scion.", dns.TypeA, mustRR(t, `alias.scion. 300 IN A 10.20.3.161`))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := ResolveHost(ctx, "alias.scion", 443, ResolveOptions{Resolver: f.addr})
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}

	var sawSCION bool
	for _, c := range res.Initial {
		if c.Family == FamilySCION {
			sawSCION = true
			if c.IA != "1-161" || c.Host != "10.20.3.161" || c.Port != 443 {
				t.Errorf("chased SCION candidate = %+v, want IA=1-161 Host=10.20.3.161 Port=443", c)
			}
			if len(c.ALPN) != 1 || c.ALPN[0] != "h3" {
				t.Errorf("chased SCION candidate ALPN = %v, want [h3] (from target.scion.'s record)", c.ALPN)
			}
		}
	}
	if !sawSCION {
		t.Fatalf("Initial has no SCION candidate after alias chase: %v", labels(res.Initial))
	}

	if _, closed := drainUpdates(res.Updates, time.Second); !closed {
		t.Fatal("Updates did not close")
	}
}

// (e) SVCB query unreachable/times out: plain AAAA/A candidates still
// resolve via the 50ms Resolution Delay gate; the failed SVCB query is
// non-fatal and Updates still closes once the (2s-bounded) SVCB query gives
// up.
func TestResolveHost_SVCBTimeoutNonFatal(t *testing.T) {
	f := newFakeDNS(t)
	// No SVCB record registered and a delay far past queryTimeout so the
	// resolver's own 2s per-query ceiling is what ends the SVCB query.
	f.setDelay("unreachable.scion.", dns.TypeSVCB, 3*time.Second)
	f.set("unreachable.scion.", dns.TypeAAAA, mustRR(t, `unreachable.scion. 300 IN AAAA 2001:db8::42`))
	f.set("unreachable.scion.", dns.TypeA, mustRR(t, `unreachable.scion. 300 IN A 10.20.3.42`))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	res, err := ResolveHost(ctx, "unreachable.scion", 443, ResolveOptions{Resolver: f.addr})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Initial released after %v, want it gated only by the 50ms Resolution Delay", elapsed)
	}
	if len(res.Initial) != 2 {
		t.Fatalf("Initial = %v, want the 2 AAAA/A candidates despite the hung SVCB query", labels(res.Initial))
	}

	// The SVCB query's own ~2s ceiling still has to expire before Updates
	// closes; give it generous headroom.
	if _, closed := drainUpdates(res.Updates, 3*time.Second); !closed {
		t.Fatal("Updates did not close after the SVCB query timed out")
	}
}

// (f) §4.2 gate path (a): SVCB and AAAA both answer promptly; A is held back
// well past the default 50ms Resolution Delay. Per draft-ietf-happy-
// happyeyeballs-v3-04 §4.2, release does not wait on A once there is a
// positive address answer, AAAA is final, and SVCB is final — Initial must
// release almost immediately, carrying the SCION+v6 candidates, with the A
// candidate arriving later via Updates.
func TestResolveHost_ReleasesWithoutAWhenSVCBAndAAAAComplete(t *testing.T) {
	f := newFakeDNS(t)
	f.set("early.scion.", dns.TypeSVCB, mustRR(t,
		`early.scion. 300 IN SVCB 1 . alpn=h3 scion=1-150\,10.20.3.215`))
	f.set("early.scion.", dns.TypeAAAA, mustRR(t, `early.scion. 300 IN AAAA 2001:db8::215`))
	f.set("early.scion.", dns.TypeA, mustRR(t, `early.scion. 300 IN A 10.20.3.215`))
	f.setDelay("early.scion.", dns.TypeA, 500*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	res, err := ResolveHost(ctx, "early.scion", 443, ResolveOptions{Resolver: f.addr})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}

	if elapsed >= 100*time.Millisecond {
		t.Fatalf("Initial released after %v, want well before 100ms (A must not gate release once AAAA+SVCB are final)", elapsed)
	}
	if got := labels(res.Initial); len(got) != 2 {
		t.Fatalf("Initial = %v, want exactly the SCION+v6 candidates", got)
	}
	var sawSCION, sawV6 bool
	for _, c := range res.Initial {
		switch c.Family {
		case FamilySCION:
			sawSCION = true
		case FamilyIPv6:
			sawV6 = true
		case FamilyIPv4:
			t.Fatalf("Initial contains an A candidate before the delayed A answer arrived: %+v", c)
		}
	}
	if !sawSCION || !sawV6 {
		t.Fatalf("Initial = %v, want a SCION and a v6 candidate", labels(res.Initial))
	}

	updates, closed := drainUpdates(res.Updates, 2*time.Second)
	if !closed {
		t.Fatal("Updates did not close")
	}
	if len(updates) != 1 {
		t.Fatalf("got %d updates, want exactly 1 (the A-merged set)", len(updates))
	}
	final := updates[0]
	if len(final) != 3 {
		t.Fatalf("merged update = %v, want 3 candidates", labels(final))
	}
	var gotA bool
	for _, c := range final {
		if c.Family == FamilyIPv4 {
			gotA = true
			if c.Host != "10.20.3.215" {
				t.Errorf("A candidate = %+v, want Host=10.20.3.215", c)
			}
		}
	}
	if !gotA {
		t.Fatalf("merged update has no A candidate: %v", labels(final))
	}
}

// (g) §4.2 gate path (b): only A answers positively and promptly; AAAA (no
// record, and slow) never becomes the required "AAAA final" for gate path
// (a) within the Resolution Delay. Initial must still release at ~50ms via
// the delay path, carrying the A candidate — a positive answer for ANY
// family plus an elapsed Resolution Delay is sufficient; A is never itself
// a release requirement, but it is a sufficient trigger.
func TestResolveHost_ResolutionDelayReleasesWithAOnly(t *testing.T) {
	f := newFakeDNS(t)
	f.set("aonly.scion.", dns.TypeA, mustRR(t, `aonly.scion. 300 IN A 10.20.3.9`))
	f.setDelay("aonly.scion.", dns.TypeAAAA, 500*time.Millisecond) // no AAAA record; answers negative, late

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	res, err := ResolveHost(ctx, "aonly.scion", 8080, ResolveOptions{Resolver: f.addr})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}

	if elapsed < 30*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Fatalf("Initial released after %v, want ~50ms (Resolution Delay path, AAAA still outstanding)", elapsed)
	}
	if got := labels(res.Initial); len(got) != 1 {
		t.Fatalf("Initial = %v, want exactly the A candidate", got)
	}
	if res.Initial[0].Family != FamilyIPv4 || res.Initial[0].Host != "10.20.3.9" {
		t.Fatalf("Initial candidate = %+v, want the A answer 10.20.3.9", res.Initial[0])
	}

	updates, closed := drainUpdates(res.Updates, 2*time.Second)
	if !closed {
		t.Fatal("Updates did not close")
	}
	if len(updates) != 0 {
		t.Fatalf("got %d updates, want none (the late AAAA answer is negative, so the candidate set doesn't change): %v", len(updates), updates)
	}
}

// (h) A caller that reads Initial but abandons Updates without ever
// cancelling ctx would (per the ResolveHost doc comment) delay run()'s
// cleanup until ctx ends; a caller that DOES cancel ctx must see that
// escape trigger promptly instead of blocking on the send forever. This
// pins the ctx.Done() escape on the internal Updates send (resolver.go).
func TestResolveHost_ContextCancelUnblocksAbandonedUpdatesSend(t *testing.T) {
	f := newFakeDNS(t)
	f.set("leak.scion.", dns.TypeSVCB, mustRR(t,
		`leak.scion. 300 IN SVCB 1 . alpn=h3 scion=1-150\,10.20.3.215`))
	f.setDelay("leak.scion.", dns.TypeSVCB, 300*time.Millisecond) // arrives after Initial, forcing an Update send
	f.set("leak.scion.", dns.TypeAAAA, mustRR(t, `leak.scion. 300 IN AAAA 2001:db8::215`))
	f.set("leak.scion.", dns.TypeA, mustRR(t, `leak.scion. 300 IN A 10.20.3.215`))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	res, err := ResolveHost(ctx, "leak.scion", 443, ResolveOptions{Resolver: f.addr})
	if err != nil {
		cancel()
		t.Fatalf("ResolveHost: %v", err)
	}
	if len(res.Initial) == 0 {
		cancel()
		t.Fatal("Initial unexpectedly empty")
	}

	// Deliberately never read from res.Updates — only cancel ctx. This must
	// unblock run()'s pending Updates send (the delayed SVCB answer is still
	// 250ms+ away) well before that answer would otherwise arrive.
	cancel()

	select {
	case _, ok := <-res.Updates:
		if ok {
			t.Fatal("Updates delivered a value after abandonment+cancel; want it to close with no send observed")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Updates did not close promptly after ctx cancellation (no ctx.Done() escape on the Updates send)")
	}
}

// (i) End-to-end hex-ASN scenario using the exact deployed zone form
// (config/coredns/scion.zone's "games" record): the SCION candidate's IA
// and Host must round-trip untouched through scionIA/SCIONAddr parsing.
func TestResolveHost_HexASNScionParam(t *testing.T) {
	f := newFakeDNS(t)
	f.set("games.scion.", dns.TypeSVCB, mustRR(t,
		`games.scion. 300 IN SVCB 1 . alpn=h3 scion=71-2:0:4a\,10.44.25.3`))
	f.set("games.scion.", dns.TypeAAAA, mustRR(t, `games.scion. 300 IN AAAA 2001:db8::44`))
	f.set("games.scion.", dns.TypeA, mustRR(t, `games.scion. 300 IN A 10.44.25.9`))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := ResolveHost(ctx, "games.scion", 443, ResolveOptions{Resolver: f.addr})
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}

	final := res.Initial
	updates, closed := drainUpdates(res.Updates, time.Second)
	if !closed {
		t.Fatal("Updates did not close")
	}
	if len(updates) > 0 {
		final = updates[len(updates)-1]
	}

	var scion *Candidate
	for i := range final {
		if final[i].Family == FamilySCION {
			scion = &final[i]
		}
	}
	if scion == nil {
		t.Fatalf("no SCION candidate in final set: %v", labels(final))
	}
	if scion.IA != "71-2:0:4a" {
		t.Errorf("IA = %q, want 71-2:0:4a", scion.IA)
	}
	if scion.Host != "10.44.25.3" {
		t.Errorf("Host = %q, want 10.44.25.3", scion.Host)
	}
	if scion.Label != "scion:71-2:0:4a,10.44.25.3" {
		t.Errorf("Label = %q, want scion:71-2:0:4a,10.44.25.3 (same canonical form as IA, not addr.String()'s decimal-ASN rendering)", scion.Label)
	}
}

// (k) SCION-only name (no A/AAAA records at all — NODATA for both, matching
// the deployed zone's "games" pattern: SVCB scion= with no address RRs):
// ResolveHost must still release promptly instead of hanging, because
// hasPositiveAddrs required at least one positive AAAA/A answer and neither
// family is ever positive here. A final SVCB that produced a SCION candidate
// must itself count as a positive answer for the delay-path gate, and once
// every query (SVCB/AAAA/A) is final there is nothing left to wait for
// regardless.
func TestResolveHost_SCIONOnlyNoAddresses(t *testing.T) {
	f := newFakeDNS(t)
	f.set("games.scion.", dns.TypeSVCB, mustRR(t,
		`games.scion. 300 IN SVCB 1 . alpn=h3 scion=1-150\,10.20.3.215`))
	// Deliberately no AAAA/A records registered: both are NODATA.

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	res, err := ResolveHost(ctx, "games.scion", 443, ResolveOptions{Resolver: f.addr})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ResolveHost: %v, want nil error (SCION-only names must resolve, not hang to ctx deadline)", err)
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("ResolveHost took %v, want a prompt return (<500ms) for a SCION-only name with a 2s ctx", elapsed)
	}

	final := res.Initial
	updates, closed := drainUpdates(res.Updates, time.Second)
	if !closed {
		t.Fatal("Updates did not close")
	}
	if len(updates) > 0 {
		final = updates[len(updates)-1]
	}

	if len(final) != 1 {
		t.Fatalf("final candidate set = %v, want exactly the SCION candidate", labels(final))
	}
	if final[0].Family != FamilySCION || final[0].IA != "1-150" || final[0].Host != "10.20.3.215" {
		t.Fatalf("candidate = %+v, want the SCION candidate 1-150,10.20.3.215", final[0])
	}
}

// (l) Every query (SVCB, AAAA, A) resolves negative — no SVCB record, no
// AAAA/A records (the NXDOMAIN/no-records case): ResolveHost must return
// promptly with an empty candidate set and a nil error once all three
// queries are final. An empty Initial is a valid outcome; the caller (not
// the resolver) decides what to do with no candidates.
func TestResolveHost_AllNegativeReturnsEmptyPromptly(t *testing.T) {
	f := newFakeDNS(t) // no records registered for this name at all

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	res, err := ResolveHost(ctx, "nowhere.scion", 443, ResolveOptions{Resolver: f.addr})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ResolveHost: %v, want nil error even though every query answered negative", err)
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("ResolveHost took %v, want a prompt return (<500ms) once all queries are final-negative", elapsed)
	}
	if len(res.Initial) != 0 {
		t.Fatalf("Initial = %v, want empty (no SVCB/AAAA/A records at all)", labels(res.Initial))
	}

	updates, closed := drainUpdates(res.Updates, time.Second)
	if !closed {
		t.Fatal("Updates did not close")
	}
	if len(updates) != 0 {
		t.Fatalf("got %d updates, want none", len(updates))
	}
}

// (m) SCION-only name whose AAAA answer is negative but held back well past
// the default 50ms Resolution Delay (A answers promptly negative): the SCION
// candidate from the completed SVCB must release at ~delay via the §4.2
// delay path, not block until AAAA's slow final answer.
func TestResolveHost_SCIONOnlyReleasesAtDelayWithSlowAAAA(t *testing.T) {
	f := newFakeDNS(t)
	f.set("slowv6.scion.", dns.TypeSVCB, mustRR(t,
		`slowv6.scion. 300 IN SVCB 1 . alpn=h3 scion=1-150\,10.20.3.215`))
	f.setDelay("slowv6.scion.", dns.TypeAAAA, 500*time.Millisecond) // no AAAA record; negative, late

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	res, err := ResolveHost(ctx, "slowv6.scion", 443, ResolveOptions{Resolver: f.addr})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}

	if elapsed < 30*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Fatalf("Initial released after %v, want ~50ms (delay path; AAAA still outstanding)", elapsed)
	}
	if got := labels(res.Initial); len(got) != 1 {
		t.Fatalf("Initial = %v, want exactly the SCION candidate", got)
	}
	if res.Initial[0].Family != FamilySCION || res.Initial[0].IA != "1-150" || res.Initial[0].Host != "10.20.3.215" {
		t.Fatalf("Initial candidate = %+v, want the SCION candidate 1-150,10.20.3.215", res.Initial[0])
	}

	updates, closed := drainUpdates(res.Updates, 2*time.Second)
	if !closed {
		t.Fatal("Updates did not close")
	}
	if len(updates) != 0 {
		t.Fatalf("got %d updates, want none (the late AAAA answer is negative, so the candidate set doesn't change): %v", len(updates), updates)
	}
}

// (j) Alias chase visited-set/self-alias comparison must be case-
// insensitive (RFC 9460 names are compared canonically): a ServiceMode-0
// alias whose Target differs from the queried name only in case is a
// self-alias and must be rejected immediately, without re-querying the
// case-variant name.
func TestResolveSVCB_AliasChaseCaseInsensitiveSelfAlias(t *testing.T) {
	f := newFakeDNS(t)
	// Deliberately no record registered for "CASE.scion." — if the case
	// variant were (incorrectly) treated as a distinct name, the chase
	// would requery it, find nothing, and silently return (nil, nil)
	// instead of catching the self-alias.
	f.set("case.scion.", dns.TypeSVCB, mustRR(t, `case.scion. 300 IN SVCB 0 CASE.scion.`))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tl := &Timeline{}
	r := &resolveRun{ctx: ctx, server: f.addr, host: dns.Fqdn("case.scion"), port: 443, tl: tl}

	_, err := r.resolveSVCB()
	if err == nil {
		t.Fatal("resolveSVCB: want a self-alias error for a case-variant self-alias, got nil")
	}
	if !strings.Contains(err.Error(), "alias to self") {
		t.Fatalf("resolveSVCB error = %v, want an alias-to-self error", err)
	}

	queries := 0
	for _, ev := range tl.Events() {
		if ev.Kind == "query" {
			queries++
		}
	}
	if queries != 1 {
		t.Fatalf("issued %d SVCB queries, want exactly 1 (case-variant self-alias must be caught before requerying)", queries)
	}
}
