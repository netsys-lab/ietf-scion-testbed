package main

import (
	"testing"
	"time"

	"github.com/netsys-lab/ietf-scion-testbed/hev3/pkg/hev3"
)

// TestBuildRaceRows_SuppressesNeverStartedSCIONProtoParent pins the fix for
// the spurious "never-started" row: a SCION proto-candidate's un-suffixed
// label gets a "candidate" event but is expanded into "#pN" children before
// any "attempt" fires, so the parent label must not render as its own
// permanent never-started row next to its started children. A genuinely
// never-started, unrelated candidate (no started row shares its label as a
// prefix) must still survive untouched.
func TestBuildRaceRows_SuppressesNeverStartedSCIONProtoParent(t *testing.T) {
	const proto = "scion:1-150,10.20.3.215"
	const p1 = proto + "#p1"
	const p2 = proto + "#p2"
	const unrelated = "v4:9.9.9.9"

	events := []hev3.Event{
		{At: 0, Kind: "candidate", Label: proto},
		{At: 1 * time.Millisecond, Kind: "candidate", Label: unrelated},
		{At: 2 * time.Millisecond, Kind: "attempt", Label: p1},
		{At: 3 * time.Millisecond, Kind: "attempt", Label: p2},
		{At: 10 * time.Millisecond, Kind: "success", Label: p1},
		{At: 10 * time.Millisecond, Kind: "winner", Label: p1},
		{At: 12 * time.Millisecond, Kind: "cancel", Label: p2},
	}

	rows := buildRaceRows(events)

	byLabel := map[string]raceRow{}
	for _, r := range rows {
		byLabel[r.label] = r
	}

	if _, ok := byLabel[proto]; ok {
		t.Errorf("buildRaceRows: expanded proto-candidate row %q must be suppressed, got rows %+v", proto, rows)
	}
	if r, ok := byLabel[p1]; !ok {
		t.Errorf("buildRaceRows: expected winner row %q, got rows %+v", p1, rows)
	} else if !r.winner || r.outcome != "won" {
		t.Errorf("buildRaceRows: %q = %+v, want winner/won", p1, r)
	}
	if r, ok := byLabel[p2]; !ok {
		t.Errorf("buildRaceRows: expected cancelled row %q, got rows %+v", p2, rows)
	} else if r.outcome != "cancelled" {
		t.Errorf("buildRaceRows: %q outcome = %q, want cancelled", p2, r.outcome)
	}
	r, ok := byLabel[unrelated]
	if !ok {
		t.Fatalf("buildRaceRows: unrelated never-started candidate %q must survive, got rows %+v", unrelated, rows)
	}
	if r.started || r.outcome != "" {
		t.Errorf("buildRaceRows: unrelated row %q = %+v, want untouched never-started", unrelated, r)
	}

	if len(rows) != 3 {
		t.Errorf("buildRaceRows: got %d rows %+v, want exactly 3 (proto parent suppressed)", len(rows), rows)
	}
}
