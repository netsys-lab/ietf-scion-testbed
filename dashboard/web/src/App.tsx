import { useEffect } from "react";
import { connectLive } from "./api";
import { useFabricStore } from "./store";
import Chrome from "./components/Chrome";
import FabricMap from "./components/FabricMap";
import ParticleLayer from "./components/ParticleLayer";
import Ticker from "./components/Ticker";
import LinkPanel from "./components/LinkPanel";
import AsPanel from "./components/AsPanel";
import TracePanel from "./components/TracePanel";
import "./components/chrome.css";
import "./components/panel.css";

// App shell: the mockup's three-row grid — masthead / map stage / ticker. It
// opens the /api/live socket, feeds snapshots and frames into the store, and
// drives the LIVE/RECONNECTING indicator off the socket's own open/close via
// connectLive's onStatusChange (so a dropped link surfaces immediately). The
// first snapshot also emits the boot "FABRIC ONLINE" ticker line. It also
// puts a "stale" class on #stage whenever the fabric has booted but the
// socket is currently down: the RECONNECTING pill says so in words, but a
// frozen map otherwise still looks fully live (particles keep drifting on
// last-known rates) — the class drives a dim/desaturate veil (index.css) so
// the map itself reads as "not trustworthy right now" too. The side panel
// (aside) is a placeholder here; Task 11 fills it.
function App() {
  const applySnapshot = useFabricStore((s) => s.applySnapshot);
  const applyFrame = useFabricStore((s) => s.applyFrame);
  const setConnected = useFabricStore((s) => s.setConnected);
  const pushEvent = useFabricStore((s) => s.pushEvent);
  const selected = useFabricStore((s) => s.selected);
  const connected = useFabricStore((s) => s.connected);
  const booted = useFabricStore((s) => s.booted);

  useEffect(() => {
    const dispose = connectLive(
      (topo, initialFrame) => {
        // Only the first snapshot is a genuine boot; a reconnect's re-snapshot
        // (topology already present) must not re-announce FABRIC ONLINE.
        const firstBoot = useFabricStore.getState().topology === undefined;
        applySnapshot(topo, initialFrame);
        if (firstBoot) {
          pushEvent({
            t: Date.now(),
            text: `FABRIC ONLINE · ${topo.links.length} LINKS · ${topo.ases.length} ASES`,
            cls: "good",
          });
        }
      },
      applyFrame,
      setConnected,
    );
    return dispose;
  }, [applySnapshot, applyFrame, setConnected, pushEvent]);

  return (
    <div id="app">
      <Chrome />
      <main id="main" className={selected ? "has-panel" : undefined}>
        <div id="stage" className={booted && !connected ? "stale" : undefined}>
          <FabricMap />
          <ParticleLayer />
        </div>
        <aside id="panel" aria-live="polite">
          {selected?.kind === "link" && <LinkPanel id={selected.id} />}
          {selected?.kind === "as" && <AsPanel num={Number(selected.id)} />}
          {selected?.kind === "trace" && <TracePanel />}
        </aside>
      </main>
      <footer>
        <Ticker />
      </footer>
    </div>
  );
}

export default App;
