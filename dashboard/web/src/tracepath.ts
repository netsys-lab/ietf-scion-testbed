// tracePathD concatenates the drawn route of each traced link into one path
// string, oriented src -> dst — the same geometry linkPath renders (the
// hand-routed `via` polyline when present, else octilinear routePoints), so
// the comet never skirts off a drawn trunk. routePoints(from, to) always
// emits points from -> to; `via` is stored a -> b and gets reversed (on a
// copy, never in place) for b -> a traversal; pathFrom's quadratic arc (core
// diagonals) is direction-symmetric. Returns null when a link id is unknown
// to the layout or doesn't chain (defensive: the map then simply draws no
// overlay rather than a wrong one).
import { linkMeta, pathFrom, routePoints } from "./layout";
import type { TraceVM } from "./types";

export function tracePathD(pathLinks: string[], srcNum: number): string | null {
  let at = srcNum;
  const parts: string[] = [];
  for (const id of pathLinks) {
    const meta = linkMeta(id);
    if (!meta) return null;
    const to = at === meta.a ? meta.b : at === meta.b ? meta.a : NaN;
    if (Number.isNaN(to)) return null;
    const pts = meta.via
      ? at === meta.a
        ? meta.via
        : [...meta.via].reverse()
      : routePoints(at, to);
    parts.push(pathFrom(pts, meta.arc));
    at = to;
  }
  return parts.join(" ");
}

export function traceEndpoints(trace: TraceVM): [number, number] {
  return [Number(trace.src.split("-")[1]), Number(trace.dst.split("-")[1])];
}
