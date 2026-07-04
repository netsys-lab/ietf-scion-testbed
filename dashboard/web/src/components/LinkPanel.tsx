// LinkPanel — the selected link's instrument + shaping console, ported from the
// mockup's selectLink() markup. It shows the band state line, three sparklines
// (RTT / throughput A+B / loss), and the scion-linkd shaping controls: four
// sliders with mono readouts, an A→B / B→A / both direction toggle, and
// Apply / Clear buttons that drive putShaping / resetShaping.
//
// Series handling: each sparkline keeps a ring of the last RING frame values in
// component state, appended once per live frame (deduped on frame.t so React
// StrictMode's double-invoke can't double-count). The RTT ring is backfilled on
// selection from the A-side history key (`<as>/br/rtt/<ifid>`); throughput and
// loss have no directly-historical series, so they seed empty and fill live.
//
// Shaping errors: when a linkd endpoint fails, the panel renders the per-
// endpoint results inline (daemon-note style) and leaves the sliders enabled.
// A total failure comes back as an HTTP 502 that the api client throws — its
// message is the {"results":[...]} body, which resultsFromErrorBody recovers,
// so mock mode (no real linkd) still shows structured, readable errors.
import { useEffect, useRef, useState } from "react";
import type { Band, Direction, Shaping, ShapingResult } from "../types";
import { useFabricStore } from "../store";
import { putShaping, resetShaping, fetchHistory } from "../api";
import {
  DIRECTIONS,
  NEUTRAL,
  appliedSummary,
  buildShaping,
  directionLabel,
  resultsFromErrorBody,
  shapingToValues,
  type SliderValues,
} from "../shaping";
import Spark from "./Spark";

// Ring length: the mockup keeps the last 120 samples per series.
const RING = 120;

// Band → display word / state-dot colour, ported from the mockup's BANDWORD /
// BANDCOL tables (extended with the backend's "stale" band).
const BAND_WORD: Record<Band, string> = {
  nominal: "NOMINAL",
  elevated: "ELEVATED",
  degraded: "DEGRADED",
  critical: "CRITICAL",
  down: "LINK DOWN",
  stale: "STALE",
};
const BAND_COL: Record<Band, string> = {
  nominal: "var(--steel)",
  elevated: "var(--warn)",
  degraded: "var(--bad)",
  critical: "var(--crit)",
  down: "#2A3140",
  stale: "var(--ink-mute)",
};

interface Series {
  rtt: number[];
  rate: number[];
  loss: number[];
}

function cap(a: number[]): number[] {
  return a.length > RING ? a.slice(a.length - RING) : a;
}

export default function LinkPanel({ id }: { id: string }) {
  const link = useFabricStore((s) => s.linksById[id]);
  const topoLink = useFabricStore((s) => s.topology?.links.find((l) => l.id === id));
  const frameT = useFabricStore((s) => s.frame?.t);
  const pushEvent = useFabricStore((s) => s.pushEvent);
  const select = useFabricStore((s) => s.select);

  const aAs = topoLink?.a.as;
  const bAs = topoLink?.b.as;
  const aIfid = topoLink?.a.ifid;

  const [series, setSeries] = useState<Series>({ rtt: [], rate: [], loss: [] });
  const [dir, setDir] = useState<Direction>("both");
  const [vals, setVals] = useState<SliderValues>(NEUTRAL);
  const [busy, setBusy] = useState(false);
  const [results, setResults] = useState<ShapingResult[] | null>(null);
  const [netError, setNetError] = useState<string | null>(null);
  const lastT = useRef<number | undefined>(undefined);

  // Reset per-link state and backfill the RTT ring from history on selection.
  useEffect(() => {
    lastT.current = undefined;
    setSeries({ rtt: [], rate: [], loss: [] });
    setResults(null);
    setNetError(null);
    setDir("both");
    setVals(shapingToValues(useFabricStore.getState().linksById[id]?.shaping));

    if (aAs === undefined || aIfid === undefined) return;
    let cancelled = false;
    fetchHistory(`${aAs}/br/rtt/${aIfid}`, 15)
      .then((samples) => {
        if (cancelled) return;
        const hist = samples.map((s) => s.v);
        // Prepend history before whatever live points already accumulated.
        setSeries((s) => ({ ...s, rtt: cap([...hist, ...s.rtt]) }));
      })
      .catch(() => {
        /* history is best-effort; the ring still fills live */
      });
    return () => {
      cancelled = true;
    };
  }, [id, aAs, aIfid]);

  // Append the current frame's readings to each ring, once per new frame.
  useEffect(() => {
    if (frameT === undefined || lastT.current === frameT) return;
    const l = useFabricStore.getState().linksById[id];
    if (!l) return;
    lastT.current = frameT;
    setSeries((s) => ({
      rtt: cap([...s.rtt, l.rtt_ms_a]),
      rate: cap([...s.rate, l.rate_ab_mbit + l.rate_ba_mbit]),
      loss: cap([...s.loss, l.loss_pct]),
    }));
  }, [frameT, id]);

  if (!link || !topoLink || aAs === undefined || bAs === undefined) return null;

  const band = link.band;
  const down = band === "down";
  const typeLabel =
    topoLink.type === "core" ? "core" : topoLink.type === "peer" ? "peer" : "parent / child";

  const rttReading = down ? "—" : `${link.rtt_ms_a.toFixed(1)} ms`;
  const rateReading = `${(link.rate_ab_mbit + link.rate_ba_mbit).toFixed(1)} Mbit/s`;
  const lossReading = `${link.loss_pct.toFixed(1)} %`;

  const finishApply = (res: ShapingResult[], params: Shaping, clear: boolean) => {
    const failed = res.filter((r) => !r.ok);
    if (failed.length === 0) {
      const summary = clear ? "CLEARED" : appliedSummary(params);
      const verb = clear ? "SHAPING CLEARED" : "SHAPING APPLIED";
      const text = clear ? `${verb} ${aAs}↔${bAs}` : `${verb} ${aAs}↔${bAs} · ${summary}`;
      pushEvent({ t: Date.now(), text, cls: "brass" });
      setResults(null);
      setNetError(null);
    } else {
      setResults(failed);
      setNetError(null);
    }
  };

  const runApply = async (clear: boolean) => {
    setBusy(true);
    setResults(null);
    setNetError(null);
    const params = clear ? {} : buildShaping(vals);
    try {
      const res = clear ? await resetShaping(id, dir) : await putShaping(id, dir, params);
      finishApply(res.results, params, clear);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      const parsed = resultsFromErrorBody(msg);
      if (parsed) finishApply(parsed, params, clear);
      else setNetError(msg);
    } finally {
      setBusy(false);
    }
  };

  const shaping = link.shaping;
  const chipText = shaping
    ? `applied · ${shaping.rate_mbit ?? 100} MBIT · +${shaping.delay_ms ?? 0} MS · ${shaping.loss_pct ?? 0} %`
    : "";

  return (
    <div className="panel-inner">
      <div className="panel-head">
        <button className="closebtn" aria-label="Close panel" onClick={() => select(undefined)}>
          ✕
        </button>
        <div className="eyebrow">Link · {typeLabel}</div>
        <h2>
          {aAs} ↔ {bAs}
          <span className="mono">{topoLink.subnet}</span>
        </h2>
        <div className="stateline">
          <span className="dot" style={{ background: BAND_COL[band] }} />
          <span>{BAND_WORD[band]}</span>
        </div>
      </div>

      <div className="sparkblock">
        <div className="row">
          <span className="name">Round-trip time</span>
          <span className="reading">{rttReading}</span>
        </div>
        <Spark data={series.rtt} color="#9AA3B2" />
      </div>
      <div className="sparkblock">
        <div className="row">
          <span className="name">Throughput A+B</span>
          <span className="reading">{rateReading}</span>
        </div>
        <Spark data={series.rate} color="#5A7A9E" />
      </div>
      <div className="sparkblock">
        <div className="row">
          <span className="name">Loss</span>
          <span className="reading">{lossReading}</span>
        </div>
        <Spark data={series.loss} color="#EC835A" />
      </div>

      <div className="shapingbox">
        <h3>Shaping — scion-linkd</h3>

        <div className="slider">
          <label htmlFor="s-delay">Delay</label>
          <input
            id="s-delay"
            type="range"
            min={0}
            max={500}
            value={vals.delay}
            onChange={(e) => setVals((v) => ({ ...v, delay: Number(e.target.value) }))}
          />
          <output htmlFor="s-delay">{vals.delay} ms</output>
        </div>
        <div className="slider">
          <label htmlFor="s-jit">Jitter</label>
          <input
            id="s-jit"
            type="range"
            min={0}
            max={100}
            value={vals.jitter}
            onChange={(e) => setVals((v) => ({ ...v, jitter: Number(e.target.value) }))}
          />
          <output htmlFor="s-jit">{vals.jitter} ms</output>
        </div>
        <div className="slider">
          <label htmlFor="s-loss">Loss</label>
          <input
            id="s-loss"
            type="range"
            min={0}
            max={20}
            step={0.5}
            value={vals.loss}
            onChange={(e) => setVals((v) => ({ ...v, loss: Number(e.target.value) }))}
          />
          <output htmlFor="s-loss">{vals.loss} %</output>
        </div>
        <div className="slider">
          <label htmlFor="s-rate">Rate</label>
          <input
            id="s-rate"
            type="range"
            min={1}
            max={100}
            value={vals.rate}
            onChange={(e) => setVals((v) => ({ ...v, rate: Number(e.target.value) }))}
          />
          <output htmlFor="s-rate">{vals.rate} Mbit</output>
        </div>

        <div className="dirrow" role="group" aria-label="Direction">
          {DIRECTIONS.map((d) => (
            <button key={d} aria-pressed={dir === d} onClick={() => setDir(d)}>
              {directionLabel(d, aAs, bAs)}
            </button>
          ))}
        </div>

        <div className="btnrow">
          <button className="primary" disabled={busy} onClick={() => runApply(false)}>
            Apply shaping
          </button>
          <button className="ghost" disabled={busy} onClick={() => runApply(true)}>
            Clear
          </button>
        </div>

        {results
          ? results.map((r) => (
              <span className="daemon-note err" key={r.as}>
                linkd @ 10.20.3.{r.as} — {r.error || "unreachable"}
              </span>
            ))
          : netError
            ? <span className="daemon-note err">{netError}</span>
            : shaping
              ? <span className="applied-chip">{chipText}</span>
              : (
                <span className="daemon-note">
                  linkd @ 10.20.3.{aAs}:30480 · 10.20.3.{bAs}:30480
                </span>
              )}
      </div>
    </div>
  );
}
