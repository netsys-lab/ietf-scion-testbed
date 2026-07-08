// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from "vitest";
import { asRole, confFilename, loadClaim, pickConf, saveClaim, type ClaimResult } from "./join";

const claim: ClaimResult = {
  slot: 2, ip: "10.20.5.2", as: 158, isd_as: "1-158",
  fc00_identity: "fc00:1000:9e00::ffff:a14:502",
  conf: "…Endpoint = [fd99::201]:51820…",
  conf_v4: "…Endpoint = 203.0.113.7:51820…",
  endpoint_v6: "[fd99::201]:51820", endpoint_v4: "203.0.113.7:51820",
};

describe("join helpers", () => {
  beforeEach(() => localStorage.clear());

  it("names the conf file by AS", () => {
    expect(confFilename(158)).toBe("scion-ietf126-as158.conf");
  });

  it("picks the v4 conf only when asked and available", () => {
    expect(pickConf(claim, false)).toContain("[fd99::201]");
    expect(pickConf(claim, true)).toContain("203.0.113.7");
    expect(pickConf({ ...claim, conf_v4: undefined }, true)).toContain("[fd99::201]");
  });

  it("round-trips a claim through localStorage", () => {
    expect(loadClaim()).toBeNull();
    saveClaim(claim);
    expect(loadClaim()?.fc00_identity).toBe(claim.fc00_identity);
  });

  it("survives corrupt storage", () => {
    localStorage.setItem("wg-claim", "{nope");
    expect(loadClaim()).toBeNull();
  });

  it("labels AS roles by topology tier", () => {
    expect(asRole(150)).toBe("core");
    expect(asRole(153)).toBe("core");
    expect(asRole(155)).toBe("hub");
    expect(asRole(158)).toBe("leaf");
    expect(asRole(161)).toBe("leaf");
  });
});
