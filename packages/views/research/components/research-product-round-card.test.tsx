import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
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
  it("renders open detail with gaps, focus, and goal patch", () => {
    render(<ResearchProductRoundCardView card={card} currentGoal="旧目标" />);
    expect(screen.getByText("缺口 A")).toBeTruthy();
    expect(screen.getByText("收窄到成本对比")).toBeTruthy();
    expect(screen.getByText("补成本证据")).toBeTruthy();
    expect(screen.getByText("Agree")).toBeTruthy();
  });
});
