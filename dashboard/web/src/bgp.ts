// BGP session badge derived by fabricd (up|degraded|down|unknown); the field
// is absent entirely until BIRD is rolled out, so all consumers null-guard.
export type BgpState = "up" | "degraded" | "down" | "unknown";

export const BGP_WORD: Record<BgpState, string> = {
  up: "UP",
  degraded: "FLAPPING",
  down: "DOWN",
  unknown: "—",
};

// Map overlay text: only states demanding attention render on the map;
// steady-state and unknown stay panel-only to avoid clutter.
export function bgpChipText(s?: BgpState): string | null {
  if (s === "down") return "BGP DOWN";
  if (s === "degraded") return "BGP FLAP";
  return null;
}
