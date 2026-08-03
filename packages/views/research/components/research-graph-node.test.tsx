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
          },
        },
        card_menu: { open: "打开菜单" },
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
});
