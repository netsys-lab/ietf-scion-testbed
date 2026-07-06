// Typed port of the approved fabric mockup's topology + octilinear routing
// (docs/superpowers/specs/mockups/fabric-mockup.html). NODES coordinates,
// core flags, the LINKS layout list (sub / kind / arc / via), R=16 bend
// radius, and the routePoints / pathFrom / norm geometry are byte-identical
// to that reference — it is normative. Link IDs use the topo.Graph form
// "<lowerAS>-<higherAS>", so layoutFor("150-154") lines the map up with the
// backend graph and the live frame. This module stays framework-agnostic so
// layout.test.ts can exercise it directly and Task 10's particle layer can
// reuse linkPath (it samples the rendered <path id="link-path-<id>">).

export type Point = [number, number];

export interface Node {
  x: number;
  y: number;
  core?: boolean;
}

// Fixed hand-tuned station coordinates (three horizontal bands: core 150-153,
// mid 154-157, leaf 158-161). No force simulation — see the design doc.
export const NODES: Record<number, Node> = {
  150: { x: 480, y: 150, core: true },
  151: { x: 760, y: 150, core: true },
  152: { x: 1040, y: 150, core: true },
  153: { x: 1320, y: 150, core: true },
  154: { x: 350, y: 440 },
  155: { x: 760, y: 440 },
  156: { x: 1120, y: 440 },
  157: { x: 1400, y: 440 },
  158: { x: 480, y: 710 },
  159: { x: 700, y: 710 },
  160: { x: 900, y: 710 },
  161: { x: 1180, y: 710 },
};

export interface LinkLayout {
  a: number;
  b: number;
  sub: string; // native testbed link id (hex bridge suffix); vestigial — the
  // map now labels links by AS-pair, so nothing reads this today. Kept because
  // the LINKS table below is byte-identical to the mockup.
  kind?: "core" | "peer"; // absent => parent/child
  arc?: number; // core-diagonal quadratic arc height (px, above the band)
  via?: Point[]; // explicit hand-routed polyline (overrides routePoints)
}

// Link layout list, byte-identical to the mockup. The two core diagonals get
// arcs so they don't collide with the straight core band; 155-161 is routed
// explicitly to skirt the leaf band.
export const LINKS: LinkLayout[] = [
  { a: 150, b: 151, sub: "1", kind: "core" },
  { a: 150, b: 152, sub: "2", kind: "core", arc: -130 },
  { a: 150, b: 153, sub: "3", kind: "core", arc: -180 },
  { a: 151, b: 152, sub: "4", kind: "core" },
  { a: 151, b: 153, sub: "5", kind: "core", arc: -130 },
  { a: 152, b: 153, sub: "6", kind: "core" },
  { a: 150, b: 154, sub: "7" },
  { a: 151, b: 154, sub: "8" },
  { a: 151, b: 155, sub: "9" },
  { a: 152, b: 155, sub: "a" },
  { a: 152, b: 156, sub: "b" },
  { a: 152, b: 157, sub: "c" },
  { a: 153, b: 156, sub: "d" },
  { a: 153, b: 157, sub: "e" },
  { a: 154, b: 158, sub: "f" },
  { a: 154, b: 155, sub: "10" },
  { a: 155, b: 158, sub: "11", kind: "peer" },
  { a: 155, b: 156, sub: "12" },
  { a: 158, b: 159, sub: "13" },
  { a: 155, b: 159, sub: "14" },
  { a: 155, b: 160, sub: "15" },
  { a: 155, b: 161, sub: "16", via: [[760, 440], [1030, 710], [1180, 710]] },
  { a: 156, b: 161, sub: "17" },
  { a: 157, b: 161, sub: "18" },
];

// ─────────────────────────── Octilinear routing ──
export const R = 16; // bend radius

export function routePoints(a: number, b: number): Point[] {
  const A = NODES[a];
  const B = NODES[b];
  if (A.y === B.y || A.x === B.x) return [[A.x, A.y], [B.x, B.y]];
  const dx = B.x - A.x;
  const dy = B.y - A.y;
  const sx = Math.sign(dx);
  const sy = Math.sign(dy);
  const adx = Math.abs(dx);
  const ady = Math.abs(dy);
  if (ady >= adx) {
    // vertical – diagonal – vertical
    const v = (ady - adx) / 2;
    return [[A.x, A.y], [A.x, A.y + sy * v], [B.x, A.y + sy * (v + adx)], [B.x, B.y]];
  }
  // horizontal – diagonal – horizontal
  const h = (adx - ady) / 2;
  return [[A.x, A.y], [A.x + sx * h, A.y], [A.x + sx * (h + ady), B.y], [B.x, B.y]];
}

export function pathFrom(pts: Point[], arc?: number): string {
  if (arc) {
    // core diagonal: quadratic arc above the band
    const p0 = pts[0];
    const p1 = pts[pts.length - 1];
    const mx = (p0[0] + p1[0]) / 2;
    const my = (p0[1] + p1[1]) / 2 + arc;
    return `M ${p0[0]} ${p0[1]} Q ${mx} ${my} ${p1[0]} ${p1[1]}`;
  }
  if (pts.length === 2) return `M ${pts[0][0]} ${pts[0][1]} L ${pts[1][0]} ${pts[1][1]}`;
  let d = `M ${pts[0][0]} ${pts[0][1]}`;
  for (let i = 1; i < pts.length - 1; i++) {
    const [px, py] = pts[i - 1];
    const [cx, cy] = pts[i];
    const [nx, ny] = pts[i + 1];
    const v1 = norm([cx - px, cy - py]);
    const v2 = norm([nx - cx, ny - cy]);
    d += ` L ${cx - v1[0] * R} ${cy - v1[1] * R} Q ${cx} ${cy} ${cx + v2[0] * R} ${cy + v2[1] * R}`;
  }
  d += ` L ${pts[pts.length - 1][0]} ${pts[pts.length - 1][1]}`;
  return d;
}

export function norm(v: Point): Point {
  const m = Math.hypot(v[0], v[1]) || 1;
  return [v[0] / m, v[1] / m];
}

// ─────────────────────────── Keyed accessors ──
export interface Station {
  num: number;
  x: number;
  y: number;
  core: boolean;
}

/** All 12 stations in NODES declaration order, ready to render. */
export const stationList: Station[] = Object.entries(NODES).map(([num, n]) => ({
  num: Number(num),
  x: n.x,
  y: n.y,
  core: Boolean(n.core),
}));

export interface LinkMeta extends LinkLayout {
  id: string;
}

const byId = new Map<string, LinkMeta>(
  LINKS.map((L) => [`${L.a}-${L.b}`, { ...L, id: `${L.a}-${L.b}` }]),
);

/** Layout metadata for a link id ("150-154"), or undefined if unmapped. */
export function linkMeta(id: string): LinkMeta | undefined {
  return byId.get(id);
}

/**
 * SVG path `d` for a link id: the hand-routed `via` polyline when present,
 * otherwise the octilinear routePoints, rendered through pathFrom (which
 * applies the core-diagonal arc). Returns "" for an unmapped id.
 */
export function linkPath(id: string): string {
  const L = byId.get(id);
  if (!L) return "";
  return pathFrom(L.via ?? routePoints(L.a, L.b), L.arc);
}

export const VIEWBOX = "0 0 1520 840";
