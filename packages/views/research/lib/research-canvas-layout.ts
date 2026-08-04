import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";
import { selectAggregateTree, selectAggregateTreeColumns } from "./aggregate-tree";
import { layoutAggregateTreeShell, layoutResearchGraph } from "./layout-graph";

/**
 * Selects the canonical aggregate-tree window when the server supplies the
 * complete LRM-1278 projection. The Git topology remains available to the
 * existing keyboard model, while incomplete snapshots retain the Git canvas.
 */
export function layoutResearchCanvas(
  nodes: ResearchGraphNode[],
  edges: ResearchGraphEdge[],
) {
  const gitLayout = layoutResearchGraph(nodes, edges);
  const columns = selectAggregateTreeColumns(selectAggregateTree(nodes));

  if (!columns) {
    return { mode: "git" as const, layout: gitLayout, topology: gitLayout.topology };
  }

  return {
    mode: "aggregate" as const,
    layout: layoutAggregateTreeShell({
      parent: columns.root.node,
      siblings: columns.branches.map((entry) => entry.node),
      children: columns.leaves.map((entry) => entry.node),
      edges,
    }),
    topology: gitLayout.topology,
  };
}
