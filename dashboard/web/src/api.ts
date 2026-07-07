// Live-data client: the /api/live WebSocket protocol plus the REST calls for
// history, link shaping, and the /join attendee flow. Kept framework-agnostic
// (no React/zustand here) so it can be unit-tested and reused independent of
// the store.
import type { ClaimResult } from "./join";
import type { Direction, Frame, Graph, IdintPathsResponse, Sample, Shaping, ShapingResponse } from "./types";

const MIN_BACKOFF_MS = 500;
const MAX_BACKOFF_MS = 8000;

function liveURL(): string {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  return `${proto}://${location.host}/api/live`;
}

/**
 * connectLive opens the /api/live WebSocket and dispatches parsed messages to
 * onSnapshot (first message, and again on every reconnect since the server
 * always re-sends a fresh snapshot to a newly-connected client) and onFrame
 * (every periodic update thereafter). On disconnect it reconnects with
 * exponential backoff (0.5s -> 8s cap), resetting the backoff to its floor
 * after a successful connection. Returns a disposer that stops reconnecting
 * and closes the socket.
 *
 * onStatusChange (optional) fires true on every successful open and false on
 * every disconnect, so the caller can drive a LIVE/RECONNECTING indicator. It
 * is deliberately silenced after dispose(): a disposer-triggered close is an
 * intentional teardown, not a lost connection.
 */
export function connectLive(
  onSnapshot: (topology: Graph, frame: Frame) => void,
  onFrame: (frame: Frame) => void,
  onStatusChange?: (connected: boolean) => void,
): () => void {
  let disposed = false;
  let socket: WebSocket | null = null;
  let backoff = MIN_BACKOFF_MS;
  let reconnectTimer: ReturnType<typeof setTimeout> | undefined;

  function connect() {
    if (disposed) return;
    const ws = new WebSocket(liveURL());
    socket = ws;

    ws.onopen = () => {
      backoff = MIN_BACKOFF_MS;
      onStatusChange?.(true);
    };

    ws.onmessage = (ev) => {
      let msg: { type?: string; topology?: Graph; frame?: Frame };
      try {
        msg = JSON.parse(ev.data as string);
      } catch {
        return;
      }
      if (msg.type === "snapshot" && msg.topology && msg.frame) {
        onSnapshot(msg.topology, msg.frame);
      } else if (msg.type === "frame" && msg.frame) {
        onFrame(msg.frame);
      }
    };

    ws.onclose = () => {
      if (disposed) return;
      onStatusChange?.(false);
      scheduleReconnect();
    };

    ws.onerror = () => {
      ws.close();
    };
  }

  function scheduleReconnect() {
    const delay = backoff;
    backoff = Math.min(backoff * 2, MAX_BACKOFF_MS);
    reconnectTimer = setTimeout(connect, delay);
  }

  connect();

  return () => {
    disposed = true;
    if (reconnectTimer !== undefined) clearTimeout(reconnectTimer);
    socket?.close();
  };
}

/** request performs a fetch and throws the server's error body text on any
 * non-2xx response, otherwise resolves with the parsed JSON body. */
async function request<T>(input: string, init?: RequestInit): Promise<T> {
  const res = await fetch(input, init);
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as T;
}

export function putShaping(linkId: string, direction: Direction, params: Shaping = {}): Promise<ShapingResponse> {
  return request<ShapingResponse>(`/api/links/${encodeURIComponent(linkId)}/shaping`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ direction, ...params }),
  });
}

export function resetShaping(linkId: string, direction: Direction): Promise<ShapingResponse> {
  return request<ShapingResponse>(`/api/links/${encodeURIComponent(linkId)}/reset`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ direction }),
  });
}

export function fetchHistory(key: string, mins: number): Promise<Sample[]> {
  const params = new URLSearchParams({ key, mins: String(mins) });
  return request<Sample[]>(`/api/history?${params.toString()}`);
}

export type JoinMeta = {
  enabled: boolean; joinable_ases: number[]; playground_ases: number[];
  slots_total: number; slots_claimed: number; slots_burned: number;
  hub_ok: boolean; endpoint_v6: string; endpoint_v4?: string;
};

export async function fetchJoinMeta(): Promise<JoinMeta> {
  const r = await fetch("/api/join/meta");
  if (!r.ok) throw new Error(`meta: ${r.status}`);
  return r.json();
}

export async function claimConf(as: number, code: string): Promise<ClaimResult> {
  const r = await fetch("/api/join/claim", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ as, code }),
  });
  if (!r.ok) throw new Error(String(r.status));
  return r.json();
}

export async function fetchInstructions(): Promise<{ name: string; title: string }[]> {
  const r = await fetch("/api/instructions");
  if (!r.ok) throw new Error(`instructions: ${r.status}`);
  return r.json();
}

export async function fetchInstruction(name: string): Promise<string> {
  const r = await fetch(`/api/instructions/${name}`);
  if (!r.ok) throw new Error(`instruction: ${r.status}`);
  return r.text();
}

export function fetchIdintPaths(src: number, dst: number): Promise<IdintPathsResponse> {
  return request<IdintPathsResponse>(`/api/idint/paths?src=${src}&dst=${dst}`);
}

export async function putTrace(src: number, dst: number, fingerprint?: string): Promise<void> {
  await request<{ ok: boolean }>(`/api/idint/trace`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(fingerprint ? { src, dst, fingerprint } : { src, dst }),
  });
}

export async function stopTrace(): Promise<void> {
  await request<{ ok: boolean }>(`/api/idint/trace`, { method: "DELETE" });
}
