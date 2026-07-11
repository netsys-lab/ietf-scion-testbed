package hev3

import (
	"context"
	"net"
	"reflect"
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

	if len(res.Initial) != 3 {
		t.Fatalf("Initial = %v, want 3 candidates", labels(res.Initial))
	}

	want := map[string]Candidate{
		"scion:1-150,10.20.3.215": {Family: FamilySCION, Host: "10.20.3.215", Port: 8443, IA: "1-150", ALPN: []string{"h3"}, Priority: 1, Label: "scion:1-150,10.20.3.215"},
		"v6:2001:db8::215":        {Family: FamilyIPv6, Host: "2001:db8::215", Port: 8443, ALPN: []string{"h3"}, Priority: 1, Label: "v6:2001:db8::215"},
		"v4:10.20.3.215":          {Family: FamilyIPv4, Host: "10.20.3.215", Port: 8443, ALPN: []string{"h3"}, Priority: 1, Label: "v4:10.20.3.215"},
	}
	for _, c := range res.Initial {
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

	updates, closed := drainUpdates(res.Updates, time.Second)
	if !closed {
		t.Fatal("Updates did not close")
	}
	if len(updates) != 0 {
		t.Fatalf("Updates = %v, want none (everything already merged into Initial)", updates)
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

	if len(res.Initial) != 2 {
		t.Fatalf("Initial = %v, want 2 candidates", labels(res.Initial))
	}
	for _, c := range res.Initial {
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

	if _, closed := drainUpdates(res.Updates, time.Second); !closed {
		t.Fatal("Updates did not close")
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
