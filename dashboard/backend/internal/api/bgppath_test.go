package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/bgppath"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/derive"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

func overlayIndex() bgppath.Index {
	return bgppath.NewIndex(topo.Graph{Links: []topo.Link{
		{ID: "155-158", A: topo.Endpoint{AS: 155, IfID: "3"}, B: topo.Endpoint{AS: 158, IfID: "4"}},
		{ID: "150-155", A: topo.Endpoint{AS: 150, IfID: "1"}, B: topo.Endpoint{AS: 155, IfID: "2"}},
	}})
}

func TestAttachBGPPath(t *testing.T) {
	f := derive.Frame{Trace: &derive.TraceVM{Src: "1-158", Dst: "1-150"}}
	routes := map[int]map[int]string{158: {150: "4"}, 155: {150: "2"}}
	attachBGPPath(&f, overlayIndex(), routes)
	if f.BGPPath == nil {
		t.Fatal("bgp_path not attached")
	}
	if f.BGPPath.Src != "1-158" || f.BGPPath.Dst != "1-150" || !f.BGPPath.Complete {
		t.Fatalf("vm: %+v", f.BGPPath)
	}
	if !reflect.DeepEqual(f.BGPPath.ASPath, []int{158, 155, 150}) {
		t.Fatalf("as_path: %v", f.BGPPath.ASPath)
	}
}

func TestAttachBGPPathNoTrace(t *testing.T) {
	f := derive.Frame{}
	attachBGPPath(&f, overlayIndex(), map[int]map[int]string{})
	if f.BGPPath != nil {
		t.Fatalf("no trace must attach nothing: %+v", f.BGPPath)
	}
	// Wire-shape pin: an unattached path must be omitted entirely (omitempty),
	// so pre-rollout / trace-idle frames carry no bgp_path key at all.
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "bgp_path") {
		t.Fatalf("bgp_path must be omitted when unattached: %s", b)
	}
}

func TestAttachBGPPathNoRoutes(t *testing.T) {
	f := derive.Frame{Trace: &derive.TraceVM{Src: "1-158", Dst: "1-150"}}
	attachBGPPath(&f, overlayIndex(), nil)
	if f.BGPPath != nil {
		t.Fatalf("nil routes must attach nothing (pre-first-poll): %+v", f.BGPPath)
	}
}
