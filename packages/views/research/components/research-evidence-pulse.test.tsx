// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { ResearchEvidencePulse } from "./research-evidence-pulse";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        m2: {
          evidence_loading: "Preparing your evidence map",
          evidence_partial: "Evidence is still filling in",
          evidence_ready: "Evidence is organized",
          evidence_empty_title: "No evidence to show yet",
          evidence_empty_body: "When results arrive, real fields will appear here",
          evidence_error: "Could not read evidence right now",
          evidence_permission: "You do not have permission to view research evidence",
          evidence_verification_unavailable: "核验信息未提供",
          evidence_expect_1: "Source use",
          evidence_expect_2: "Human / Agent split",
          evidence_expect_3: "Readiness",
        },
        session_page: { retry: "Retry" },
      }),
  }),
}));

afterEach(() => {
  cleanup();
});

describe("ResearchEvidencePulse (LRM-1329)", () => {
  it("keeps a persistent output live region across modes", () => {
    const { rerender } = render(
      <ResearchEvidencePulse mode="loading" revisionKey="r0" />,
    );
    const live = screen.getByTestId("research-evidence-pulse-live");
    expect(live.tagName).toBe("OUTPUT");
    expect(live.getAttribute("aria-live")).toBe("polite");
    expect(screen.getByTestId("research-evidence-pulse-expected")).toBeTruthy();

    rerender(<ResearchEvidencePulse mode="ready" revisionKey="r1" />);
    expect(screen.getByTestId("research-evidence-pulse-live")).toBe(live);
    expect(screen.getByTestId("research-evidence-pulse-status").textContent).toMatch(
      /organized/,
    );
  });

  it("never invents trust — verification slot is always 核验信息未提供", () => {
    for (const mode of ["loading", "partial", "ready", "empty"] as const) {
      const { unmount } = render(
        <ResearchEvidencePulse mode={mode} revisionKey={`k-${mode}`} />,
      );
      expect(
        screen.getByTestId("research-evidence-pulse-verification").textContent,
      ).toBe("核验信息未提供");
      unmount();
    }
  });

  it("uses a single alert for error and silences the polite live region", () => {
    render(
      <ResearchEvidencePulse mode="error" revisionKey="e" onRetry={() => {}} />,
    );
    const root = screen.getByTestId("research-evidence-pulse");
    expect(root.getAttribute("role")).toBe("alert");
    expect(screen.getByTestId("research-evidence-pulse-status").textContent).toMatch(
      /Could not read evidence/,
    );
    expect(screen.getByTestId("research-evidence-pulse-live").textContent).toBe(
      "",
    );
    expect(screen.getByRole("button", { name: "Retry" })).toBeTruthy();
    // Safe status only — no raw API summary required for the alert contract.
    expect(screen.queryByText("timeout")).toBeNull();
  });

  it("permission mode alerts without retry or leaked facts", () => {
    render(<ResearchEvidencePulse mode="permission" revisionKey="p" />);
    expect(screen.getByTestId("research-evidence-pulse").getAttribute("role")).toBe(
      "alert",
    );
    expect(screen.getByTestId("research-evidence-pulse-status").textContent).toMatch(
      /permission/i,
    );
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
  });
});
