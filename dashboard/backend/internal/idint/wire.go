package idint

// Mirrors idint-probed/prober wire types; keep byte-identical.
//
// fabricd is CGO_ENABLED=0 and idint-probed's module pulls in the cgo-only
// scion fork, so these are redeclared here rather than imported. Any change
// to the pinned contract (docs/superpowers/plans/2026-07-07-idint-panel.md)
// must be mirrored on both sides.

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
	Paths   []PathJSON `json:"paths"` // advertised-latency order; [0] = advertised-fastest
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

// PathOption is one path choice offered by GET /api/idint/paths, derived
// from a PathsResponse entry: AS-number hops, dashboard link IDs, and a
// summed latency estimate.
type PathOption struct {
	Fingerprint    string   `json:"fingerprint"`
	Hops           []int    `json:"hops"`             // AS numbers in order, e.g. [150,154,155,161]
	Links          []string `json:"links"`            // dashboard link IDs
	LatencyUsTotal int64    `json:"latency_us_total"` // -1 when any hop unset
	MTU            int      `json:"mtu"`
	Expiry         string   `json:"expiry"`
	CurrentBest    bool     `json:"current_best"` // true on the first entry
}
