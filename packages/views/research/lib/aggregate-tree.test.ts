import { describe, expect, it } from "vitest";
import type { ResearchGraphNode } from "@multica/core/types";
import {
  selectAggregateTree,
  selectAggregateTreeColumns,
  type AggregateTreeSelection,
} from "./aggregate-tree";

function node(
  partial: Partial<ResearchGraphNode> & Pick<ResearchGraphNode, "id" | "node_type" | "title">,
): ResearchGraphNode {
  return {
    session_id: "session-1",
    summary: "",
    status: "active",
    actor_agent_id: null,
    payload: {},
    created_at: "2026-08-04T00:00:00Z",
    updated_at: "2026-08-04T00:00:00Z",
    ...partial,
  };
}

function ready(selection: AggregateTreeSelection) {
  expect(selection.status).toBe("ready");
  if (selection.status !== "ready") throw new Error("expected ready aggregate tree");
  return selection;
}

describe("selectAggregateTree", () => {
  it("consumes the LRM-1278 projection without deriving tree counts or quality from edges", () => {
    const selection = ready(
      selectAggregateTree([
        node({
          id: "root",
          node_type: "goal",
          title: "Goal",
          parent_id: null,
          child_ids: ["branch-b", "branch-a"],
          child_count: 2,
          descendant_count: 3,
          theme_key: "type:goal",
          assessment: "pending_review",
        }),
        node({
          id: "branch-a",
          node_type: "question",
          title: "Pricing",
          parent_id: "root",
          child_ids: ["leaf-a"],
          child_count: 1,
          descendant_count: 1,
          theme_key: "pricing",
          assessment: "trusted",
          confidence: 0.91,
          reason: "Multiple sources agree",
        }),
        node({
          id: "branch-b",
          node_type: "question",
          title: "Risk",
          parent_id: "root",
          child_ids: [],
          child_count: 0,
          descendant_count: 0,
          theme_key: "risk",
          assessment: "detour",
          evidence_summary: "Two sources contradict",
        }),
        node({
          id: "leaf-a",
          node_type: "finding",
          title: "Price finding",
          parent_id: "branch-a",
          child_ids: [],
          child_count: 0,
          descendant_count: 0,
          theme_key: "type:finding",
          assessment: "trusted",
        }),
      ]),
    );

    expect(selection.roots.map((entry) => entry.id)).toEqual(["root"]);
    expect(selection.byId.get("root")).toMatchObject({
      childIds: ["branch-b", "branch-a"],
      childCount: 2,
      descendantCount: 3,
      themeKey: "type:goal",
      assessment: "pending_review",
    });
    expect(selection.byId.get("branch-a")).toMatchObject({
      confidence: 0.91,
      reason: "Multiple sources agree",
      assessment: "trusted",
    });
    expect(selection.byId.get("branch-b")).toMatchObject({
      evidenceSummary: "Two sources contradict",
      assessment: "detour",
    });

    const columns = selectAggregateTreeColumns(selection, {
      rootId: "root",
      branchId: "branch-a",
    });
    expect(columns?.root.id).toBe("root");
    expect(columns?.branches.map((entry) => entry.id)).toEqual(["branch-b", "branch-a"]);
    expect(columns?.branch?.id).toBe("branch-a");
    expect(columns?.leaves.map((entry) => entry.id)).toEqual(["leaf-a"]);
  });

  it("uses pending_review only for a missing or unknown assessment", () => {
    const selection = ready(
      selectAggregateTree([
        node({
          id: "missing",
          node_type: "goal",
          title: "Missing assessment",
          parent_id: null,
          child_ids: [],
          child_count: 0,
          descendant_count: 0,
          theme_key: "type:goal",
        }),
        node({
          id: "unknown",
          node_type: "finding",
          title: "Unknown assessment",
          parent_id: null,
          child_ids: [],
          child_count: 0,
          descendant_count: 0,
          theme_key: "type:finding",
          assessment: "unrecognized-server-value",
        }),
      ]),
    );

    expect(selection.roots.map((entry) => entry.assessment)).toEqual([
      "pending_review",
      "pending_review",
    ]);
  });

  it("reports an incomplete snapshot instead of fabricating hierarchy, counts, or a theme key", () => {
    const selection = selectAggregateTree([
      node({
        id: "incomplete",
        node_type: "goal",
        title: "Incomplete",
        parent_id: null,
        child_count: 0,
        descendant_count: 0,
        assessment: "trusted",
      }),
    ]);

    expect(selection).toEqual({
      status: "incomplete",
      gaps: [
        { nodeId: "incomplete", field: "child_ids" },
        { nodeId: "incomplete", field: "theme_key" },
      ],
    });
  });

  it("never infers child membership from parent_id when snapshot child_ids disagrees", () => {
    const selection = ready(
      selectAggregateTree([
        node({
          id: "root",
          node_type: "goal",
          title: "Root",
          parent_id: null,
          child_ids: [],
          child_count: 0,
          descendant_count: 0,
          theme_key: "type:goal",
          assessment: "trusted",
        }),
        node({
          id: "orphaned-by-contract",
          node_type: "finding",
          title: "Not listed by parent",
          parent_id: "root",
          child_ids: [],
          child_count: 0,
          descendant_count: 0,
          theme_key: "type:finding",
          assessment: "trusted",
        }),
      ]),
    );

    const columns = selectAggregateTreeColumns(selection, { rootId: "root" });
    expect(columns?.branches).toEqual([]);
  });
});
