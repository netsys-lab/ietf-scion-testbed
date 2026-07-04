// Chrome — the masthead <header>: brand lockup, live/reconnecting indicator,
// KPI strip, and the OPERATE/SCREEN mode toggle. Ported from the mockup's
// <header>. Screen mode drives document.body's "screen" class (the mockup's
// body.screen scale-ups live in chrome.css); ?mode=screen sets it initially so
// the booth can boot straight into the wall-display layout.
import { useEffect, useState } from "react";
import { useFabricStore } from "../store";
import KpiStrip from "./KpiStrip";

function initialScreen(): boolean {
  if (typeof window === "undefined") return false;
  return new URLSearchParams(window.location.search).get("mode") === "screen";
}

export default function Chrome() {
  const connected = useFabricStore((s) => s.connected);
  const [screen, setScreen] = useState(initialScreen);

  useEffect(() => {
    document.body.classList.toggle("screen", screen);
  }, [screen]);

  return (
    <header>
      <div className="brand">
        <h1>
          SCION <span className="thin">FABRIK</span>
        </h1>
        <span className="sub">IETF 126 · WIEN · LIVE TESTBED</span>
        <span className={"livestate" + (connected ? "" : " down")} role="status">
          <span className="dot" />
          {connected ? "LIVE" : "RECONNECTING"}
        </span>
      </div>
      <KpiStrip />
      <div className="modes" role="group" aria-label="Display mode">
        <button aria-pressed={!screen} onClick={() => setScreen(false)}>
          OPERATE
        </button>
        <button aria-pressed={screen} onClick={() => setScreen(true)}>
          SCREEN
        </button>
      </div>
    </header>
  );
}
