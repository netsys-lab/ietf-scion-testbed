// Package bgppath walks BIRD's per-AS best-route tables (polled from every
// linkd's /api/v1/bgp routes key) from a source AS toward a destination AS,
// producing the ordered AS path and dashboard link IDs the frontend draws as
// the "BGP path" overlay next to the SCION trace. Pure functions: all
// degradation (missing route data, unknown ifids, routing loops mid-
// convergence) truncates the walk and clears Complete — never an error.
package bgppath

import (
	"strconv"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// VM is the frame.bgp_path wire shape. Src/Dst are the traced pair's IA
// strings ("1-158"), filled by the api layer from the trace VM; Walk fills
// the rest.
type VM struct {
	Src       string   `json:"src"`
	Dst       string   `json:"dst"`
	ASPath    []int    `json:"as_path"`
	PathLinks []string `json:"path_links"`
	Complete  bool     `json:"complete"`
}

// hop resolves one (AS, egress ifid) to the dashboard link it rides and the
// AS on the far end.
type hop struct {
	linkID string
	peer   int
}

// Index maps "as/ifid" (both sides of every link) to its link ID + peer AS.
type Index map[string]hop

func NewIndex(g topo.Graph) Index {
	idx := make(Index, 2*len(g.Links))
	for _, l := range g.Links {
		idx[key(l.A.AS, l.A.IfID)] = hop{l.ID, l.B.AS}
		idx[key(l.B.AS, l.B.IfID)] = hop{l.ID, l.A.AS}
	}
	return idx
}

func key(as int, ifid string) string { return strconv.Itoa(as) + "/" + ifid }

// Walk follows per-AS best routes from src toward dst. routes is
// map[queriedAS]map[destinationAS]egressIfid, exactly as polled. The visited
// set both guards against transient routing loops and bounds the walk to one
// visit per AS (≤12 on this testbed) — no separate hop cap needed.
func Walk(idx Index, routes map[int]map[int]string, src, dst int) VM {
	vm := VM{ASPath: []int{src}}
	if src == dst {
		vm.Complete = true
		return vm
	}
	visited := map[int]bool{src: true}
	cur := src
	for cur != dst {
		ifid, ok := routes[cur][dst]
		if !ok {
			return vm
		}
		h, ok := idx[key(cur, ifid)]
		if !ok {
			return vm
		}
		if visited[h.peer] {
			return vm
		}
		visited[h.peer] = true
		vm.ASPath = append(vm.ASPath, h.peer)
		vm.PathLinks = append(vm.PathLinks, h.linkID)
		cur = h.peer
	}
	vm.Complete = true
	return vm
}
