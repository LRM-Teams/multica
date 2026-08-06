import { describe, expect, it } from "vitest";
import {
  aggregateTreeNavigationReducer,
  aggregateTreePathToNode,
  initialAggregateTreeNavigationState,
  selectAggregateTreeNavigation,
  type AggregateTreeParentIndex,
} from "./aggregate-tree-navigation";

const TREE: AggregateTreeParentIndex = {
  root: null,
  alpha: "root",
  "alpha-leaf": "alpha",
  beta: "root",
  "beta-leaf": "beta",
};

function reduce(
  actions: Parameters<typeof aggregateTreeNavigationReducer>[1][],
) {
  return actions.reduce(
    aggregateTreeNavigationReducer,
    initialAggregateTreeNavigationState,
  );
}

describe("aggregateTreeNavigationReducer", () => {
  it("opens a node path and replaces the old sibling branch while retaining ancestors", () => {
    const state = reduce([
      { type: "open", sessionId: "s1", path: ["root", "alpha", "alpha-leaf"] },
      { type: "select", sessionId: "s1", nodeId: "alpha-leaf" },
      { type: "open", sessionId: "s1", path: ["root", "beta"] },
    ]);

    expect(selectAggregateTreeNavigation(state, "s1")).toEqual({
      breadcrumbs: ["root", "beta"],
      expandedNodeIds: ["root", "beta"],
      focusNodeId: "beta",
      selectedNodeId: "beta",
    });
  });

  it("selects without changing the focus path", () => {
    const state = reduce([
      { type: "open", sessionId: "s1", path: ["root", "alpha"] },
      { type: "select", sessionId: "s1", nodeId: "alpha-leaf" },
    ]);

    expect(selectAggregateTreeNavigation(state, "s1")).toMatchObject({
      breadcrumbs: ["root", "alpha"],
      focusNodeId: "alpha",
      selectedNodeId: "alpha-leaf",
    });
  });

  it("collapses an expanded node to its parent and supports collapsing the root", () => {
    const state = reduce([
      { type: "open", sessionId: "s1", path: ["root", "alpha", "alpha-leaf"] },
      { type: "collapse", sessionId: "s1", nodeId: "alpha" },
    ]);
    expect(selectAggregateTreeNavigation(state, "s1")).toMatchObject({
      breadcrumbs: ["root"],
      focusNodeId: "root",
      selectedNodeId: "root",
    });

    const collapsedRoot = aggregateTreeNavigationReducer(state, {
      type: "collapse",
      sessionId: "s1",
      nodeId: "root",
    });
    expect(selectAggregateTreeNavigation(collapsedRoot, "s1")).toMatchObject({
      breadcrumbs: [],
      focusNodeId: null,
      selectedNodeId: null,
    });
  });

  it("jumps to any ancestor and ignores nodes outside the current focus path", () => {
    const opened = reduce([
      { type: "open", sessionId: "s1", path: ["root", "alpha", "alpha-leaf"] },
    ]);
    const jumped = aggregateTreeNavigationReducer(opened, {
      type: "jumpToAncestor",
      sessionId: "s1",
      nodeId: "alpha",
    });
    expect(selectAggregateTreeNavigation(jumped, "s1")).toMatchObject({
      breadcrumbs: ["root", "alpha"],
      focusNodeId: "alpha",
      selectedNodeId: "alpha",
    });

    expect(
      aggregateTreeNavigationReducer(jumped, {
        type: "jumpToAncestor",
        sessionId: "s1",
        nodeId: "beta",
      }),
    ).toBe(jumped);
  });

  it("restores the longest still-valid ancestor path after tree replacement", () => {
    const opened = reduce([
      { type: "open", sessionId: "s1", path: ["root", "alpha", "alpha-leaf"] },
      { type: "select", sessionId: "s1", nodeId: "alpha-leaf" },
    ]);

    const removedLeaf = aggregateTreeNavigationReducer(opened, {
      type: "replaceTree",
      sessionId: "s1",
      parentById: { root: null, alpha: "root", beta: "root" },
    });
    expect(selectAggregateTreeNavigation(removedLeaf, "s1")).toMatchObject({
      breadcrumbs: ["root", "alpha"],
      focusNodeId: "alpha",
      selectedNodeId: "alpha",
    });

    const mergedBranch = aggregateTreeNavigationReducer(opened, {
      type: "replaceTree",
      sessionId: "s1",
      parentById: { root: null, alpha: "beta", "alpha-leaf": "alpha", beta: "root" },
    });
    expect(selectAggregateTreeNavigation(mergedBranch, "s1")).toMatchObject({
      breadcrumbs: ["root"],
      focusNodeId: "root",
      selectedNodeId: "root",
    });
  });

  it("preserves an existing selection on replacement but removes a deleted ghost selection", () => {
    const opened = reduce([
      { type: "open", sessionId: "s1", path: ["root", "alpha"] },
      { type: "select", sessionId: "s1", nodeId: "beta" },
      { type: "replaceTree", sessionId: "s1", parentById: TREE },
    ]);
    expect(selectAggregateTreeNavigation(opened, "s1").selectedNodeId).toBe("beta");

    const deleted = aggregateTreeNavigationReducer(opened, {
      type: "replaceTree",
      sessionId: "s1",
      parentById: { root: null, alpha: "root" },
    });
    expect(selectAggregateTreeNavigation(deleted, "s1").selectedNodeId).toBe("alpha");
  });

  it("isolates navigation by sessionId without storing a server tree", () => {
    const state = reduce([
      { type: "open", sessionId: "s1", path: ["root", "alpha"] },
      { type: "open", sessionId: "s2", path: ["other-root"] },
      { type: "select", sessionId: "s2", nodeId: "other-root" },
    ]);

    expect(selectAggregateTreeNavigation(state, "s1").breadcrumbs).toEqual(["root", "alpha"]);
    expect(selectAggregateTreeNavigation(state, "s2").breadcrumbs).toEqual(["other-root"]);
    expect(state.sessions.s1).toEqual({
      focusPath: ["root", "alpha"],
      selectedNodeId: "alpha",
    });
    expect(state.sessions.s1).not.toHaveProperty("tree");
    expect(state.sessions.s1).not.toHaveProperty("nodes");
  });

  it("normalizes duplicate path ids so a malformed update cannot create a cycle", () => {
    const state = reduce([
      { type: "open", sessionId: "s1", path: ["root", "alpha", "root", "beta"] },
    ]);
    expect(selectAggregateTreeNavigation(state, "s1").breadcrumbs).toEqual(["root", "alpha"]);
  });

  it("derives a canonical path from the typed aggregate-tree parent shape", () => {
    expect(aggregateTreePathToNode(TREE, "alpha-leaf")).toEqual([
      "root",
      "alpha",
      "alpha-leaf",
    ]);
    expect(aggregateTreePathToNode({ root: null, alpha: "beta", beta: "alpha" }, "alpha")).toEqual([]);
    expect(aggregateTreePathToNode(TREE, "missing")).toEqual([]);
  });
});
