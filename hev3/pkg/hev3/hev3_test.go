package hev3

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// TestFetch_EndToEndIPOnly exercises the full resolve→merge→filter→expand→
// sort→race→GET pipeline against a local h2 server (the dev box has no
// SCION daemon or scitra route, so this is the IP-only path — SCION legs are
// exercised in testbed integration). It asserts the Timeline carries a
// query→candidate→attempt→winner sequence and the fetched body is non-empty.
func TestFetch_EndToEndIPOnly(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "hello-fetch-e2e")
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	tcp := srv.Listener.Addr().(*net.TCPAddr)

	f := newFakeDNS(t)
	f.set("fetch.test.", dns.TypeA, mustRR(t, fmt.Sprintf("fetch.test. 300 IN A %s", tcp.IP.String())))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rawURL := fmt.Sprintf("https://fetch.test:%d/", tcp.Port)
	res, err := Fetch(ctx, rawURL, Options{
		Resolver:        f.addr,
		Insecure:        true,
		ResolutionDelay: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(res.Body) == 0 {
		t.Fatal("Result.Body is empty, want the served response")
	}
	if !strings.Contains(string(res.Body), "hello-fetch-e2e") {
		t.Fatalf("Result.Body = %q, want it to contain the served response", res.Body)
	}
	if res.Winner.Family != FamilyIPv4 {
		t.Fatalf("Winner.Family = %v, want FamilyIPv4 (dev box has no SCION)", res.Winner.Family)
	}
	if !strings.Contains(res.Status, "200") {
		t.Fatalf("Result.Status = %q, want it to mention 200", res.Status)
	}

	// query -> candidate -> attempt -> winner, in that relative order.
	idx := map[string]int{}
	for i, ev := range res.Timeline {
		if _, ok := idx[ev.Kind]; !ok {
			idx[ev.Kind] = i
		}
	}
	for _, kind := range []string{"query", "candidate", "attempt", "winner"} {
		if _, ok := idx[kind]; !ok {
			t.Fatalf("Timeline has no %q event: %+v", kind, res.Timeline)
		}
	}
	if !(idx["query"] < idx["candidate"] && idx["candidate"] < idx["attempt"] && idx["attempt"] < idx["winner"]) {
		t.Fatalf("Timeline kinds out of order: query=%d candidate=%d attempt=%d winner=%d",
			idx["query"], idx["candidate"], idx["attempt"], idx["winner"])
	}
}

// closedPort returns a "127.0.0.1:port" that is guaranteed to refuse
// connections: a listener is opened and immediately closed, so nothing is
// bound there when the caller dials it (the standard Go pattern for
// deterministically getting an address that won't accept a connection).
func closedPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return addr
}

// TestFetch_SCIONOnlyInitialWithLateAStillFetches is the regression test for
// the race documented in .superpowers/sdd/task-13-report.md's "Concerns"
// section: `web`'s real SVCB record carries a scion= candidate but no AAAA
// record, exactly like this test's zone. ResolveHost's §4.2 gate can then
// release Initial as soon as SVCB+AAAA are both final (see resolver.go's
// release doc comment: "A is never itself a gate requirement"), containing
// ONLY the SCION candidate, while the A answer is still in flight. On a host
// with no SCION daemon (DaemonAddr pointed at a closed port) and no fc00
// scitra route (setRoute(t, false)), ExpandSCION drops that lone candidate.
// Pre-fix, Fetch decided "no candidates" right there. Post-fix, Fetch must
// keep waiting on Updates and succeed once the delayed A answer arrives.
func TestFetch_SCIONOnlyInitialWithLateAStillFetches(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "late-a-wins")
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	tcp := srv.Listener.Addr().(*net.TCPAddr)

	f := newFakeDNS(t)
	// SVCB carries a scion= candidate and no AAAA record at all (matches the
	// deployed zone's "web" shape). A is delayed well past ResolutionDelay,
	// so Initial releases via §4.2 gate path (a) — SVCB+AAAA final — with
	// only the SCION candidate; A only shows up on a later Update. No
	// alpn=h3: that would also apply to the A candidate the SVCB record
	// covers (buildCandidates applies ALPN uniformly), forcing an HTTP/3-QUIC
	// dial against a plain TLS+h2 httptest server.
	f.set("sciononly.test.", dns.TypeSVCB, mustRR(t,
		`sciononly.test. 300 IN SVCB 1 . scion=1-150\,10.20.3.215`))
	f.set("sciononly.test.", dns.TypeA, mustRR(t, fmt.Sprintf("sciononly.test. 300 IN A %s", tcp.IP.String())))
	f.setDelay("sciononly.test.", dns.TypeA, 150*time.Millisecond)

	setRoute(t, false) // no fc00 scitra route

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rawURL := fmt.Sprintf("https://sciononly.test:%d/", tcp.Port)
	start := time.Now()
	res, err := Fetch(ctx, rawURL, Options{
		Resolver:        f.addr,
		Insecure:        true,
		ResolutionDelay: 30 * time.Millisecond,
		DaemonAddr:      closedPort(t), // no SCION daemon
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Fetch: %v, want it to fall through to the late A candidate instead of erroring", err)
	}

	if elapsed < 150*time.Millisecond || elapsed > time.Second {
		t.Fatalf("Fetch took %v, want it bounded by the ~150ms delayed A answer, not ctx's 5s timeout", elapsed)
	}
	if !strings.Contains(string(res.Body), "late-a-wins") {
		t.Fatalf("Result.Body = %q, want it to contain the served response", res.Body)
	}
	if res.Winner.Family != FamilyIPv4 {
		t.Fatalf("Winner.Family = %v, want FamilyIPv4 (the SCION candidate must have been dropped, not raced)", res.Winner.Family)
	}

	// The dropped SCION candidate must be Timeline-noted exactly once, not
	// once per retried batch (expandCached's dedupe).
	var scionFails int
	for _, ev := range res.Timeline {
		if ev.Kind == "fail" && strings.HasPrefix(ev.Label, "scion:") {
			scionFails++
		}
	}
	if scionFails != 1 {
		t.Fatalf("got %d Timeline 'fail' notes for the SCION candidate, want exactly 1 (deduped across retried batches): %+v", scionFails, res.Timeline)
	}
}

// TestFetch_RejectsNonHTTPSScheme pins the "h3/h2 only" contract.
func TestFetch_RejectsNonHTTPSScheme(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := Fetch(ctx, "http://plain.test/", Options{})
	if err == nil {
		t.Fatal("Fetch: want an error for a non-https scheme, got nil")
	}
	if !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("error = %v, want it to mention the unsupported scheme", err)
	}
}

// TestFilterCandidates unit-tests the --no-scion/--no-ip filter logic in
// isolation from resolution.
func TestFilterCandidates(t *testing.T) {
	cands := []Candidate{
		{Family: FamilySCION, Label: "scion:1-150,10.20.3.1"},
		{Family: FamilyIPv6, Label: "v6:2001:db8::1"},
		{Family: FamilyIPv4, Label: "v4:10.0.0.1"},
	}

	tests := []struct {
		name          string
		noSCION, noIP bool
		wantLabels    []string
	}{
		{"neither", false, false, []string{"scion:1-150,10.20.3.1", "v6:2001:db8::1", "v4:10.0.0.1"}},
		{"no-scion", true, false, []string{"v6:2001:db8::1", "v4:10.0.0.1"}},
		{"no-ip", false, true, []string{"scion:1-150,10.20.3.1"}},
		{"both", true, true, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterCandidates(cands, tc.noSCION, tc.noIP)
			if len(got) != len(tc.wantLabels) {
				t.Fatalf("filterCandidates(noSCION=%v, noIP=%v) = %v, want labels %v", tc.noSCION, tc.noIP, labels(got), tc.wantLabels)
			}
			for i, c := range got {
				if c.Label != tc.wantLabels[i] {
					t.Errorf("candidate %d = %q, want %q", i, c.Label, tc.wantLabels[i])
				}
			}
		})
	}
}

// TestFetch_NoIPWithOnlyIPCandidatesErrors: the dev box's fake DNS answer is
// IP-only (no SVCB/scion param), so --no-ip must filter every candidate and
// Fetch must return the clear "no candidates" error, not hang or panic.
func TestFetch_NoIPWithOnlyIPCandidatesErrors(t *testing.T) {
	f := newFakeDNS(t)
	f.set("noip.test.", dns.TypeA, mustRR(t, "noip.test. 300 IN A 127.0.0.1"))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := Fetch(ctx, "https://noip.test/", Options{
		Resolver:        f.addr,
		NoIP:            true,
		ResolutionDelay: 10 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("Fetch: want an error when --no-ip drops every candidate, got nil")
	}
	if !strings.Contains(err.Error(), "no candidates for noip.test") {
		t.Fatalf("error = %v, want it to say \"no candidates for noip.test\"", err)
	}
}

// --- mergeResolved (empty-Initial-wait logic), tested via a stub Resolved
// rather than a live resolver: Resolved is a plain struct wrapping a
// channel, so it can be constructed by hand as the "stub resolver" the task
// brief allows in place of one built on fake DNS.

func TestMergeResolved_EmptyInitialWaitsForFirstNonEmptyUpdate(t *testing.T) {
	ch := make(chan []Candidate)
	r := Resolved{Initial: nil, Updates: ch}

	done := make(chan struct{})
	var got []Candidate
	var err error
	go func() {
		got, err = mergeResolved(context.Background(), r, "host")
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("mergeResolved returned before any Update arrived")
	case <-time.After(50 * time.Millisecond):
	}

	want := []Candidate{{Label: "v4:1.2.3.4", Family: FamilyIPv4}}
	ch <- want

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("mergeResolved did not return promptly after an Update arrived")
	}
	if err != nil {
		t.Fatalf("mergeResolved: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeResolved = %v, want %v", got, want)
	}
}

func TestMergeResolved_EmptyInitialClosedWithoutCandidatesErrors(t *testing.T) {
	ch := make(chan []Candidate)
	close(ch)
	r := Resolved{Initial: nil, Updates: ch}

	_, err := mergeResolved(context.Background(), r, "nowhere.test")
	if err == nil {
		t.Fatal("mergeResolved: want an error for an all-negative resolve, got nil")
	}
	if !strings.Contains(err.Error(), "no candidates for nowhere.test") {
		t.Fatalf("error = %v, want \"no candidates for nowhere.test\"", err)
	}
}

func TestMergeResolved_EmptyInitialCtxDoneUnblocks(t *testing.T) {
	ch := make(chan []Candidate) // never sent to, never closed
	r := Resolved{Initial: nil, Updates: ch}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var err error
	go func() {
		_, err = mergeResolved(ctx, r, "host")
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("mergeResolved did not return after ctx cancellation")
	}
	if err == nil {
		t.Fatal("mergeResolved: want ctx.Err(), got nil")
	}
}

func TestMergeResolved_NonEmptyInitialDrainsAlreadyArrivedUpdateNonBlocking(t *testing.T) {
	initial := []Candidate{{Label: "v4:1.2.3.4", Family: FamilyIPv4}}
	merged := []Candidate{
		{Label: "v4:1.2.3.4", Family: FamilyIPv4},
		{Label: "v6:2001:db8::1", Family: FamilyIPv6},
	}
	ch := make(chan []Candidate, 1)
	ch <- merged // already arrived before mergeResolved is called
	r := Resolved{Initial: initial, Updates: ch}

	got, err := mergeResolved(context.Background(), r, "host")
	if err != nil {
		t.Fatalf("mergeResolved: %v", err)
	}
	if !reflect.DeepEqual(got, merged) {
		t.Fatalf("mergeResolved = %v, want the already-arrived merged set %v (not just Initial)", got, merged)
	}
}

// TestMergeResolved_NonEmptyInitialEmptyUpdateDoesNotClobber pins the guard
// mirroring the empty-Initial branch: a (hypothetical) empty-but-not-closed
// Updates batch seen during the non-blocking drain must not replace a good
// Initial candidate set with nothing.
func TestMergeResolved_NonEmptyInitialEmptyUpdateDoesNotClobber(t *testing.T) {
	initial := []Candidate{{Label: "v4:1.2.3.4", Family: FamilyIPv4}}
	ch := make(chan []Candidate, 1)
	ch <- []Candidate{} // already arrived: an empty, non-nil batch
	r := Resolved{Initial: initial, Updates: ch}

	got, err := mergeResolved(context.Background(), r, "host")
	if err != nil {
		t.Fatalf("mergeResolved: %v", err)
	}
	if !reflect.DeepEqual(got, initial) {
		t.Fatalf("mergeResolved = %v, want Initial %v preserved (empty update must not clobber)", got, initial)
	}
}

func TestMergeResolved_NonEmptyInitialNoUpdateYetReturnsInitial(t *testing.T) {
	initial := []Candidate{{Label: "v4:1.2.3.4", Family: FamilyIPv4}}
	ch := make(chan []Candidate) // unbuffered, nothing sent
	r := Resolved{Initial: initial, Updates: ch}

	got, err := mergeResolved(context.Background(), r, "host")
	if err != nil {
		t.Fatalf("mergeResolved: %v", err)
	}
	if !reflect.DeepEqual(got, initial) {
		t.Fatalf("mergeResolved = %v, want Initial %v unchanged (later Updates must not be awaited)", got, initial)
	}
}

// TestBuildTLSConfig_ServerNameFromHostname pins the "one shared tls.Config"
// contract's most important field for the demo CA to work identically
// across legs: ServerName must come from the URL's hostname.
func TestBuildTLSConfig_ServerNameFromHostname(t *testing.T) {
	cfg, err := buildTLSConfig(Options{Insecure: true}, "web.scion")
	if err != nil {
		t.Fatalf("buildTLSConfig: %v", err)
	}
	if cfg.ServerName != "web.scion" {
		t.Errorf("ServerName = %q, want web.scion", cfg.ServerName)
	}
	if !cfg.InsecureSkipVerify {
		t.Errorf("InsecureSkipVerify = false, want true")
	}
	if _, ok := interface{}(cfg).(*tls.Config); !ok {
		t.Errorf("buildTLSConfig did not return a *tls.Config")
	}
}
