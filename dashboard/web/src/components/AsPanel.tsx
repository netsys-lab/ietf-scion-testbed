// AsPanel — the selected AS's instrument card, ported from the mockup's
// selectAS() markup. It shows the three service LEDs (BR / CS / SD, lit green
// when up and red when down, from the live frame's ASVM health flags) and an
// interface table: one row per inter-AS link touching this AS, with the peer
// AS, the fade: subnet, the link RTT (or DOWN), and this AS's own In/Out rates
// (direction-corrected so "In" is always traffic arriving at this AS).
//
// No-data shell: if the selected AS drops out of the topology (mid-selection
// during a WS drop), the panel still renders a header with the usual close
// button plus a "NO LIVE DATA" line instead of returning null — an empty
// aside with no close affordance strands touch users.
import { useFabricStore } from "../store";

export default function AsPanel({ num }: { num: number }) {
  const ia = `1-${num}`;
  const topoAS = useFabricStore((s) => s.topology?.ases.find((a) => a.num === num));
  const asvm = useFabricStore((s) => s.frame?.ases.find((a) => a.ia === ia));
  const topology = useFabricStore((s) => s.topology);
  const linksById = useFabricStore((s) => s.linksById);
  const select = useFabricStore((s) => s.select);

  if (!topoAS) {
    return (
      <div className="panel-inner">
        <div className="panel-head">
          <button className="closebtn" aria-label="Close panel" onClick={() => select(undefined)}>
            ✕
          </button>
        </div>
        <span className="daemon-note">NO LIVE DATA</span>
      </div>
    );
  }

  const core = topoAS.core;
  // Every inter-AS link with this AS on either endpoint, in topology order.
  const ifs = (topology?.links ?? []).filter((l) => l.a.as === num || l.b.as === num);

  const led = (up: boolean | undefined, name: string, port: string) => (
    <span className={"led" + (up === false ? " down" : "")}>
      <span className="dot" />
      {name} {port}
    </span>
  );

  const beacons = asvm ? asvm.beacons_per_sec.toFixed(0) : "–";

  return (
    <div className="panel-inner">
      <div className="panel-head">
        <button className="closebtn" aria-label="Close panel" onClick={() => select(undefined)}>
          ✕
        </button>
        <div className="eyebrow">{core ? "Core autonomous system" : "Autonomous system"}</div>
        <h2>
          AS {ia}
          <span className="mono">{topoAS.mgmt_ip}</span>
        </h2>
      </div>

      <div className="leds">
        {led(asvm?.br_up, "BR", ":30442")}
        {led(asvm?.cs_up, "CS", ":30452")}
        {led(asvm?.sd_up, "SD", ":30455")}
      </div>

      <table className="iftable">
        <thead>
          <tr>
            <th>Peer</th>
            <th>Link</th>
            <th>RTT</th>
            <th>In</th>
            <th>Out</th>
          </tr>
        </thead>
        <tbody>
          {ifs.map((l) => {
            const isA = l.a.as === num;
            const peer = isA ? l.b.as : l.a.as;
            const vm = linksById[l.id];
            const down = vm?.band === "down";
            const rtt = vm ? Math.max(vm.rtt_ms_a, vm.rtt_ms_b) : 0;
            const rttCell = !vm ? "–" : down ? "DOWN" : `${rtt.toFixed(1)} ms`;
            // In = traffic arriving at this AS; Out = leaving it.
            const rin = vm ? (isA ? vm.rate_ba_mbit : vm.rate_ab_mbit).toFixed(1) : "–";
            const rout = vm ? (isA ? vm.rate_ab_mbit : vm.rate_ba_mbit).toFixed(1) : "–";
            return (
              <tr key={l.id}>
                <td className="peer">
                  1-{peer}
                  {l.type === "peer" ? " ⇄" : ""}
                </td>
                <td>{l.subnet}</td>
                <td>{rttCell}</td>
                <td>{rin}</td>
                <td>{rout}</td>
              </tr>
            );
          })}
        </tbody>
      </table>

      <span className="daemon-note">In/Out in Mbit/s · beaconing {beacons}/s</span>
    </div>
  );
}
