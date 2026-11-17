// @vitest-environment jsdom

import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { CHAT_WATCHDOG_POLL_MS, isRealtimeDegraded } from "../lib/realtime-watchdog";
import { renderWithI18n } from "../../test/i18n";
import enChat from "../../locales/en/chat.json";
import zhHansChat from "../../locales/zh-Hans/chat.json";
import { RealtimeDegradedBanner } from "./realtime-degraded-banner";

describe("isRealtimeDegraded", () => {
  it("flags a socket that was connected and lost", () => {
    expect(isRealtimeDegraded("reconnecting")).toBe(true);
  });

  it("does not flag states where realtime is expected to be absent", () => {
    // `offline` = signed out / no workspace, `connecting` = first attempt.
    expect(isRealtimeDegraded("offline")).toBe(false);
    expect(isRealtimeDegraded("connecting")).toBe(false);
    expect(isRealtimeDegraded("connected")).toBe(false);
  });

  it("polls on a fixed, human-scale interval", () => {
    expect(CHAT_WATCHDOG_POLL_MS).toBeGreaterThanOrEqual(5_000);
    expect(CHAT_WATCHDOG_POLL_MS).toBeLessThanOrEqual(60_000);
  });
});

describe("RealtimeDegradedBanner", () => {
  it("renders the English notice", () => {
    renderWithI18n(<RealtimeDegradedBanner />);
    expect(screen.getByRole("status")).toHaveTextContent(
      enChat.realtime_banner.degraded,
    );
  });

  it("is translated rather than falling back to English", () => {
    renderWithI18n(<RealtimeDegradedBanner />, { locale: "zh-Hans" });
    expect(screen.getByRole("status")).toHaveTextContent(
      zhHansChat.realtime_banner.degraded,
    );
    expect(screen.getByRole("status")).not.toHaveTextContent(
      enChat.realtime_banner.degraded,
    );
  });

  it("announces itself politely to assistive tech", () => {
    renderWithI18n(<RealtimeDegradedBanner />);
    expect(screen.getByRole("status")).toHaveAttribute("aria-live", "polite");
  });
});
