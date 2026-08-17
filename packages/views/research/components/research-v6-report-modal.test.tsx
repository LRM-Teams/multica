import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ResearchV6ReportModal } from "./research-v6-report-modal";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        d5: {
          report_sandbox: {
            title: "Research report",
            isolated_document: "Isolated report document",
            frame_title: "Research report document",
            loading: "Opening isolated report…",
            unavailable_title: "Interactive report unavailable",
            unavailable_body: "Read the verified plain-text version below.",
            refresh_capability: "Request a fresh link",
            plain_text_title: "Plain-text report",
          },
        },
      }),
  }),
}));

const report = {
  id: "report-1",
  title: "Agent collaboration modes",
  packageHash: "sha256:abc",
  sandboxUrl: "https://reports.example.test/report-1?signature=x",
  plainTextFallback: "Verified report text",
};

describe("ResearchV6ReportModal", () => {
  it("mounts the capability in the exact restricted iframe sandbox", async () => {
    render(
      <ResearchV6ReportModal
        open
        report={report}
        onOpenChange={() => {}}
      />,
    );

    const frame = await screen.findByTestId("research-v6-report-frame");
    expect(frame.getAttribute("sandbox")).toBe("allow-scripts");
    expect(frame.getAttribute("referrerpolicy")).toBe("no-referrer");
    expect(frame).not.toHaveAttribute("srcdoc");
    expect(frame).not.toHaveAttribute("allow");
  });

  it("fails closed for a same-origin capability and exposes plain text", async () => {
    render(
      <ResearchV6ReportModal
        open
        report={{ ...report, sandboxUrl: `${location.origin}/report-1` }}
        onOpenChange={() => {}}
      />,
    );

    await waitFor(() => {
      expect(screen.queryByTestId("research-v6-report-frame")).toBeNull();
    });
    expect(screen.getByText("Verified report text")).toBeTruthy();
  });
});
