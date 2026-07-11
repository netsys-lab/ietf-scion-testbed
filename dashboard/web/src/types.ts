// Wire types mirroring the fabricd backend's JSON shapes exactly (field
// names match the Go json tags, snake_case). Keep this file in sync with
// dashboard/backend/internal/topo/topo.go, .../derive/derive.go, and
// .../api/api.go — those are the source of truth.

// --- topo.Graph -------------------------------------------------------

export interface Endpoint {
  ia: string; // "1-155"
  as: number; // 155
  ifid: string;
  ip: string; // underlay local host, "fd00:fade:9::155"
  link_to: string; // parent|child|core|peer
}

export interface Link {
  id: string; // "151-155" (lower AS first)
  type: string; // core|child|peer
  subnet: string; // "link 9"
  a: Endpoint;
  b: Endpoint;
}

export interface AS {
  ia: string;
  num: number;
  core: boolean;
  mgmt_ip: string;
}

export interface Graph {
  ases: AS[];
  links: Link[];
}

// --- derive.Frame -------------------------------------------------------

// Band names, ordered by increasing severity for nominal..critical; down and
// stale are health overrides that sit outside that ordering.
export type Band = "nominal" | "elevated" | "degraded" | "critical" | "down" | "stale";

export interface Shaping {
  delay_ms?: number;
  jitter_ms?: number;
  loss_pct?: number;
  rate_mbit?: number;
}

export interface LinkVM {
  id: string;
  band: Band;
  rtt_ms_a: number;
  rtt_ms_b: number;
  rate_ab_mbit: number;
  rate_ba_mbit: number;
  loss_pct: number;
  up: boolean;
  stale: boolean;
  shaping?: Shaping;
  // The link's declared baseline (story) shape: nominal one-way delay and rate
  // tier. The shaping sliders bound to these (delay can't go below the
  // baseline, rate can't go above it) since a link can only be shaped worse
  // than its default. Absent if the backend reports no baseline.
  baseline_delay_ms?: number;
  baseline_rate_mbit?: number;
  // BGP session state derived by fabricd from BIRD (Task 6); absent entirely
  // until BIRD is rolled out, so consumers null-guard.
  bgp?: "up" | "degraded" | "down" | "unknown";
}

export interface ASVM {
  ia: string;
  br_up: boolean;
  cs_up: boolean;
  sd_up: boolean;
  beacons_per_sec: number;
}

export interface KPI {
  links_up: number;
  links_total: number;
  shaped: number;
  total_mbit: number;
  avg_core_rtt_ms: number;
  beacons_per_sec: number;
}

export interface Frame {
  t: number;
  links: LinkVM[];
  ases: ASVM[];
  kpi: KPI;
  trace?: TraceVM;
  bgp_path?: BgpPathVM;
}

// --- idint trace (path inspector) ------------------------------------------

export interface TraceHop {
  ia: string;
  link: string;
  rtt_next_br_us?: number;
  egr_tx_pct?: number;
  queue_len?: number;
  node_id?: number;
  verified: boolean;
}

export interface TraceVM {
  src: string;
  dst: string;
  fingerprint: string;
  auto: boolean;
  path_links: string[];
  ok: boolean;
  error?: string;
  updated_at: number;
  probe_rtt_ms: number;
  hops: TraceHop[];
}

// frame.bgp_path: the current BGP best path for the traced pair, walked by
// fabricd from every AS's BIRD route table each poll. Present only while a
// trace session is active AND BGP route data has been polled; complete=false
// means the walk truncated (linkd unreachable / no best route) and as_path
// is the partial prefix.
export interface BgpPathVM {
  src: string;
  dst: string;
  as_path: number[];
  path_links: string[] | null;
  complete: boolean;
}

export interface PathOption {
  fingerprint: string;
  hops: number[];
  // links can be JSON null in a rare degraded case: fabricd's PathOptions
  // swallows unresolvable-path errors (e.g. a hop AS missing from the graph)
  // rather than failing the whole response, so consumers must guard before
  // calling .map on this.
  links: string[] | null;
  latency_us_total: number;
  mtu: number;
  expiry: string;
  current_best: boolean;
}

export interface IdintPathsResponse {
  src: string;
  dst: string;
  paths: PathOption[];
}

// --- /api/live envelope --------------------------------------------------

export interface SnapshotMsg {
  type: "snapshot";
  topology: Graph;
  frame: Frame;
}

export interface FrameMsg {
  type: "frame";
  frame: Frame;
}

export type LiveMsg = SnapshotMsg | FrameMsg;

// --- /api/history ---------------------------------------------------------

export interface Sample {
  t: number;
  v: number;
}

// --- /api/links/{id}/shaping and /reset -----------------------------------

export type Direction = "a_to_b" | "b_to_a" | "both";

export interface ShapingResult {
  as: number;
  ok: boolean;
  error?: string;
}

export interface ShapingResponse {
  results: ShapingResult[];
}

// --- Frontend-only: ticker events derived from band changes ---------------

export type TickerCls = "good" | "warn" | "bad" | "crit" | "brass";

export interface TickerEvent {
  t: number;
  text: string;
  cls: TickerCls;
}
