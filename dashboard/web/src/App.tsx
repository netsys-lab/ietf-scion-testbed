import { useEffect } from "react";
import { connectLive } from "./api";
import { useFabricStore } from "./store";
import FabricMap from "./components/FabricMap";

// App shell for the dashboard. Opens the /api/live socket and feeds snapshots
// and frames into the store, then renders the fabric map as the stage's single
// child. The masthead, KPI strip, ticker (Task 10) and the selection panel
// (Task 11) slot into this grid later.
function App() {
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
    <div id="app">
      <div id="stage">
        <FabricMap />
      </div>
    </div>
  );
}

export default App;
