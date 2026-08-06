import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ResearchSessionInterruptBanner } from "./research-session-interrupt-banner";
import type { SessionInterrupt } from "../lib/session-interrupt";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        interrupt: {
          title: "Session interrupted",
          hint: "Retry wake, or reassign to an active member.",
          retry: "Retry",
          retrying: "Retrying…",
          retry_again: "Retry again",
          retry_failed_title: "Retry did not recover",
          retry_failed_hint: "Check daemon/runtime, or ask Ronaldo to reassign.",
          reasons: {
            runtime_offline: "Runtime offline",
            fleet_member_not_found: "Not a fleet member",
            unknown: "Wake failed",
          },
        },
      }),
  }),
}));

const interrupt: SessionInterrupt = {
  messageId: "wake-1",
  reason: "runtime_offline",
  headline: "目标 agent 的 runtime 可能离线",
  recoveryHint: "请确认 daemon 在线后重试。",
  createdAt: "2026-08-02T10:00:00Z",
};

describe("ResearchSessionInterruptBanner (LRM-823)", () => {
  it("renders readable reason + retry CTA", () => {
    const onRetry = vi.fn();
    render(
      <ResearchSessionInterruptBanner
        interrupt={interrupt}
        phase="idle"
        onRetry={onRetry}
      />,
    );
    expect(screen.getByTestId("research-session-interrupt-banner")).toBeTruthy();
    expect(screen.getByTestId("research-session-interrupt-reason").textContent).toContain(
      "Runtime offline",
    );
    expect(screen.getByTestId("research-session-interrupt-reason").textContent).toContain(
      "目标 agent 的 runtime 可能离线",
    );
    fireEvent.click(screen.getByTestId("research-session-interrupt-retry"));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("shows secondary feedback after retry failure", () => {
    render(
      <ResearchSessionInterruptBanner
        interrupt={interrupt}
        phase="retry_failed"
        onRetry={() => {}}
      />,
    );
    expect(screen.getByTestId("research-session-interrupt-banner").getAttribute("data-phase")).toBe(
      "retry_failed",
    );
    expect(screen.getByTestId("research-session-interrupt-secondary").textContent).toContain(
      "请确认 daemon 在线后重试",
    );
    expect(screen.getByText("Retry again")).toBeTruthy();
  });

  // LRM-1213 — the pending retry must stay a real focus target. A native
  // `disabled` on the control the user just activated drops focus to <body> in
  // Chromium and never returns it, so keyboard / screen reader users lose their
  // place and never hear the retry outcome (same root cause as LRM-1169).
  it("keeps retry focusable while pending: aria-disabled, not native disabled", () => {
    render(
      <ResearchSessionInterruptBanner
        interrupt={interrupt}
        phase="pending"
        onRetry={() => {}}
      />,
    );
    const retry = screen.getByTestId("research-session-interrupt-retry") as HTMLButtonElement;
    expect(retry.hasAttribute("disabled")).toBe(false);
    expect(retry.disabled).toBe(false);
    expect(retry.getAttribute("aria-disabled")).toBe("true");
    expect(screen.getByText("Retrying…")).toBeTruthy();
  });

  it("swallows repeat activation while pending, and retries again once re-enabled", () => {
    const onRetry = vi.fn();
    const { rerender } = render(
      <ResearchSessionInterruptBanner interrupt={interrupt} phase="idle" onRetry={onRetry} />,
    );
    const retry = screen.getByTestId("research-session-interrupt-retry");
    fireEvent.click(retry);
    expect(onRetry).toHaveBeenCalledTimes(1);

    rerender(
      <ResearchSessionInterruptBanner interrupt={interrupt} phase="pending" onRetry={onRetry} />,
    );
    fireEvent.click(screen.getByTestId("research-session-interrupt-retry"));
    fireEvent.keyDown(screen.getByTestId("research-session-interrupt-retry"), { key: "Enter" });
    expect(onRetry).toHaveBeenCalledTimes(1);

    rerender(
      <ResearchSessionInterruptBanner
        interrupt={interrupt}
        phase="retry_failed"
        onRetry={onRetry}
      />,
    );
    const again = screen.getByTestId("research-session-interrupt-retry");
    expect(again.getAttribute("aria-disabled")).toBeNull();
    fireEvent.click(again);
    expect(onRetry).toHaveBeenCalledTimes(2);
  });

  it("does not blur the pending retry control on re-render", () => {
    const { rerender } = render(
      <ResearchSessionInterruptBanner interrupt={interrupt} phase="idle" onRetry={() => {}} />,
    );
    const retry = screen.getByTestId("research-session-interrupt-retry");
    retry.focus();
    rerender(
      <ResearchSessionInterruptBanner interrupt={interrupt} phase="pending" onRetry={() => {}} />,
    );
    const pendingRetry = screen.getByTestId("research-session-interrupt-retry");
    expect(pendingRetry).toBe(retry);
    expect(document.activeElement).toBe(pendingRetry);
    // jsdom does not enforce the disabled-focus restriction, so the browser-level
    // proof lives on LRM-1213 (real Chromium probe). The contract asserted here is
    // that the DOM never carries `disabled` on a control the user can be focused on.
    expect((pendingRetry as HTMLButtonElement).hasAttribute("disabled")).toBe(false);
  });
});
