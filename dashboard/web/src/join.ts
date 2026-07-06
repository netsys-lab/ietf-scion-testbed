// Pure helpers for the /join page. Keep React out of here so vitest covers
// the logic without rendering.

export type ClaimResult = {
  slot: number; ip: string; as: number; isd_as: string; fc00_identity: string;
  conf: string; conf_v4?: string; endpoint_v6: string; endpoint_v4?: string;
};

const KEY = "wg-claim";

export function confFilename(as: number): string {
  return `scion-ietf126-as${as}.conf`;
}

export function pickConf(c: ClaimResult, v4: boolean): string {
  return v4 && c.conf_v4 ? c.conf_v4 : c.conf;
}

export function saveClaim(c: ClaimResult): void {
  localStorage.setItem(KEY, JSON.stringify(c));
}

export function loadClaim(): ClaimResult | null {
  const raw = localStorage.getItem(KEY);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as ClaimResult;
  } catch {
    return null;
  }
}
