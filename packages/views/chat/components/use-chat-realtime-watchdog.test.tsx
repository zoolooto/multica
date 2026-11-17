// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { chatKeys } from "@multica/core/chat/queries";
import type { WSStatus } from "@multica/core/realtime";
import { CHAT_WATCHDOG_POLL_MS } from "../lib/realtime-watchdog";
import { useChatRealtimeWatchdog } from "./use-chat-realtime-watchdog";

const WS_ID = "ws-1";
const SESSION = "11111111-1111-4111-8111-111111111111";
const TASK = "22222222-2222-4222-8222-222222222222";

function setup() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const invalidate = vi.spyOn(qc, "invalidateQueries");
  function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  }
  return { qc, invalidate, Wrapper };
}

function invalidatedKeys(invalidate: { mock: { calls: unknown[][] } }): string[] {
  return invalidate.mock.calls.map((call) =>
    JSON.stringify((call[0] as { queryKey?: unknown } | undefined)?.queryKey ?? null),
  );
}

describe("useChatRealtimeWatchdog", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("does nothing while the socket is connected", () => {
    const { invalidate, Wrapper } = setup();
    renderHook(
      () =>
        useChatRealtimeWatchdog({
          status: "connected",
          wsId: WS_ID,
          sessionId: SESSION,
          taskId: TASK,
        }),
      { wrapper: Wrapper },
    );
    act(() => {
      vi.advanceTimersByTime(CHAT_WATCHDOG_POLL_MS * 3);
    });
    expect(invalidate).not.toHaveBeenCalled();
  });

  it("treats a client that never connected as healthy, not degraded", () => {
    // `connecting` is the first attempt of a fresh client; `offline` means no
    // connection is wanted at all (signed out / no workspace).
    for (const status of ["connecting", "offline"] as WSStatus[]) {
      const { invalidate, Wrapper } = setup();
      renderHook(
        () =>
          useChatRealtimeWatchdog({ status, wsId: WS_ID, sessionId: SESSION, taskId: null }),
        { wrapper: Wrapper },
      );
      act(() => {
        vi.advanceTimersByTime(CHAT_WATCHDOG_POLL_MS * 2);
      });
      expect(invalidate).not.toHaveBeenCalled();
    }
  });

  it("reconciles immediately and then on every interval while reconnecting", () => {
    const { invalidate, Wrapper } = setup();
    renderHook(
      () =>
        useChatRealtimeWatchdog({
          status: "reconnecting",
          wsId: WS_ID,
          sessionId: SESSION,
          taskId: TASK,
        }),
      { wrapper: Wrapper },
    );

    // Leading edge: the terminal frame may already be lost by the time the
    // client notices the socket is down.
    const firstRound = invalidatedKeys(invalidate);
    expect(firstRound).toContain(JSON.stringify(chatKeys.pendingTasks(WS_ID)));
    expect(firstRound).toContain(JSON.stringify(chatKeys.pendingTask(SESSION)));
    expect(firstRound).toContain(JSON.stringify(chatKeys.messagesPage(SESSION)));
    expect(firstRound).toContain(JSON.stringify(chatKeys.taskMessages(TASK)));

    invalidate.mockClear();
    act(() => {
      vi.advanceTimersByTime(CHAT_WATCHDOG_POLL_MS);
    });
    expect(invalidatedKeys(invalidate)).toContain(
      JSON.stringify(chatKeys.pendingTask(SESSION)),
    );
  });

  it("stops reconciling once the socket is back", () => {
    const { invalidate, Wrapper } = setup();
    const { rerender } = renderHook(
      ({ status }: { status: WSStatus }) =>
        useChatRealtimeWatchdog({ status, wsId: WS_ID, sessionId: SESSION, taskId: TASK }),
      { wrapper: Wrapper, initialProps: { status: "reconnecting" as WSStatus } },
    );
    expect(invalidate).toHaveBeenCalled();

    rerender({ status: "connected" });
    invalidate.mockClear();
    act(() => {
      vi.advanceTimersByTime(CHAT_WATCHDOG_POLL_MS * 3);
    });
    expect(invalidate).not.toHaveBeenCalled();
  });

  it("reconciles again when the socket drops a second time", () => {
    const { invalidate, Wrapper } = setup();
    const { rerender } = renderHook(
      ({ status }: { status: WSStatus }) =>
        useChatRealtimeWatchdog({ status, wsId: WS_ID, sessionId: SESSION, taskId: TASK }),
      { wrapper: Wrapper, initialProps: { status: "connected" as WSStatus } },
    );
    expect(invalidate).not.toHaveBeenCalled();

    rerender({ status: "reconnecting" });
    expect(invalidatedKeys(invalidate)).toContain(
      JSON.stringify(chatKeys.pendingTask(SESSION)),
    );
  });

  it("skips the live timeline when no task id is known yet", () => {
    const { invalidate, Wrapper } = setup();
    renderHook(
      () =>
        useChatRealtimeWatchdog({
          status: "reconnecting",
          wsId: WS_ID,
          sessionId: SESSION,
          taskId: null,
        }),
      { wrapper: Wrapper },
    );
    const keys = invalidatedKeys(invalidate);
    expect(keys).toContain(JSON.stringify(chatKeys.pendingTask(SESSION)));
    expect(keys.some((key) => key.includes("task-messages"))).toBe(false);
  });

  it("ignores a non-UUID task id instead of querying it", () => {
    const { invalidate, Wrapper } = setup();
    renderHook(
      () =>
        useChatRealtimeWatchdog({
          status: "reconnecting",
          wsId: WS_ID,
          sessionId: SESSION,
          taskId: "client-side-pending",
        }),
      { wrapper: Wrapper },
    );
    expect(invalidatedKeys(invalidate).some((key) => key.includes("task-messages"))).toBe(false);
  });

  it("does nothing when no conversation is open", () => {
    const { invalidate, Wrapper } = setup();
    renderHook(
      () =>
        useChatRealtimeWatchdog({
          status: "reconnecting",
          wsId: WS_ID,
          sessionId: null,
          taskId: null,
        }),
      { wrapper: Wrapper },
    );
    expect(invalidate).not.toHaveBeenCalled();
  });

  it("clears its timer on unmount", () => {
    const { invalidate, Wrapper } = setup();
    const { unmount } = renderHook(
      () =>
        useChatRealtimeWatchdog({
          status: "reconnecting",
          wsId: WS_ID,
          sessionId: SESSION,
          taskId: TASK,
        }),
      { wrapper: Wrapper },
    );
    unmount();
    invalidate.mockClear();
    act(() => {
      vi.advanceTimersByTime(CHAT_WATCHDOG_POLL_MS * 3);
    });
    expect(invalidate).not.toHaveBeenCalled();
  });
});
