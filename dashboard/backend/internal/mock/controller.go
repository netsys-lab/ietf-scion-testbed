package mock

import (
	"context"

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

// AllHealth reports every AS in the graph as reachable: there is no real
// linkd to fail to reach in mock mode.
func (c *Controller) AllHealth(ctx context.Context) map[int]bool {
	out := make(map[int]bool, len(c.g.ASes))
	for _, as := range c.g.ASes {
		out[as.Num] = true
	}
	return out
}
