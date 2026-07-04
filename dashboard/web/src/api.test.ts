import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { connectLive } from "./api";

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
