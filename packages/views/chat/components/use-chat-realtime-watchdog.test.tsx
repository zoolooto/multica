// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { chatKeys } from "@multica/core/chat/queries";
import type { WSStatus } from "@multica/core/realtime";
import { CHAT_WATCHDOG_POLL_MS, CHAT_PENDING_RECONCILE_MS } from "../lib/realtime-watchdog";
import { useChatRealtimeWatchdog } from "./use-chat-realtime-watchdog";

const WS_ID = "ws-1";
const SESSION = "11111111-1111-4111-8111-111111111111";
const OTHER_SESSION = "33333333-3333-4333-8333-333333333333";
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

  it("does nothing while the socket is healthy and no turn is running", () => {
    const { invalidate, Wrapper } = setup();
    renderHook(
      () =>
        useChatRealtimeWatchdog({
          status: "connected",
          enabled: true,
          wsId: WS_ID,
          sessionId: SESSION,
          taskId: null,
        }),
      { wrapper: Wrapper },
    );
    act(() => {
      vi.advanceTimersByTime(CHAT_WATCHDOG_POLL_MS * 3);
    });
    expect(invalidate).not.toHaveBeenCalled();
  });

  it("treats a first connection attempt and a signed-out client as healthy", () => {
    for (const status of ["connecting", "offline"] as WSStatus[]) {
      const { invalidate, Wrapper } = setup();
      renderHook(
        () =>
          useChatRealtimeWatchdog({
            status,
            enabled: true,
            wsId: WS_ID,
            sessionId: SESSION,
            taskId: null,
          }),
        { wrapper: Wrapper },
      );
      act(() => {
        vi.advanceTimersByTime(CHAT_WATCHDOG_POLL_MS * 2);
      });
      expect(invalidate).not.toHaveBeenCalled();
    }
  });

  it("re-reads the conversation once, then polls only the turn keys", () => {
    const { invalidate, Wrapper } = setup();
    renderHook(
      () =>
        useChatRealtimeWatchdog({
          status: "reconnecting",
          enabled: true,
          wsId: WS_ID,
          sessionId: SESSION,
          taskId: TASK,
        }),
      { wrapper: Wrapper },
    );

    // Leading edge: the terminal frame may already be lost by the time the
    // client notices the socket is down, so the transcript is re-read too.
    const firstRound = invalidatedKeys(invalidate);
    expect(firstRound).toContain(JSON.stringify(chatKeys.pendingTasks(WS_ID)));
    expect(firstRound).toContain(JSON.stringify(chatKeys.pendingTask(SESSION)));
    expect(firstRound).toContain(JSON.stringify(chatKeys.messagesPage(SESSION)));
    expect(firstRound).toContain(JSON.stringify(chatKeys.taskMessages(TASK)));

    // Later ticks must not re-read the infinite transcript: invalidating it
    // refetches every loaded page, which is the opposite of a cheap fallback.
    invalidate.mockClear();
    act(() => {
      vi.advanceTimersByTime(CHAT_WATCHDOG_POLL_MS);
    });
    const tick = invalidatedKeys(invalidate);
    expect(tick).toContain(JSON.stringify(chatKeys.pendingTasks(WS_ID)));
    expect(tick).toContain(JSON.stringify(chatKeys.pendingTask(SESSION)));
    expect(tick).toContain(JSON.stringify(chatKeys.taskMessages(TASK)));
    expect(tick).not.toContain(JSON.stringify(chatKeys.messagesPage(SESSION)));
  });

  it("re-checks a running turn even when the socket reports itself healthy", () => {
    // The reported stuck state: the socket never notices it is half-open, so
    // `status` stays `connected` and only the pending state can be polled.
    const { invalidate, Wrapper } = setup();
    renderHook(
      () =>
        useChatRealtimeWatchdog({
          status: "connected",
          enabled: true,
          wsId: WS_ID,
          sessionId: SESSION,
          taskId: TASK,
        }),
      { wrapper: Wrapper },
    );

    const first = invalidatedKeys(invalidate);
    expect(first).toContain(JSON.stringify(chatKeys.pendingTask(SESSION)));
    expect(first).not.toContain(JSON.stringify(chatKeys.messagesPage(SESSION)));
    expect(first).not.toContain(JSON.stringify(chatKeys.taskMessages(TASK)));

    invalidate.mockClear();
    act(() => {
      vi.advanceTimersByTime(CHAT_PENDING_RECONCILE_MS);
    });
    expect(invalidatedKeys(invalidate)).toContain(
      JSON.stringify(chatKeys.pendingTask(SESSION)),
    );
  });

  it("keeps the running-turn cadence slower than the degraded one", () => {
    const { invalidate, Wrapper } = setup();
    renderHook(
      () =>
        useChatRealtimeWatchdog({
          status: "connected",
          enabled: true,
          wsId: WS_ID,
          sessionId: SESSION,
          taskId: TASK,
        }),
      { wrapper: Wrapper },
    );
    invalidate.mockClear();
    act(() => {
      vi.advanceTimersByTime(CHAT_WATCHDOG_POLL_MS);
    });
    expect(invalidate).not.toHaveBeenCalled();

    act(() => {
      vi.advanceTimersByTime(CHAT_PENDING_RECONCILE_MS - CHAT_WATCHDOG_POLL_MS);
    });
    expect(invalidatedKeys(invalidate)).toContain(
      JSON.stringify(chatKeys.pendingTask(SESSION)),
    );
  });

  it("does not treat switching conversations as a settled turn", () => {
    const { invalidate, Wrapper } = setup();
    const { rerender } = renderHook(
      ({ sessionId, taskId }: { sessionId: string; taskId: string | null }) =>
        useChatRealtimeWatchdog({
          status: "connected",
          enabled: true,
          wsId: WS_ID,
          sessionId,
          taskId,
        }),
      {
        wrapper: Wrapper,
        initialProps: { sessionId: SESSION, taskId: TASK as string | null },
      },
    );
    invalidate.mockClear();

    rerender({ sessionId: OTHER_SESSION, taskId: null });
    expect(invalidatedKeys(invalidate)).not.toContain(
      JSON.stringify(chatKeys.messagesPage(OTHER_SESSION)),
    );
  });

  it("catches up on a turn that settled while the surface was hidden", () => {
    const { invalidate, Wrapper } = setup();
    const { rerender } = renderHook(
      ({ enabled, taskId }: { enabled: boolean; taskId: string | null }) =>
        useChatRealtimeWatchdog({
          status: "connected",
          enabled,
          wsId: WS_ID,
          sessionId: SESSION,
          taskId,
        }),
      {
        wrapper: Wrapper,
        initialProps: { enabled: true, taskId: TASK as string | null },
      },
    );
    rerender({ enabled: false, taskId: TASK });
    invalidate.mockClear();

    // The turn ends while nobody is looking; re-reading the hidden transcript
    // would be exactly the traffic the gate exists to avoid.
    rerender({ enabled: false, taskId: null });
    expect(invalidatedKeys(invalidate)).not.toContain(
      JSON.stringify(chatKeys.messagesPage(SESSION)),
    );

    // Reopening the conversation must reveal the reply that never streamed.
    rerender({ enabled: true, taskId: null });
    expect(invalidatedKeys(invalidate)).toContain(
      JSON.stringify(chatKeys.messagesPage(SESSION)),
    );
  });

  it("re-reads the transcript when the turn settles", () => {
    const { invalidate, Wrapper } = setup();
    const { rerender } = renderHook(
      ({ taskId }: { taskId: string | null }) =>
        useChatRealtimeWatchdog({
          status: "connected",
          enabled: true,
          wsId: WS_ID,
          sessionId: SESSION,
          taskId,
        }),
      { wrapper: Wrapper, initialProps: { taskId: TASK as string | null } },
    );
    invalidate.mockClear();

    // The pending row vanished while the terminal frame never arrived.
    rerender({ taskId: null });
    expect(invalidatedKeys(invalidate)).toContain(
      JSON.stringify(chatKeys.messagesPage(SESSION)),
    );
  });

  it("stops reconciling once the socket is back", () => {
    const { invalidate, Wrapper } = setup();
    const { rerender } = renderHook(
      ({ status, taskId }: { status: WSStatus; taskId: string | null }) =>
        useChatRealtimeWatchdog({
          status,
          enabled: true,
          wsId: WS_ID,
          sessionId: SESSION,
          taskId,
        }),
      {
        wrapper: Wrapper,
        initialProps: { status: "reconnecting" as WSStatus, taskId: TASK as string | null },
      },
    );
    expect(invalidate).toHaveBeenCalled();

    rerender({ status: "connected", taskId: null });
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
        useChatRealtimeWatchdog({
          status,
          enabled: true,
          wsId: WS_ID,
          sessionId: SESSION,
          taskId: null,
        }),
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
          enabled: true,
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
          enabled: true,
          wsId: WS_ID,
          sessionId: SESSION,
          taskId: "client-side-pending",
        }),
      { wrapper: Wrapper },
    );
    expect(invalidatedKeys(invalidate).some((key) => key.includes("task-messages"))).toBe(
      false,
    );
  });

  it("does nothing when no conversation is open", () => {
    const { invalidate, Wrapper } = setup();
    renderHook(
      () =>
        useChatRealtimeWatchdog({
          status: "reconnecting",
          enabled: true,
          wsId: WS_ID,
          sessionId: null,
          taskId: null,
        }),
      { wrapper: Wrapper },
    );
    expect(invalidate).not.toHaveBeenCalled();
  });

  it("does nothing while the conversation is not on screen", () => {
    // The floating window stays mounted when collapsed and keeps its
    // `activeSessionId`; an outage must not poll a hidden transcript.
    for (const status of ["reconnecting", "connected"] as WSStatus[]) {
      const { invalidate, Wrapper } = setup();
      renderHook(
        () =>
          useChatRealtimeWatchdog({
            status,
            enabled: false,
            wsId: WS_ID,
            sessionId: SESSION,
            taskId: TASK,
          }),
        { wrapper: Wrapper },
      );
      act(() => {
        vi.advanceTimersByTime(CHAT_WATCHDOG_POLL_MS * 2);
      });
      expect(invalidate).not.toHaveBeenCalled();
    }
  });

  it("clears its timer on unmount", () => {
    const { invalidate, Wrapper } = setup();
    const { unmount } = renderHook(
      () =>
        useChatRealtimeWatchdog({
          status: "reconnecting",
          enabled: true,
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
