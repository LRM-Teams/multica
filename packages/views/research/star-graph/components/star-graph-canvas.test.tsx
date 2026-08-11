// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { TypedGraphEdge, TypedGraphNode } from "@multica/core/research";
import type { ResearchGraphNode } from "@multica/core/types";

import { buildStarCanvasViewModel } from "../lib/star-canvas-view-model";
import { StarGraphCanvas } from "./star-graph-canvas";

const setViewport = vi.fn();

vi.mock("@multica/core/research", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/research")>();
  return {
    ...actual,
    useResearchCanvasStore: (selector: (state: {
      viewport: null;
      setViewport: typeof setViewport;
    }) => unknown) =>
      selector({
        viewport: null,
        setViewport,
      }),
  };
});

class ResizeObserverMock {
  observe() {}
  unobserve() {}
  disconnect() {}
}

function node(partial: Partial<TypedGraphNode> & { id: string }): TypedGraphNode {
  return {
    session_id: "session-1",
    node_type: "subquestion",
    title: "",
    summary: "",
    status: "active",
    actor_agent_id: null,
    level: "m",
    round: 1,
    cluster_id: null,
    confidence: null,
    document_count: 0,
    conclusion_count: 0,
    goal_version_id: null,
    derived_from: null,
    merged_from: [],
    superseded_by: null,
    restart_of: null,
    invalidated_by: null,
    superseded_at: null,
    invalidated_at: null,
    parent_id: null,
    child_ids: [],
    children_of: [],
    created_at: "",
    updated_at: "",
    ...partial,
  };
}

function edge(partial: {
  id: string;
  from_node_id: string;
  to_node_id: string;
  edge_type: string;
}): TypedGraphEdge {
  return { session_id: "session-1", created_at: "", ...partial };
}

function fixtureModel() {
  return buildStarCanvasViewModel({
    nodes: [
      node({ id: "goal", node_type: "goal", title: "Research goal", level: "xxl" }),
      node({
        id: "stable-a",
        title: "Stable A",
        level: "l",
        cluster_id: "cluster-a",
        confidence: 82,
        document_count: 12,
        conclusion_count: 5,
      }),
      node({
        id: "agent-a",
        node_type: "agent_activity",
        status: "running",
        title: "Probe A",
        level: "s",
        cluster_id: "cluster-a",
        parent_id: "stable-a",
        actor_agent_id: "agent-1",
      }),
    ],
    edges: [
      edge({ id: "e1", from_node_id: "goal", to_node_id: "stable-a", edge_type: "leads_to" }),
      edge({ id: "e2", from_node_id: "stable-a", to_node_id: "agent-a", edge_type: "escalated_to" }),
    ],
  });
}

describe("StarGraphCanvas (Slice A renderer)", () => {
  beforeEach(() => {
    vi.stubGlobal("ResizeObserver", ResizeObserverMock);
    Object.defineProperty(HTMLElement.prototype, "clientWidth", {
      configurable: true,
      get() {
        return 1200;
      },
    });
    Object.defineProperty(HTMLElement.prototype, "clientHeight", {
      configurable: true,
      get() {
        return 800;
      },
    });
  });

  it("renders tiered nodes, edges, clusters, map key and zoom controls", () => {
    render(
      <StarGraphCanvas
        model={fixtureModel()}
        summaryTitle="调研星图"
        summaryDetail="1 个稳定结果"
      />,
    );

    expect(screen.getByTestId("star-graph-canvas")).toBeTruthy();
    expect(screen.getAllByTestId("star-graph-node")).toHaveLength(3);
    expect(screen.getByTestId("star-graph-edges").querySelectorAll("path")).toHaveLength(2);
    expect(screen.getByTestId("star-graph-cluster-cluster-a")).toBeTruthy();
    expect(screen.getByTestId("star-graph-map-key")).toBeTruthy();
    expect(screen.getByTestId("star-graph-zoom-controls")).toBeTruthy();
    expect(screen.getByTestId("star-graph-summary").textContent).toContain("调研星图");
  });

  it("selects a node without treating map-key buttons as canvas nodes", () => {
    const onSelectNode = vi.fn();
    render(<StarGraphCanvas model={fixtureModel()} onSelectNode={onSelectNode} />);

    fireEvent.click(screen.getByRole("button", { name: /Stable A/ }));
    expect(onSelectNode).toHaveBeenCalledWith("stable-a");
  });

  it("degrades safely for an empty graph", () => {
    render(
      <StarGraphCanvas
        model={buildStarCanvasViewModel({ nodes: [], edges: [] })}
        showMapKey={false}
      />,
    );

    expect(screen.getByTestId("star-graph-canvas")).toBeTruthy();
    expect(screen.queryAllByTestId("star-graph-node")).toHaveLength(0);
    expect(screen.queryByTestId("star-graph-edges")).toBeNull();
  });

  it("shows a budget note when entities exceed the DOM cap", () => {
    const nodes = [
      node({ id: "goal", node_type: "goal", title: "Goal", level: "xxl" }),
      ...Array.from({ length: 12 }, (_, index) =>
        node({
          id: `n-${index}`,
          title: `Node ${index}`,
          level: "s",
          actor_agent_id: `agent-${index}`,
        }),
      ),
    ];
    const edges = nodes.slice(1).map((entry, index) =>
      edge({
        id: `e-${index}`,
        from_node_id: "goal",
        to_node_id: entry.id,
        edge_type: "leads_to",
      }),
    );
    render(
      <StarGraphCanvas
        model={buildStarCanvasViewModel({ nodes, edges })}
        entityBudget={5}
        selectedNodeId="n-0"
        relatedNodeIds={new Set(["goal", "n-0"])}
      />,
    );

    expect(screen.getAllByTestId("star-graph-node")).toHaveLength(5);
    expect(screen.getByTestId("star-graph-budget-note").textContent).toContain("5/13");
  });

  it("supports keyboard navigation when keyboardNav is provided", () => {
    const onSelectNode = vi.fn();
    const keyboardNodes = [
      {
        id: "goal",
        session_id: "session-1",
        node_type: "goal",
        title: "Research goal",
        summary: "",
        status: "active",
        actor_agent_id: null,
        created_at: "",
        updated_at: "",
        payload: null,
      },
      {
        id: "stable-a",
        session_id: "session-1",
        node_type: "subquestion",
        title: "Stable A",
        summary: "",
        status: "active",
        actor_agent_id: null,
        created_at: "",
        updated_at: "",
        payload: null,
      },
    ] satisfies ResearchGraphNode[];

    render(
      <StarGraphCanvas
        model={fixtureModel()}
        selectedNodeId="stable-a"
        onSelectNode={onSelectNode}
        keyboardNav={{
          nodes: keyboardNodes,
          edges: [
            {
              from_node_id: "goal",
              to_node_id: "stable-a",
              edge_type: "leads_to",
            },
          ],
        }}
      />,
    );

    const canvas = screen.getByTestId("star-graph-canvas");
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "Home" });
    expect(onSelectNode).toHaveBeenCalledWith("goal");
    expect(screen.getByTestId("star-graph-canvas-live").textContent).toContain("Research goal");
  });
});
