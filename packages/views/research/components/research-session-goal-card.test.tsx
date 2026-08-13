import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { ResearchSessionGoalCard } from "./research-session-goal-card";

const mobileState = { isMobile: false };

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => mobileState.isMobile,
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        goal_card: {
          label: "GOAL",
          final_title: "最终目标",
          card_title: "查看最终目标",
          icon_title: "最终目标",
          empty_summary: "尚未收敛",
          loading_summary: "更新中…",
          error_summary: "目标加载失败",
          pending_summary: "待确认换题…",
          empty_body: "目标尚未收敛。",
          loading_body: "正在根据你的发言更新目标…",
          error_body: "无法加载目标，可重试。",
          optimized_note: "目标已根据你的话优化",
          previous_label: "上一版",
          substantive_label: "待确认换题",
          close: "关闭",
          retry: "重试",
          confirm_substantive: "确认换题（substantive）",
          confirming_substantive: "正在确认换题…",
          collapse_icon: "收起为图标",
          expand_card: "展开卡片",
        },
        d5: {
          goal_panel: {
            title: "目标版本",
            version: "VERSION",
            round: "ROUND",
            round_with_budget: "ROUND / BUDGET",
            current: "当前",
            impact: "影响",
          },
        },
      }),
  }),
}));

describe("ResearchSessionGoalCard (LRM-1008 / LRM-1010)", () => {
  beforeEach(() => {
    window.localStorage.clear();
    mobileState.isMobile = false;
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders compact Goal label + truncated summary + status dot", () => {
    render(
      <ResearchSessionGoalCard
        sessionId="s1"
        goal="人员编制、Linux 环境与能否落地页游的可行性调研"
      />,
    );
    const card = screen.getByTestId("research-session-goal-card");
    expect(card.getAttribute("data-state")).toBe("ready");
    expect(card.textContent).toContain("GOAL");
    expect(card.textContent).toMatch(/人员编制/);
    expect(screen.getByTestId("research-session-goal-dot")).toBeTruthy();
  });

  it("shows canonical goal version and product-round budget in the command bar", () => {
    render(
      <ResearchSessionGoalCard
        sessionId="s1"
        goal="目标"
        goalVersion={3}
        productRound={2}
        productRoundBudget={5}
      />,
    );
    expect(screen.getByTestId("research-session-goal-card")).toHaveTextContent(
      "GOAL · VERSION · ROUND / BUDGET",
    );
  });

  it("LRM-1010: uses brand semantic tokens (no hardcoded violet)", () => {
    const { container } = render(
      <ResearchSessionGoalCard sessionId="s1" goal="品牌色目标" />,
    );
    const card = screen.getByTestId("research-session-goal-card");
    expect(card.className).toContain("bg-brand/10");
    expect(card.className).toContain("border-brand/30");
    expect(card.className).not.toContain("6b5cff");
    expect(card.className).not.toContain("research-goal");
    expect(card.className).not.toContain("violet");
    expect(container.querySelector(".text-brand")).toBeTruthy();
    expect(screen.getByTestId("research-session-goal-dot").className).toContain(
      "bg-muted-foreground",
    );
  });

  it("LRM-1010: narrow viewport forces icon trigger (no toolbar overflow)", () => {
    mobileState.isMobile = true;
    render(
      <ResearchSessionGoalCard sessionId="s1" goal="窄屏也不挤爆顶栏" />,
    );
    expect(screen.getByTestId("research-session-goal-icon")).toBeTruthy();
    expect(screen.queryByTestId("research-session-goal-card")).toBeNull();
    fireEvent.click(screen.getByTestId("research-session-goal-icon"));
    expect(screen.getByTestId("research-session-goal-popover")).toBeTruthy();
    expect(screen.queryByTestId("research-session-goal-toggle-collapse")).toBeNull();
  });

  it("opens dialog with full text; Esc/close works via dialog", () => {
    render(
      <ResearchSessionGoalCard sessionId="s1" goal="完整最终目标文本内容" />,
    );
    fireEvent.click(screen.getByTestId("research-session-goal-card"));
    expect(screen.getByTestId("research-session-goal-popover")).toBeTruthy();
    expect(screen.getByTestId("research-session-goal-full").textContent).toBe(
      "完整最终目标文本内容",
    );
    fireEvent.click(screen.getByTestId("research-session-goal-close"));
  });

  it("pulses on user goal change and keeps previous version", () => {
    const { rerender } = render(
      <ResearchSessionGoalCard sessionId="s1" goal="旧目标" />,
    );
    rerender(<ResearchSessionGoalCard sessionId="s1" goal="新目标全文" />);
    expect(screen.getByTestId("research-session-goal-card").getAttribute("data-state")).toBe(
      "updated",
    );
    fireEvent.click(screen.getByTestId("research-session-goal-card"));
    expect(screen.getByTestId("research-session-goal-previous").textContent).toContain(
      "旧目标",
    );
    act(() => {
      vi.advanceTimersByTime(3000);
    });
    expect(screen.getByTestId("research-session-goal-card").getAttribute("data-state")).toBe(
      "ready",
    );
  });

  it("shows pending substantive state and confirm CTA", () => {
    const onConfirm = vi.fn();
    render(
      <ResearchSessionGoalCard
        sessionId="s1"
        goal="当前目标"
        pendingSubstantive="换题提案"
        onConfirmSubstantive={onConfirm}
      />,
    );
    expect(screen.getByTestId("research-session-goal-card").getAttribute("data-state")).toBe(
      "pending_substantive",
    );
    fireEvent.click(screen.getByTestId("research-session-goal-card"));
    fireEvent.click(screen.getByTestId("research-session-goal-confirm-substantive"));
    expect(onConfirm).toHaveBeenCalledWith("换题提案");
  });

  it("keeps the proposal and focus while confirmation is pending", async () => {
    let resolveConfirm: (() => void) | undefined;
    const onConfirm = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveConfirm = resolve;
        }),
    );
    const { rerender } = render(
      <ResearchSessionGoalCard
        sessionId="s1"
        goal="当前目标"
        pendingSubstantive="换题提案"
        onConfirmSubstantive={onConfirm}
      />,
    );
    fireEvent.click(screen.getByTestId("research-session-goal-card"));
    const confirm = screen.getByTestId(
      "research-session-goal-confirm-substantive",
    );
    confirm.focus();
    fireEvent.click(confirm);
    fireEvent.click(confirm);
    expect(onConfirm).toHaveBeenCalledTimes(1);

    rerender(
      <ResearchSessionGoalCard
        sessionId="s1"
        goal="当前目标"
        pendingSubstantive="换题提案"
        onConfirmSubstantive={onConfirm}
        confirmSubstantivePending
      />,
    );
    const pendingConfirm = screen.getByTestId(
      "research-session-goal-confirm-substantive",
    ) as HTMLButtonElement;
    expect(screen.getAllByText("换题提案").length).toBeGreaterThan(0);
    expect(pendingConfirm.disabled).toBe(false);
    expect(pendingConfirm).toHaveAttribute("aria-disabled", "true");
    expect(pendingConfirm).toHaveAttribute("aria-busy", "true");
    expect(document.activeElement).toBe(pendingConfirm);
    fireEvent.click(pendingConfirm);
    expect(onConfirm).toHaveBeenCalledTimes(1);

    await act(async () => {
      resolveConfirm?.();
      await Promise.resolve();
    });
    expect(
      screen.queryByTestId("research-session-goal-confirm-substantive"),
    ).toBeNull();
  });

  it("keeps the substantive proposal open when confirmation fails", async () => {
    const onConfirm = vi.fn().mockRejectedValue(new Error("steer failed"));
    render(
      <ResearchSessionGoalCard
        sessionId="s1"
        goal="当前目标"
        pendingSubstantive="换题提案"
        onConfirmSubstantive={onConfirm}
      />,
    );
    fireEvent.click(screen.getByTestId("research-session-goal-card"));
    fireEvent.click(screen.getByTestId("research-session-goal-confirm-substantive"));
    await act(async () => Promise.resolve());

    expect(onConfirm).toHaveBeenCalledWith("换题提案");
    expect(screen.getAllByText("换题提案").length).toBeGreaterThan(0);
    expect(
      screen.getByTestId("research-session-goal-confirm-substantive"),
    ).toBeTruthy();
  });

  it("can collapse to icon and expand again from dialog", () => {
    render(<ResearchSessionGoalCard sessionId="s1" goal="目标" />);
    fireEvent.contextMenu(screen.getByTestId("research-session-goal-card"));
    expect(screen.getByTestId("research-session-goal-icon")).toBeTruthy();
    fireEvent.click(screen.getByTestId("research-session-goal-icon"));
    fireEvent.click(screen.getByTestId("research-session-goal-toggle-collapse"));
    expect(screen.getByTestId("research-session-goal-card")).toBeTruthy();
  });
});
