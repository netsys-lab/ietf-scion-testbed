// FabricMap — the signature element: the testbed drawn as a Viennese transit
// map. Renders one SVG whose geometry is the mockup's octilinear routing
// (layout.ts), colored live from the store's per-link band. Link state comes
// from frame.links via store.linksById (default "nominal" before the first
// frame). Clicking / Enter / Space on a link or station writes the selection
// into the store; Escape clears it (the selection panel itself is Task 11).
// Overlay glyphs (break ✕, shaping chips, hover/selected label, peer marker)
// are positioned at each trunk path's length-midpoint, measured from the
// rendered <path> the same way the mockup does (getPointAtLength).
//
// Before the first topology snapshot arrives (cold boot, or the backend is
// down at load), no links or stations are drawn — a dim "CONNECTING TO
// FABRIC…" label stands in for the map instead of the mockup's stationList
// floating with no links (there's nothing live to show yet).
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { Band, LinkVM } from "../types";
import { useFabricStore } from "../store";
import { VIEWBOX, NODES, linkMeta, linkPath, stationList } from "../layout";
import { traceEndpoints, tracePathD } from "../tracepath";
import "./fabric.css";

interface Mid {
  x: number;
  y: number;
}

// Compact shaping-chip text, matching the mockup's map chip: "50M · +50MS · 1%"
// (rate only when throttled below 100 Mbit, delay/loss only when non-zero).
function chipLabel(vm: LinkVM): string {
  const s = vm.shaping;
  if (!s) return "";
  const parts: string[] = [];
  if (s.rate_mbit != null && s.rate_mbit < 100) parts.push(`${s.rate_mbit}M`);
  if (s.delay_ms) parts.push(`+${s.delay_ms}MS`);
  if (s.loss_pct) parts.push(`${s.loss_pct}%`);
  return parts.join(" · ");
}

export default function FabricMap() {
  const topology = useFabricStore((s) => s.topology);
  const linksById = useFabricStore((s) => s.linksById);
  const selected = useFabricStore((s) => s.selected);
  const select = useFabricStore((s) => s.select);
  const setBooted = useFabricStore((s) => s.setBooted);
  const screen = useFabricStore((s) => s.screen);
  const trace = useFabricStore((s) => s.frame?.trace);

  const svgRef = useRef<SVGSVGElement>(null);
  const [mids, setMids] = useState<Record<string, Mid>>({});
  const [hovered, setHovered] = useState<string | null>(null);

  // Only draw topology links that have a hand-tuned layout entry.
  const links = useMemo(
    () => (topology?.links ?? []).filter((l) => linkMeta(l.id) !== undefined),
    [topology],
  );

  const bandOf = (id: string): Band => linksById[id]?.band ?? "nominal";

  // Recompute only when the path itself or the src endpoint changes, not on
  // every per-frame TraceVM object identity change (rtt/hops tick each poll).
  const traceD = useMemo(
    () => (trace ? tracePathD(trace.path_links, Number(trace.src.split("-")[1])) : null),
    [trace?.path_links.join(","), trace?.src],
  );

  // Measure the length-midpoint of every trunk path after commit (mirrors the
  // mockup's getPointAtLength(len/2)); overlays hang off these. Re-runs when
  // the link set changes — geometry itself is static.
  useLayoutEffect(() => {
    const svg = svgRef.current;
    if (!svg) return;
    const next: Record<string, Mid> = {};
    for (const l of links) {
      const path = svg.querySelector<SVGPathElement>(`#link-path-${l.id}`);
      if (!path || typeof path.getTotalLength !== "function") continue;
      const len = path.getTotalLength();
      const p = path.getPointAtLength(len / 2);
      next[l.id] = { x: p.x, y: p.y };
    }
    setMids(next);
  }, [links]);

  // Boot draw-in, ported from the mockup's boot(): each link's trunk+gap draws
  // in via stroke-dashoffset over 1.1s, staggered 0.02s/link, while stations
  // fade in staggered; inline styles are cleared and the store's booted flag
  // flips after 1.7s (which is what releases the particle layer). Under reduced
  // motion the whole animation is skipped and booted flips immediately. Runs
  // once — guarded on the store flag so a reconnect's re-snapshot won't replay
  // it — and is StrictMode-safe (cleanup cancels the pending rAF/timer).
  const bootTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const bootRaf = useRef<number | null>(null);
  useLayoutEffect(() => {
    const svg = svgRef.current;
    if (!svg || links.length === 0) return;
    if (useFabricStore.getState().booted) return;

    if (matchMedia("(prefers-reduced-motion: reduce)").matches) {
      setBooted(true);
      return;
    }

    const pairs = Array.from(svg.querySelectorAll<SVGGElement>("g.link"))
      .map((g) => ({
        trunk: g.querySelector<SVGPathElement>(".trunk"),
        gap: g.querySelector<SVGPathElement>(".gap"),
      }))
      .filter((p): p is { trunk: SVGPathElement; gap: SVGPathElement } => !!p.trunk && !!p.gap);
    const stations = Array.from(svg.querySelectorAll<SVGGElement>("g.station"));

    pairs.forEach(({ trunk, gap }, i) => {
      const len = trunk.getTotalLength();
      for (const p of [trunk, gap]) {
        p.style.strokeDasharray = String(len);
        p.style.strokeDashoffset = String(len);
        p.style.transition = `stroke-dashoffset 1.1s ease ${i * 0.02}s`;
      }
    });
    stations.forEach((g, i) => {
      g.style.opacity = "0";
      g.style.transition = `opacity .4s ease ${0.5 + i * 0.05}s`;
    });

    bootRaf.current = requestAnimationFrame(() =>
      requestAnimationFrame(() => {
        pairs.forEach(({ trunk, gap }) => {
          trunk.style.strokeDashoffset = "0";
          gap.style.strokeDashoffset = "0";
        });
        stations.forEach((g) => {
          g.style.opacity = "1";
        });
      }),
    );
    bootTimer.current = setTimeout(() => {
      pairs.forEach(({ trunk, gap }) => {
        for (const p of [trunk, gap]) {
          p.style.strokeDasharray = "";
          p.style.strokeDashoffset = "";
          p.style.transition = "";
        }
      });
      setBooted(true);
    }, 1700);

    return () => {
      if (bootTimer.current !== null) clearTimeout(bootTimer.current);
      if (bootRaf.current !== null) cancelAnimationFrame(bootRaf.current);
    };
  }, [links, setBooted]);

  // Escape clears the current selection (close button lives in Task 11's panel).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") select(undefined);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [select]);

  // Flash a link once when its band changes ("loud when hurt"). Timers are
  // keyed by id and self-clearing, so a frame that carries no change never
  // cancels an in-flight flash.
  const prevBands = useRef<Record<string, Band>>({});
  const flashTimers = useRef<Record<string, ReturnType<typeof setTimeout>>>({});
  const [flashing, setFlashing] = useState<Set<string>>(() => new Set());
  useEffect(() => {
    const changed: string[] = [];
    for (const id in linksById) {
      const band = linksById[id].band;
      const prev = prevBands.current[id];
      if (prev !== undefined && prev !== band) changed.push(id);
      prevBands.current[id] = band;
    }
    if (changed.length === 0) return;
    setFlashing((prev) => {
      const n = new Set(prev);
      for (const id of changed) n.add(id);
      return n;
    });
    for (const id of changed) {
      if (flashTimers.current[id]) clearTimeout(flashTimers.current[id]);
      flashTimers.current[id] = setTimeout(() => {
        delete flashTimers.current[id];
        setFlashing((prev) => {
          if (!prev.has(id)) return prev;
          const n = new Set(prev);
          n.delete(id);
          return n;
        });
      }, 260);
    }
  }, [linksById]);
  useEffect(() => {
    const timers = flashTimers.current;
    return () => {
      for (const id in timers) clearTimeout(timers[id]);
    };
  }, []);

  const selectLink = (id: string) => select({ kind: "link", id });
  const selectAS = (num: number) => select({ kind: "as", id: String(num) });

  // One label at a time: the hovered link, else the selected link.
  const labelId =
    hovered && linkMeta(hovered) ? hovered : selected?.kind === "link" ? selected.id : null;

  return (
    <>
      <svg
        ref={svgRef}
        id="map"
        viewBox={VIEWBOX}
        preserveAspectRatio="xMidYMid meet"
        aria-label="Testbed topology map"
      >
        {/* Links: trunk (colored), gap (page-colored seam), hit (wide invisible target). */}
        <g>
          {links.map((l) => {
            const meta = linkMeta(l.id)!;
            const d = linkPath(l.id);
            const selectedLink = selected?.kind === "link" && selected.id === l.id;
            const cls =
              "link" + (selectedLink ? " selected" : "") + (flashing.has(l.id) ? " flash" : "");
            return (
              <g
                key={l.id}
                className={cls}
                tabIndex={0}
                role="button"
                aria-label={`Link AS${meta.a} to AS${meta.b}`}
                data-band={bandOf(l.id)}
                onClick={() => selectLink(l.id)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    selectLink(l.id);
                  }
                }}
                onMouseEnter={() => setHovered(l.id)}
                onMouseLeave={() => setHovered((h) => (h === l.id ? null : h))}
                onFocus={() => setHovered(l.id)}
                onBlur={() => setHovered((h) => (h === l.id ? null : h))}
              >
                <path id={`link-path-${l.id}`} className="trunk" d={d} />
                <path className="gap" d={d} />
                <path className="hit" d={d} />
              </g>
            );
          })}
        </g>

        {/* Peer marker (155↔158). */}
        <g>
          {links.map((l) => {
            const meta = linkMeta(l.id)!;
            const m = mids[l.id];
            if (meta.kind !== "peer" || !m) return null;
            return (
              <text key={l.id} className="peer-glyph" x={m.x + 14} y={m.y - 16}>
                PEER
              </text>
            );
          })}
        </g>

        {/* Core-link marker: "CORE" text at the midpoint, the core-link
            counterpart to the PEER marker. */}
        <g>
          {links.map((l) => {
            const meta = linkMeta(l.id)!;
            const m = mids[l.id];
            if (meta.kind !== "core" || !m) return null;
            return (
              <text key={l.id} className="core-glyph" x={m.x} y={m.y - 12}>
                CORE
              </text>
            );
          })}
        </g>

        {/* Parent/child annotation on the graph: "parent" near the parent AS
            and "child" near the child AS along each hierarchical link, so the
            direction is readable directly on the map. Parent = the endpoint
            whose link_to is "child" (it sees its neighbor as its child). */}
        <g>
          {links.map((l) => {
            const meta = linkMeta(l.id)!;
            if (meta.kind !== undefined) return null;
            const parentAS = l.a.link_to === "child" ? l.a.as : l.b.as;
            const childAS = parentAS === l.a.as ? l.b.as : l.a.as;
            const pp = NODES[parentAS];
            const cc = NODES[childAS];
            if (!pp || !cc) return null;
            const dx = cc.x - pp.x;
            const dy = cc.y - pp.y;
            const len = Math.hypot(dx, dy) || 1;
            const ux = dx / len;
            const uy = dy / len;
            const off = 40;
            return (
              <g key={l.id} className="pc-label">
                <text x={pp.x + ux * off} y={pp.y + uy * off}>
                  <title>parent</title>P
                </text>
                <text x={cc.x - ux * off} y={cc.y - uy * off}>
                  <title>child</title>C
                </text>
              </g>
            );
          })}
        </g>

        {/* Stations: core ring, disc, halo'd label + eyebrow. Nothing renders
            here until the first topology snapshot arrives — stationList is
            static layout data, not live state, so drawing it before then
            would show all 12 stations floating with no links (cold-boot /
            backend-down false-positive); the placeholder text below covers
            that gap instead. */}
        <g>
          {topology === undefined ? (
            <text x={760} y={420} textAnchor="middle" className="connecting-label">
              CONNECTING TO FABRIC…
            </text>
          ) : (
            stationList.map((st) => {
              const selectedAS = selected?.kind === "as" && selected.id === String(st.num);
              const isTraceEnd = trace !== undefined && traceEndpoints(trace).includes(st.num);
              const cls =
                "station" +
                (st.core ? " core" : "") +
                (selectedAS ? " selected" : "") +
                (isTraceEnd ? " trace-end" : "");
              // Screen mode bumps the disc radius +2 for booth-distance
              // legibility; `r` is a plain SVG attribute (not a CSS
              // custom-prop hook like the map's font-sizes/stroke-widths in
              // fabric.css), so the bump is computed here instead.
              const rBase = st.core ? 15 : 12;
              const r = screen ? rBase + 2 : rBase;
              return (
                <g
                  key={st.num}
                  className={cls}
                  tabIndex={0}
                  role="button"
                  aria-label={`AS ${st.num}`}
                  onClick={() => selectAS(st.num)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      selectAS(st.num);
                    }
                  }}
                >
                  {st.core && <circle className="core-ring" cx={st.x} cy={st.y} r={21} />}
                  <circle className="station-disc" cx={st.x} cy={st.y} r={r} />
                  <text x={st.x} y={st.y - (st.core ? 30 : 24)} textAnchor="middle">
                    {st.num}
                  </text>
                  <text
                    className="as-eyebrow"
                    x={st.x}
                    y={st.y - (st.core ? 44 : 37)}
                    textAnchor="middle"
                  >
                    {st.core ? "CORE" : "AS"}
                  </text>
                </g>
              );
            })
          )}
        </g>

        {/* Overlays: break glyph for down links, else a shaping chip. */}
        <g>
          {links.map((l) => {
            const m = mids[l.id];
            if (!m) return null;
            const vm = linksById[l.id];
            if (bandOf(l.id) === "down") {
              return (
                <g key={l.id} className="break-glyph">
                  <circle cx={m.x} cy={m.y} r={10} />
                  <line x1={m.x - 4.5} y1={m.y - 4.5} x2={m.x + 4.5} y2={m.y + 4.5} />
                  <line x1={m.x - 4.5} y1={m.y + 4.5} x2={m.x + 4.5} y2={m.y - 4.5} />
                </g>
              );
            }
            if (!vm?.shaping) return null;
            const label = chipLabel(vm);
            if (!label) return null;
            const w = label.length * 6.4 + 14;
            return (
              <g key={l.id} className="chip">
                <rect x={m.x - w / 2} y={m.y + 10} width={w} height={17} rx={4} />
                <text x={m.x} y={m.y + 22}>
                  {label}
                </text>
              </g>
            );
          })}
        </g>

        {/* Hover / selected link label. */}
        <g>
          {labelId &&
            mids[labelId] &&
            (() => {
              const m = mids[labelId];
              const meta = linkMeta(labelId)!;
              const vm = linksById[labelId];
              const down = bandOf(labelId) === "down";
              const rtt = vm ? Math.max(vm.rtt_ms_a, vm.rtt_ms_b) : 0;
              const val = down ? "DOWN" : `${rtt.toFixed(1)} ms`;
              const txt = `${meta.a}–${meta.b}  ${val}`;
              const w = txt.length * 7.2 + 20;
              return (
                <g className="linklabel">
                  <rect x={m.x - w / 2} y={m.y - 34} width={w} height={20} rx={4} />
                  <text x={m.x - w / 2 + 10} y={m.y - 20}>
                    {`${meta.a}–${meta.b}  `}
                    <tspan className="val">{val}</tspan>
                  </text>
                </g>
              );
            })()}
        </g>

        {/* ID-INT trace overlay: brass path along the probed links, with a
            travelling comet while the probe is healthy. Rendered last so it
            sits above every other overlay. */}
        {trace && traceD && (
          <g className="trace-overlay" aria-hidden="true">
            <path className="trace-base" d={traceD} />
            {trace.ok && (
              <>
                <circle className="trace-comet" r={4}>
                  <animateMotion dur="2.2s" repeatCount="indefinite" path={traceD} />
                </circle>
                <circle className="trace-comet tail" r={2.2}>
                  <animateMotion dur="2.2s" begin="0.12s" repeatCount="indefinite" path={traceD} />
                </circle>
              </>
            )}
          </g>
        )}
      </svg>

      <div id="legend" aria-hidden="true">
        <span>
          <span className="sw" style={{ background: "var(--steel)" }} />
          Nominal
        </span>
        <span>
          <span className="sw" style={{ background: "var(--warn)" }} />
          Elevated
        </span>
        <span>
          <span className="sw" style={{ background: "var(--bad)" }} />
          Degraded
        </span>
        <span>
          <span className="sw" style={{ background: "var(--crit)" }} />
          Critical
        </span>
        <span>
          <span className="sw" style={{ background: "#2A3140" }} />
          Down
        </span>
      </div>
    </>
  );
}
