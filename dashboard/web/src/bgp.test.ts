import { describe, expect, it } from "vitest";
import { bgpChipText, BGP_WORD } from "./bgp";

describe("bgpChipText", () => {
  it("renders only attention states on the map", () => {
    expect(bgpChipText("down")).toBe("BGP DOWN");
    expect(bgpChipText("degraded")).toBe("BGP FLAP");
    expect(bgpChipText("up")).toBeNull();
    expect(bgpChipText("unknown")).toBeNull();
    expect(bgpChipText(undefined)).toBeNull();
  });
  it("has a panel word for every state", () => {
    expect(BGP_WORD.up).toBe("UP");
    expect(BGP_WORD.degraded).toBe("FLAPPING");
    expect(BGP_WORD.down).toBe("DOWN");
    expect(BGP_WORD.unknown).toBe("—");
  });
});
