// Package linkdclient talks to every AS's scion-linkd control API
// (http://<MgmtIP>:30480, see linkd/internal/api) to poll the shaping
// currently applied to inter-AS links and to apply or clear shaping on
// them. It is the dashboard backend's only integration point with linkd.
package linkdclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/derive"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// defaultPort is the port scion-linkd's REST API listens on.
const defaultPort = 30480

// Result is the outcome of one Apply/AllHealth call against a single AS's
// linkd. It doubles as the wire shape returned to the dashboard frontend by
// the /api/links/{id}/shaping and /api/links/{id}/reset endpoints (Task 6).
type Result struct {
	AS    int    `json:"as"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Client fans requests out to every AS's linkd instance, addressed by the
// AS's management IP from the topology Graph.
type Client struct {
	g      topo.Graph
	client *http.Client
	base   map[int]string // AS num -> "http://host:port" base URL
}

// New builds a Client for graph g. client governs per-request timeout and
// transport (e.g. a 2s timeout); Poll/Apply/AllHealth never impose their own
// deadline beyond ctx and the client's own settings.
//
// An AS's management IP is normally a bare host ("10.20.3.150"), to which
// defaultPort is appended. If it already contains a port (as test doubles
// do, pointing at an httptest server on an arbitrary port) that port is
// used as-is.
func New(g topo.Graph, client *http.Client) *Client {
	base := make(map[int]string, len(g.ASes))
	for _, as := range g.ASes {
		host := as.MgmtIP
		if _, _, err := net.SplitHostPort(host); err != nil {
			host = net.JoinHostPort(host, strconv.Itoa(defaultPort))
		}
		base[as.Num] = "http://" + host
	}
	return &Client{g: g, client: client, base: base}
}

// linkEntry is one element of a linkd GET /api/v1/links response. Shaping
// reuses derive.Shaping directly: the wire shapes are identical (same
// pointer fields, same json tags), so no separate type is needed. Shaped
// reports whether the interface's current tc state differs from linkd's
// story baseline; every interface carries some Shaping (linkd preshapes all
// links to the baseline), so Shaped -- not Shaping's nilness -- is what
// distinguishes user-applied shaping from baseline preshape.
type linkEntry struct {
	IfID     string          `json:"ifid"`
	Shaping  *derive.Shaping `json:"shaping"`
	Shaped   bool            `json:"shaped"`
	Baseline *derive.Shaping `json:"baseline"`
}

// baseURL returns the "http://host:port" prefix for AS as, and whether as is
// known in the graph.
func (c *Client) baseURL(as int) (string, bool) {
	u, ok := c.base[as]
	return u, ok
}

// Poll fetches GET /api/v1/links from every AS in the graph and returns a
// linkID -> Shaping map using the A-side interface's shaping value: nil when
// A's linkd reports that interface as present but not flagged shaped:true
// (either genuinely unshaped or merely preshaped to the story baseline), a
// non-nil *derive.Shaping only when linkd flags it shaped:true. ASes whose
// linkd cannot be reached are skipped entirely; any link whose A side falls
// on a skipped AS is simply absent from the result. Callers (derive.Deriver)
// treat a missing key the same as "unshaped" -- acceptable v1 semantics per
// the design brief.
// Poll returns two linkID-keyed maps: the current user-applied shaping (nil
// for an unshaped link) and the declared baseline (story) shape linkd
// preshapes every link to. The baseline is what the dashboard treats as a
// link's nominal state — the RTT band and the shaping-slider bounds are both
// relative to it.
func (c *Client) Poll(ctx context.Context) (map[string]*derive.Shaping, map[string]*derive.Shaping) {
	// aSideIdx maps "AS/ifid" to the link ID for every link's A side, so the
	// per-AS interface lists can be matched back to links in O(1).
	aSideIdx := make(map[string]string, len(c.g.Links))
	for _, l := range c.g.Links {
		aSideIdx[asIfKey(l.A.AS, l.A.IfID)] = l.ID
	}

	type fetched struct {
		as   int
		list []linkEntry
		ok   bool
	}
	res := make([]fetched, len(c.g.ASes))
	var wg sync.WaitGroup
	for i, as := range c.g.ASes {
		wg.Add(1)
		go func(i int, as topo.AS) {
			defer wg.Done()
			list, err := c.fetchLinks(ctx, as.Num)
			res[i] = fetched{as: as.Num, list: list, ok: err == nil}
		}(i, as)
	}
	wg.Wait()

	out := make(map[string]*derive.Shaping)
	base := make(map[string]*derive.Shaping)
	for _, r := range res {
		if !r.ok {
			continue
		}
		for _, e := range r.list {
			if linkID, ok := aSideIdx[asIfKey(r.as, e.IfID)]; ok {
				if e.Shaped {
					out[linkID] = e.Shaping
				} else {
					out[linkID] = nil
				}
				base[linkID] = e.Baseline
			}
		}
	}
	return out, base
}

func asIfKey(as int, ifid string) string { return strconv.Itoa(as) + "/" + ifid }

// fetchLinks GETs one AS's /api/v1/links and decodes the response.
func (c *Client) fetchLinks(ctx context.Context, as int) ([]linkEntry, error) {
	base, ok := c.baseURL(as)
	if !ok {
		return nil, fmt.Errorf("AS%d: not in graph", as)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/links", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("AS%d: GET /api/v1/links: status %d", as, resp.StatusCode)
	}
	var list []linkEntry
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("AS%d: decode /api/v1/links: %w", as, err)
	}
	return list, nil
}

type bgpSessionJSON struct {
	IfID  string `json:"ifid"`
	State string `json:"state"`
	Since int64  `json:"since_unix"`
}

// bgpRouteJSON is one entry of a linkd's /api/v1/bgp "routes" array: the
// egress ifid this AS uses as its current best route toward PrefixAS.
type bgpRouteJSON struct {
	PrefixAS int    `json:"prefix_as"`
	IfID     string `json:"ifid"`
}

// PollBGP fans out GET /api/v1/bgp to every AS's linkd. It joins the
// per-endpoint sessions onto links (an AS that errors — linkd down, BIRD
// absent → 503 — leaves its side nil, which derive renders as "unknown") and
// separately returns each reachable AS's best-route table as polled:
// map[queriedAS]map[destinationAS]egressIfid. An AS that errors is absent
// from the routes map entirely (not present with an empty map), and an old
// linkd (pre-0.3.2) that omits the "routes" key decodes to an empty map for
// that AS, so bgppath.Walk simply truncates there — safe during a rolling
// linkd upgrade.
func (c *Client) PollBGP(ctx context.Context) (map[string]*derive.BGPLink, map[int]map[int]string) {
	type side struct {
		linkID string
		isA    bool
	}
	idx := make(map[string]side, 2*len(c.g.Links))
	out := make(map[string]*derive.BGPLink, len(c.g.Links))
	for _, l := range c.g.Links {
		idx[asIfKey(l.A.AS, l.A.IfID)] = side{l.ID, true}
		idx[asIfKey(l.B.AS, l.B.IfID)] = side{l.ID, false}
		out[l.ID] = &derive.BGPLink{}
	}

	type fetched struct {
		as       int
		sessions []bgpSessionJSON
		routes   []bgpRouteJSON
		ok       bool
	}
	res := make([]fetched, len(c.g.ASes))
	var wg sync.WaitGroup
	for i, as := range c.g.ASes {
		wg.Add(1)
		go func(i int, as topo.AS) {
			defer wg.Done()
			ss, rr, err := c.fetchBGP(ctx, as.Num)
			res[i] = fetched{as: as.Num, sessions: ss, routes: rr, ok: err == nil}
		}(i, as)
	}
	wg.Wait()

	routesOut := make(map[int]map[int]string, len(res))
	for _, r := range res {
		if !r.ok {
			continue
		}
		for _, s := range r.sessions {
			sd, ok := idx[asIfKey(r.as, s.IfID)]
			if !ok {
				continue
			}
			bs := &derive.BGPSide{State: s.State, SinceUnix: s.Since}
			if sd.isA {
				out[sd.linkID].A = bs
			} else {
				out[sd.linkID].B = bs
			}
		}

		m := make(map[int]string, len(r.routes))
		for _, rt := range r.routes {
			m[rt.PrefixAS] = rt.IfID
		}
		routesOut[r.as] = m
	}
	return out, routesOut
}

func (c *Client) fetchBGP(ctx context.Context, as int) ([]bgpSessionJSON, []bgpRouteJSON, error) {
	base, ok := c.baseURL(as)
	if !ok {
		return nil, nil, fmt.Errorf("AS%d: not in graph", as)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/bgp", nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("AS%d: GET /api/v1/bgp: status %d", as, resp.StatusCode)
	}
	var body struct {
		Sessions []bgpSessionJSON `json:"sessions"`
		Routes   []bgpRouteJSON   `json:"routes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, nil, fmt.Errorf("AS%d: decode /api/v1/bgp: %w", as, err)
	}
	return body.Sessions, body.Routes, nil
}

// Apply sends PUT (or DELETE when clear) to the linkd(s) owning the
// direction's egress interface(s): "a_to_b" targets link.A only, "b_to_a"
// targets link.B only, "both" targets both (A first, then B). p is ignored
// when clear is true. One Result is returned per endpoint contacted, in
// that order; an unrecognized direction contacts nothing and returns nil.
func (c *Client) Apply(ctx context.Context, link topo.Link, direction string, p derive.Shaping, clear bool) []Result {
	var endpoints []topo.Endpoint
	switch direction {
	case "a_to_b":
		endpoints = []topo.Endpoint{link.A}
	case "b_to_a":
		endpoints = []topo.Endpoint{link.B}
	case "both":
		endpoints = []topo.Endpoint{link.A, link.B}
	}

	results := make([]Result, 0, len(endpoints))
	for _, ep := range endpoints {
		results = append(results, c.applyOne(ctx, ep, p, clear))
	}
	return results
}

// applyOne sends a single PUT or DELETE to the linkd owning endpoint ep.
func (c *Client) applyOne(ctx context.Context, ep topo.Endpoint, p derive.Shaping, clear bool) Result {
	base, ok := c.baseURL(ep.AS)
	if !ok {
		return Result{AS: ep.AS, OK: false, Error: "AS not in graph"}
	}
	url := fmt.Sprintf("%s/api/v1/links/%s", base, ep.IfID)

	method := http.MethodPut
	var body io.Reader
	if clear {
		method = http.MethodDelete
	} else {
		raw, err := json.Marshal(p)
		if err != nil {
			return Result{AS: ep.AS, OK: false, Error: err.Error()}
		}
		body = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return Result{AS: ep.AS, OK: false, Error: err.Error()}
	}
	if !clear {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return Result{AS: ep.AS, OK: false, Error: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Result{AS: ep.AS, OK: false, Error: fmt.Sprintf("status %d: %s", resp.StatusCode, string(msg))}
	}
	return Result{AS: ep.AS, OK: true}
}

// AllHealth GETs /healthz on every AS's linkd and reports which ones
// responded 200 OK. Used to populate the "linkd" section of the /api/health
// endpoint (Task 6).
func (c *Client) AllHealth(ctx context.Context) map[int]bool {
	out := make(map[int]bool, len(c.g.ASes))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, as := range c.g.ASes {
		wg.Add(1)
		go func(as topo.AS) {
			defer wg.Done()
			ok := c.healthOne(ctx, as.Num)
			mu.Lock()
			out[as.Num] = ok
			mu.Unlock()
		}(as)
	}
	wg.Wait()
	return out
}

// healthOne GETs one AS's /healthz and reports whether it responded 200 OK.
func (c *Client) healthOne(ctx context.Context, as int) bool {
	base, ok := c.baseURL(as)
	if !ok {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
