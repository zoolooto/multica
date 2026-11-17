"use client";

import { WifiOff } from "@multica/ui/icons";
import { cn } from "@multica/ui/lib/utils";
import { CHAT_COLUMN, CHAT_GUTTER } from "./chat-column";
import { useT } from "../../i18n";

/**
 * Inline notice for a workspace socket that is down.
 *
 * Without it the chat surface looks exactly like a healthy one while every
 * cache is stale (`staleTime: Infinity` + event-driven updates), which is what
 * made GH #10805 undiagnosable from the UI: the run looked frozen, and only a
 * manual Stop — which forces a refetch — revealed the reply. The banner is
 * paired with `useChatRealtimeWatchdog`, so the state also stops being wrong;
 * this part exists so the user knows why the lag happened.
 */
export function RealtimeDegradedBanner() {
  const { t } = useT("chat");
  return (
    <div className={cn(CHAT_GUTTER, "mb-1.5")}>
      <div
        role="status"
        aria-live="polite"
        className={cn(
          CHAT_COLUMN,
          "flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-caption bg-amber-50 dark:bg-amber-950/40 text-amber-900 dark:text-amber-200 ring-1 ring-amber-200/60 dark:ring-amber-900/40",
        )}
      >
        <WifiOff className="size-3.5 shrink-0" />
        <span className="truncate">{t(($) => $.realtime_banner.degraded)}</span>
      </div>
    </div>
  );
}
