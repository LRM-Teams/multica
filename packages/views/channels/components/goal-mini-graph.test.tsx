// @vitest-environment jsdom

import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { WorkGraphDetail, WorkGraphNode } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { GoalMiniGraph } from "./goal-mini-graph";

function graphNode(id: string, patch: Partial<WorkGraphNode> = {}): WorkGraphNode {
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
    objective: `Prove ${id} keeps world continuity across default surfaces`,
    completion_contract: [`artifact-${id}`],
    based_on_graph_version: 5,
    ...patch,
  };
}

const graph: WorkGraphDetail = {
  id: "graph-1",
  workspace_id: "workspace-1",
  anchor_kind: "channel_goal",
  anchor_id: "goal-1",
  status: "active",
  current_version: 5,
  admission_decision: "GRAPH",
  nodes: [
    graphNode("a", { execution_status: "running" }),
    graphNode("b"),
    graphNode("c", { role: "verifier", review_status: "reviewing" }),
  ],
  edges: [
    {
      id: "a-b",
      from_node_id: "a",
      to_node_id: "b",
      edge_type: "depends_on",
      required: true,
    },
    {
      id: "b-c",
      from_node_id: "b",
      to_node_id: "c",
      edge_type: "depends_on",
      required: true,
    },
  ],
};

describe("GoalMiniGraph presentation", () => {
  it("renders compact HTML chips with readable labels and opens read-only detail", async () => {
    const user = userEvent.setup();
    renderWithI18n(<GoalMiniGraph graph={graph} />);

    expect(screen.getByTestId("goal-mini-graph-viewport")).toBeInTheDocument();
    expect(screen.getByTestId("goal-mini-graph-zoom-controls")).toBeInTheDocument();
    expect(screen.getByTestId("goal-mini-graph-node-a")).toHaveTextContent(/Prove a keeps/);
    expect(screen.queryByTestId("goal-mini-graph-detail")).not.toBeInTheDocument();

    await user.click(screen.getByTestId("goal-mini-graph-node-a"));
    expect(screen.getByTestId("goal-mini-graph-detail")).toHaveTextContent("artifact-a");
    expect(screen.getByTestId("goal-mini-graph-detail")).toHaveTextContent("issue-a");
  });
});
