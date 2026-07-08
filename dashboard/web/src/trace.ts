// trace.ts — pure display logic for the TracePanel: turning a live TraceVM's
// hops into renderable rows (RTT bar percentages relative to the trace's own
// worst hop, shaping-overlay detection) and formatting a PathOption's hop
// list / advertised latency for the path picker. Kept out of the React
// component so trace.test.ts can exercise the logic directly (mirrors
// shaping.ts's role for LinkPanel).
import type { LinkVM, PathOption, TraceVM } from "./types";

export interface HopRow {
  link: string;
  ia: string;
  rttMs: number | null;
  barPct: number;
  egrPct: number | null;
  shaped: boolean;
}

// hopRows: one row per hop; barPct scales each hop's RTT against the max hop
// RTT in this VM (0 when no reading); shaped = that link currently carries a
// shaping overlay in the live frame (bar renders warn-colored).
export function hopRows(vm: TraceVM, linksById: Record<string, LinkVM>): HopRow[] {
  const hops = vm.hops ?? [];
  const rtts = hops.map((h) => h.rtt_next_br_us ?? 0);
  const max = Math.max(...rtts, 1);
  return hops.map((h) => ({
    link: h.link,
    ia: h.ia,
    rttMs: h.rtt_next_br_us != null ? h.rtt_next_br_us / 1000 : null,
    barPct: h.rtt_next_br_us != null ? (100 * h.rtt_next_br_us) / max : 0,
    egrPct: h.egr_tx_pct ?? null,
    shaped: linksById[h.link]?.shaping != null,
  }));
}

// pathLabel renders a PathOption's hop AS numbers, e.g. "150 · 154 · 155 · 161".
export function pathLabel(p: PathOption): string {
  return p.hops.join(" · ");
}

// latencyLabel renders the path's advertised (segment-metadata) one-way total
// latency in plain ms, or "no latency data" for the -1 sentinel the backend
// uses when no segment on the path carries latency metadata. The "advertised
// in beacons" framing is a one-time caption in TracePanel, not repeated here.
export function latencyLabel(p: PathOption): string {
  return p.latency_us_total >= 0 ? `${(p.latency_us_total / 1000).toFixed(1)} ms` : "no latency data";
}
