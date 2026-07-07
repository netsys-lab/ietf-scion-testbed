import { describe, expect, it } from "vitest";
import { NODES } from "./layout";
import { traceEndpoints, tracePathD } from "./tracepath";
import type { TraceVM } from "./types";

// Real, drawn link chain (see layout.test.ts's REAL_LINK_IDS / LINKS):
// 150-154, 154-155, 155-161 all have layout entries.
const CHAIN = ["150-154", "154-155", "155-161"];

describe("tracePathD", () => {
  it("chains src -> dst, starting and ending at the endpoint coordinates", () => {
    const d = tracePathD(CHAIN, 150);
    expect(d).not.toBeNull();
    const [x150, y150] = [NODES[150].x, NODES[150].y];
    const [x161, y161] = [NODES[161].x, NODES[161].y];
    expect(d!.startsWith(`M ${x150} ${y150}`)).toBe(true);
    expect(d!.endsWith(`${x161} ${y161}`)).toBe(true);
  });

  it("chains in reverse traversal order (same links, src at the other end)", () => {
    // path_links always walks from the actual src to the actual dst, so a
    // trace running 161 -> 150 supplies the same three link ids in reverse.
    const d = tracePathD([...CHAIN].reverse(), 161);
    expect(d).not.toBeNull();
    const [x150, y150] = [NODES[150].x, NODES[150].y];
    const [x161, y161] = [NODES[161].x, NODES[161].y];
    expect(d!.startsWith(`M ${x161} ${y161}`)).toBe(true);
    expect(d!.endsWith(`${x150} ${y150}`)).toBe(true);
  });

  it("returns null for a broken chain", () => {
    const d = tracePathD(["150-154", "155-161"], 150);
    expect(d).toBeNull();
  });

  it("returns null for an unknown link id", () => {
    const d = tracePathD(["999-998"], 999);
    expect(d).toBeNull();
  });
});

describe("traceEndpoints", () => {
  it("parses the AS numbers out of src/dst IA strings", () => {
    const trace = { src: "1-150", dst: "1-161" } as TraceVM;
    expect(traceEndpoints(trace)).toEqual([150, 161]);
  });
});
