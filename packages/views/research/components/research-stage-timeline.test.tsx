import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ResearchStageTimeline } from "./research-stage-timeline";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        stage: {
          s1_plan: "S1 · Plan",
          s2_sources: "S2 · Explore",
          s3_validation: "S3 · Validate",
          s4_delivery: "S4 · Deliver",
        },
        timeline: {
          label: "Research stages",
          done: "Done",
          current: "Current",
          upcoming: "Upcoming",
          done_feedback: "Stage completed",
        },
      }),
  }),
}));

describe("ResearchStageTimeline", () => {
  it("renders the full stage sequence and highlights the current stage", () => {
    const { container } = render(
      <ResearchStageTimeline currentStage="s2_sources" sessionStatus="running" />,
    );
    expect(screen.getByLabelText("Research stages")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /S1 · Plan/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /S2 · Explore/i })).toHaveAttribute(
      "aria-current",
      "step",
    );
    expect(screen.getByRole("button", { name: /S4 · Deliver/i })).toBeDisabled();
    expect(container.querySelector('[data-stage-state="current"]')).toBeTruthy();
    expect(container.querySelectorAll('[data-stage-state="done"]').length).toBe(1);
    expect(container.querySelectorAll('[data-stage-state="upcoming"]').length).toBe(2);
  });

  it("invokes onSelectStage for done/current steps only", () => {
    const onSelectStage = vi.fn();
    render(
      <ResearchStageTimeline
        currentStage="s2_sources"
        sessionStatus="running"
        onSelectStage={onSelectStage}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /S1 · Plan/i }));
    fireEvent.click(screen.getByRole("button", { name: /S2 · Explore/i }));
    fireEvent.click(screen.getByRole("button", { name: /S3 · Validate/i }));
    expect(onSelectStage).toHaveBeenCalledWith("s1_plan");
    expect(onSelectStage).toHaveBeenCalledWith("s2_sources");
    expect(onSelectStage).toHaveBeenCalledTimes(2);
  });

  it("marks all stages done when session completed", () => {
    render(
      <ResearchStageTimeline
        currentStage="s4_delivery"
        sessionStatus="completed"
        onSelectStage={vi.fn()}
      />,
    );
    const s1 = screen.getByRole("button", { name: /S1 · Plan/i });
    expect(s1).not.toBeDisabled();
    expect(screen.getAllByText("Stage completed").length).toBe(4);
  });
});
