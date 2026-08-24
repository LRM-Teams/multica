import { describe, expect, it } from "vitest";
import type { WorkGraphEdge, WorkGraphNode } from "@multica/core/types";
import {
  GOAL_MINI_INITIAL_LAYER_BUDGET,
  GOAL_MINI_NODE_HEIGHT,
  GOAL_MINI_NODE_WIDTH,
  goalNodeVisualState,
  layoutGoalMiniGraph,
  visibleGoalMiniGraphSlice,
} from "./goal-mini-graph-layout";

function node(id: string, patch: Partial<WorkGraphNode> = {}): WorkGraphNode {
  return {
    id,
    issue_id: `issue-${id}`,
    role: "worker",
    context_policy: "bounded",
    execution_status: "queued",
    validity_status: "valid",
    review_status: "unreviewed",
    completion_authority: "kernel_evidence",
    effective_completion: "pending",
    objective: `Node ${id}`,
    completion_contract: [],
    based_on_graph_version: 1,
    ...patch,
  };
}

function edge(from: string, to: string): WorkGraphEdge {
  return {
    id: `${from}-${to}`,
    from_node_id: from,
    to_node_id: to,
    edge_type: "depends_on",
    required: true,
  };
}

describe("Goal mini graph layout", () => {
  it("maps durable completion, work, review, blocker, and failure states", () => {
    expect(goalNodeVisualState(node("done", { effective_completion: "satisfied" }))).toBe("done");
    expect(goalNodeVisualState(node("running", { execution_status: "running" }))).toBe("working");
    expect(
      goalNodeVisualState(node("review", { review_status: "reviewing", role: "verifier" })),
    ).toBe("reviewing");
    expect(goalNodeVisualState(node("blocked", { review_status: "blocked" }))).toBe("blocked");
    expect(goalNodeVisualState(node("failed", { execution_status: "failed" }))).toBe("error");
    expect(
      goalNodeVisualState(node("rejected", { execution_status: "ready", review_status: "rejected" })),
    ).toBe("error");
    expect(
      goalNodeVisualState(node("stale", { validity_status: "stale", effective_completion: "stale" })),
    ).toBe("stale");
    expect(goalNodeVisualState(node("queued"))).toBe("pending");
  });

  it("lays dependencies left-to-right without overlapping peers", () => {
    const layout = layoutGoalMiniGraph(
      [node("plan"), node("build"), node("test"), node("review", { role: "verifier" })],
      [edge("plan", "build"), edge("plan", "test"), edge("build", "review"), edge("test", "review")],
    );
    const byId = new Map(layout.nodes.map((item) => [item.id, item]));

    expect(byId.get("plan")!.x).toBeLessThan(byId.get("build")!.x);
    expect(byId.get("plan")!.x).toBeLessThan(byId.get("test")!.x);
    expect(byId.get("build")!.x).toBeLessThan(byId.get("review")!.x);
    expect(byId.get("build")!.y).not.toBe(byId.get("test")!.y);
    expect(layout.edges).toHaveLength(4);
    expect(layout.nodes.every((item) => item.width === GOAL_MINI_NODE_WIDTH)).toBe(true);
    expect(layout.nodes.every((item) => item.height === GOAL_MINI_NODE_HEIGHT)).toBe(true);
  });

  it("grows the canvas with deep or tall graphs instead of shrinking labels away", () => {
    const nodes = Array.from({ length: 6 }, (_, index) => node(`n${index}`));
    const edges = nodes.slice(0, -1).map((item, index) => edge(item.id, nodes[index + 1]!.id));
    const chain = layoutGoalMiniGraph(nodes, edges);
    expect(chain.width).toBeGreaterThan(360);
    expect(chain.maxLayer).toBe(5);

    const fan = layoutGoalMiniGraph(
      [node("root"), ...Array.from({ length: 5 }, (_, index) => node(`leaf${index}`))],
      Array.from({ length: 5 }, (_, index) => edge("root", `leaf${index}`)),
    );
    expect(fan.height).toBeGreaterThan(144);
    expect(Math.min(...fan.nodes.map((item) => item.height))).toBe(GOAL_MINI_NODE_HEIGHT);
  });

  it("keeps malformed API edges out of the visual snapshot", () => {
    const layout = layoutGoalMiniGraph(
      [node("one"), node("two")],
      [edge("one", "two"), edge("missing", "two"), edge("two", "absent")],
    );

    expect(layout.nodes).toHaveLength(2);
    expect(layout.edges).toEqual([expect.objectContaining({ id: "one-two" })]);
  });

  it("pages large graphs by layer budget without dropping earlier edges", () => {
    const nodes = Array.from({ length: 8 }, (_, index) => node(`n${index}`));
    const edges = nodes.slice(0, -1).map((item, index) => edge(item.id, nodes[index + 1]!.id));
    const layout = layoutGoalMiniGraph(nodes, edges);
    const slice = visibleGoalMiniGraphSlice(layout, GOAL_MINI_INITIAL_LAYER_BUDGET);

    expect(slice.nodes).toHaveLength(GOAL_MINI_INITIAL_LAYER_BUDGET);
    expect(slice.hasMore).toBe(true);
    expect(visibleGoalMiniGraphSlice(layout, layout.maxLayer + 1).hasMore).toBe(false);
  });
});
