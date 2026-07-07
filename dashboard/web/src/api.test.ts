import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { connectLive, fetchIdintPaths, putTrace, stopTrace } from "./api";

// A minimal stand-in for the browser WebSocket: it records every instance and
// exposes the handler slots connectLive assigns, so a test can drive the
// open/close lifecycle synchronously.
class MockWebSocket {
  static instances: MockWebSocket[] = [];
  url: string;
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  close = vi.fn();
  constructor(url: string) {
    this.url = url;
    MockWebSocket.instances.push(this);
  }
}

beforeEach(() => {
  MockWebSocket.instances = [];
  vi.stubGlobal("WebSocket", MockWebSocket);
  vi.stubGlobal("location", { protocol: "http:", host: "localhost:8080" });
  vi.useFakeTimers();
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("connectLive onStatusChange", () => {
  it("reports true on open and false on disconnect", () => {
    const statuses: boolean[] = [];
    const dispose = connectLive(
      () => {},
      () => {},
      (connected) => statuses.push(connected),
    );

    const ws = MockWebSocket.instances[0];
    expect(ws).toBeDefined();

    ws.onopen?.();
    expect(statuses).toEqual([true]);

    ws.onclose?.();
    expect(statuses).toEqual([true, false]);

    dispose();
  });

  it("stays silent when the disposer triggers the close", () => {
    const statuses: boolean[] = [];
    const dispose = connectLive(
      () => {},
      () => {},
      (connected) => statuses.push(connected),
    );

    const ws = MockWebSocket.instances[0];
    ws.onopen?.();
    expect(statuses).toEqual([true]);

    dispose();
    // A stray close event arriving after dispose must not report a lost link.
    ws.onclose?.();
    expect(statuses).toEqual([true]);
  });
});

describe("fetchIdintPaths", () => {
  it("requests the src/dst query string and parses the response", async () => {
    const body = { src: "1-150", dst: "1-161", paths: [] };
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => body,
    });
    vi.stubGlobal("fetch", fetchMock);

    const result = await fetchIdintPaths(150, 161);

    expect(fetchMock).toHaveBeenCalledWith("/api/idint/paths?src=150&dst=161", undefined);
    expect(result).toEqual(body);
  });

  it("throws the server's error body text on a non-2xx response", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 404,
        statusText: "Not Found",
        text: async () => "idint disabled",
      }),
    );

    await expect(fetchIdintPaths(150, 161)).rejects.toThrow("idint disabled");
  });
});

describe("putTrace", () => {
  it("sends src/dst without a fingerprint key when pinning is absent", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ ok: true }) });
    vi.stubGlobal("fetch", fetchMock);

    await putTrace(150, 161);

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/idint/trace",
      expect.objectContaining({
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ src: 150, dst: 161 }),
      }),
    );
  });

  it("includes the fingerprint when pinning a specific path", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ ok: true }) });
    vi.stubGlobal("fetch", fetchMock);

    await putTrace(150, 161, "abcd1234");

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/idint/trace",
      expect.objectContaining({
        method: "PUT",
        body: JSON.stringify({ src: 150, dst: 161, fingerprint: "abcd1234" }),
      }),
    );
  });
});

describe("stopTrace", () => {
  it("issues a DELETE to /api/idint/trace", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ ok: true }) });
    vi.stubGlobal("fetch", fetchMock);

    await stopTrace();

    expect(fetchMock).toHaveBeenCalledWith("/api/idint/trace", { method: "DELETE" });
  });
});
