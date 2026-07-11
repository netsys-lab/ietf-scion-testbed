package bgppath

import (
	"reflect"
	"testing"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// Line topology 150 -- 155 -- 158 plus a detour 150 -- 154 -- 158.
func testIndex() Index {
	return NewIndex(topo.Graph{Links: []topo.Link{
		{ID: "150-155", A: topo.Endpoint{AS: 150, IfID: "1"}, B: topo.Endpoint{AS: 155, IfID: "2"}},
		{ID: "155-158", A: topo.Endpoint{AS: 155, IfID: "3"}, B: topo.Endpoint{AS: 158, IfID: "4"}},
		{ID: "150-154", A: topo.Endpoint{AS: 150, IfID: "5"}, B: topo.Endpoint{AS: 154, IfID: "6"}},
		{ID: "154-158", A: topo.Endpoint{AS: 154, IfID: "7"}, B: topo.Endpoint{AS: 158, IfID: "8"}},
	}})
}

func routesVia155() map[int]map[int]string {
	return map[int]map[int]string{
		158: {150: "4"}, // 158's best toward 150 leaves on ifid 4 (link 155-158)
		155: {150: "2"},
	}
}

func TestWalkNominal(t *testing.T) {
	vm := Walk(testIndex(), routesVia155(), 158, 150)
	if !vm.Complete {
		t.Fatalf("want complete, got %+v", vm)
	}
	if !reflect.DeepEqual(vm.ASPath, []int{158, 155, 150}) {
		t.Fatalf("as_path: %v", vm.ASPath)
	}
	if !reflect.DeepEqual(vm.PathLinks, []string{"155-158", "150-155"}) {
		t.Fatalf("path_links: %v", vm.PathLinks)
	}
}

func TestWalkReroute(t *testing.T) {
	// BFD tore down 155-158: 158 now routes via 154.
	routes := map[int]map[int]string{158: {150: "8"}, 154: {150: "6"}}
	vm := Walk(testIndex(), routes, 158, 150)
	if !vm.Complete || !reflect.DeepEqual(vm.ASPath, []int{158, 154, 150}) {
		t.Fatalf("rerouted walk: %+v", vm)
	}
}

func TestWalkTruncatesOnMissingRoute(t *testing.T) {
	// 155 reported nothing (linkd 503) — walk stops after the first hop.
	vm := Walk(testIndex(), map[int]map[int]string{158: {150: "4"}}, 158, 150)
	if vm.Complete {
		t.Fatalf("want incomplete, got %+v", vm)
	}
	if !reflect.DeepEqual(vm.ASPath, []int{158, 155}) || !reflect.DeepEqual(vm.PathLinks, []string{"155-158"}) {
		t.Fatalf("truncated walk: %+v", vm)
	}
}

func TestWalkTruncatesOnUnknownIfid(t *testing.T) {
	vm := Walk(testIndex(), map[int]map[int]string{158: {150: "999"}}, 158, 150)
	if vm.Complete || len(vm.PathLinks) != 0 || !reflect.DeepEqual(vm.ASPath, []int{158}) {
		t.Fatalf("unknown-ifid walk: %+v", vm)
	}
}

func TestWalkCycleGuard(t *testing.T) {
	// 155 points back toward 158: the visited set stops the loop.
	routes := map[int]map[int]string{158: {150: "4"}, 155: {150: "3"}}
	vm := Walk(testIndex(), routes, 158, 150)
	if vm.Complete {
		t.Fatalf("cycle must not complete: %+v", vm)
	}
	if !reflect.DeepEqual(vm.ASPath, []int{158, 155}) {
		t.Fatalf("cycle walk: %+v", vm)
	}
}

func TestWalkSrcEqualsDst(t *testing.T) {
	vm := Walk(testIndex(), nil, 150, 150)
	if !vm.Complete || !reflect.DeepEqual(vm.ASPath, []int{150}) || len(vm.PathLinks) != 0 {
		t.Fatalf("self walk: %+v", vm)
	}
}
