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
});
