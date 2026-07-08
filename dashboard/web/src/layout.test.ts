import { describe, expect, it } from "vitest";
import { LINKS, NODES, linkMeta, linkPath, pathFrom, routePoints } from "./layout";

// Count the octilinear/arc bend commands ("Q") in a path string.
function countQ(d: string): number {
  return (d.match(/Q/g) ?? []).length;
}

// The 24 inter-AS link IDs the corrected testbed config produces (topo.Graph
// order, "<lowerAS>-<higherAS>"). FabricMap only draws links that have a
// layout entry, so every one of these must resolve.
const REAL_LINK_IDS = [
  "150-151", "150-152", "150-153", "151-152", "151-153", "152-153",
  "150-154", "151-154", "151-155", "152-155", "152-156", "152-157",
  "153-156", "153-157", "154-158", "154-155", "155-158", "155-156",
  "158-159", "155-159", "155-160", "155-161", "156-161", "157-161",
];

describe("pathFrom / routePoints", () => {
  it("routes 150-154 as a vertical-diagonal-vertical octilinear path with two rounded bends", () => {
    const d = pathFrom(routePoints(150, 154));
    expect(d.startsWith("M 365 150")).toBe(true);
    expect(countQ(d)).toBe(2);
  });

  it("routes the 155-161 via-path from its first via point with exactly one rounded bend", () => {
    const d = linkPath("155-161");
    expect(d.startsWith("M 645 440")).toBe(true);
    expect(countQ(d)).toBe(1);
  });
});

describe("LINKS integrity", () => {
  it("has a unique id per link and both endpoints exist in NODES", () => {
    const seen = new Set<string>();
    for (const L of LINKS) {
      const id = `${L.a}-${L.b}`;
      expect(seen.has(id)).toBe(false);
      seen.add(id);
      expect(NODES[L.a]).toBeDefined();
      expect(NODES[L.b]).toBeDefined();
    }
  });

  it("provides a layout entry for every real topology link id", () => {
    for (const id of REAL_LINK_IDS) {
      const meta = linkMeta(id);
      expect(meta, `missing layout for ${id}`).toBeDefined();
      expect(linkPath(id).length).toBeGreaterThan(0);
    }
    // and there are exactly 24 links total
    expect(LINKS.length).toBe(REAL_LINK_IDS.length);
  });
});
