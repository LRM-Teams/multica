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

  it("disables retry while pending", () => {
    render(
      <ResearchSessionInterruptBanner
        interrupt={interrupt}
        phase="pending"
        onRetry={() => {}}
      />,
    );
    expect(
      (screen.getByTestId("research-session-interrupt-retry") as HTMLButtonElement).disabled,
    ).toBe(true);
    expect(screen.getByText("Retrying…")).toBeTruthy();
  });
});
