package topo

import (
	"net/netip"
	"testing"
)

func TestLoad(t *testing.T) {
	ifs, err := Load("testdata/topology.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(ifs) != 2 {
		t.Fatalf("want 2 interfaces, got %d", len(ifs))
	}
	if ifs[0].IfID != "6049" || ifs[1].IfID != "64100" {
		t.Fatalf("bad order: %+v", ifs)
	}
	want := netip.MustParseAddr("fd00:fade:9::155")
	if ifs[0].LocalIP != want || ifs[0].Neighbor != "1-151" || ifs[0].LinkTo != "parent" {
		t.Fatalf("got %+v", ifs[0])
	}
}

func TestLoadNoMatch(t *testing.T) {
	if _, err := Load("testdata/nope-*.json"); err == nil {
		t.Fatal("want error")
	}
}
