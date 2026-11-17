"use client";

import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { chatKeys, isTaskMessageTaskId } from "@multica/core/chat/queries";
import type { WSStatus } from "@multica/core/realtime";
import { CHAT_WATCHDOG_POLL_MS, isRealtimeDegraded } from "../lib/realtime-watchdog";

export interface ChatRealtimeWatchdogOptions {
  /** Live workspace socket state (`useWSStatus()`). */
  status: WSStatus;
  /** Workspace UUID, needed for the cross-session FAB aggregate. */
  wsId: string | null | undefined;
  /** Conversation currently on screen, if any. */
  sessionId: string | null | undefined;
  /** In-flight task for that conversation, if one is known. */
  taskId: string | null | undefined;
}

/**
 * Keeps the open conversation honest while the realtime socket is down.
 *
 * Every chat cache is advanced by WS events only (`chat:message`,
 * `chat:done`, `task:completed`, ...) and is `staleTime: Infinity` otherwise,
 * so a client that believes it is connected but stops receiving frames shows a
 * turn as running forever and reveals the answer only after a manual refresh —
 * GH #10805's "长时间卡住，点中止后一次性全部显示".
 *
 * A *detected* disconnect already self-heals: `WSClient` reconnects and
 * `invalidateWorkspaceScopedQueries` re-pulls the chat caches. This hook covers
 * the window in which the client knows the socket is down but nothing has been
 * refetched yet, and keeps reconciling until it is back.
 *
 * Reconciliation is invalidation against the same query keys the WS handlers
 * use, so the server response stays authoritative — no optimistic writes and
 * no second source of truth.
 */
export function useChatRealtimeWatchdog({
  status,
  wsId,
  sessionId,
  taskId,
}: ChatRealtimeWatchdogOptions): void {
  const qc = useQueryClient();
  const degraded = isRealtimeDegraded(status);

  useEffect(() => {
    if (!degraded || !sessionId) return;

    const reconcile = () => {
      // FAB / thread-list "running" aggregate: only the server knows which
      // of the caller's tasks are still in flight.
      if (wsId) qc.invalidateQueries({ queryKey: chatKeys.pendingTasks(wsId) });
      // The turn's own pending state and transcript.
      qc.invalidateQueries({ queryKey: chatKeys.pendingTask(sessionId) });
      qc.invalidateQueries({ queryKey: chatKeys.messagesPage(sessionId) });
      qc.invalidateQueries({ queryKey: chatKeys.messages(sessionId) });
      // Live timeline (thinking / tool steps) of the in-flight task.
      if (isTaskMessageTaskId(taskId)) {
        qc.invalidateQueries({ queryKey: chatKeys.taskMessages(taskId) });
      }
    };

    // Reconcile as soon as the degradation is observed, not one interval
    // later: the missed terminal frame may already have happened.
    reconcile();
    const timer = setInterval(reconcile, CHAT_WATCHDOG_POLL_MS);
    return () => clearInterval(timer);
  }, [degraded, qc, sessionId, taskId, wsId]);
}
