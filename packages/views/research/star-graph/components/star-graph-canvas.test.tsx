// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { useState } from "react";
import { fireEvent, render, screen, within } from "@testing-library/react";
import type { TypedGraphEdge, TypedGraphNode } from "@multica/core/research";
import type { ResearchGraphNode } from "@multica/core/types";
import enResearch from "../../../locales/en/research.json";

import { buildStarCanvasViewModel } from "../lib/star-canvas-view-model";
import { StarGraphCanvas } from "./star-graph-canvas";
import { emptyCanvasFilter } from "@multica/core/research";

const setViewport = vi.fn();

vi.mock("../../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (bundle: typeof enResearch) => unknown, vars?: Record<string, unknown>) => {
      const raw = selector(enResearch);
      if (typeof raw !== "string" || !vars) return raw;
      return raw.replace(/\{\{(\w+)\}\}/g, (_, key: string) => String(vars[key] ?? ""));
    },
  }),
}));

vi.mock("@multica/core/research", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/research")>();
  return {
    ...actual,
    useResearchCanvasStore: (selector: (state: {
      viewport: null;
      filter: ReturnType<typeof emptyCanvasFilter>;
      setViewport: typeof setViewport;
    }) => unknown) =>
      selector({
        viewport: null,
        filter: emptyCanvasFilter(),
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
    expect(screen.getByRole("button", { name: "Zoom out" })).toBeTruthy();
    expect(screen.getByText("Support")).toBeTruthy();
    expect(screen.getByText("IMPORTANT RESULT")).toBeTruthy();
    expect(screen.getByTestId("star-graph-summary").textContent).toContain("调研星图");
    expect(
      within(screen.getByRole("button", { name: /Stable A/ })).getByTestId("star-graph-document-badge")
        .textContent,
    ).toBe("DOC · 12");
    expect(screen.getByRole("button", { name: /Stable A/ }).textContent).not.toContain(
      "12 文档",
    );
  });

  it("selects a node without treating map-key buttons as canvas nodes", () => {
    const onSelectNode = vi.fn();
    render(<StarGraphCanvas model={fixtureModel()} onSelectNode={onSelectNode} />);

    fireEvent.click(screen.getByRole("button", { name: /Stable A/ }));
    expect(onSelectNode).toHaveBeenCalledWith("stable-a");
  });

  it("dispatches one open command when select and open handlers are both provided", () => {
    const onSelectNode = vi.fn();
    const onOpenNode = vi.fn();
    render(
      <StarGraphCanvas
        model={fixtureModel()}
        onSelectNode={onSelectNode}
        onOpenNode={onOpenNode}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: /Stable A/ }));

    expect(onOpenNode).toHaveBeenCalledOnce();
    expect(onOpenNode).toHaveBeenCalledWith("stable-a");
    expect(onSelectNode).not.toHaveBeenCalled();
  });

  it("delegates expandable nodes to the server-backed one-layer toggle", () => {
    const onSelectNode = vi.fn();
    const onOpenNode = vi.fn();
    const onToggleNode = vi.fn();
    render(
      <StarGraphCanvas
        model={fixtureModel()}
        onSelectNode={onSelectNode}
        onOpenNode={onOpenNode}
        expansionControl={{
          expandableNodeIds: new Set(["stable-a"]),
          expandedNodeIds: new Set(),
          loadingNodeIds: new Set(["stable-a"]),
          onToggleNode,
        }}
      />,
    );

    const node = screen.getByRole("button", { name: /Stable A/ });
    expect(node).toHaveAttribute("aria-expanded", "false");
    expect(node).toHaveAttribute("aria-busy", "true");
    fireEvent.click(node);

    expect(onSelectNode).toHaveBeenCalledWith("stable-a");
    expect(onToggleNode).toHaveBeenCalledWith("stable-a");
    expect(onOpenNode).not.toHaveBeenCalled();
  });

  it("keeps a failed expansion retryable without changing canonical graph data", () => {
    const onToggleNode = vi.fn();
    render(
      <StarGraphCanvas
        model={fixtureModel()}
        expansionControl={{
          expandableNodeIds: new Set(["stable-a"]),
          expandedNodeIds: new Set(),
          failedNodeIds: new Set(["stable-a"]),
          failureLabel: "Expansion failed; activate to retry",
          onToggleNode,
        }}
      />,
    );

    const node = screen.getByRole("button", {
      name: /Stable A.*Expansion failed; activate to retry/,
    });
    expect(node).toHaveAttribute("aria-invalid", "true");
    expect(node).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(node);
    expect(onToggleNode).toHaveBeenCalledWith("stable-a");
    expect(screen.getAllByTestId("star-graph-node")).toHaveLength(3);
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

    const selected = screen.getByRole("button", { name: /Stable A/ });
    expect(selected).toHaveAttribute("tabindex", "0");
    expect(screen.getByRole("button", { name: /Research goal/ })).toHaveAttribute(
      "tabindex",
      "-1",
    );
    selected.focus();
    fireEvent.keyDown(selected, { key: "Home" });
    expect(onSelectNode).toHaveBeenCalledWith("goal");
    expect(screen.getByTestId("star-graph-canvas-live").textContent).toContain("Research goal");
  });

  it("keeps only the first visible node in the tab order before selection", () => {
    render(<StarGraphCanvas model={fixtureModel()} keyboardNav={{ nodes: [], edges: [] }} />);
    const nodes = screen.getAllByTestId("star-graph-node");
    expect(nodes.filter((entry) => entry.getAttribute("tabindex") === "0")).toHaveLength(1);
    expect(nodes[0]).toHaveAttribute("tabindex", "0");
  });

  it("moves DOM focus to the node selected by keyboard navigation", () => {
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
    function Harness() {
      const [selected, setSelected] = useState("stable-a");
      return (
        <StarGraphCanvas
          model={fixtureModel()}
          selectedNodeId={selected}
          onSelectNode={setSelected}
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
        />
      );
    }
    render(<Harness />);
    const stable = screen.getByRole("button", { name: /Stable A/ });
    stable.focus();
    fireEvent.keyDown(stable, { key: "Home" });

    const goal = screen.getByRole("button", { name: /Research goal/ });
    expect(document.activeElement).toBe(goal);
    expect(goal).toHaveAttribute("tabindex", "0");
    expect(stable).toHaveAttribute("tabindex", "-1");
  });

  it("renders the load-more control when pagination is available", () => {
    const onLoadMore = vi.fn();
    render(
      <StarGraphCanvas
        model={buildStarCanvasViewModel({
          nodes: [node({ id: "goal", node_type: "goal", title: "Goal", level: "xxl" })],
          edges: [],
        })}
        loadMoreLabel="Load more (21 remaining)"
        onLoadMore={onLoadMore}
      />,
    );

    const loadMore = screen.getByTestId("star-graph-load-more");
    expect(loadMore.textContent).toContain("Load more");
    fireEvent.click(loadMore);
    expect(onLoadMore).toHaveBeenCalledTimes(1);
  });
});
