package topo

import "testing"

func TestLoadGraph(t *testing.T) {
	g, err := Load("testdata")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.ASes) != 2 {
		t.Fatalf("want 2 ASes, got %d", len(g.ASes))
	}
	if g.ASes[0].Num != 155 || g.ASes[0].MgmtIP != "10.20.3.155" || g.ASes[0].Core {
		t.Fatalf("AS155 wrong: %+v", g.ASes[0])
	}
	// only one link is fully paired inside this fixture: 155<->160 (fade:15)
	var found *Link
	for i := range g.Links {
		if g.Links[i].ID == "155-160" {
			found = &g.Links[i]
		}
	}
	if found == nil {
		t.Fatalf("no 155-160 link: %+v", g.Links)
	}
	if found.Subnet != "fade:15" || found.A.AS != 155 || found.B.AS != 160 ||
		found.A.IfID != "36530" || found.B.IfID != "39652" || found.Type != "child" {
		t.Fatalf("link wrong: %+v", *found)
	}
}
