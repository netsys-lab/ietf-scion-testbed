import { describe, expect, it } from "vitest";
import { lossColor } from "./LinkPanel";

describe("lossColor", () => {
  it("is the panel's neutral steel color at exactly zero loss", () => {
    expect(lossColor(0)).toBe("#5A7A9E");
  });

  it("switches to alarm orange for any positive loss", () => {
    expect(lossColor(0.1)).toBe("#EC835A");
    expect(lossColor(1)).toBe("#EC835A");
    expect(lossColor(20)).toBe("#EC835A");
  });

  it("never returns orange for zero, even repeatedly", () => {
    expect(lossColor(0)).not.toBe("#EC835A");
  });
});
