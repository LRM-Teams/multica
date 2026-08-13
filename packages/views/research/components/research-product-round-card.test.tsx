import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
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

  it("auto-adopts Ronaldo decision after timeout without writing goal_patch", async () => {
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
    await act(async () => {
      vi.advanceTimersByTime(2500);
      await Promise.resolve();
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

  it("keeps the round open and suppresses rapid repeats until agree commits", async () => {
    let resolveAgree: (() => void) | undefined;
    const onAgree = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveAgree = resolve;
        }),
    );
    render(
      <ResearchProductRoundCardView
        card={card}
        onAgree={onAgree}
        autoAdoptSeconds={0}
      />,
    );

    const agree = screen.getByTestId("research-round-agree");
    fireEvent.click(agree);
    fireEvent.click(agree);
    expect(onAgree).toHaveBeenCalledTimes(1);
    expect(screen.getByText("缺口 A")).toBeTruthy();

    await act(async () => {
      resolveAgree?.();
      await Promise.resolve();
    });
    expect(screen.queryByText("缺口 A")).toBeNull();
  });

  it("preserves the round and goal draft when a decision commit fails", async () => {
    const onAgree = vi.fn().mockRejectedValue(new Error("commit failed"));
    const onConfirmGoalPatch = vi
      .fn()
      .mockRejectedValue(new Error("goal commit failed"));
    render(
      <ResearchProductRoundCardView
        card={card}
        onAgree={onAgree}
        onConfirmGoalPatch={onConfirmGoalPatch}
        autoAdoptSeconds={0}
      />,
    );

    fireEvent.click(screen.getByTestId("research-round-agree"));
    await act(async () => Promise.resolve());
    expect(screen.getByText("缺口 A")).toBeTruthy();

    fireEvent.click(screen.getByTestId("research-round-goal-edit"));
    const draft = screen.getByRole("textbox");
    fireEvent.change(draft, { target: { value: "保留修改后的目标" } });
    fireEvent.click(screen.getByTestId("research-round-goal-confirm-send"));
    await act(async () => Promise.resolve());
    expect(onConfirmGoalPatch).toHaveBeenCalledWith("保留修改后的目标");
    expect(screen.getByRole("textbox")).toHaveValue("保留修改后的目标");
  });

  it("does not permanently lock a round when automatic adoption fails", async () => {
    const onAgree = vi.fn().mockRejectedValue(new Error("offline"));
    render(
      <ResearchProductRoundCardView
        card={card}
        onAgree={onAgree}
        autoAdoptSeconds={2}
      />,
    );

    await act(async () => {
      vi.advanceTimersByTime(2500);
      await Promise.resolve();
    });
    expect(onAgree).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId("research-round-agree")).not.toBeDisabled();
    expect(screen.queryByText("Timed out — adopted")).toBeNull();
  });

  it("LRM-1239: pending uses aria-disabled (not native disabled) and guards handlers", () => {
    const onAgree = vi.fn();
    const onRejectContinue = vi.fn();
    const onRejectGoalPatch = vi.fn();
    render(
      <ResearchProductRoundCardView
        card={card}
        onAgree={onAgree}
        onRejectContinue={onRejectContinue}
        onRejectGoalPatch={onRejectGoalPatch}
        pending
        autoAdoptSeconds={0}
      />,
    );

    const agree = screen.getByTestId("research-round-agree") as HTMLButtonElement;
    const reject = screen.getByTestId(
      "research-round-reject-continue",
    ) as HTMLButtonElement;
    const goalReject = screen.getByTestId(
      "research-round-goal-reject",
    ) as HTMLButtonElement;

    for (const el of [agree, reject, goalReject]) {
      expect(el.hasAttribute("disabled")).toBe(false);
      expect(el.disabled).toBe(false);
      expect(el.getAttribute("aria-disabled")).toBe("true");
    }

    agree.focus();
    expect(document.activeElement).toBe(agree);
    fireEvent.click(agree);
    fireEvent.keyDown(agree, { key: "Enter" });
    expect(document.activeElement).toBe(agree);

    fireEvent.click(reject);
    fireEvent.click(goalReject);
    expect(onAgree).not.toHaveBeenCalled();
    expect(onRejectContinue).not.toHaveBeenCalled();
    expect(onRejectGoalPatch).not.toHaveBeenCalled();
  });

  it("LRM-1239: auto-adopt / countdown announcements use native <output>", async () => {
    render(<ResearchProductRoundCardView card={card} autoAdoptSeconds={5} />);
    const countdown = screen.getByText(/Auto-adopt in/);
    expect(countdown.tagName).toBe("OUTPUT");

    await act(async () => {
      vi.advanceTimersByTime(5500);
      await Promise.resolve();
    });
    const adopted = screen.getByText("Timed out — adopted");
    expect(adopted.tagName).toBe("OUTPUT");
  });
});

/**
 * LRM-1339 — 同 LRM-1252 缺陷类的另一个文件面。
 *
 * summary 行的小字曾是 `text-[11px] opacity-80`（摘要）与 `text-[10px] opacity-70`
 * （倒计时 / 预算），而这些 span 的前景色是 `decisionTone` 给的语义色
 * （`text-brand` / `text-success` / `text-warning` / `text-muted-foreground`），
 * 且落在同色低透明度 wash（`bg-brand/5` 等）上 —— alpha 一乘就掉到 WCAG AA 4.5:1 以下。
 * `goal_patch` 的旧目标行 `text-muted-foreground × opacity-70` 与 LRM-1252 那条
 * 实测 2.6:1 完全同型。
 *
 * 层级只允许靠字号 / 字重 / 等宽 / `line-through` 表达，不允许靠 alpha 压文字。
 * 真 `disabled` 态的 `opacity-50` 属 WCAG 1.4.3 豁免，故白名单保留。
 *
 * jsdom 不解析 token、也不合成祖先 opacity，所以单测只能守类名；真实 WCAG 数值
 * 由 `scripts/lrm1339-gate-shots.mjs` 在真 Chromium 里活 DOM 实测。
 */
describe("ResearchProductRoundCardView text contrast (LRM-1339)", () => {
  const decisions: ResearchProductRoundCard["decision"][] = [
    "continue",
    "stop_enough",
    "stop_budget",
  ];

  it("keeps every summary-row text node free of opacity-*", () => {
    for (const decision of decisions) {
      const { container, unmount } = render(
        <ResearchProductRoundCardView
          card={{ ...card, decision }}
          compact
          autoAdoptSeconds={5}
        />,
      );
      const summary = container.querySelector('[data-testid="research-round-summary"]');
      expect(summary).not.toBeNull();
      expect(summary!.getAttribute("data-round-decision")).toBe(decision);
      expect(summary!.className).not.toMatch(/\bopacity-\d/);
      for (const span of summary!.querySelectorAll("span")) {
        expect(span.className).not.toMatch(/\bopacity-\d/);
        expect(span.className).not.toMatch(/text-[a-z-]+\/\d/);
      }
      unmount();
    }
  });

  it("keeps summary hierarchy via weight/size/mono instead of alpha", () => {
    const { container } = render(
      <ResearchProductRoundCardView card={card} compact autoAdoptSeconds={5} />,
    );
    const cls = (id: string) =>
      container.querySelector(`[data-testid="${id}"]`)?.className ?? "";

    expect(cls("research-round-summary-note")).toContain("text-[11px]");
    expect(cls("research-round-summary-note")).toContain("font-normal");
    expect(cls("research-round-summary-note")).toContain("truncate");

    for (const id of [
      "research-round-summary-countdown",
      "research-round-summary-budget",
    ]) {
      expect(cls(id)).toContain("font-mono");
      expect(cls(id)).toContain("text-[10px]");
      expect(cls(id)).toContain("tabular-nums");
    }
  });

  it("keeps goal_patch old-goal + budget-capped notes solid (line-through / font-normal only)", () => {
    const { container } = render(
      <ResearchProductRoundCardView
        card={{ ...card, decision: "stop_budget" }}
        currentGoal="旧目标"
        autoAdoptSeconds={0}
      />,
    );
    // DialogContent 走 portal，不在 render container 里，故查 document。
    expect(container).toBeTruthy();
    const oldGoal = document.querySelector('[data-testid="research-round-goal-current"]');
    expect(oldGoal).not.toBeNull();
    expect(oldGoal!.className).toContain("text-muted-foreground");
    expect(oldGoal!.className).toContain("line-through");
    expect(oldGoal!.className).not.toMatch(/\bopacity-\d/);
    expect(oldGoal!.className).not.toMatch(/text-muted-foreground\/\d/);

    const capped = document.querySelector(
      '[data-testid="research-round-budget-capped"]',
    );
    expect(capped).not.toBeNull();
    expect(capped!.className).toContain("font-normal");
    expect(capped!.className).not.toMatch(/\bopacity-\d/);
  });

  it("regression guard: only disabled/pending affordances may carry opacity-*", () => {
    const source = readFileSync(
      join(dirname(fileURLToPath(import.meta.url)), "research-product-round-card.tsx"),
      "utf8",
    );
    // 文字色禁 alpha 变体（`text-*-foreground/70` 之类）。
    expect(source).not.toMatch(/text-[a-z-]*foreground\/[5-8]\d/);
    // 每一处残留 opacity-* 必须由真 disabled/pending 语义把守。
    for (const line of source.split("\n")) {
      const hit = line.match(/\bopacity-\d+/);
      if (!hit) continue;
      expect(line).toMatch(/cursor-not-allowed|disabled|pending|gateBusy|isPending/);
    }
  });
});
