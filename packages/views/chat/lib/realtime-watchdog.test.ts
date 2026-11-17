// @vitest-environment node

import { describe, expect, it } from "vitest";
import type { WSStatus } from "@multica/core/realtime";
import { isRealtimeDegraded } from "./realtime-watchdog";

describe("isRealtimeDegraded", () => {
  it("flags a socket the client had to reconnect", () => {
    expect(isRealtimeDegraded("reconnecting")).toBe(true);
  });

  it("leaves states where realtime is not expected to be live alone", () => {
    // `offline` = signed out / no workspace, `connecting` = first attempt.
    const healthy: WSStatus[] = ["connected", "connecting", "offline"];
    for (const status of healthy) {
      expect(isRealtimeDegraded(status)).toBe(false);
    }
  });
});
