import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import type { ResearchProductRoundCard } from "@multica/core/types";
import { ResearchProductRoundCardView } from "./research-product-round-card";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown, vars?: Record<string, unknown>) => {
      const dict = {
        round: {
          round_n: `Round ${vars?.n ?? ""}`,
          subtitle: "Judgment",
          open_detail: "Open",
          confidence: "Confidence",
          empty_confidence: "None",
          gaps: "Gaps",
          gaps_empty: "Covered",
          budget_used: "Used",
          budget_remaining: "Left",
          budget_capped: "Capped",
          next_focus: "Focus",
          goal_patch: "Goal patch",
          goal_patch_hint: "Hint",
          goal_dialog_title: "Confirm goal",
          goal_confirm: "Confirm",
          goal_edit: "Edit",
          goal_edit_send: "Send edit",
          goal_reject: "Reject",
          agree: "Agree",
          reject_continue: "Reject continue",
          reject_stop: "Reject stop",
          auto_adopt_countdown: `Auto-adopt in ${vars?.s ?? ""}s`,
          auto_adopted: "Timed out — adopted",
          decision: {
            continue: "Continue",
            stop_enough: "Enough",
            stop_budget: "Budget",
          },
        },
      };
      return fn(dict);
    },
  }),
}));

const card: ResearchProductRoundCard = {
  id: "c1",
  session_id: "s1",
  round_number: 2,
  decision: "continue",
  coverage_gaps: ["缺口 A"],
  confidence_note: "证据偏弱",
  budget_used: 2,
  budget_remaining: 3,
  goal_patch_proposal: "收窄到成本对比",
  next_round_focus: "补成本证据",
  decided_by_agent_id: "agent-1",
  created_at: "2026-07-31T08:00:00Z",
};

describe("ResearchProductRoundCardView", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders open detail with gaps, focus, and goal patch", () => {
    render(
      <ResearchProductRoundCardView card={card} currentGoal="旧目标" autoAdoptSeconds={0} />,
    );
    expect(screen.getByText("缺口 A")).toBeTruthy();
    expect(screen.getByText("收窄到成本对比")).toBeTruthy();
    expect(screen.getByText("补成本证据")).toBeTruthy();
    expect(screen.getByText("Agree")).toBeTruthy();
  });

  it("auto-adopts Ronaldo decision after timeout without writing goal_patch", () => {
    const onAgree = vi.fn();
    const onConfirmGoalPatch = vi.fn();
    render(
      <ResearchProductRoundCardView
        card={card}
        onAgree={onAgree}
        onConfirmGoalPatch={onConfirmGoalPatch}
        autoAdoptSeconds={2}
      />,
    );
    expect(screen.getByText(/Auto-adopt in/)).toBeTruthy();
    act(() => {
      vi.advanceTimersByTime(2500);
    });
    expect(onAgree).toHaveBeenCalledTimes(1);
    expect(onConfirmGoalPatch).not.toHaveBeenCalled();
  });

  it("cancels auto-adopt when the user agrees early", () => {
    const onAgree = vi.fn();
    render(
      <ResearchProductRoundCardView card={card} onAgree={onAgree} autoAdoptSeconds={5} />,
    );
    fireEvent.click(screen.getByText("Agree"));
    expect(onAgree).toHaveBeenCalledTimes(1);
    act(() => {
      vi.advanceTimersByTime(6000);
    });
    expect(onAgree).toHaveBeenCalledTimes(1);
  });
});
