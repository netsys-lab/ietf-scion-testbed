package mock

import (
	"context"
	"time"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/api"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/derive"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/linkdclient"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// Controller is the mock-mode implementation of api.Controller: instead of
// fanning shaping requests out over HTTP to per-AS linkd instances, it reads
// and writes shaping directly on the Generator driving the mock telemetry, so
// the dashboard's shaping controls work end-to-end without a live testbed.
type Controller struct {
	g   topo.Graph
	gen *Generator
}

var _ api.Controller = (*Controller)(nil)

// NewController builds a Controller for graph g backed by gen.
func NewController(g topo.Graph, gen *Generator) *Controller {
	return &Controller{g: g, gen: gen}
}

// Poll returns the Generator's current shaping snapshot, standing in for a
// poll of every AS's linkd.
func (c *Controller) Poll(ctx context.Context) (shaping, baseline map[string]*derive.Shaping) {
	// The mock has no declared baseline profile; a nil baseline map makes the
	// deriver fall back to its observed-min RTT baseline, which is fine for
	// synthetic demo data.
	return c.gen.CurrentShaping(), nil
}

// Apply sets or clears shaping on the Generator for link.ID. Unlike the real
// linkd client, the mock generator has no notion of per-direction shaping
// (one Shaping value covers the whole link, both directions); direction only
// controls which endpoint(s) get a synthesized Result, mirroring the shape of
// linkdclient.Client.Apply's response.
func (c *Controller) Apply(ctx context.Context, link topo.Link, direction string, p derive.Shaping, clear bool) []linkdclient.Result {
	if clear {
		c.gen.SetShaping(link.ID, nil)
	} else {
		shaping := p
		c.gen.SetShaping(link.ID, &shaping)
	}

	var endpoints []topo.Endpoint
	switch direction {
	case "a_to_b":
		endpoints = []topo.Endpoint{link.A}
	case "b_to_a":
		endpoints = []topo.Endpoint{link.B}
	case "both":
		endpoints = []topo.Endpoint{link.A, link.B}
	}

	results := make([]linkdclient.Result, 0, len(endpoints))
	for _, ep := range endpoints {
		results = append(results, linkdclient.Result{AS: ep.AS, OK: true})
	}
	return results
}

// PollBGP synthesizes session state so the badge and failure demo are
// rehearsable off-fleet: 100% mock loss reads as a torn-down session. It also
// synthesizes a best-route table per AS (see bgpRoutes) so the BGP path
// overlay has something to walk without a live BGP fleet.
func (c *Controller) PollBGP(ctx context.Context) (map[string]*derive.BGPLink, map[int]map[int]string) {
	now := time.Now().Unix()
	shaped := c.gen.CurrentShaping()
	out := make(map[string]*derive.BGPLink, len(c.g.Links))
	for _, l := range c.g.Links {
		state := "Established"
		if p := shaped[l.ID]; p != nil && p.LossPct != nil && *p.LossPct >= 100 {
			state = "Idle"
		}
		side := func() *derive.BGPSide { return &derive.BGPSide{State: state, SinceUnix: now - 3600} }
		out[l.ID] = &derive.BGPLink{A: side(), B: side()}
	}
	return out, c.bgpRoutes()
}

// bgpRoutes synthesizes per-AS best-route tables as shortest hop-count (BFS)
// over links not currently at 100% mock loss, so the overlay reroutes in the
// failure demo the way live BFD does. Links are walked in graph order, which
// is stable, so tie-breaks are deterministic across polls.
func (c *Controller) bgpRoutes() map[int]map[int]string {
	type edge struct {
		peer int
		ifid string
	}
	shaped := c.gen.CurrentShaping()
	adj := map[int][]edge{}
	for _, l := range c.g.Links {
		if p := shaped[l.ID]; p != nil && p.LossPct != nil && *p.LossPct >= 100 {
			continue
		}
		adj[l.A.AS] = append(adj[l.A.AS], edge{l.B.AS, l.A.IfID})
		adj[l.B.AS] = append(adj[l.B.AS], edge{l.A.AS, l.B.IfID})
	}
	out := make(map[int]map[int]string, len(c.g.ASes))
	for _, as := range c.g.ASes {
		src := as.Num
		first := map[int]string{}
		seen := map[int]bool{src: true}
		queue := []int{src}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, e := range adj[cur] {
				if seen[e.peer] {
					continue
				}
				seen[e.peer] = true
				if cur == src {
					first[e.peer] = e.ifid
				} else {
					first[e.peer] = first[cur]
				}
				queue = append(queue, e.peer)
			}
		}
		out[src] = first
	}
	return out
}

// AllHealth reports every AS in the graph as reachable: there is no real
// linkd to fail to reach in mock mode.
func (c *Controller) AllHealth(ctx context.Context) map[int]bool {
	out := make(map[int]bool, len(c.g.ASes))
	for _, as := range c.g.ASes {
		out[as.Num] = true
	}
	return out
}
