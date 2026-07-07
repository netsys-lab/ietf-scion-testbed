// TracePanel — the ID-INT path-inspector console. An operator picks a
// src/dst AS pair, lists sciond's candidate paths for that pair (AUTO =
// follow the advertised-fastest path, or pin a specific fingerprint), and
// a shared backend trace session probes the pinned path at 1 Hz. Every live
// frame carries the session's current state as frame.trace (TraceVM); this
// panel is a thin, mostly-derived view over that plus the local
// src/dst/path-list state needed to drive the picker.
//
// Two REST calls (Task 7's api.ts) plus the live frame do all the work:
// fetchIdintPaths lists candidates for the selected pair (refetched whenever
// src/dst changes), putTrace pins a path (or AUTO with no fingerprint) and
// starts/retargets the shared session, stopTrace ends it. The panel's close
// button only deselects — the trace keeps running server-side so a second
// operator (or the same one, later) can reopen the panel and see it live.
//
// Sparkline: mirrors LinkPanel's ring-per-frame pattern — the last RING probe
// RTTs, appended once per new vm.updated_at (deduped via a ref, same trick
// LinkPanel uses on frame.t) and skipped while the session isn't ok (a
// failing probe's RTT is meaningless).
//
// Rendering only ever shows the forward direction: vm.hops (as delivered by
// the backend) already excludes the trace's Rev leg — v1 doesn't have
// display-ready hop numbering for the reverse direction, so there is nothing
// to guard against here beyond simply not inventing a second table.
import { useEffect, useRef, useState } from "react";
import type { PathOption } from "../types";
import { useFabricStore } from "../store";
import { fetchIdintPaths, putTrace, stopTrace } from "../api";
import { hopRows, latencyLabel, pathLabel } from "../trace";
import Spark from "./Spark";

// Ring length: same convention as LinkPanel's sparkline rings.
const RING = 120;

function cap(a: number[]): number[] {
  return a.length > RING ? a.slice(a.length - RING) : a;
}

function ia(num: number): string {
  return `1-${num}`;
}

export default function TracePanel() {
  const topology = useFabricStore((s) => s.topology);
  const linksById = useFabricStore((s) => s.linksById);
  const vm = useFabricStore((s) => s.frame?.trace);
  const select = useFabricStore((s) => s.select);

  const [src, setSrc] = useState(150);
  const [dst, setDst] = useState(161);
  const [paths, setPaths] = useState<PathOption[] | null>(null);
  const [pathsError, setPathsError] = useState<string | null>(null);
  const [pickError, setPickError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [rttRing, setRttRing] = useState<number[]>([]);
  const lastUpdated = useRef<number | undefined>(undefined);

  // Refetch the candidate path list whenever the operator changes either
  // endpoint. A 404 (feature disabled in fabricd's config) is the only way
  // this throws, so any thrown error is treated as that case.
  useEffect(() => {
    let cancelled = false;
    setPaths(null);
    setPathsError(null);
    setPickError(null);
    fetchIdintPaths(src, dst)
      .then((resp) => {
        if (!cancelled) setPaths(resp.paths);
      })
      .catch(() => {
        if (!cancelled) setPathsError("ID-INT TRACING DISABLED");
      });
    return () => {
      cancelled = true;
    };
  }, [src, dst]);

  // Append the current frame's probe RTT to the ring, once per new reading.
  useEffect(() => {
    if (!vm || !vm.ok || vm.updated_at === lastUpdated.current) return;
    lastUpdated.current = vm.updated_at;
    setRttRing((r) => cap([...r, vm.probe_rtt_ms]));
  }, [vm]);

  const pickPath = async (fingerprint?: string) => {
    if (busy) return;
    setBusy(true);
    setPickError(null);
    try {
      await putTrace(src, dst, fingerprint);
    } catch (e) {
      setPickError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const runStop = async () => {
    if (busy) return;
    setBusy(true);
    setPickError(null);
    try {
      await stopTrace();
    } catch (e) {
      setPickError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const rows = vm ? hopRows(vm, linksById) : [];
  const hops = vm?.hops ?? [];
  const allVerified = vm !== undefined && hops.length > 0 && hops.every((h) => h.verified);
  const footer =
    rows.length > 0
      ? [...rows.map((r) => (r.rttMs != null ? `${r.rttMs.toFixed(1)} ms` : "– ms")), allVerified ? "MAC verified ✓" : "unverified"].join(
          " · ",
        )
      : "";

  return (
    <div className="panel-inner">
      <div className="panel-head">
        <button className="closebtn" aria-label="Close panel" onClick={() => select(undefined)}>
          ✕
        </button>
        <div className="eyebrow">ID-INT path trace</div>
        <h2>
          {ia(src)} → {ia(dst)}
        </h2>
      </div>

      <div className="shapingbox">
        <h3>Path</h3>
        <div className="traceselect">
          <select aria-label="Source AS" value={src} onChange={(e) => setSrc(Number(e.target.value))}>
            {topology?.ases.map((a) => (
              <option key={a.num} value={a.num} disabled={a.num === dst}>
                1-{a.num}
              </option>
            ))}
          </select>
          <span>→</span>
          <select aria-label="Destination AS" value={dst} onChange={(e) => setDst(Number(e.target.value))}>
            {topology?.ases.map((a) => (
              <option key={a.num} value={a.num} disabled={a.num === src}>
                1-{a.num}
              </option>
            ))}
          </select>
        </div>

        {pathsError ? (
          <span className="daemon-note err">{pathsError}</span>
        ) : (
          <div className="pathlist">
            <button
              type="button"
              className="pathrow"
              disabled={busy}
              aria-pressed={!!vm?.auto && vm.src === ia(src) && vm.dst === ia(dst)}
              onClick={() => pickPath(undefined)}
            >
              <span>AUTO — follow advertised-fastest path</span>
            </button>
            {paths?.map((p) => (
              <button
                key={p.fingerprint}
                type="button"
                className="pathrow"
                disabled={busy}
                aria-pressed={vm?.fingerprint === p.fingerprint && !vm?.auto}
                onClick={() => pickPath(p.fingerprint)}
              >
                <span>{pathLabel(p)}</span>
                <span>{latencyLabel(p)} · PIN</span>
              </button>
            ))}
          </div>
        )}
        {pickError && <span className="daemon-note err">{pickError}</span>}
      </div>

      {vm && vm.ok && (
        <div className="sparkblock">
          <div className="row">
            <span className="name">Probe RTT</span>
            <span className="reading">{vm.probe_rtt_ms.toFixed(1)} ms</span>
          </div>
          <Spark data={rttRing} color="#C9B37E" />
        </div>
      )}

      <div className="shapingbox">
        <h3>Hops</h3>
        {!vm && <span className="daemon-note">NO ACTIVE TRACE — pick a path above</span>}
        {vm?.error && <span className="daemon-note err">{vm.error}</span>}
        {vm && !vm.error && vm.ok && hops.length === 0 && <span className="daemon-note">PROBING…</span>}
        {vm && (vm.error || rows.length > 0) && (
          <>
            <div className={vm.error ? "trace-stale" : undefined}>
              <div className="tracehead">
                <span>LINK</span>
                <span>RTT BR</span>
                <span>EGR TX</span>
                <span>Q</span>
              </div>
              {rows.map((r) => (
                <div className="tracerow" key={r.link}>
                  <span>{r.link}</span>
                  <div className="tracebar">
                    <i style={{ width: `${r.barPct}%` }} className={r.shaped ? "hot" : undefined} />
                  </div>
                  <span>{r.egrPct != null ? `${r.egrPct.toFixed(1)}%` : "–"}</span>
                  <span>{r.queue ?? "–"}</span>
                </div>
              ))}
            </div>
            {rows.length > 0 && <span className="daemon-note">{footer}</span>}
          </>
        )}

        <div className="btnrow">
          <button className="ghost" disabled={busy} onClick={runStop}>
            STOP
          </button>
        </div>
      </div>
    </div>
  );
}
