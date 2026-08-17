import type { TypedGraphResponse } from "@multica/core/research";
import type {
  ResearchV6DirectorDensityBin,
  ResearchV6DirectorProjectionEdge,
  ResearchV6DirectorProjectionNode,
  ResearchV6DirectorProjectionSnapshot,
} from "@multica/core/types/research-v6-director";

export interface ResearchV6DirectorCanvasProjection {
  runId: string;
  eventSequence: number;
  nodes: readonly ResearchV6DirectorProjectionNode[];
  edges: readonly ResearchV6DirectorProjectionEdge[];
  densityBins?: readonly ResearchV6DirectorDensityBin[];
}

export interface ResearchV6DirectorCanvasAdapterResult {
  graph: TypedGraphResponse;
  expandableNodeIds: ReadonlySet<string>;
  hiddenChildCountByNodeId: ReadonlyMap<string, number>;
  densityBins: readonly ResearchV6DirectorDensityBin[];
}

function rendererStatus(node: ResearchV6DirectorProjectionNode): string {
  if (node.state.integration === "absorbed" || node.state.integration === "excluded") {
    return "abandoned";
  }
  if (node.state.conclusion === "challenged") return "conflict";
  if (node.state.conclusion === "refuted" || node.state.conclusion === "invalid") {
    return "abandoned";
  }
  if (node.state.execution === "running") return "running";
  if (node.state.execution === "failed" || node.state.execution === "lost") {
    return "failed";
  }
  if (node.state.execution === "cancelled") return "cancelled";
  if (node.state.conclusion === "accepted") return "accepted";
  if (node.state.execution === "succeeded") return "succeeded";
  return "pending";
}

/**
 * Adapts only explicit Projection fields. It never calculates tier, absorption,
 * parenthood, confidence, or graph membership from text or node counts.
 */
export function adaptResearchV6DirectorCanvas(
  projection: ResearchV6DirectorCanvasProjection,
): ResearchV6DirectorCanvasAdapterResult {
  const absorbedInputs = new Map<string, string[]>();
  for (const edge of projection.edges) {
    if (edge.kind !== "absorbed_into") continue;
    const inputs = absorbedInputs.get(edge.to_node_id) ?? [];
    inputs.push(edge.from_node_id);
    absorbedInputs.set(edge.to_node_id, inputs);
  }

  const expandableNodeIds = new Set<string>();
  const hiddenChildCountByNodeId = new Map<string, number>();
  for (const node of projection.nodes) {
    if (node.expandable) expandableNodeIds.add(node.id);
    hiddenChildCountByNodeId.set(node.id, node.hidden_child_count);
  }

  const merged: Record<string, string[]> = {};
  for (const [successorId, inputIds] of absorbedInputs) {
    merged[successorId] = [...new Set(inputIds)];
  }

  return {
    graph: {
      session_id: projection.runId,
      graph_version: projection.eventSequence,
      total_node_count: projection.nodes.length,
      nodes: projection.nodes.map((node) => ({
        id: node.id,
        session_id: projection.runId,
        node_type: node.kind,
        title: node.title ?? node.catalog_summary,
        summary: node.catalog_summary,
        status: rendererStatus(node),
        actor_agent_id: null,
        payload: {
          canonical_ref: node.canonical_ref,
          branch_ids: node.branch_ids,
          projection_state: node.state,
          projection_tier: node.tier,
          absorbed: node.absorbed,
          terminal: node.terminal,
          expandable: node.expandable,
          hidden_child_count: node.hidden_child_count,
        },
        // Goal is outside the S→XXL knowledge ladder. The existing D5 renderer
        // has no Goal tier token, so it uses the top-size presentation while
        // retaining canonical tier=GOAL in payload.
        level: node.tier === "GOAL" ? "xxl" : node.tier.toLowerCase(),
        cluster_id: null,
        confidence: null,
        goal_version_id: null,
        derived_from: null,
        merged_from: merged[node.id] ?? [],
        superseded_by: null,
        restart_of: null,
        invalidated_by: null,
        superseded_at: null,
        invalidated_at: null,
        parent_id: null,
        child_ids: [],
        children_of: [],
        created_at: "",
        updated_at: node.updated_at,
      })),
      edges: projection.edges.map((edge) => ({
        id: edge.id,
        session_id: projection.runId,
        from_node_id: edge.from_node_id,
        to_node_id: edge.to_node_id,
        edge_type: edge.kind,
        created_at: "",
      })),
      clusters: [],
      lineage: {
        derived: {},
        merged,
        superseded: {},
        restarted: {},
        invalidated: {},
        supersedes: {},
      },
    },
    expandableNodeIds,
    hiddenChildCountByNodeId,
    densityBins: projection.densityBins ?? [],
  };
}

export function adaptResearchV6DirectorSnapshot(
  snapshot: ResearchV6DirectorProjectionSnapshot,
): ResearchV6DirectorCanvasAdapterResult {
  return adaptResearchV6DirectorCanvas({
    runId: snapshot.run_id,
    eventSequence: snapshot.through_event_sequence,
    nodes: snapshot.nodes,
    edges: snapshot.edges,
    densityBins: snapshot.density_bins,
  });
}
