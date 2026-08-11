import type { TypedGraphNode } from "@multica/core/research";

export interface ResearchD5Summary {
  loadedDirections: number;
  totalDirections: number | null;
  stableResults: number;
  activeProbes: number;
  newFrontiers: number;
  stoppedDirections: number;
}

const STABLE_LEVELS = new Set(["l", "xl", "xxl"]);
const STOP_STATUSES = new Set(["abandoned", "deprecated", "failed", "superseded", "archived"]);

type ResearchD5SummaryNode = Pick<
  TypedGraphNode,
  "id" | "level" | "status" | "node_type"
> & {
  cluster_id?: TypedGraphNode["cluster_id"];
};

export function summarizeTypedGraph(
  nodes: readonly ResearchD5SummaryNode[],
  options?: { totalNodeCount?: number | null },
): ResearchD5Summary {
  let stableResults = 0;
  let activeProbes = 0;
  let newFrontiers = 0;
  let stoppedDirections = 0;

  for (const node of nodes) {
    const level = (node.level || "").toLowerCase();
    const status = (node.status || "").toLowerCase();

    if (STABLE_LEVELS.has(level)) stableResults += 1;
    if (level === "s") {
      if (STOP_STATUSES.has(status)) stoppedDirections += 1;
      else if (status === "running" || status === "queued" || status === "in_progress") {
        activeProbes += 1;
      }
    }
    if (!node.cluster_id && level !== "xxl" && node.node_type !== "goal") {
      newFrontiers += 1;
    }
  }

  return {
    loadedDirections: nodes.length,
    totalDirections:
      options?.totalNodeCount != null && options.totalNodeCount > nodes.length
        ? options.totalNodeCount
        : null,
    stableResults,
    activeProbes,
    newFrontiers,
    stoppedDirections,
  };
}
