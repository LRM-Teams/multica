import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ResearchCompletionCard } from "./research-completion-card";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        completion_guide: {
          done_title: "Research complete",
          done_body: "Report is ready.",
          failed_title: "Research unfinished",
          failed_body: "This run did not finish.",
          view_report: "View report",
          new_research: "New research",
          home: "Home",
          dismiss: "Dismiss",
          done_badge: "Done",
          failed_badge: "Failed",
        },
      }),
  }),
}));

describe("ResearchCompletionCard (LRM-832)", () => {
  it("renders done copy and wires primary actions", () => {
    const onViewReport = vi.fn();
    const onNewResearch = vi.fn();
    const onHome = vi.fn();
    const onDismiss = vi.fn();
    render(
      <ResearchCompletionCard
        kind="done"
        onViewReport={onViewReport}
        onNewResearch={onNewResearch}
        onHome={onHome}
        onDismiss={onDismiss}
      />,
    );
    const card = screen.getByTestId("research-completion-card");
    expect(card).toHaveAttribute("data-completion-kind", "done");
    expect(screen.getByText("Research complete")).toBeTruthy();
    fireEvent.click(screen.getByText("View report"));
    fireEvent.click(screen.getByText("New research"));
    fireEvent.click(screen.getByText("Home"));
    expect(onViewReport).toHaveBeenCalledTimes(1);
    expect(onNewResearch).toHaveBeenCalledTimes(1);
    expect(onHome).toHaveBeenCalledTimes(1);
  });

  it("uses distinct failed copy", () => {
    render(
      <ResearchCompletionCard
        kind="failed"
        onViewReport={() => {}}
        onNewResearch={() => {}}
        onHome={() => {}}
        onDismiss={() => {}}
      />,
    );
    expect(screen.getByTestId("research-completion-card")).toHaveAttribute(
      "data-completion-kind",
      "failed",
    );
    expect(screen.getByText("Research unfinished")).toBeTruthy();
    expect(screen.getByText("This run did not finish.")).toBeTruthy();
    expect(screen.queryByText("Research complete")).toBeNull();
  });

  // LRM-1244 — no fullscreen dismiss scrim (same root cause as LRM-1243 / #2082).
  // aria-hidden + tabIndex=-1 is still focusable; native dialog focusing steps
  // parked initial focus on the invisible layer. Delete the node; gutter click
  // on the dialog box itself dismisses.
  it("exposes no dismiss scrim; gutter click on dialog dismisses", () => {
    const onDismiss = vi.fn();
    render(
      <ResearchCompletionCard
        kind="done"
        onViewReport={() => {}}
        onNewResearch={() => {}}
        onHome={() => {}}
        onDismiss={onDismiss}
      />,
    );
    const dialog = screen.getByTestId("research-completion-card");
    expect(dialog.tagName).toBe("DIALOG");
    expect(dialog.querySelector("button.absolute.inset-0")).toBeNull();
    expect(
      dialog.querySelector('[aria-hidden="true"][tabindex="-1"]'),
    ).toBeNull();

    const namedDismiss = screen.getAllByRole("button", { name: "Dismiss" });
    expect(namedDismiss).toHaveLength(1);

    fireEvent.click(dialog);
    expect(onDismiss).toHaveBeenCalledTimes(1);

    onDismiss.mockClear();
    const card = dialog.querySelector("[role='document']");
    expect(card).toBeTruthy();
    fireEvent.click(card!);
    expect(onDismiss).not.toHaveBeenCalled();
  });
});
