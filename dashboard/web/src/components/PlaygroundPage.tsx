// PlaygroundPage — a booth-ops convenience view: all four SCION playground
// terminals (AS 152/155/158/161) tiled 2x2 in one full-viewport page, so a
// demo can drive/watch every endhost shell at once instead of tabbing
// between four /play/<as>/ windows. Like JoinPage, this is a separate,
// self-contained tree switched on pathname in main.tsx (outside App) — it
// has no need for the fabric map's WebSocket/store.
//
// Each cell embeds ttyd via <iframe src="/play/<as>/">, proxied same-origin
// by fabricd (internal/api/playproxy.go). ttyd guards those routes with HTTP
// basic auth (realm "ttyd"); since all four iframes are same origin + same
// realm, the browser should prompt for the booth code once and reuse it for
// the rest — that's expected, not a bug to work around.
import "./playground.css";

const PLAYGROUND_ASES = [152, 155, 158, 161];

export default function PlaygroundPage() {
  return (
    <div className="playground">
      <header className="playground-strip">
        <h1>SCION Playground — 4 live terminals</h1>
        <a href="/">← testbed map</a>
      </header>
      <div className="playground-grid">
        {PLAYGROUND_ASES.map((n) => (
          <section className="playground-cell" key={n}>
            <div className="playground-cellhead">
              AS 1-{n} · play-{n}
            </div>
            <iframe
              src={`/play/${n}/`}
              title={`AS 1-${n} terminal`}
              className="playground-frame"
            />
          </section>
        ))}
      </div>
    </div>
  );
}
