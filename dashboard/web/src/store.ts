// Zustand store: the single source of truth for topology/frame data, the
// live-connection flag, the current selection, and the ticker log. applyFrame
// is the state machine that turns raw frame-over-frame band changes into
// human-readable TickerEvents, porting the message/class logic from the
// mockup's stepMock (docs/superpowers/specs/mockups/fabric-mockup.html).
import { create } from "zustand";
import type { Band, Frame, Graph, LinkVM, TickerCls, TickerEvent } from "./types";

export interface Selection {
  kind: "link" | "as";
  id: string;
}

export interface FabricState {
  topology?: Graph;
  frame?: Frame;
  selected?: Selection;
  connected: boolean;
  events: TickerEvent[];
  /** Derived map for cheap O(1) component access, kept in sync with frame. */
  linksById: Record<string, LinkVM>;

  applySnapshot: (topology: Graph, frame: Frame) => void;
  applyFrame: (frame: Frame) => void;
  select: (selection: Selection | undefined) => void;
  setConnected: (connected: boolean) => void;
}

const MAX_EVENTS = 9;

// Display word for each band, mirroring the mockup's BANDWORD table.
const BAND_WORD: Record<Band, string> = {
  nominal: "NOMINAL",
  elevated: "ELEVATED",
  degraded: "DEGRADED",
  critical: "CRITICAL",
  down: "LINK DOWN",
  stale: "STALE",
};

// Severity order used only to decide whether a band change is an improvement
// (mirrors the mockup's up/down check on the nominal..down index). down and
// stale are both health overrides that sit outside the RTT/loss ordering, so
// they share the worst rank: a down<->stale transition is neither better nor
// worse, just a different way of saying "not trustworthy right now".
const SEVERITY: Record<Band, number> = {
  nominal: 0,
  elevated: 1,
  degraded: 2,
  critical: 3,
  down: 4,
  stale: 4,
};

// classFor maps a (previous -> next) band transition to a ticker style:
// improvements are "good"; critical/down/stale are "crit"; degraded is
// "bad"; elevated is "warn". ("brass" is reserved for shaping-applied
// events emitted later by the shaping panel, Task 11.)
function classFor(prevBand: Band, band: Band): TickerCls {
  if (SEVERITY[band] < SEVERITY[prevBand]) return "good";
  switch (band) {
    case "critical":
    case "down":
    case "stale":
      return "crit";
    case "degraded":
      return "bad";
    case "elevated":
      return "warn";
    default:
      return "good";
  }
}

// eventText formats "<a>↔<b> <BAND>[ · RTT <n> MS]", e.g.
// "155↔158 DEGRADED · RTT 53 MS". Link IDs are "<lowerAS>-<higherAS>", so
// splitting on "-" recovers the two AS numbers in display order. The RTT
// suffix uses the worse of the two sides' readings and is omitted for
// down/stale links, whose RTT numbers are not meaningful.
function eventText(link: LinkVM, band: Band): string {
  const [a, b] = link.id.split("-");
  const word = BAND_WORD[band];
  if (band === "down" || band === "stale") {
    return `${a}↔${b} ${word}`;
  }
  const rtt = Math.round(Math.max(link.rtt_ms_a, link.rtt_ms_b));
  return `${a}↔${b} ${word} · RTT ${rtt} MS`;
}

function indexLinks(links: LinkVM[]): Record<string, LinkVM> {
  const map: Record<string, LinkVM> = {};
  for (const l of links) map[l.id] = l;
  return map;
}

export const useFabricStore = create<FabricState>((set, get) => ({
  topology: undefined,
  frame: undefined,
  selected: undefined,
  connected: false,
  events: [],
  linksById: {},

  applySnapshot: (topology, frame) => {
    set({ topology, frame, linksById: indexLinks(frame.links) });
  },

  applyFrame: (frame) => {
    const prevFrame = get().frame;
    const prevById = prevFrame ? indexLinks(prevFrame.links) : {};

    // Collect new events in frame order, then reverse before prepending so
    // that, within one frame, the last-changed link ends up on top -- same
    // as the mockup's repeated tickerEl.prepend(li) calls.
    const changed: TickerEvent[] = [];
    for (const link of frame.links) {
      const prevLink = prevById[link.id];
      if (!prevLink || prevLink.band === link.band) continue;
      changed.push({
        t: Date.now(),
        text: eventText(link, link.band),
        cls: classFor(prevLink.band, link.band),
      });
    }
    changed.reverse();

    const events = changed.length > 0 ? [...changed, ...get().events].slice(0, MAX_EVENTS) : get().events;

    set({ frame, linksById: indexLinks(frame.links), events });
  },

  select: (selected) => set({ selected }),

  setConnected: (connected) => set({ connected }),
}));
