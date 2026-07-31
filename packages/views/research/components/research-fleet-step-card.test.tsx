import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import type { FleetStepCardModel } from "../lib/fleet-step-cards";
import { ResearchFleetStepCard } from "./research-fleet-step-card";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        step_card: {
          status: {
            done: "完成",
            running: "进行中",
            waiting: "等待",
            failed: "失败",
          },
          expand: "展开证据",
          collapse: "收起",
          retry: "重试",
          reassign: "改派",
        },
      }),
  }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <div data-testid="avatar" />,
}));

function base(partial: Partial<FleetStepCardModel> = {}): FleetStepCardModel {
  return {
    kind: "step",
    id: "c1",
    status: "done",
    title: "寻源",
    stepLabel: "S2 · 09:30",
    summaryHeadline: "12 条高权重源",
    summaryDetail: "Godot 模板 / 引擎文档",
    bullets: ["why：验证美术供给"],
    evidence: "full evidence wall",
    mergeCount: 1,
    reason: null,
    recoveryHint: null,
    actorAgentId: null,
    createdAt: "2026-07-31T09:30:00Z",
    showRetry: false,
    showReassign: false,
    ...partial,
  };
}

describe("ResearchFleetStepCard", () => {
  it("renders done card with bullets and expandable evidence", () => {
    render(<ResearchFleetStepCard card={base()} />);
    expect(screen.getByText("寻源")).toBeTruthy();
    expect(screen.getByText("完成")).toBeTruthy();
    expect(screen.getByText("12 条高权重源")).toBeTruthy();
    expect(screen.getByText("why：验证美术供给")).toBeTruthy();
    fireEvent.click(screen.getByText("展开证据"));
    expect(screen.getByText("full evidence wall")).toBeTruthy();
  });

  it("renders failed card actions", () => {
    const onRetry = vi.fn();
    const onReassign = vi.fn();
    render(
      <ResearchFleetStepCard
        card={base({
          status: "failed",
          title: "交叉验证唤醒",
          summaryHeadline: "agent model is required",
          summaryDetail: "已合并 3 次同类失败，不再刷屏。",
          bullets: [],
          showRetry: true,
          showReassign: true,
          evidence: "raw dump",
        })}
        onRetry={onRetry}
        onReassign={onReassign}
      />,
    );
    expect(screen.getByText("失败")).toBeTruthy();
    fireEvent.click(screen.getByText("重试"));
    fireEvent.click(screen.getByText("改派"));
    expect(onRetry).toHaveBeenCalledTimes(1);
    expect(onReassign).toHaveBeenCalledTimes(1);
  });

  it.each([
    ["running", "进行中"],
    ["waiting", "等待"],
  ] as const)("renders %s status badge", (status, label) => {
    render(<ResearchFleetStepCard card={base({ status, evidence: null })} />);
    expect(screen.getByText(label)).toBeTruthy();
    expect(screen.getByTestId("fleet-step-card").getAttribute("data-status")).toBe(
      status,
    );
  });
});
