"use client";

import { WifiOff } from "@multica/ui/icons";
import { cn } from "@multica/ui/lib/utils";
import { CHAT_COLUMN, CHAT_GUTTER } from "./chat-column";
import { useT } from "../../i18n";

/**
 * Inline notice for a workspace socket that is down.
 *
 * Chat caches advance on WS events only, so the surface looks identical
 * whether a turn is running or the connection dropped. The notice names the
 * cause; `useChatRealtimeWatchdog` fixes the state behind it.
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
