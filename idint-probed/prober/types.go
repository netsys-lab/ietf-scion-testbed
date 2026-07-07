// Package prober implements pure logic to convert SCION path metadata and
// decoded ID-INT reports into the wire JSON types served by idint-probed.
package prober

import "github.com/scionproto/scion/pkg/slayers/idint"

// IfaceJSON is one path interface.
type IfaceJSON struct {
	IA   string `json:"ia"` // "1-150"
	IfID uint64 `json:"ifid"`
}

// PathJSON is a flattened sciond path.
type PathJSON struct {
	Fingerprint string      `json:"fingerprint"` // hex sha256 (snet.Fingerprint .String())
	MTU         int         `json:"mtu"`
	Expiry      string      `json:"expiry"` // RFC3339
	Interfaces  []IfaceJSON `json:"interfaces"`
	LatencyUs   []int64     `json:"latency_us"` // per metadata entry; -1 = unset
}

// PathsResponse is the response to GET /api/v1/paths.
type PathsResponse struct {
	LocalIA string     `json:"local_ia"`
	Paths   []PathJSON `json:"paths"` // sciond order; [0] = current best
}

// HopRecord is one hop's worth of decoded ID-INT telemetry.
type HopRecord struct {
	Hop           int      `json:"hop"`
	IA            string   `json:"ia"`
	Source        bool     `json:"source"`
	Ingress       bool     `json:"ingress"`
	Egress        bool     `json:"egress"`
	Aggregated    bool     `json:"aggregated"`
	Encrypted     bool     `json:"encrypted"`
	Verified      bool     `json:"verified"`
	NodeId        *uint32  `json:"node_id,omitempty"`
	IgrIfid       *uint16  `json:"igr_ifid,omitempty"`
	EgrIfid       *uint16  `json:"egr_ifid,omitempty"`
	RttNextBrUs   *int64   `json:"rtt_next_br_us,omitempty"`
	EgrLinkTxPct  *float64 `json:"egr_link_tx_pct,omitempty"` // 0..100
	IngressTstamp *uint64  `json:"ingress_tstamp,omitempty"`
	QueueLen      *int64   `json:"queue_len,omitempty"`
}

// ProbeResult is the response to POST /api/v1/probe.
type ProbeResult struct {
	Path           PathJSON    `json:"path"`
	ProbeRttMs     float64     `json:"probe_rtt_ms"`
	MaxLenExceeded bool        `json:"max_len_exceeded"`
	Fwd            []HopRecord `json:"fwd"`
	Rev            []HopRecord `json:"rev"`
}

// FixedInstructions is the one instruction set every probe requests (spec:
// no presets). Order matters: slot index identifies the value downstream.
var FixedInstructions = [4]uint8{
	idint.InRttNextBr,     // 0x4A, µs
	idint.InEgressLinkTx,  // 0x4F, fraction of ^uint32(0)
	idint.InIngressTstamp, // 0x82, 48-bit ns timestamp
	idint.InInstQueueLen,  // 0x51, packets
}
