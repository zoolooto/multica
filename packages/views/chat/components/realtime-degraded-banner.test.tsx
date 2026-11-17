// @vitest-environment jsdom

import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderWithI18n } from "../../test/i18n";
import enChat from "../../locales/en/chat.json";
import zhHansChat from "../../locales/zh-Hans/chat.json";
import { RealtimeDegradedBanner } from "./realtime-degraded-banner";

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
