import type { WSStatus } from "@multica/core/realtime";

/**
 * How often a degraded realtime connection re-reads the open conversation.
 *
 * The chat caches are event-driven with `staleTime: Infinity` (see
 * `packages/core/chat/queries.ts`), so when a `chat:done` / `task:completed`
 * frame never reaches this client the pending state survives until the user
 * hits Stop or reloads (GH #10805). While the socket is known to be down we
 * reconcile against the authoritative endpoints instead: short enough that a
 * finished turn shows up while the user is still looking, long enough that a
 * long outage does not turn into a request storm.
 */
export const CHAT_WATCHDOG_POLL_MS = 10_000;

/**
 * How often a turn that is still running on a *healthy* socket is re-checked.
 *
 * This is the fallback for a socket that never reports itself as broken:
 * `WSClient` only reacts to `onclose`, so a half-open connection stops
 * delivering `chat:done` while the client keeps believing it is connected —
 * the reported "stuck until Stop" state. Slower than the degraded cadence
 * because the socket is not known to be down; the cost is only paid while a
 * turn is actually in flight.
 */
export const CHAT_PENDING_RECONCILE_MS = 20_000;

/**
 * True when the workspace socket exists but is not currently usable.
 *
 * `offline` is deliberately excluded: it means no connection is wanted
 * (signed out, or no workspace open), not that realtime is failing — there is
 * nothing to reconcile and no banner to show. `connecting` is the very first
 * attempt of a fresh client and usually settles within a round trip. Every
 * later attempt — including the first retry after the first attempt failed —
 * is `reconnecting`, so a client whose very first connection never succeeded is
 * reported as degraded too, which is correct: it is receiving no events.
 */
export function isRealtimeDegraded(status: WSStatus): boolean {
  return status === "reconnecting";
}
