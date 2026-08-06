import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ResearchGraphNode } from "@multica/core/types";
import { ResearchGraphNode as ResearchGraphNodeView } from "./research-graph-node";

vi.mock("@xyflow/react", () => ({
  Handle: () => null,
  Position: { Left: "left", Right: "right", Top: "top", Bottom: "bottom" },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("../../agents/use-agent-live-status", () => ({
  useAgentActivityProjection: () => null,
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (picker: (keys: Record<string, unknown>) => unknown) => {
      const keys = {
        logic: {
          end_title: "交付",
          lane: {
            orchestrate: "编排",
            explore: "探索",
            source: "寻源",
            synthesize: "综合",
            deliver: "交付",
          },
          status: {
            pending: "待开始",
            running: "进行中",
            completed: "已完成",
            failed: "失败",
            blocked: "阻塞",
            abandoned: "已废弃",
            done: "完成",
            waiting: "等待",
            kickoff: "开题",
            pending_delivery: "待交付",
          },
        },
        card_menu: { open: "打开菜单" },
        content_faces: {
          goal: "目标",
          operation_approach: "操作思路",
          research_approach: "调研思路",
          result: "调研结果",
          missing: "未提供",
          result_pending: "结果整理中",
          result_pending_detail: "正在整理，暂未形成可展示结果。",
          result_failed: "本轮未产出可展示结果",
          result_failed_detail: "本轮未产出可展示结果。",
        },
      };
      return picker(keys as never);
    },
  }),
}));

const node: ResearchGraphNode = {
  id: "n1",
  session_id: "s1",
  node_type: "finding",
  title: "探源",
  summary: "",
  status: "completed",
  actor_agent_id: null,
  payload: { low_confidence: true },
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-01T00:00:00Z",
};

describe("ResearchGraphNode a11y (LRM-1105 slice3)", () => {
  it("exposes role=button with roving tabindex and accessible name", () => {
    render(
      <ResearchGraphNodeView
        id="n1"
        type="research"
        data={{
          research: node,
          laneId: "source",
          branchId: "main",
          logicRole: "step",
        }}
        selected
        dragging={false}
        zIndex={1}
        selectable
        deletable={false}
        draggable={false}
        isConnectable={false}
        positionAbsoluteX={0}
        positionAbsoluteY={0}
      />,
    );
    const card = screen.getByRole("button", { name: /探源/ });
    expect(card).toHaveAttribute("tabindex", "0");
    expect(card.getAttribute("aria-label")).toContain("低置信");
    expect(card.getAttribute("aria-label")).toContain("目标");
    expect(card.getAttribute("aria-label")).toContain("操作思路");
    expect(screen.getByTestId("research-node-content-faces-surface")).toBeTruthy();
  });

  it("renders projected content faces without summary fallback", () => {
    const withContent = {
      ...node,
      summary: "SUMMARY_LEAK",
      content: {
        goal: "达成定价决策",
        operation_approach: "官网交叉",
        research_approach: "先横向",
        result: "三条结论",
      },
    } as ResearchGraphNode;
    render(
      <ResearchGraphNodeView
        id="n1"
        type="research"
        data={{
          research: withContent,
          laneId: "source",
          branchId: "main",
          logicRole: "step",
        }}
        selected
        dragging={false}
        zIndex={1}
        selectable
        deletable={false}
        draggable={false}
        isConnectable={false}
        positionAbsoluteX={0}
        positionAbsoluteY={0}
      />,
    );
    const surface = screen.getByTestId("research-node-content-faces-surface");
    expect(surface.textContent).toContain("达成定价决策");
    expect(surface.textContent).toContain("官网交叉");
    expect(surface.textContent).not.toContain("SUMMARY_LEAK");
  });

  it("keeps unselected nodes in tab order as tabindex=-1", () => {
    render(
      <ResearchGraphNodeView
        id="n1"
        type="research"
        data={{
          research: node,
          laneId: "source",
          branchId: "main",
          logicRole: "step",
        }}
        selected={false}
        dragging={false}
        zIndex={1}
        selectable
        deletable={false}
        draggable={false}
        isConnectable={false}
        positionAbsoluteX={0}
        positionAbsoluteY={0}
      />,
    );
    const card = screen.getByRole("button", { name: /探源/ });
    expect(card).toHaveAttribute("tabindex", "-1");
  });

  it("renders the aggregate parent shell at the layout-provided size", () => {
    render(
      <ResearchGraphNodeView
        id="n1"
        type="research"
        data={{
          research: node,
          laneId: "source",
          branchId: "theme:research",
          logicRole: "step",
          aggregateTier: "parent",
          aggregateSize: { width: 282, height: 242 },
        }}
        selected={false}
        dragging={false}
        zIndex={1}
        selectable
        deletable={false}
        draggable={false}
        isConnectable={false}
        positionAbsoluteX={0}
        positionAbsoluteY={0}
      />,
    );
    const shell = screen.getByTestId("research-logic-card");
    expect(shell).toHaveAttribute("data-aggregate-tier", "parent");
    expect(shell).toHaveStyle({ width: "282px", height: "242px" });
  });

  it("LRM-1333: abandoned surface uses dashed muted wash + pill; name includes 已废弃", () => {
    const abandoned = {
      ...node,
      status: "abandoned",
      title: "定价支",
      assessment: "detour",
    } as ResearchGraphNode;
    render(
      <ResearchGraphNodeView
        id="n1"
        type="research"
        data={{
          research: abandoned,
          laneId: "source",
          branchId: "main",
          logicRole: "step",
        }}
        selected
        dragging={false}
        zIndex={1}
        selectable
        deletable={false}
        draggable={false}
        isConnectable={false}
        positionAbsoluteX={0}
        positionAbsoluteY={0}
      />,
    );
    const shell = screen.getByTestId("research-logic-card");
    expect(shell).toHaveAttribute("data-abandoned", "true");
    expect(shell.className).toContain("border-dashed");
    expect(shell.className).toContain("bg-muted");
    expect(shell.className).not.toContain("destructive");
    expect(screen.getByTestId("research-node-abandoned-pill").textContent).toBe("已废弃");
    const card = screen.getByRole("button", { name: /定价支/ });
    expect(card.getAttribute("aria-label")).toContain("已废弃");
    expect(card).not.toBeDisabled();
  });

  it("LRM-1333: detour-only node does not show abandoned surface", () => {
    const detour = {
      ...node,
      status: "done",
      assessment: "detour",
    } as ResearchGraphNode;
    render(
      <ResearchGraphNodeView
        id="n1"
        type="research"
        data={{
          research: detour,
          laneId: "source",
          branchId: "main",
          logicRole: "step",
        }}
        selected
        dragging={false}
        zIndex={1}
        selectable
        deletable={false}
        draggable={false}
        isConnectable={false}
        positionAbsoluteX={0}
        positionAbsoluteY={0}
      />,
    );
    expect(screen.getByTestId("research-logic-card")).not.toHaveAttribute("data-abandoned");
    expect(screen.queryByTestId("research-node-abandoned-pill")).toBeNull();
  });
});
