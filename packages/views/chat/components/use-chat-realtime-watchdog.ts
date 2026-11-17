"use client";

import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { chatKeys, isTaskMessageTaskId } from "@multica/core/chat/queries";
import type { WSStatus } from "@multica/core/realtime";
import { CHAT_PENDING_RECONCILE_MS, CHAT_WATCHDOG_POLL_MS, isRealtimeDegraded } from "../lib/realtime-watchdog";

export interface ChatRealtimeWatchdogOptions {
  /** Live workspace socket state (`useWSStatus()`). */
  status: WSStatus;
  /**
   * Whether the conversation is on screen. A closed floating window stays
   * mounted (and keeps its `activeSessionId`), so without this gate an
   * outage would poll a transcript nobody is looking at — the same reason
   * `useChatController` takes `isActive`.
   */
  enabled: boolean;
  /** Workspace UUID, needed for the cross-session FAB aggregate. */
  wsId: string | null | undefined;
  /** Conversation currently on screen, if any. */
  sessionId: string | null | undefined;
  /** In-flight task for that conversation, if one is known. */
  taskId: string | null | undefined;
}

/**
 * Keeps the open conversation honest when realtime updates do not arrive.
 *
 * Every chat cache advances on WS events only (`chat:message`, `chat:done`,
 * `task:completed`, ...) and is `staleTime: Infinity` otherwise, so a lost
 * terminal frame leaves the turn running forever and reveals the answer only
 * after a manual refresh (GH #10805). Two situations get a fallback:
 *
 * - the socket is known to be down: re-read the conversation immediately, then
 *   keep polling the turn keys until it is back;
 * - a turn is still running: re-check its pending state on a slower cadence,
 *   because the socket may be half-open and never report the loss.
 *
 * Reconciliation is invalidation against the same query keys the WS handlers
 * use, so the server response stays authoritative — no optimistic writes and
 * no second source of truth.
 */
export function useChatRealtimeWatchdog({
  status,
  enabled,
  wsId,
  sessionId,
  taskId,
}: ChatRealtimeWatchdogOptions): void {
  const qc = useQueryClient();
  const degraded = isRealtimeDegraded(status);
  const hasTask = isTaskMessageTaskId(taskId);
  const watching = enabled && Boolean(sessionId) && (degraded || hasTask);
  const pollMs = degraded ? CHAT_WATCHDOG_POLL_MS : CHAT_PENDING_RECONCILE_MS;

  useEffect(() => {
    if (!watching || !sessionId) return;

    // One-off catch-up: the terminal frame may already have been lost before
    // the outage was noticed. `messagesPage` is an infinite query, so it is
    // read here and on settle, never on every tick.
    if (degraded) {
      qc.invalidateQueries({ queryKey: chatKeys.messagesPage(sessionId) });
    }

    const reconcile = () => {
      // FAB / thread-list "running" aggregate: only the server knows which of
      // the caller's tasks are still in flight.
      if (wsId) qc.invalidateQueries({ queryKey: chatKeys.pendingTasks(wsId) });
      // The turn's own pending state, and the live timeline (thinking / tool
      // steps) of the in-flight task.
      qc.invalidateQueries({ queryKey: chatKeys.pendingTask(sessionId) });
      if (degraded && isTaskMessageTaskId(taskId)) {
        qc.invalidateQueries({ queryKey: chatKeys.taskMessages(taskId) });
      }
    };

    // Reconcile on the leading edge, not one interval later.
    reconcile();
    // A loop rather than per-query `refetchInterval`: one tick invalidates
    // several keys, and the transcript is re-read outside the loop.
    const timer = setInterval(reconcile, pollMs);
    return () => clearInterval(timer);
  }, [degraded, hasTask, pollMs, qc, sessionId, taskId, watching, wsId]);

  // A turn that settles — finished, failed, or replaced by the next one —
  // changes the transcript, and nothing re-read it if the terminal frame was
  // the thing that got lost. Tracked per conversation so switching chats does
  // not read as a settle, and remembered while the surface is off-screen so
  // reopening it catches up.
  const settledFrom = useRef<{ sessionId: string; taskId: string | null } | null>(
    null,
  );
  const awaitingCatchUp = useRef(false);
  useEffect(() => {
    if (!sessionId) {
      settledFrom.current = null;
      awaitingCatchUp.current = false;
      return;
    }
    const current = hasTask ? taskId : null;
    const previous = settledFrom.current;
    settledFrom.current = { sessionId, taskId: current };

    if (!previous || previous.sessionId !== sessionId) {
      // Another conversation has its own transcript and its own history.
      awaitingCatchUp.current = false;
    } else if (previous.taskId !== current) {
      if (enabled) {
        qc.invalidateQueries({ queryKey: chatKeys.messagesPage(sessionId) });
      } else {
        awaitingCatchUp.current = true;
      }
    }

    if (!enabled || !awaitingCatchUp.current) return;
    awaitingCatchUp.current = false;
    qc.invalidateQueries({ queryKey: chatKeys.messagesPage(sessionId) });
  }, [enabled, hasTask, qc, sessionId, taskId]);
}
