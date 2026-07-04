// ParticleLayer — the animation heart: a <canvas> overlay that draws traffic
// as drifting warm-white sprites, one lane per direction, density (never
// speed) encoding throughput. This is a verbatim port of the mockup's particle
// engine (docs/superpowers/specs/mockups/fabric-mockup.html): the 28px radial
// sprite, SPEED=95, spawnRate, per-particle alpha, loss "pops" with burst
// rings, ±2.6 lane offset along the path normal, additive ('lighter')
// compositing, and the ResizeObserver/devicePixelRatio transform that maps the
// 1520×840 viewBox onto the canvas.
//
// Two deliberate ports of the mockup's globals into React idiom:
//   • Path geometry is sampled every 6px from the rendered SVG paths that
//     FabricMap draws (<path id="link-path-<id>">), re-sampled whenever the
//     topology changes.
//   • Live rate/loss/up state and the booted flag are read from the store via
//     getState() inside the rAF loop, so fresh frames drive the animation with
//     zero React re-renders. Down links spawn nothing; particles only start
//     once booted flips (after the boot draw-in, or immediately under reduced
//     motion — where this layer runs no loop at all and the map stays static).
import { useEffect, useRef } from "react";
import { useFabricStore } from "../store";

const SPEED = 95; // viewBox px/s — constant: density encodes rate, never speed
const STEP = 6; // path sampling stride, px

function spawnRate(mbit: number): number {
  return mbit <= 0 ? 0 : Math.min(6, 0.12 + Math.log10(1 + mbit) * 2.6);
}

interface Sampled {
  id: string;
  samples: [number, number][];
  len: number;
}

interface Particle {
  id: string;
  dir: 0 | 1;
  pos: number;
  popAt: number;
  a: number;
}

export default function ParticleLayer() {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const topology = useFabricStore((s) => s.topology);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    // prefers-reduced-motion: no rAF loop at all — the map stays static.
    if (matchMedia("(prefers-reduced-motion: reduce)").matches) return;

    const ctx = canvas.getContext("2d");
    const stage = canvas.parentElement; // #stage
    if (!ctx || !stage) return;

    // Pre-rendered radial sprite (soft warm-white dot).
    const sprite = document.createElement("canvas");
    sprite.width = sprite.height = 28;
    {
      const c = sprite.getContext("2d")!;
      const g = c.createRadialGradient(14, 14, 1, 14, 14, 14);
      g.addColorStop(0, "rgba(255,243,220,1)");
      g.addColorStop(0.35, "rgba(255,243,220,0.55)");
      g.addColorStop(1, "rgba(255,243,220,0)");
      c.fillStyle = g;
      c.fillRect(0, 0, 28, 28);
    }

    // ── viewBox → canvas transform (dpr-aware, centered) ──
    let scale = 1;
    let ox = 0;
    let oy = 0;
    let dpr = 1;
    function resize() {
      dpr = window.devicePixelRatio || 1;
      const w = stage!.clientWidth;
      const h = stage!.clientHeight;
      canvas!.width = w * dpr;
      canvas!.height = h * dpr;
      scale = Math.min(w / 1520, h / 840);
      ox = (w - 1520 * scale) / 2;
      oy = (h - 840 * scale) / 2;
    }
    const ro = new ResizeObserver(resize);
    ro.observe(stage);
    resize();

    // ── sample every rendered trunk path every 6px ──
    const links: Sampled[] = [];
    const byId = new Map<string, Sampled>();
    const acc = new Map<string, [number, number]>();
    const ids = useFabricStore.getState().topology?.links.map((l) => l.id) ?? [];
    for (const id of ids) {
      const path = document.getElementById(`link-path-${id}`) as SVGPathElement | null;
      if (!path || typeof path.getTotalLength !== "function") continue;
      const len = path.getTotalLength();
      const pts: [number, number][] = [];
      for (let s = 0; s <= len; s += STEP) {
        const p = path.getPointAtLength(s);
        pts.push([p.x, p.y]);
      }
      const sampled: Sampled = { id, samples: pts, len };
      links.push(sampled);
      byId.set(id, sampled);
      acc.set(id, [0, 0]);
    }

    const particles: Particle[] = [];
    const bursts: { x: number; y: number; t: number }[] = [];

    function samplePt(p: Particle): [number, number] {
      const S = byId.get(p.id)!.samples;
      let idx = Math.floor(p.pos / STEP);
      if (idx >= S.length) idx = S.length - 1;
      const i = p.dir === 0 ? idx : S.length - 1 - idx;
      const cur = S[i];
      const nxt = S[Math.min(S.length - 1, i + 1)] || cur;
      const nx = nxt[1] - cur[1];
      const ny = -(nxt[0] - cur[0]);
      const m = Math.hypot(nx, ny) || 1;
      const off = p.dir === 0 ? 2.6 : -2.6;
      return [cur[0] + (nx / m) * off, cur[1] + (ny / m) * off];
    }

    let raf = 0;
    let lastT = performance.now();
    function frame(now: number) {
      const dt = Math.min(0.05, (now - lastT) / 1000);
      lastT = now;

      const state = useFabricStore.getState();
      if (state.booted) {
        const linksById = state.linksById;
        for (const l of links) {
          const vm = linksById[l.id];
          if (!vm || !vm.up) continue; // down links spawn nothing
          const rates = [vm.rate_ab_mbit, vm.rate_ba_mbit];
          const loss = vm.loss_pct;
          const a = acc.get(l.id)!;
          for (const dir of [0, 1] as const) {
            a[dir] += spawnRate(rates[dir]) * dt;
            while (a[dir] >= 1) {
              a[dir] -= 1;
              const popAt =
                loss > 0 && Math.random() * 100 < loss * 6
                  ? (0.3 + Math.random() * 0.4) * l.len
                  : -1;
              particles.push({
                id: l.id,
                dir,
                pos: 0,
                popAt,
                a: Math.min(1, 0.4 + rates[dir] / 25),
              });
            }
          }
        }
        for (let i = particles.length - 1; i >= 0; i--) {
          const p = particles[i];
          p.pos += SPEED * dt;
          if (p.popAt > 0 && p.pos >= p.popAt) {
            const pt = samplePt(p);
            bursts.push({ x: pt[0], y: pt[1], t: 0 });
            particles.splice(i, 1);
            continue;
          }
          if (p.pos >= byId.get(p.id)!.len) particles.splice(i, 1);
        }
      }

      ctx!.setTransform(scale * dpr, 0, 0, scale * dpr, ox * dpr, oy * dpr);
      ctx!.clearRect(-ox / scale, -oy / scale, canvas!.width / (scale * dpr), canvas!.height / (scale * dpr));
      ctx!.globalCompositeOperation = "lighter";
      for (const p of particles) {
        const pt = samplePt(p);
        ctx!.globalAlpha = p.a;
        ctx!.drawImage(sprite, pt[0] - 5.5, pt[1] - 5.5, 11, 11);
      }
      ctx!.globalAlpha = 1;
      ctx!.globalCompositeOperation = "source-over";
      for (let i = bursts.length - 1; i >= 0; i--) {
        const b = bursts[i];
        b.t += dt;
        if (b.t > 0.32) {
          bursts.splice(i, 1);
          continue;
        }
        const k = b.t / 0.32;
        ctx!.beginPath();
        ctx!.arc(b.x, b.y, 3 + k * 9, 0, 7);
        ctx!.strokeStyle = `rgba(236,131,90,${(1 - k) * 0.9})`;
        ctx!.lineWidth = 1.6;
        ctx!.stroke();
      }
      raf = requestAnimationFrame(frame);
    }
    raf = requestAnimationFrame(frame);

    return () => {
      cancelAnimationFrame(raf);
      ro.disconnect();
    };
  }, [topology]);

  return <canvas ref={canvasRef} id="fx" aria-hidden="true" />;
}
