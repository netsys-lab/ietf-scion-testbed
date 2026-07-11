package hev3

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/scionproto/scion/pkg/addr"
	"github.com/scionproto/scion/pkg/daemon"
	daemon_types "github.com/scionproto/scion/pkg/daemon/types"
	"github.com/scionproto/scion/pkg/snet"
	"github.com/scionproto/scion/pkg/snet/addrutil"
)

// pathQuerier lists SCION paths to a destination IA. It is the seam tests use
// to feed ExpandSCION synthetic paths without a live daemon.
type pathQuerier interface {
	Paths(ctx context.Context, dst addr.IA) ([]snet.Path, error)
}

// daemonQuerier is the production pathQuerier: it asks sciond for paths from the
// local IA.
type daemonQuerier struct {
	conn    daemon.Connector
	localIA addr.IA
}

func (q daemonQuerier) Paths(ctx context.Context, dst addr.IA) ([]snet.Path, error) {
	return q.conn.Paths(ctx, dst, q.localIA, daemon_types.PathReqFlags{})
}

// resolveQuerier connects to sciond and returns a daemonQuerier. It is a var so
// tests can inject a fake querier or force the daemon-absent branch.
var resolveQuerier = func(ctx context.Context, o DialerOptions) (pathQuerier, error) {
	conn, err := daemon.NewService(daemonAddress(o)).Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("hev3: connecting to daemon: %w", err)
	}
	localIA, err := conn.LocalIA(ctx)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("hev3: querying local IA: %w", err)
	}
	return daemonQuerier{conn: conn, localIA: localIA}, nil
}

// ExpandSCION turns proto-SCION candidates (FamilySCION, no pinned Path) into
// dialable candidates. With a reachable daemon each proto-candidate becomes up
// to K native per-path candidates ranked by advertised latency then hop count,
// labelled "…#p1".."…#pK". With no daemon but an fc00 scitra route present,
// each becomes one ViaScitra candidate dialed as its mapped IPv6 address; with
// neither, the SCION candidates are dropped (recorded as Timeline "fail"
// notes). Non-SCION and already-expanded candidates pass through unchanged.
func ExpandSCION(ctx context.Context, cands []Candidate, o DialerOptions) []Candidate {
	k := o.K
	if k <= 0 {
		k = defaultK
	}

	var passthrough, proto []Candidate
	for _, c := range cands {
		if c.Family == FamilySCION && c.Path == nil && !c.ViaScitra {
			proto = append(proto, c)
		} else {
			passthrough = append(passthrough, c)
		}
	}
	if len(proto) == 0 {
		return passthrough
	}

	out := passthrough
	q, err := resolveQuerier(ctx, o)
	if err != nil {
		// Daemon absent: fall back to scitra when the route exists, else drop.
		return append(out, fallbackNoDaemon(proto, o)...)
	}
	for _, c := range proto {
		out = append(out, expandOne(ctx, q, c, k, o)...)
	}
	return out
}

// expandOne queries and ranks paths for one proto-candidate and emits up to k
// native per-path candidates. A parse failure or a daemon that reports no paths
// drops the candidate with a Timeline note (the daemon is present, so scitra —
// the no-daemon fallback — does not apply here).
func expandOne(ctx context.Context, q pathQuerier, c Candidate, k int, o DialerOptions) []Candidate {
	dst, err := addr.ParseIA(c.IA)
	if err != nil {
		note(o, c, fmt.Sprintf("bad IA %q: %v", c.IA, err))
		return nil
	}
	paths, err := q.Paths(ctx, dst)
	if err != nil {
		note(o, c, fmt.Sprintf("path lookup failed: %v", err))
		return nil
	}
	if len(paths) == 0 {
		note(o, c, "no SCION paths")
		return nil
	}
	rankPaths(paths)
	if len(paths) > k {
		paths = paths[:k]
	}
	out := make([]Candidate, 0, len(paths))
	for i, p := range paths {
		md := p.Metadata()
		nc := c
		nc.Label = fmt.Sprintf("%s#p%d", c.Label, i+1)
		nc.Path = &SCIONPath{
			Fingerprint: snet.Fingerprint(md.Interfaces).String(),
			Latency:     knownLatency(md),
			Hops:        len(md.Interfaces),
			SNET:        p,
		}
		out = append(out, nc)
	}
	return out
}

// fallbackNoDaemon maps each proto-candidate to one ViaScitra candidate when an
// fc00 route exists, or drops them all with Timeline notes otherwise.
func fallbackNoDaemon(proto []Candidate, o DialerOptions) []Candidate {
	if !scitraAvailable() {
		for _, c := range proto {
			note(o, c, "no SCION daemon and no scitra route")
		}
		return nil
	}
	var out []Candidate
	for _, c := range proto {
		sc, err := scitraCandidate(c)
		if err != nil {
			note(o, c, err.Error())
			continue
		}
		out = append(out, sc)
	}
	return out
}

// scitraCandidate rewrites a proto-SCION candidate into an IPv6 ViaScitra
// candidate whose Host is the SCION-IP-translator mapping of IA+Host, keeping
// port and ALPN.
func scitraCandidate(c Candidate) (Candidate, error) {
	host, err := netip.ParseAddr(c.Host)
	if err != nil {
		return Candidate{}, fmt.Errorf("bad host %q: %w", c.Host, err)
	}
	mapped, err := ScitraMap(c.IA, host, scitraPrefixDefault)
	if err != nil {
		return Candidate{}, err
	}
	nc := c
	nc.Family = FamilyIPv6
	nc.ViaScitra = true
	nc.Host = mapped.String()
	nc.Path = nil
	nc.Label = "scitra:" + c.IA + "," + mapped.String()
	return nc, nil
}

func note(o DialerOptions, c Candidate, detail string) {
	if o.Timeline != nil {
		o.Timeline.Add("fail", c.Label, detail)
	}
}

// knownLatency sums the advertised (non-negative) per-hop latencies; unset
// (negative) entries are skipped.
func knownLatency(md *snet.PathMetadata) time.Duration {
	var total time.Duration
	for _, d := range md.Latency {
		if d >= 0 {
			total += d
		}
	}
	return total
}

// rankPaths orders paths by advertised latency ascending, then hop count, then
// fingerprint (deterministic). A path with any unset latency entry sorts after
// all fully-annotated paths, mirroring idint-probed's SortPaths.
func rankPaths(paths []snet.Path) {
	type key struct {
		unset bool
		total time.Duration
		hops  int
		fp    string
	}
	keyOf := func(p snet.Path) key {
		md := p.Metadata()
		k := key{hops: len(md.Interfaces), fp: snet.Fingerprint(md.Interfaces).String()}
		for _, d := range md.Latency {
			if d < 0 {
				k.unset = true
			} else {
				k.total += d
			}
		}
		return k
	}
	keys := make([]key, len(paths))
	for i, p := range paths {
		keys[i] = keyOf(p)
	}
	idx := make([]int, len(paths))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool {
		a, b := keys[idx[i]], keys[idx[j]]
		if a.unset != b.unset {
			return !a.unset
		}
		if !a.unset && a.total != b.total {
			return a.total < b.total
		}
		if a.hops != b.hops {
			return a.hops < b.hops
		}
		return a.fp < b.fp
	})
	sorted := make([]snet.Path, len(paths))
	for i, j := range idx {
		sorted[i] = paths[j]
	}
	copy(paths, sorted)
}

// dialSCION dials native HTTP/3 over SCION QUIC on the candidate's pinned path.
//
// It dials quic-go directly over an snet.Conn (quic.Transport{Conn: snetConn})
// rather than pkg/snet/squic: squic.ConnDialer wraps the QUIC session as a
// single-stream net.Conn for control-plane RPC, whereas HTTP/3 needs the raw
// *quic.Conn. The remote *snet.UDPAddr pins the dataplane path and next hop.
func dialSCION(ctx context.Context, c Candidate, o DialerOptions) (*Established, error) {
	if c.Path == nil {
		return nil, errors.New("hev3: dial scion: candidate has no pinned path")
	}
	p, ok := c.Path.SNET.(snet.Path)
	if !ok {
		return nil, fmt.Errorf("hev3: dial scion: pinned path is %T, not snet.Path", c.Path.SNET)
	}
	dstIA, err := addr.ParseIA(c.IA)
	if err != nil {
		return nil, fmt.Errorf("hev3: dial scion: bad IA %q: %w", c.IA, err)
	}
	host, err := netip.ParseAddr(c.Host)
	if err != nil {
		return nil, fmt.Errorf("hev3: dial scion: bad host %q: %w", c.Host, err)
	}

	sciond, err := daemon.NewService(daemonAddress(o)).Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("hev3: dial scion: connecting to daemon: %w", err)
	}
	topo, err := daemon.LoadTopology(ctx, sciond)
	if err != nil {
		_ = sciond.Close()
		return nil, fmt.Errorf("hev3: dial scion: loading topology: %w", err)
	}
	localIP, err := addrutil.DefaultLocalIP(ctx, daemon.TopoQuerier{Connector: sciond})
	if err != nil {
		_ = sciond.Close()
		return nil, fmt.Errorf("hev3: dial scion: resolving local IP: %w", err)
	}

	sn := &snet.SCIONNetwork{
		Topology: topo,
		SCMPHandler: snet.SCMPPropagationStopper{
			Handler: snet.DefaultSCMPHandler{
				RevocationHandler: daemon.RevHandler{Connector: sciond},
			},
		},
	}
	conn, err := sn.Listen(ctx, "udp", &net.UDPAddr{IP: localIP, Port: 0})
	if err != nil {
		_ = sciond.Close()
		return nil, fmt.Errorf("hev3: dial scion: opening socket: %w", err)
	}

	tr := &quic.Transport{Conn: conn}
	remote := &snet.UDPAddr{
		IA:      dstIA,
		Host:    net.UDPAddrFromAddrPort(netip.AddrPortFrom(host, c.Port)),
		Path:    p.Dataplane(),
		NextHop: p.UnderlayNextHop(),
	}
	qconn, err := tr.Dial(ctx, remote, h3TLSConfig(o.TLS, host.String()), nil)
	if err != nil {
		_ = tr.Close()
		_ = conn.Close()
		_ = sciond.Close()
		return nil, fmt.Errorf("hev3: dial scion %s: %w", remote, err)
	}
	return newH3Established(c, qconn, func() error {
		_ = tr.Close()
		_ = conn.Close()
		return sciond.Close()
	}), nil
}
