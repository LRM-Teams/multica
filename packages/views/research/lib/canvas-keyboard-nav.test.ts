// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";
import {
  buildNodeAccessibleName,
  crossLaneNeighbor,
  isForkPoint,
  mainChainNeighbor,
  mainChainOrder,
  mergeStatusAnnouncements,
  resolveCanvasKeyAction,
  resolveCanvasKeyEvent,
  type CanvasKeyboardContext,
} from "./canvas-keyboard-nav";

function node(
  partial: Partial<ResearchGraphNode> & Pick<ResearchGraphNode, "id" | "node_type" | "title">,
): ResearchGraphNode {
  return {
    session_id: "s1",
    summary: "",
    status: "active",
    actor_agent_id: null,
    payload: {},
    created_at: "2026-07-29T00:00:00Z",
    updated_at: "2026-07-29T00:00:00Z",
    ...partial,
  };
}

function edge(
  id: string,
  from: string,
  to: string,
  edge_type: ResearchGraphEdge["edge_type"] = "leads_to",
): ResearchGraphEdge {
  return {
    id,
    session_id: "s1",
    from_node_id: from,
    to_node_id: to,
    edge_type,
    created_at: "2026-07-29T00:00:00Z",
  };
}

/** Goal → fork → two parallel probes → merge finding. */
function forkFixture() {
  const nodes = [
    node({ id: "goal", node_type: "goal", title: "Goal" }),
    node({ id: "fork", node_type: "stage_gate", title: "Fork" }),
    node({
      id: "a",
      node_type: "probe",
      title: "Lane A",
      payload: { logic_lane: "source" },
    }),
    node({
      id: "b",
      node_type: "finding",
      title: "Lane B",
      payload: { logic_lane: "deep_read" },
    }),
    node({ id: "merge", node_type: "finding", title: "Merge" }),
  ];
  const edges = [
    edge("e1", "goal", "fork"),
    edge("e2", "fork", "a"),
    edge("e3", "fork", "b"),
    edge("e4", "a", "merge"),
    edge("e5", "b", "merge"),
  ];
  return { nodes, edges };
}

function baseCtx(
  partial: Partial<CanvasKeyboardContext> &
    Pick<CanvasKeyboardContext, "focusId">,
): CanvasKeyboardContext {
  const { nodes, edges } = forkFixture();
  return {
    nodes,
    edges,
    overlay: null,
    activeBranchId: null,
    ...partial,
  };
}

describe("canvas-keyboard-nav neighbors (LRM-1105 / 1102 semantics A)", () => {
  it("mainChainOrder follows leads_to spine", () => {
    const { nodes, edges } = forkFixture();
    const order = mainChainOrder(nodes, edges);
    expect(order[0]).toBe("goal");
    expect(order.indexOf("fork")).toBeLessThan(order.indexOf("a"));
    expect(order.indexOf("fork")).toBeLessThan(order.indexOf("b"));
  });

  it("isForkPoint is true only when leads_to out-degree ≥ 2", () => {
    const { nodes, edges } = forkFixture();
    expect(isForkPoint("fork", nodes, edges)).toBe(true);
    expect(isForkPoint("goal", nodes, edges)).toBe(false);
    expect(isForkPoint("a", nodes, edges)).toBe(false);
  });

  it("mainChainNeighbor moves along leads_to; at fork prefers current lane", () => {
    const { nodes, edges } = forkFixture();
    expect(mainChainNeighbor(nodes, edges, "goal", 1)).toBe("fork");
    expect(
      mainChainNeighbor(nodes, edges, "fork", 1, { preferLaneFrom: "a" }),
    ).toBe("a");
    expect(
      mainChainNeighbor(nodes, edges, "fork", 1, { preferLaneFrom: "b" }),
    ).toBe("b");
    expect(mainChainNeighbor(nodes, edges, "a", -1)).toBe("fork");
  });

  it("crossLaneNeighbor only works at fork points (semantics A)", () => {
    const { nodes, edges } = forkFixture();
    expect(crossLaneNeighbor(nodes, edges, "a", 1)).toBeNull();
    expect(crossLaneNeighbor(nodes, edges, "goal", 1)).toBeNull();
    expect(crossLaneNeighbor(nodes, edges, "fork", 1)).toBe("a");
    expect(
      crossLaneNeighbor(nodes, edges, "fork", 1, { activeBranchId: "a" }),
    ).toBe("b");
    expect(
      crossLaneNeighbor(nodes, edges, "fork", -1, { activeBranchId: "a" }),
    ).toBe("b");
  });
});

describe("resolveCanvasKeyAction keyboard map", () => {
  it("ArrowRight/Left move on main chain", () => {
    expect(resolveCanvasKeyAction("ArrowRight", baseCtx({ focusId: "goal" }))).toEqual({
      type: "moveFocus",
      nodeId: "fork",
    });
    expect(resolveCanvasKeyAction("ArrowLeft", baseCtx({ focusId: "fork" }))).toEqual({
      type: "moveFocus",
      nodeId: "goal",
    });
  });

  it("ArrowDown/Up cross lane only at fork (A); noop elsewhere", () => {
    expect(resolveCanvasKeyAction("ArrowDown", baseCtx({ focusId: "a" }))).toEqual({
      type: "noop",
    });
    expect(
      resolveCanvasKeyAction(
        "ArrowDown",
        baseCtx({ focusId: "fork", activeBranchId: "a" }),
      ),
    ).toEqual({ type: "moveFocus", nodeId: "b" });
    expect(
      resolveCanvasKeyAction(
        "ArrowUp",
        baseCtx({ focusId: "fork", activeBranchId: "a" }),
      ),
    ).toEqual({ type: "moveFocus", nodeId: "b" });
  });

  it("Enter/Space open detail; . and Shift+F10 open ring", () => {
    expect(resolveCanvasKeyAction("Enter", baseCtx({ focusId: "fork" }))).toEqual({
      type: "openDetail",
    });
    expect(resolveCanvasKeyAction(" ", baseCtx({ focusId: "fork" }))).toEqual({
      type: "openDetail",
    });
    expect(resolveCanvasKeyAction(".", baseCtx({ focusId: "fork" }))).toEqual({
      type: "openRing",
    });
    expect(
      resolveCanvasKeyEvent({ key: "F10", shiftKey: true }, baseCtx({ focusId: "fork" })),
    ).toEqual({ type: "openRing" });
  });

  it("Esc closes ring then detail; graph keys ignored while overlay open", () => {
    expect(
      resolveCanvasKeyAction("Escape", baseCtx({ focusId: "fork", overlay: "ring" })),
    ).toEqual({ type: "closeOverlay", layer: "ring" });
    expect(
      resolveCanvasKeyAction("Escape", baseCtx({ focusId: "fork", overlay: "detail" })),
    ).toEqual({ type: "closeOverlay", layer: "detail" });
    expect(
      resolveCanvasKeyAction("ArrowRight", baseCtx({ focusId: "fork", overlay: "detail" })),
    ).toEqual({ type: "noop" });
  });

  it("+/- /0 zoom; Home/End jump chain ends", () => {
    expect(resolveCanvasKeyAction("+", baseCtx({ focusId: "fork" }))).toEqual({
      type: "zoomIn",
    });
    expect(resolveCanvasKeyAction("-", baseCtx({ focusId: "fork" }))).toEqual({
      type: "zoomOut",
    });
    expect(resolveCanvasKeyAction("0", baseCtx({ focusId: "fork" }))).toEqual({
      type: "fitView",
    });
    expect(resolveCanvasKeyAction("Home", baseCtx({ focusId: "merge" }))).toEqual({
      type: "moveFocus",
      nodeId: "goal",
    });
    expect(resolveCanvasKeyAction("End", baseCtx({ focusId: "goal" }))).toEqual({
      type: "moveFocus",
      nodeId: "merge",
    });
  });
});

describe("accessible name + live region merge", () => {
  it("buildNodeAccessibleName joins title, status, lane; appends 低置信", () => {
    const n = node({
      id: "a",
      node_type: "probe",
      title: "探源",
      status: "done",
      payload: { logic_lane: "source", low_confidence: true },
    });
    expect(buildNodeAccessibleName(n)).toBe(
      "探源，已完成，寻源轨，低置信，目标 未提供，操作思路 未提供，调研思路 未提供，调研结果 未提供",
    );
  });

  it("mergeStatusAnnouncements collapses same-tick bursts", () => {
    expect(
      mergeStatusAnnouncements([{ nodeId: "a", title: "探源", status: "done" }]),
    ).toBe("探源 已完成");
    expect(
      mergeStatusAnnouncements([
        { nodeId: "a", title: "探源", status: "done" },
        { nodeId: "b", title: "冲突", status: "failed" },
      ]),
    ).toBe("2 个节点更新");
  });
});
