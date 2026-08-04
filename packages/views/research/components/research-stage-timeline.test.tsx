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
 * 弱化层级只允许靠字号/字重/等宽/glyph，不允许靠 alpha 压文字。
 */
describe("ResearchStageTimeline text contrast (LRM-1252)", () => {
  it("keeps upcoming rows free of opacity-* and renders a solid muted label", () => {
    const { container } = render(
      <ResearchStageTimeline currentStage="s2_sources" sessionStatus="running" />,
    );
    const upcoming = [...container.querySelectorAll('[data-stage-state="upcoming"]')];
    expect(upcoming.length).toBe(2);
    for (const li of upcoming) {
      expect(li.className).not.toMatch(/\bopacity-\d/);
      const label = li.querySelector("span.font-mono");
      expect(label).not.toBeNull();
      expect(label!.className).toContain("text-muted-foreground");
      expect(label!.className).not.toMatch(/text-muted-foreground\/\d/);
    }
  });

  it("keeps the three step states visually distinct without alpha on the label", () => {
    const { container } = render(
      <ResearchStageTimeline currentStage="s2_sources" sessionStatus="running" />,
    );
    const labelOf = (state: string) =>
      container
        .querySelector(`[data-stage-state="${state}"] span.block.truncate`)
        ?.className ?? "";
    expect(labelOf("current")).toContain("font-semibold");
    expect(labelOf("current")).toContain("text-foreground");
    expect(labelOf("done")).toContain("font-mono");
    expect(labelOf("done")).toContain("text-foreground/75");
    expect(labelOf("upcoming")).toContain("font-mono");
    expect(labelOf("upcoming")).toContain("text-muted-foreground");
    expect(labelOf("upcoming")).not.toBe(labelOf("done"));
    // 连线与 glyph 视觉不变（装饰仍可用 alpha）
    expect(container.querySelector(".bg-border\\/80")).toBeTruthy();
    expect(container.querySelector('[aria-hidden].opacity-70')).toBeTruthy();
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
      if (!ownText) continue;
      if (el.closest('[aria-hidden="true"]')) continue;
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
