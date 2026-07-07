import { describe, expect, it } from "vitest";
import { hopRows, latencyLabel, pathLabel } from "./trace";
import type { LinkVM, PathOption, TraceHop, TraceVM } from "./types";

function vm(hops: TraceHop[]): TraceVM {
  return {
    src: "1-150",
    dst: "1-161",
    fingerprint: "abc",
    auto: true,
    path_links: [],
    ok: true,
    updated_at: 1,
    probe_rtt_ms: 5,
    hops,
  };
}

function hop(partial: Partial<TraceHop> & { link: string }): TraceHop {
  return { ia: "1-150", verified: true, ...partial };
}

function link(partial: Partial<LinkVM> & { id: string }): LinkVM {
  return {
    band: "nominal",
    rtt_ms_a: 1,
    rtt_ms_b: 1,
    rate_ab_mbit: 0,
    rate_ba_mbit: 0,
    loss_pct: 0,
    up: true,
    stale: false,
    ...partial,
  };
}

describe("hopRows", () => {
  it("scales barPct against the max hop RTT in this VM, the max hop reading 100", () => {
    const rows = hopRows(
      vm([
        hop({ link: "150-154", rtt_next_br_us: 1000 }),
        hop({ link: "154-155", rtt_next_br_us: 2000 }),
        hop({ link: "155-161", rtt_next_br_us: 500 }),
      ]),
      {},
    );
    expect(rows.map((r) => r.barPct)).toEqual([50, 100, 25]);
    expect(rows.map((r) => r.rttMs)).toEqual([1, 2, 0.5]);
  });

  it("gives barPct 0 and rttMs null for a hop with no RTT reading", () => {
    const rows = hopRows(vm([hop({ link: "150-154" })]), {});
    expect(rows[0].barPct).toBe(0);
    expect(rows[0].rttMs).toBeNull();
  });

  it("does not blow up when every hop is missing an RTT reading (max floors at 1)", () => {
    const rows = hopRows(vm([hop({ link: "a" }), hop({ link: "b" })]), {});
    expect(rows.every((r) => r.barPct === 0)).toBe(true);
  });

  it("marks shaped true only when the hop's link carries a live shaping overlay", () => {
    const linksById: Record<string, LinkVM> = {
      "150-154": link({ id: "150-154", shaping: { delay_ms: 50 } }),
      "154-155": link({ id: "154-155" }), // no shaping
    };
    const rows = hopRows(
      vm([
        hop({ link: "150-154", rtt_next_br_us: 1000 }),
        hop({ link: "154-155", rtt_next_br_us: 1000 }),
        hop({ link: "not-in-map", rtt_next_br_us: 1000 }),
      ]),
      linksById,
    );
    expect(rows.map((r) => r.shaped)).toEqual([true, false, false]);
  });

  it("carries egr/queue through, defaulting missing values to null", () => {
    const rows = hopRows(vm([hop({ link: "a", egr_tx_pct: 12.5, queue_len: 3 }), hop({ link: "b" })]), {});
    expect(rows[0]).toMatchObject({ egrPct: 12.5, queue: 3 });
    expect(rows[1]).toMatchObject({ egrPct: null, queue: null });
  });
});

describe("pathLabel", () => {
  it("joins the path's hop AS numbers with the middle dot", () => {
    const p: PathOption = {
      fingerprint: "f1",
      hops: [150, 154, 155, 161],
      links: null,
      latency_us_total: 1000,
      mtu: 1400,
      expiry: "",
      current_best: false,
    };
    expect(pathLabel(p)).toBe("150 · 154 · 155 · 161");
  });
});

describe("latencyLabel", () => {
  const base: PathOption = {
    fingerprint: "f1",
    hops: [150, 161],
    links: null,
    latency_us_total: 0,
    mtu: 1400,
    expiry: "",
    current_best: false,
  };

  it("formats a non-negative advertised latency in ms", () => {
    expect(latencyLabel({ ...base, latency_us_total: 18900 })).toBe("Σ 18.9 ms adv.");
  });

  it("reads 'no latency data' for a -1 sentinel", () => {
    expect(latencyLabel({ ...base, latency_us_total: -1 })).toBe("no latency data");
  });
});
