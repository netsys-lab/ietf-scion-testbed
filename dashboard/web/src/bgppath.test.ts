import { describe, expect, it } from "vitest";
import { asPathText } from "./bgppath";
import type { BgpPathVM } from "./types";

const base: BgpPathVM = { src: "1-158", dst: "1-150", as_path: [158, 155, 150], path_links: ["155-158", "150-155"], complete: true };

describe("asPathText", () => {
  it("joins hops with › when complete", () => {
    expect(asPathText(base)).toBe("158 › 155 › 150");
  });
  it("marks truncated walks with a trailing ?", () => {
    expect(asPathText({ ...base, as_path: [158, 155], complete: false })).toBe("158 › 155 › ?");
  });
  it("renders a single-AS truncation honestly", () => {
    expect(asPathText({ ...base, as_path: [158], path_links: null, complete: false })).toBe("158 › ?");
  });
});
