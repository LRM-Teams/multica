// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";
import {
  aggregateTreeCardBoxes,
  layoutAggregateTreeShell,
} from "./layout-graph";

const now = "2026-08-04T00:00:00Z";

function node(id: string, title: string): ResearchGraphNode {
  return {
    id,
    session_id: "aggregate-fixture",
    node_type: id === "root" ? "goal" : "finding",
    title,
    summary: "",
    status: "active",
    actor_agent_id: null,
    payload: {},
    created_at: now,
    updated_at: now,
  };
}

function edge(id: string, from: string, to: string): ResearchGraphEdge {
  return {
    id,
    session_id: "aggregate-fixture",
    from_node_id: from,
    to_node_id: to,
    edge_type: "leads_to",
    created_at: now,
  };
}

describe("layoutAggregateTreeShell (LRM-1295)", () => {
  it("uses a stable three-column viewport without reading parent/child projection fields", () => {
    const parent = node("root", "Platform and architecture");
    const siblings = [
      node("agent", "Agent orchestration"),
      node("runtime", "Runtime containers"),
      node("data", "Events and storage"),
      node("experience", "Canvas and navigation"),
    ];
    const children = [
      node("scheduler", "Task scheduling"),
      node("context", "Context management"),
      node("execution", "Execution runtime"),
      node("delivery", "Delivery reading"),
    ];
    const edges = [
      ...siblings.map((s) => edge(`root-${s.id}`, parent.id, s.id)),
      ...children.map((child) => edge(`agent-${child.id}`, "agent", child.id)),
      // Non-tree relation must not draw a shell connector.
      { ...edge("support", "runtime", "context"), edge_type: "supports" },
    ];

    const laid = layoutAggregateTreeShell({ parent, siblings, children, edges });
    const boxes = aggregateTreeCardBoxes(laid);
    const parentBox = boxes.find((box) => box.id === parent.id)!;
    const siblingBoxes = boxes.filter((box) => box.tier === "sibling");
    const childBoxes = boxes.filter((box) => box.tier === "child");

    expect(parentBox.tier).toBe("parent");
    expect(siblingBoxes).toHaveLength(4);
    expect(childBoxes).toHaveLength(4);
    expect(Math.max(...siblingBoxes.map((box) => box.x))).toBeLessThan(
      Math.min(...childBoxes.map((box) => box.x)),
    );
    expect(parentBox.x).toBeLessThan(Math.min(...siblingBoxes.map((box) => box.x)));
    expect(parentBox.w).toBeGreaterThan(Math.max(...siblingBoxes.map((box) => box.w)));
    expect(Math.min(...siblingBoxes.map((box) => box.w))).toBeGreaterThan(
      Math.max(...childBoxes.map((box) => box.w)),
    );

    for (let index = 0; index < boxes.length; index += 1) {
      for (let other = index + 1; other < boxes.length; other += 1) {
        const a = boxes[index]!;
        const b = boxes[other]!;
        const overlaps =
          a.x < b.x + b.w && a.x + a.w > b.x && a.y < b.y + b.h && a.y + a.h > b.y;
        expect(overlaps).toBe(false);
      }
    }

    expect(laid.edges.map((item) => item.id)).toEqual([
      "root-agent",
      "root-runtime",
      "root-data",
      "root-experience",
      "agent-scheduler",
      "agent-context",
      "agent-execution",
      "agent-delivery",
    ]);
  });

  it("stages a two-branch window across distinct vertical tracks", () => {
    const parent = node("root", "Root strategy");
    const siblings = [
      node("evidence", "Evidence branch"),
      node("risk", "Risk branch"),
    ];
    const laid = layoutAggregateTreeShell({ parent, siblings, children: [], edges: [] });
    const boxes = aggregateTreeCardBoxes(laid);
    const siblingBoxes = boxes
      .filter((box) => box.tier === "sibling")
      .sort((a, b) => a.y - b.y);

    expect(siblingBoxes).toHaveLength(2);
    expect(siblingBoxes[1]!.y).toBeGreaterThan(siblingBoxes[0]!.y + siblingBoxes[0]!.h);

    const top = Math.min(...boxes.map((box) => box.y));
    const bottom = Math.max(...boxes.map((box) => box.y + box.h));
    expect(bottom - top).toBeGreaterThanOrEqual(524);
  });
});
