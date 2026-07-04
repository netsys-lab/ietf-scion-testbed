import { useEffect } from "react";
import { connectLive } from "./api";
import { useFabricStore } from "./store";

// Placeholder shell for Task 8: wires the live-data client into the store
// and renders enough state to prove the pipeline works end to end. The real
// fabric map / KPI strip / ticker / panels land in Tasks 9-11.
function App() {
  const topology = useFabricStore((s) => s.topology);
  const frame = useFabricStore((s) => s.frame);
  const connected = useFabricStore((s) => s.connected);
  const events = useFabricStore((s) => s.events);
  const applySnapshot = useFabricStore((s) => s.applySnapshot);
  const applyFrame = useFabricStore((s) => s.applyFrame);
  const setConnected = useFabricStore((s) => s.setConnected);

  useEffect(() => {
    const dispose = connectLive(
      (topo, initialFrame) => {
        applySnapshot(topo, initialFrame);
        setConnected(true);
      },
      (nextFrame) => {
        applyFrame(nextFrame);
        setConnected(true);
      },
    );
    return dispose;
  }, [applySnapshot, applyFrame, setConnected]);

  return (
    <main>
      <h1>SCION Fabrik</h1>
      <p>connected: {String(connected)}</p>
      <p>
        topology:{" "}
        {topology ? `${topology.ases.length} ASes, ${topology.links.length} links` : "loading…"}
      </p>
      <p>frame: {frame ? `t=${frame.t}, links_up=${frame.kpi.links_up}/${frame.kpi.links_total}` : "none"}</p>
      <ul>
        {events.map((e) => (
          <li key={e.t + e.text} className={e.cls}>
            {e.text}
          </li>
        ))}
      </ul>
    </main>
  );
}

export default App;
