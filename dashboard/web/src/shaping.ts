// Pure, framework-agnostic helpers for the shaping panel: turning the four
// slider positions into a wire-shaping payload (omitting neutral fields the
// same way the mockup's null-semantics do), formatting the applied summary
// for the ticker, and the direction control's ordering/labels. Kept out of the
// React component so shaping.test.ts can exercise the logic directly.
import type { Direction, Shaping, ShapingResult } from "./types";

// The four raw slider values, in their display units.
export interface SliderValues {
  delay: number; // ms
  jitter: number; // ms
  loss: number; // %
  rate: number; // Mbit
}

// Neutral (identity) slider positions: no added delay/jitter/loss and the full
// 100 Mbit rate ceiling — i.e. "no shaping". Sliders reset here for a link with
// no active shaping.
export const NEUTRAL: SliderValues = { delay: 0, jitter: 0, loss: 0, rate: 100 };

// buildShaping emits only the fields that depart from neutral (delay>0,
// jitter>0, loss>0, rate<100), mirroring the mockup's null semantics: an
// all-neutral slider set produces an empty payload (a clear).
export function buildShaping(v: SliderValues): Shaping {
  const p: Shaping = {};
  if (v.delay > 0) p.delay_ms = v.delay;
  if (v.jitter > 0) p.jitter_ms = v.jitter;
  if (v.loss > 0) p.loss_pct = v.loss;
  if (v.rate < 100) p.rate_mbit = v.rate;
  return p;
}

// isNeutral is true when buildShaping would send nothing (all sliders at rest).
export function isNeutral(v: SliderValues): boolean {
  return Object.keys(buildShaping(v)).length === 0;
}

// shapingToValues seeds the sliders from a link's current shaping (from the
// live frame), defaulting each missing field to its neutral position.
export function shapingToValues(s: Shaping | undefined): SliderValues {
  return {
    delay: s?.delay_ms ?? 0,
    jitter: s?.jitter_ms ?? 0,
    loss: s?.loss_pct ?? 0,
    rate: s?.rate_mbit ?? 100,
  };
}

// appliedSummary renders the ticker suffix for a successful apply, e.g.
// "+50 MS · 1 % · 50 MBIT"; an empty payload reads "CLEARED".
export function appliedSummary(p: Shaping): string {
  const parts: string[] = [];
  if (p.delay_ms) parts.push(`+${p.delay_ms} MS`);
  if (p.loss_pct) parts.push(`${p.loss_pct} %`);
  if (p.rate_mbit != null && p.rate_mbit < 100) parts.push(`${p.rate_mbit} MBIT`);
  return parts.length > 0 ? parts.join(" · ") : "CLEARED";
}

// The three shaping directions, in the order the toggle renders them.
export const DIRECTIONS: readonly Direction[] = ["a_to_b", "b_to_a", "both"] as const;

// nextDirection cycles a→b → b→a → both → a→b, for keyboard-driven toggling.
export function nextDirection(current: Direction): Direction {
  const i = DIRECTIONS.indexOf(current);
  return DIRECTIONS[(i + 1) % DIRECTIONS.length];
}

// directionLabel is the button caption for a direction given the two AS
// numbers, e.g. directionLabel("a_to_b", 151, 155) === "151 → 155".
export function directionLabel(dir: Direction, aAs: number, bAs: number): string {
  if (dir === "a_to_b") return `${aAs} → ${bAs}`;
  if (dir === "b_to_a") return `${bAs} → ${aAs}`;
  return "both";
}

// resultsFromErrorBody recovers the per-endpoint results from a thrown error's
// message. When every linkd endpoint fails the backend returns HTTP 502 with a
// {"results":[...]} body, which the api client surfaces as a thrown Error whose
// message is that raw body — so the error path still yields structured results
// to render inline (this is exactly the mock-mode case: no real linkd).
export function resultsFromErrorBody(message: string): ShapingResult[] | null {
  try {
    const o = JSON.parse(message) as { results?: unknown };
    if (o && Array.isArray(o.results)) return o.results as ShapingResult[];
  } catch {
    // Not a JSON results body (network failure, plain-text 4xx) — caller shows
    // the raw message instead.
  }
  return null;
}
