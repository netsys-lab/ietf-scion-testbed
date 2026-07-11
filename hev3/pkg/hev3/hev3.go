// Package hev3's Fetch ties resolver.go, sort.go, race.go and the dialers
// together into the full draft-ietf-happy-happyeyeballs-v3-04 pipeline
// (resolve → expand → sort → race → GET), extended with SCION as described
// in docs/superpowers/specs/2026-07-10-scion-svcb-hev3-design.md.
package hev3

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

// defaultCAFile is the root CA PEM Fetch trusts when Options.CAFile is unset
// and the file exists. It matches the testbed's one-off *.scion CA (see
// Section 4 of the companion spec).
const defaultCAFile = "/etc/hev3/ca.pem"

// maxBodyBytes caps how much of the winning response body Fetch reads.
const maxBodyBytes = 4096

// Options configures Fetch.
type Options struct {
	Resolver        string        // "ip:53"; empty ⇒ system resolver via /etc/resolv.conf
	K               int           // ranked SCION paths per candidate; 0 ⇒ ExpandSCION's default
	NoSCION         bool          // drop SCION candidates once resolved, before racing
	NoIP            bool          // drop IPv6/IPv4 candidates once resolved, before racing
	Insecure        bool          // tls.Config.InsecureSkipVerify on every leg
	CAFile          string        // PEM root CA file; "" ⇒ defaultCAFile if it exists, else the system pool
	AttemptDelay    time.Duration // Race stagger; 0 ⇒ Race's default (250ms)
	ResolutionDelay time.Duration // ResolveHost §4.2 gate; 0 ⇒ ResolveHost's default (50ms)
	DaemonAddr      string        // sciond address passed through to DialerOptions; "" ⇒ DialerOptions' own default
}

// Result is the outcome of a successful Fetch.
type Result struct {
	Winner   Candidate // the Candidate that won the race
	ALPN     string    // protocol negotiated on the winning connection (h3/h2/http1.1)
	Status   string    // e.g. "HTTP/2.0 200 OK"
	Body     []byte    // first 4KiB of the response body
	Timeline []Event   // every query/candidate/attempt/success/fail/cancel/winner event
}

// Fetch resolves rawURL (which must be an https:// URL — hev3 only speaks
// h3/h2 over TLS), expands and races its SCION/IPv6/IPv4 candidates per
// draft-ietf-happy-happyeyeballs-v3-04 §4-6, and issues one GET over the
// winning connection.
func Fetch(ctx context.Context, rawURL string, o Options) (*Result, error) {
	u, host, port, err := parseTarget(rawURL)
	if err != nil {
		return nil, err
	}

	tl := &Timeline{}

	resolved, err := ResolveHost(ctx, host, port, ResolveOptions{
		Resolver:        o.Resolver,
		ResolutionDelay: o.ResolutionDelay,
		Timeline:        tl,
	})
	if err != nil {
		return nil, fmt.Errorf("hev3: fetch: resolve %s: %w", host, err)
	}

	cands, err := mergeResolved(ctx, resolved, host)
	if err != nil {
		return nil, err
	}

	tlsConfig, err := buildTLSConfig(o, host)
	if err != nil {
		return nil, err
	}
	dOpts := DialerOptions{TLS: tlsConfig, K: o.K, Timeline: tl, DaemonAddr: o.DaemonAddr}

	sorted, err := raceReady(ctx, cands, resolved.Updates, host, o, dOpts)
	if err != nil {
		return nil, err
	}

	dial := NewDialer(dOpts)
	est, err := Race(ctx, sorted, dial, RaceOptions{AttemptDelay: o.AttemptDelay, Timeline: tl})
	if err != nil {
		return nil, fmt.Errorf("hev3: fetch: race: %w", err)
	}
	defer est.Close()

	status, body, err := getOverWinner(ctx, est, u)
	if err != nil {
		return nil, err
	}

	return &Result{
		Winner:   est.Cand,
		ALPN:     est.ALPN,
		Status:   status,
		Body:     body,
		Timeline: tl.Events(),
	}, nil
}

// parseTarget parses rawURL, requiring the https scheme (hev3 only speaks
// h3/h2 over TLS) and defaulting the port to 443 when the URL has none.
func parseTarget(rawURL string) (*url.URL, string, uint16, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", 0, fmt.Errorf("hev3: fetch: parsing URL %q: %w", rawURL, err)
	}
	if u.Scheme != "https" {
		return nil, "", 0, fmt.Errorf("hev3: fetch: unsupported scheme %q (hev3 only speaks h3/h2 over https)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return nil, "", 0, fmt.Errorf("hev3: fetch: URL %q has no host", rawURL)
	}
	port := uint16(443)
	if p := u.Port(); p != "" {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil {
			return nil, "", 0, fmt.Errorf("hev3: fetch: bad port %q in URL %q: %w", p, rawURL, err)
		}
		port = uint16(n)
	}
	return u, host, port, nil
}

// mergeResolved returns the first candidate batch worth trying: the pragmatic
// §4.3 merge Race (a fixed-slice API) requires:
//
//   - Initial non-empty: race Initial plus whatever Updates has already
//     delivered (drained non-blocking) — later Updates are not awaited here
//     and do not restart a running race (full live-merge is out of scope;
//     see the design doc). raceReady is what keeps reading Updates beyond
//     this point, if this first batch turns out not to be raceable once
//     filtered and expanded.
//   - Initial empty: block for the first non-empty Updates batch, Updates
//     closing, or ctx.Done(), whichever comes first.
//   - Updates closes having delivered nothing (all-negative resolve, or a
//     caller draining an already-empty Initial with no update ever coming):
//     a clear "no candidates for <host>" error.
//
// Every value ResolveHost sends — on Initial or on Updates — is already the
// complete current candidate set (see resolveRun.release in resolver.go), so
// merging is "take the latest set seen", never an append.
func mergeResolved(ctx context.Context, r Resolved, host string) ([]Candidate, error) {
	if len(r.Initial) > 0 {
		cands := r.Initial
	drain:
		for {
			select {
			case more, ok := <-r.Updates:
				if !ok {
					break drain
				}
				if len(more) > 0 {
					cands = more
				}
				// An empty-but-not-closed update is not expected from the
				// current resolver, but — mirroring the empty-Initial
				// branch below — must not clobber a good candidate set
				// already in hand.
			default:
				break drain
			}
		}
		return cands, nil
	}

	for {
		select {
		case more, ok := <-r.Updates:
			if !ok {
				return nil, fmt.Errorf("hev3: fetch: no candidates for %s", host)
			}
			if len(more) > 0 {
				return more, nil
			}
			// An empty-but-not-closed update is not expected from the
			// current resolver (see resolveRun.release), but loop rather
			// than treat it as fatal — a future resolver revision that
			// streams intermediate empty states should not break Fetch.
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// filterCandidates drops SCION and/or IP-family candidates per the CLI's
// --no-scion/--no-ip flags.
func filterCandidates(cands []Candidate, noSCION, noIP bool) []Candidate {
	if !noSCION && !noIP {
		return cands
	}
	out := make([]Candidate, 0, len(cands))
	for _, c := range cands {
		if noSCION && c.Family == FamilySCION {
			continue
		}
		if noIP && c.Family != FamilySCION {
			continue
		}
		out = append(out, c)
	}
	return out
}

// hasNativeSCIONPath reports whether any candidate carries a pinned SCION
// path — the signal that ExpandSCION found a live sciond (native expansion),
// as opposed to a scitra fallback (FamilyIPv6, Path nil) or no SCION at all.
func hasNativeSCIONPath(cands []Candidate) bool {
	for _, c := range cands {
		if c.Path != nil {
			return true
		}
	}
	return false
}

// raceReady turns a resolved candidate batch into the sorted, raceable set
// Race needs, applying filterCandidates + ExpandSCION + Sort — and, if that
// first batch turns out empty post-expansion, keeps retrying on updates
// (ctx-bounded) instead of failing immediately.
//
// This is the fix for a real race: ResolveHost's §4.2 gate may legitimately
// release Initial containing ONLY SCION proto-candidates (SVCB positive and
// AAAA final, A still outstanding — resolver.go's release doc comment: "A is
// never itself a gate requirement"). On a host with no SCION daemon and no
// fc00 scitra route, ExpandSCION then drops those candidates entirely, and
// deciding "no candidates" at that point — before expansion even ran, as the
// old code did — is wrong: the A candidate is typically only milliseconds
// away on updates. The empty check must happen POST-expansion-and-filter,
// and an empty result must not be fatal while updates remains open.
//
// The non-empty first-shot fast path adds no latency: a batch that already
// yields a non-empty raceable set after filter+expand returns immediately,
// without ever touching updates.
func raceReady(ctx context.Context, first []Candidate, updates <-chan []Candidate, host string, o Options, dOpts DialerOptions) ([]Candidate, error) {
	cache := map[string][]Candidate{}

	if sorted := expandAndSort(ctx, first, o, dOpts, cache); len(sorted) > 0 {
		return sorted, nil
	}

	for {
		select {
		case more, ok := <-updates:
			if !ok {
				return nil, fmt.Errorf("hev3: fetch: no candidates for %s", host)
			}
			if len(more) == 0 {
				// An empty-but-not-closed update is not expected from the
				// current resolver, but loop rather than treat it as fatal
				// (mirrors mergeResolved's own guard).
				continue
			}
			if sorted := expandAndSort(ctx, more, o, dOpts, cache); len(sorted) > 0 {
				return sorted, nil
			}
			// This batch also expands to nothing (e.g. it is still
			// SCION-only, or every candidate got filtered) — keep waiting
			// for updates rather than giving up.
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// expandAndSort applies the CLI's --no-scion/--no-ip family filter (§CLI
// contract: applies post-resolve, before SCION path expansion — it filters
// resolved candidate *families*, not individual expanded per-path
// sub-candidates), then ExpandSCION (via expandCached) and Sort.
//
// ExpandSCION does not expose whether it found a live sciond directly;
// nativeSCION for Sort is derived from whether any candidate carries a
// pinned path (native per-path expansion is the only source of Path != nil —
// a scitra leg clears Path and flips to FamilyIPv6 instead).
func expandAndSort(ctx context.Context, batch []Candidate, o Options, dOpts DialerOptions, cache map[string][]Candidate) []Candidate {
	filtered := filterCandidates(batch, o.NoSCION, o.NoIP)
	if len(filtered) == 0 {
		return nil
	}
	expanded := expandCached(ctx, filtered, dOpts, cache)
	if len(expanded) == 0 {
		return nil
	}
	return Sort(expanded, hasNativeSCIONPath(expanded), 1)
}

// expandCached runs ExpandSCION over cands, but for any SCION proto-
// candidate (FamilySCION, Path nil, not ViaScitra) already present in cache
// — because an earlier batch in this same raceReady loop already ran it
// through ExpandSCION — reuses the cached result instead of calling
// ExpandSCION again.
//
// Without this, a SCION proto-candidate that ResolveHost keeps re-sending
// across batches (its Label is stable; resolveRun.buildCandidates rebuilds
// the full set fresh on every release, so the same dropped candidate
// reappears in every subsequent merged batch) would get handed to
// ExpandSCION again on every retried batch — and, since ExpandSCION emits a
// Timeline "fail" note each time it drops a candidate (see dial_scion.go's
// note calls), that would duplicate the drop-note once per retry instead of
// recording it once.
func expandCached(ctx context.Context, cands []Candidate, dOpts DialerOptions, cache map[string][]Candidate) []Candidate {
	var toExpand, out []Candidate
	for _, c := range cands {
		if c.Family == FamilySCION && c.Path == nil && !c.ViaScitra {
			if cached, ok := cache[c.Label]; ok {
				out = append(out, cached...)
				continue
			}
			toExpand = append(toExpand, c)
			continue
		}
		out = append(out, c)
	}
	for _, c := range toExpand {
		res := ExpandSCION(ctx, []Candidate{c}, dOpts)
		cache[c.Label] = res
		out = append(out, res...)
	}
	return out
}

// buildTLSConfig builds the single tls.Config shared by every race leg:
// ServerName pinned to the URL's hostname (so the demo CA's *.scion cert
// verifies identically on SCION, v6 and v4 legs), RootCAs from o.CAFile (or
// defaultCAFile when it exists and o.CAFile is unset), InsecureSkipVerify
// from o.Insecure.
func buildTLSConfig(o Options, hostname string) (*tls.Config, error) {
	cfg := &tls.Config{ServerName: hostname, InsecureSkipVerify: o.Insecure}

	caFile := o.CAFile
	if caFile == "" {
		if _, err := os.Stat(defaultCAFile); err == nil {
			caFile = defaultCAFile
		}
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("hev3: fetch: reading CA file %q: %w", caFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("hev3: fetch: no certificates found in CA file %q", caFile)
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}

// getOverWinner issues one GET for the original URL over est.RT and returns
// an "HTTP/x.y 200 OK"-style status line plus up to maxBodyBytes of the
// response body.
func getOverWinner(ctx context.Context, est *Established, u *url.URL) (string, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", nil, fmt.Errorf("hev3: fetch: building request: %w", err)
	}
	resp, err := est.RT.RoundTrip(req)
	if err != nil {
		return "", nil, fmt.Errorf("hev3: fetch: GET over %s: %w", est.Cand.Label, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", nil, fmt.Errorf("hev3: fetch: reading body from %s: %w", est.Cand.Label, err)
	}
	status := resp.Status
	if resp.Proto != "" {
		status = resp.Proto + " " + resp.Status
	}
	return status, body, nil
}
