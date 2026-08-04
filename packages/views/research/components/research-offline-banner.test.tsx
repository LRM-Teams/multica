import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ResearchOfflineBanner } from "./research-offline-banner";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        connectivity: {
          offline_title: "You are offline",
          offline_hint: "Research stays on screen — reconnect when the network returns.",
          reconnecting_title: "Reconnecting…",
          reconnecting_hint: "Refreshing research data.",
          reconnect_failed_title: "Reconnect failed",
          reconnect_failed_hint: "Check the network, then retry.",
          retry: "Retry",
          retrying: "Retrying…",
        },
      }),
  }),
}));

describe("ResearchOfflineBanner (LRM-833)", () => {
  it("shows offline copy without a retry CTA", () => {
    render(<ResearchOfflineBanner mode="offline" />);
    const banner = screen.getByTestId("research-offline-banner");
    expect(banner.getAttribute("data-mode")).toBe("offline");
    expect(banner.textContent).toContain("You are offline");
    expect(screen.queryByTestId("research-offline-banner-retry")).toBeNull();
  });

  it("shows failed mode with manual retry", () => {
    const onRetry = vi.fn();
    render(<ResearchOfflineBanner mode="failed" onRetry={onRetry} />);
    expect(screen.getByTestId("research-offline-banner").getAttribute("data-mode")).toBe(
      "failed",
    );
    fireEvent.click(screen.getByTestId("research-offline-banner-retry"));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  // LRM-1345 A-2 — contract change (not a relaxation): the reconnecting Retry must
  // stay focusable, so a native `disabled` is now forbidden. Previous assertion:
  //   expect((btn as HTMLButtonElement).disabled).toBe(true);
  it("marks retry aria-disabled while reconnecting without native disabled", () => {
    const onRetry = vi.fn();
    render(<ResearchOfflineBanner mode="reconnecting" onRetry={onRetry} />);
    const btn = screen.getByTestId("research-offline-banner-retry");
    expect((btn as HTMLButtonElement).disabled).toBe(false);
    expect(btn.hasAttribute("disabled")).toBe(false);
    expect(btn.getAttribute("aria-disabled")).toBe("true");
    expect(btn.textContent).toContain("Retrying");
    fireEvent.click(btn);
    expect(onRetry).not.toHaveBeenCalled();
  });

  // LRM-1345 A-1 — the shell keeps one element identity across modes, so the focused
  // Retry button survives the failed → reconnecting transition instead of being
  // unmounted (old code swapped <div role="alert"> ⇄ <output>, dropping focus to body).
  it("keeps the focused retry node across failed → reconnecting", () => {
    const { rerender } = render(
      <ResearchOfflineBanner mode="failed" onRetry={() => {}} />,
    );
    const before = screen.getByTestId("research-offline-banner-retry");
    before.focus();
    expect(document.activeElement).toBe(before);

    rerender(<ResearchOfflineBanner mode="reconnecting" onRetry={() => {}} />);
    const after = screen.getByTestId("research-offline-banner-retry");
    expect(after).toBe(before);
    expect(document.activeElement).toBe(before);
  });
});
