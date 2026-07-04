// KpiStrip — the masthead's right-aligned instrument readout, ported from the
// mockup's #kpis block. Values come straight off frame.kpi; before the first
// frame arrives each tile shows the mockup's en-dash placeholder. The SHAPED
// tile is brass (chrome accent, never a data encoding) per the design doc.
import { useFabricStore } from "../store";

const DASH = "–";

export default function KpiStrip() {
  const kpi = useFabricStore((s) => s.frame?.kpi);

  const links = kpi ? `${kpi.links_up}/${kpi.links_total}` : DASH;
  const load = kpi ? kpi.total_mbit.toFixed(1) : DASH;
  const rtt = kpi ? kpi.avg_core_rtt_ms.toFixed(1) : DASH;
  const beacons = kpi ? kpi.beacons_per_sec.toFixed(0) : DASH;
  const shaped = kpi ? String(kpi.shaped) : DASH;

  return (
    <div className="kpis">
      <div className="kpi">
        <div className="label">Links up</div>
        <div className="value">{links}</div>
      </div>
      <div className="kpi">
        <div className="label">Fabric load</div>
        <div className="value">
          {load} <small>Mbit/s</small>
        </div>
      </div>
      <div className="kpi">
        <div className="label">Core RTT</div>
        <div className="value">
          {rtt} <small>ms</small>
        </div>
      </div>
      <div className="kpi">
        <div className="label">Beacons</div>
        <div className="value">
          {beacons} <small>/s</small>
        </div>
      </div>
      <div className="kpi brass">
        <div className="label">Shaped</div>
        <div className="value">{shaped}</div>
      </div>
    </div>
  );
}
