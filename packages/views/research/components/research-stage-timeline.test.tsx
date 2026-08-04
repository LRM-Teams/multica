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

/**
 * LRM-1252 — upcoming 阶段名曾是 `opacity-75` × `text-muted-foreground/80`
 * → 有效 alpha 0.60，亮色实测 ≈2.6:1（WCAG AA 4.5 FAIL）。
 * LRM-1291 — 能量轨不得以低对比 alpha 或纯颜色承担状态语义。
 */
describe("ResearchStageTimeline energy rail (LRM-1252, LRM-1291)", () => {
  it("renders one semantic four-segment rail with redundant state glyphs", () => {
    const { container } = render(
      <ResearchStageTimeline currentStage="s2_sources" sessionStatus="running" />,
    );
    const rail = container.querySelector('[data-testid="research-stage-energy-rail"]');
    expect(rail).toBeTruthy();
    expect(rail?.querySelectorAll("[data-stage-energy-segment]")).toHaveLength(4);
    expect(rail?.querySelectorAll('[data-stage-energy-state="done"]')).toHaveLength(1);
    expect(rail?.querySelectorAll('[data-stage-energy-state="current"]')).toHaveLength(1);
    expect(rail?.querySelectorAll('[data-stage-energy-state="upcoming"]')).toHaveLength(2);
    expect(rail?.querySelector('[data-stage-glyph="done"]')).toBeTruthy();
    expect(rail?.querySelector('[data-stage-glyph="current"]')).toBeTruthy();
    expect(rail?.querySelectorAll('[data-stage-glyph="upcoming"]')).toHaveLength(2);
  });

  it("animates only the current segment and disables that animation for reduced motion", () => {
    const { container } = render(
      <ResearchStageTimeline currentStage="s2_sources" sessionStatus="running" />,
    );
    const flow = container.querySelectorAll('[data-stage-flow="current"]');
    expect(flow).toHaveLength(1);
    expect(flow[0]!.className).toContain("animate-pulse");
    expect(flow[0]!.className).toContain("motion-reduce:animate-none");
    expect(container.querySelectorAll('[data-stage-flow="none"]')).toHaveLength(3);
  });

  it("keeps narrow visual labels compact without losing localized full accessible names", () => {
    render(<ResearchStageTimeline currentStage="s2_sources" sessionStatus="running" />);
    const current = screen.getByRole("button", { name: /S2 · Explore.*Current/i });
    expect(current).toHaveAttribute("aria-current", "step");
    expect(current.querySelector("[data-stage-short-label]")).toHaveTextContent("S2");
  });

  it("keeps upcoming rows free of opacity-* and renders a solid muted label", () => {
    const { container } = render(
      <ResearchStageTimeline currentStage="s2_sources" sessionStatus="running" />,
    );
    const upcoming = [...container.querySelectorAll('[data-stage-state="upcoming"]')];
    expect(upcoming.length).toBe(2);
    for (const li of upcoming) {
      expect(li.className).not.toMatch(/\bopacity-\d/);
      const label = li.querySelector("[data-stage-label]");
      expect(label).not.toBeNull();
      expect(label!.className).toContain("text-muted-foreground");
      expect(label!.className).not.toMatch(/text-muted-foreground\/\d/);
    }
  });

  it("keeps the three step states visually distinct without alpha on their labels", () => {
    const { container } = render(
      <ResearchStageTimeline currentStage="s2_sources" sessionStatus="running" />,
    );
    const labelOf = (state: string) =>
      container.querySelector(`[data-stage-state="${state}"] [data-stage-label]`)?.className ?? "";
    expect(labelOf("current")).toContain("font-medium");
    expect(labelOf("current")).toContain("text-foreground");
    expect(labelOf("done")).toContain("text-foreground");
    expect(labelOf("upcoming")).toContain("text-muted-foreground");
    expect(labelOf("upcoming")).not.toBe(labelOf("done"));
  });

  it("guard: no text-bearing node uses alpha-dimmed muted text or an opacity ancestor", () => {
    const { container } = render(
      <ResearchStageTimeline currentStage="s2_sources" sessionStatus="running" />,
    );
    const offenders: string[] = [];
    for (const el of container.querySelectorAll<HTMLElement>("*")) {
      const ownText = [...el.childNodes]
        .filter((n) => n.nodeType === 3)
        .map((n) => n.textContent ?? "")
        .join("")
        .trim();
      if (!ownText || el.closest('[aria-hidden="true"]')) continue;
      const chain: string[] = [];
      let cur: HTMLElement | null = el;
      while (cur && cur !== container) {
        chain.push(cur.className || "");
        cur = cur.parentElement;
      }
      const classes = chain.join(" ");
      if (/text-muted-foreground\/[5-8]\d/.test(classes)) {
        offenders.push(`${ownText}: dimmed muted text (${classes})`);
      }
      if (/\bopacity-\d/.test(classes)) {
        offenders.push(`${ownText}: opacity ancestor (${classes})`);
      }
    }
    expect(offenders).toEqual([]);
  });
});
