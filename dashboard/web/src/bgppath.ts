// bgppath.ts — pure display logic for the BGP path overlay (frame.bgp_path):
// AS-path text for TracePanel. Kept out of the React components so
// bgppath.test.ts can exercise it directly (mirrors trace.ts's role).
import type { BgpPathVM } from "./types";

// asPathText renders "158 › 155 › 150"; a truncated walk (complete=false —
// linkd unreachable or no BGP best route at the cut point) gets a trailing
// "?" so the panel never presents a partial path as the full one.
export function asPathText(bp: BgpPathVM): string {
  const hops = bp.as_path.join(" › ");
  return bp.complete ? hops : `${hops} › ?`;
}
