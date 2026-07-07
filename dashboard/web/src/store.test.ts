import { beforeEach, describe, expect, it } from "vitest";
import { useFabricStore } from "./store";
import type { Frame, Graph, LinkVM, TraceVM } from "./types";

function makeLink(overrides: Partial<LinkVM> = {}): LinkVM {
  return {
    id: "151-155",
    band: "nominal",
    rtt_ms_a: 1.2,
    rtt_ms_b: 1.4,
    rate_ab_mbit: 0.5,
    rate_ba_mbit: 0.4,
    loss_pct: 0,
    up: true,
    stale: false,
    ...overrides,
  };
}

function makeFrame(links: LinkVM[], t = 1): Frame {
  return {
    t,
    links,
    ases: [],
    kpi: {
      links_up: links.filter((l) => l.up).length,
      links_total: links.length,
      shaped: 0,
      total_mbit: 0,
      avg_core_rtt_ms: 0,
      beacons_per_sec: 0,
    },
  };
}

function makeTrace(overrides: Partial<TraceVM> = {}): TraceVM {
  return {
    src: "1-150",
    dst: "1-161",
    fingerprint: "abcd1234",
    auto: true,
    path_links: ["150-155", "155-161"],
    ok: true,
    updated_at: 1,
    probe_rtt_ms: 12.5,
    hops: [],
    ...overrides,
  };
}

const emptyTopology: Graph = { ases: [], links: [] };

function resetStore() {
  // Merge (not replace) so the action functions defined by `create` survive
  // the reset -- only the data fields need to go back to their defaults.
  useFabricStore.setState({
    topology: undefined,
    frame: undefined,
    selected: undefined,
    connected: false,
    booted: false,
    screen: false,
    events: [],
    linksById: {},
  });
}

beforeEach(() => {
  resetStore();
});

describe("applyFrame", () => {
  it("emits exactly one ticker event with the expected text/cls when a link's band changes", () => {
    const before = makeFrame([makeLink({ band: "nominal" })]);
    useFabricStore.getState().applySnapshot(emptyTopology, before);

    const after = makeFrame([makeLink({ band: "degraded", rtt_ms_a: 53, rtt_ms_b: 40 })], 2);
    useFabricStore.getState().applyFrame(after);

    const { events } = useFabricStore.getState();
    expect(events).toHaveLength(1);
    expect(events[0].text).toBe("151↔155 DEGRADED · RTT 53 MS");
    expect(events[0].cls).toBe("bad");
  });

  it("emits a 'good' event when a link's band improves", () => {
    const before = makeFrame([makeLink({ band: "critical" })]);
    useFabricStore.getState().applySnapshot(emptyTopology, before);

    const after = makeFrame([makeLink({ band: "elevated", rtt_ms_a: 10, rtt_ms_b: 9 })], 2);
    useFabricStore.getState().applyFrame(after);

    const { events } = useFabricStore.getState();
    expect(events).toHaveLength(1);
    expect(events[0].cls).toBe("good");
  });

  it("omits the RTT suffix and uses 'crit' for a down link", () => {
    const before = makeFrame([makeLink({ band: "nominal" })]);
    useFabricStore.getState().applySnapshot(emptyTopology, before);

    const after = makeFrame([makeLink({ band: "down", up: false })], 2);
    useFabricStore.getState().applyFrame(after);

    const { events } = useFabricStore.getState();
    expect(events).toHaveLength(1);
    expect(events[0].text).toBe("151↔155 LINK DOWN");
    expect(events[0].cls).toBe("crit");
  });

  it("emits no events when the band is unchanged", () => {
    const before = makeFrame([makeLink({ band: "nominal" })]);
    useFabricStore.getState().applySnapshot(emptyTopology, before);

    const after = makeFrame([makeLink({ band: "nominal", rtt_ms_a: 1.5 })], 2);
    useFabricStore.getState().applyFrame(after);

    expect(useFabricStore.getState().events).toHaveLength(0);
  });

  it("caps events at 9, dropping the oldest", () => {
    let frame = makeFrame([makeLink({ band: "nominal" })], 0);
    useFabricStore.getState().applySnapshot(emptyTopology, frame);

    const bands: Array<LinkVM["band"]> = [
      "elevated",
      "nominal",
      "elevated",
      "nominal",
      "elevated",
      "nominal",
      "elevated",
      "nominal",
      "elevated",
      "nominal",
      "elevated",
    ];
    bands.forEach((band, i) => {
      frame = makeFrame([makeLink({ band })], i + 1);
      useFabricStore.getState().applyFrame(frame);
    });

    const { events } = useFabricStore.getState();
    expect(events).toHaveLength(9);
    // Most recent change (the last band flip applied) is at the front.
    expect(events[0].text).toContain("ELEVATED");
  });

  it("keeps linksById in sync with the latest frame", () => {
    const before = makeFrame([makeLink({ id: "150-151" })]);
    useFabricStore.getState().applySnapshot(emptyTopology, before);
    expect(useFabricStore.getState().linksById["150-151"]).toBeDefined();

    const after = makeFrame([makeLink({ id: "150-151", band: "elevated" })], 2);
    useFabricStore.getState().applyFrame(after);
    expect(useFabricStore.getState().linksById["150-151"].band).toBe("elevated");
  });

  it("carries frame.trace through into store state, readable via frame", () => {
    const before = makeFrame([makeLink({ band: "nominal" })]);
    useFabricStore.getState().applySnapshot(emptyTopology, before);
    expect(useFabricStore.getState().frame?.trace).toBeUndefined();

    const trace = makeTrace();
    const after = { ...makeFrame([makeLink({ band: "nominal" })], 2), trace };
    useFabricStore.getState().applyFrame(after);

    expect(useFabricStore.getState().frame?.trace).toEqual(trace);
  });
});

describe("setConnected", () => {
  it("flips the connected flag", () => {
    expect(useFabricStore.getState().connected).toBe(false);
    useFabricStore.getState().setConnected(true);
    expect(useFabricStore.getState().connected).toBe(true);
    useFabricStore.getState().setConnected(false);
    expect(useFabricStore.getState().connected).toBe(false);
  });
});

describe("setBooted", () => {
  it("flips the booted flag", () => {
    expect(useFabricStore.getState().booted).toBe(false);
    useFabricStore.getState().setBooted(true);
    expect(useFabricStore.getState().booted).toBe(true);
  });
});

describe("pushEvent", () => {
  it("prepends the event newest-first and caps the log at 9", () => {
    const push = useFabricStore.getState().pushEvent;
    for (let i = 0; i < 11; i++) {
      push({ t: i, text: `E${i}`, cls: "good" });
    }
    const { events } = useFabricStore.getState();
    expect(events).toHaveLength(9);
    expect(events[0].text).toBe("E10");
    expect(events[8].text).toBe("E2");
  });
});

describe("select", () => {
  it("sets the selection for a link", () => {
    expect(useFabricStore.getState().selected).toBeUndefined();
    useFabricStore.getState().select({ kind: "link", id: "151-155" });
    expect(useFabricStore.getState().selected).toEqual({ kind: "link", id: "151-155" });
  });

  it("sets the selection for an AS", () => {
    useFabricStore.getState().select({ kind: "as", id: "155" });
    expect(useFabricStore.getState().selected).toEqual({ kind: "as", id: "155" });
  });

  it("sets and round-trips the trace selection", () => {
    useFabricStore.getState().select({ kind: "trace", id: "trace" });
    expect(useFabricStore.getState().selected).toEqual({ kind: "trace", id: "trace" });
  });

  it("clears the selection with undefined", () => {
    useFabricStore.getState().select({ kind: "link", id: "151-155" });
    useFabricStore.getState().select(undefined);
    expect(useFabricStore.getState().selected).toBeUndefined();
  });

  it("keeps the selection across a frame update", () => {
    useFabricStore.getState().applySnapshot(emptyTopology, makeFrame([makeLink({ band: "nominal" })]));
    useFabricStore.getState().select({ kind: "link", id: "151-155" });

    // A frame that changes a link's band must not disturb the selection.
    const after = makeFrame([makeLink({ band: "degraded", rtt_ms_a: 53 })], 2);
    useFabricStore.getState().applyFrame(after);

    expect(useFabricStore.getState().selected).toEqual({ kind: "link", id: "151-155" });
    expect(useFabricStore.getState().linksById["151-155"].band).toBe("degraded");
  });
});
