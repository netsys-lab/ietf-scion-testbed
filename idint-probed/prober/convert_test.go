package prober_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netsys-lab/ietf-scion-testbed/idint-probed/prober"
	"github.com/scionproto/scion/pkg/addr"
	"github.com/scionproto/scion/pkg/snet"
	spath "github.com/scionproto/scion/pkg/snet/path"
)

var errHopIndex = errors.New("hop index out of range")

func mkHop(idx uint8, src, in, eg bool, nodeID uint32, rttUs uint64) snet.IntMetadata {
	h := snet.IntMetadata{HopIndex: idx, Source: src, Ingress: in, Egress: eg}
	h.SetNodeId(nodeID)
	if rttUs > 0 {
		h.SetDataUint32(0, uint32(rttUs)) // slot 0 = InRttNextBr
	}
	return h
}

func TestRecordsSlotMapping(t *testing.T) {
	rep := &snet.IntReport{
		Instructions: prober.FixedInstructions,
		Data: []snet.IntMetadata{
			mkHop(0, true, false, false, 0, 0),    // source entry, no data
			mkHop(0, false, false, true, 2, 5600), // egress record w/ RTT
		},
	}
	ias := snet.HopToIA(func(i uint) (addr.IA, error) { return addr.MustParseIA("1-150"), nil })
	recs, err := prober.Records(rep, ias, true)
	if err != nil {
		t.Fatalf("Records: unexpected error: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("len(recs) = %d, want 2", len(recs))
	}
	if recs[0].RttNextBrUs != nil {
		t.Errorf("recs[0].RttNextBrUs = %v, want nil", *recs[0].RttNextBrUs)
	}
	if recs[1].RttNextBrUs == nil {
		t.Fatalf("recs[1].RttNextBrUs = nil, want *5600")
	}
	if *recs[1].RttNextBrUs != 5600 {
		t.Errorf("*recs[1].RttNextBrUs = %d, want 5600", *recs[1].RttNextBrUs)
	}
	if !recs[1].Egress {
		t.Errorf("recs[1].Egress = false, want true")
	}
	if !recs[1].Verified {
		t.Errorf("recs[1].Verified = false, want true")
	}
	if recs[1].IA != "1-150" {
		t.Errorf("recs[1].IA = %q, want %q", recs[1].IA, "1-150")
	}
}

func TestRecordsHopIndexError(t *testing.T) {
	rep := &snet.IntReport{
		Instructions: prober.FixedInstructions,
		Data:         []snet.IntMetadata{mkHop(0, true, false, false, 0, 0)},
	}
	failing := snet.HopToIA(func(i uint) (addr.IA, error) {
		return 0, errHopIndex
	})
	recs, err := prober.Records(rep, failing, true)
	if err == nil {
		t.Fatal("Records: expected error, got nil")
	}
	if recs != nil {
		t.Errorf("Records: expected nil records on error, got %v", recs)
	}
}

func TestPathToJSONLatencyUnset(t *testing.T) {
	expiry := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	md := snet.PathMetadata{
		Interfaces: []snet.PathInterface{
			{IA: addr.MustParseIA("1-150"), ID: 1},
			{IA: addr.MustParseIA("1-154"), ID: 2},
			{IA: addr.MustParseIA("1-154"), ID: 3},
		},
		MTU:     1472,
		Expiry:  expiry,
		Latency: []time.Duration{5 * time.Millisecond, -1},
	}
	p := spath.Path{Meta: md}

	pj := prober.PathToJSON(p)

	if len(pj.LatencyUs) != 2 {
		t.Fatalf("len(LatencyUs) = %d, want 2", len(pj.LatencyUs))
	}
	if pj.LatencyUs[0] != 5000 {
		t.Errorf("LatencyUs[0] = %d, want 5000", pj.LatencyUs[0])
	}
	if pj.LatencyUs[1] != -1 {
		t.Errorf("LatencyUs[1] = %d, want -1", pj.LatencyUs[1])
	}
	if pj.Fingerprint == "" {
		t.Fatal("Fingerprint is empty, want non-empty hex string")
	}
	if strings.Trim(pj.Fingerprint, "0123456789abcdef") != "" {
		t.Errorf("Fingerprint = %q, want hex-only", pj.Fingerprint)
	}
	if pj.MTU != 1472 {
		t.Errorf("MTU = %d, want 1472", pj.MTU)
	}
	if pj.Expiry != expiry.Format(time.RFC3339) {
		t.Errorf("Expiry = %q, want %q", pj.Expiry, expiry.Format(time.RFC3339))
	}
}
