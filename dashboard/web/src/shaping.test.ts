import { describe, expect, it } from "vitest";
import {
  DIRECTIONS,
  NEUTRAL,
  appliedSummary,
  buildShaping,
  directionLabel,
  isNeutral,
  nextDirection,
  resultsFromErrorBody,
  shapingToValues,
} from "./shaping";

describe("buildShaping", () => {
  it("omits every field at its neutral position", () => {
    expect(buildShaping(NEUTRAL)).toEqual({});
  });

  it("includes only the fields that depart from neutral", () => {
    expect(buildShaping({ delay: 50, jitter: 0, loss: 0, rate: 100 })).toEqual({ delay_ms: 50 });
    expect(buildShaping({ delay: 0, jitter: 0, loss: 2.5, rate: 100 })).toEqual({ loss_pct: 2.5 });
    expect(buildShaping({ delay: 0, jitter: 0, loss: 0, rate: 50 })).toEqual({ rate_mbit: 50 });
  });

  it("includes rate only when throttled below the 100 Mbit ceiling", () => {
    expect(buildShaping({ delay: 0, jitter: 0, loss: 0, rate: 100 }).rate_mbit).toBeUndefined();
    expect(buildShaping({ delay: 0, jitter: 0, loss: 0, rate: 99 }).rate_mbit).toBe(99);
  });

  it("carries all four fields when all are shaped", () => {
    expect(buildShaping({ delay: 50, jitter: 5, loss: 1, rate: 50 })).toEqual({
      delay_ms: 50,
      jitter_ms: 5,
      loss_pct: 1,
      rate_mbit: 50,
    });
  });
});

describe("isNeutral", () => {
  it("is true only when nothing would be sent", () => {
    expect(isNeutral(NEUTRAL)).toBe(true);
    expect(isNeutral({ delay: 0, jitter: 0, loss: 0, rate: 99 })).toBe(false);
    expect(isNeutral({ delay: 1, jitter: 0, loss: 0, rate: 100 })).toBe(false);
  });
});

describe("shapingToValues", () => {
  it("defaults each missing field to its neutral position", () => {
    expect(shapingToValues(undefined)).toEqual(NEUTRAL);
  });

  it("round-trips a fully-shaped payload", () => {
    const v = { delay: 50, jitter: 5, loss: 1, rate: 50 };
    expect(shapingToValues(buildShaping(v))).toEqual(v);
  });
});

describe("appliedSummary", () => {
  it("reads CLEARED for an empty payload", () => {
    expect(appliedSummary({})).toBe("CLEARED");
  });

  it("formats the shaped fields in delay/loss/rate order", () => {
    expect(appliedSummary({ delay_ms: 50, loss_pct: 1, rate_mbit: 50 })).toBe("+50 MS · 1 % · 50 MBIT");
  });
});

describe("direction control", () => {
  it("orders the three directions a→b, b→a, both", () => {
    expect(DIRECTIONS).toEqual(["a_to_b", "b_to_a", "both"]);
  });

  it("cycles through the directions in order and wraps", () => {
    expect(nextDirection("a_to_b")).toBe("b_to_a");
    expect(nextDirection("b_to_a")).toBe("both");
    expect(nextDirection("both")).toBe("a_to_b");
  });

  it("labels each direction with the AS numbers in flow order", () => {
    expect(directionLabel("a_to_b", 151, 155)).toBe("151 → 155");
    expect(directionLabel("b_to_a", 151, 155)).toBe("155 → 151");
    expect(directionLabel("both", 151, 155)).toBe("both");
  });
});

describe("resultsFromErrorBody", () => {
  it("recovers per-endpoint results from a 502 JSON body", () => {
    const body = JSON.stringify({
      results: [
        { as: 151, ok: false, error: "dial tcp: connection refused" },
        { as: 155, ok: false, error: "context deadline exceeded" },
      ],
    });
    const parsed = resultsFromErrorBody(body);
    expect(parsed).toHaveLength(2);
    expect(parsed?.[0]).toEqual({ as: 151, ok: false, error: "dial tcp: connection refused" });
  });

  it("returns null for a non-JSON error message", () => {
    expect(resultsFromErrorBody("invalid direction")).toBeNull();
    expect(resultsFromErrorBody("")).toBeNull();
  });

  it("returns null when JSON lacks a results array", () => {
    expect(resultsFromErrorBody(JSON.stringify({ error: "boom" }))).toBeNull();
  });
});
