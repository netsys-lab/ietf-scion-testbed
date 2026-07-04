// Ticker — the footer event log, ported from the mockup's #ticker. Renders
// store.events (already newest-first, capped at 9): each band change becomes a
// timestamped line whose accent class (ev-good/warn/bad/crit/brass) is set by
// the store's classFor. Stable keys mean only a freshly-prepended line mounts
// and plays the tick-in slide (chrome.css); existing lines keep their identity.
import { useFabricStore } from "../store";

function hhmmss(t: number): string {
  return new Date(t).toTimeString().slice(0, 8);
}

export default function Ticker() {
  const events = useFabricStore((s) => s.events);

  return (
    <ul id="ticker" role="log" aria-label="Event ticker">
      {events.map((e) => (
        <li key={`${e.t}-${e.text}`}>
          <span className="t">{hhmmss(e.t)}</span>
          <span className={`ev-${e.cls}`}>{e.text}</span>
        </li>
      ))}
    </ul>
  );
}
