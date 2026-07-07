// tracePathD concatenates the octilinear route of each traced link into one
// path string, oriented src -> dst. routePoints(from, to) always emits
// points from -> to, and pathFrom's quadratic arc (core diagonals) is
// direction-symmetric, so per-link orientation comes free. Returns null when
// a link id is unknown to the layout or doesn't chain (defensive: the map
// then simply draws no overlay rather than a wrong one).
import { linkMeta, pathFrom, routePoints } from "./layout";
import type { TraceVM } from "./types";

export function tracePathD(pathLinks: string[], srcNum: number): string | null {
  let at = srcNum;
  const parts: string[] = [];
  for (const id of pathLinks) {
    const meta = linkMeta(id);
    if (!meta) return null;
    const [a, b] = id.split("-").map(Number);
    const to = at === a ? b : at === b ? a : NaN;
    if (Number.isNaN(to)) return null;
    parts.push(pathFrom(routePoints(at, to), meta.arc));
    at = to;
  }
  return parts.join(" ");
}

export function traceEndpoints(trace: TraceVM): [number, number] {
  return [Number(trace.src.split("-")[1]), Number(trace.dst.split("-")[1])];
}
