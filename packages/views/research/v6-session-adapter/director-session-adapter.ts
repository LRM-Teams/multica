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
  /** Composite roots whose one-layer disclosure is currently open. */
  expandedRootIds?: ReadonlySet<string>;
}

export interface ResearchV6DirectorCanvasAdapterResult {
  graph: TypedGraphResponse;
  projectionNodeById: ReadonlyMap<string, ResearchV6DirectorProjectionNode>;
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
  if (node.state.execution === "idle") return "idle";
  if (node.state.execution === "offline") return "offline";
  if (node.state.conclusion === "accepted") return "accepted";
  if (node.state.execution === "succeeded") return "succeeded";
  return "pending";
}

function isAgentProjectionNode(node: ResearchV6DirectorProjectionNode): boolean {
  const refKind = node.canonicalRef.kind;
  return (
    refKind === "agent" ||
    refKind === "pending_agent" ||
    node.kind.trim().toLowerCase() === "agent"
  );
}

function isResearchContentNode(node: ResearchV6DirectorProjectionNode): boolean {
  if (node.absorbed || isAgentProjectionNode(node)) return false;
  const kind = node.kind.trim().toLowerCase();
  if (kind === "goal" || node.tier === "GOAL") return false;
  const refKind = node.canonicalRef.kind;
  return (
    kind === "work_s" ||
    kind === "result_s" ||
    kind === "insight" ||
    refKind === "work_item" ||
    refKind === "result" ||
    refKind === "insight"
  );
}

function rendererLevel(node: ResearchV6DirectorProjectionNode): "xxl" | "xl" | "l" | "m" | "s" {
  switch (node.tier) {
    case "XXL":
      return "xxl";
    case "GOAL":
      // Goal is the compact origin, not the final synthesis. The layout gives
      // it a dedicated 118px semantic radius while M typography keeps the
      // visual hierarchy available for integrated L/XL/XXL results.
      return "m";
    case "XL":
      return "xl";
    case "L":
      return "l";
    case "S":
      return "s";
    case "M":
    default:
      return "m";
  }
}

/**
 * Adapts only explicit Projection fields. It never calculates tier, absorption,
 * parenthood, confidence, or graph membership from text or node counts.
 */
export function adaptResearchV6DirectorCanvas(
  projection: ResearchV6DirectorCanvasProjection,
): ResearchV6DirectorCanvasAdapterResult {
  const projectionNodeById = new Map(
    projection.nodes.map((node) => [node.id, node]),
  );
  const assignedAgentByWorkNodeId = new Map<
    string,
    ResearchV6DirectorProjectionNode
  >();
  for (const edge of projection.edges) {
    if (edge.kind !== "assigned_to") continue;
    const agentNode = projectionNodeById.get(edge.toNodeId);
    if (agentNode?.canonicalRef.kind === "agent") {
      assignedAgentByWorkNodeId.set(edge.fromNodeId, agentNode);
    }
  }

  // Roster circles are a staffing overlay, not knowledge-graph members.
  // Keep them only until the first research Work / Result / Insight appears.
  const absorbedInputs = new Map<string, string[]>();
  const expandedAbsorbedNodeIds = new Set<string>();
  for (const edge of projection.edges) {
    if (edge.kind !== "absorbed_into") continue;
    const inputs = absorbedInputs.get(edge.toNodeId) ?? [];
    inputs.push(edge.fromNodeId);
    absorbedInputs.set(edge.toNodeId, inputs);
    if (projection.expandedRootIds?.has(edge.toNodeId)) {
      expandedAbsorbedNodeIds.add(edge.fromNodeId);
    }
  }

  const staffing = !projection.nodes.some(isResearchContentNode);
  const visibleNodes = projection.nodes.filter((node) => {
    // Absorbed inputs are hidden in the default constellation, but a
    // disclosure slice must be able to reveal the exact inputs of its open
    // successor. The server supplies the canonical absorbed_into edge; the
    // client only uses the explicit expanded-root intent to change visibility.
    if (node.absorbed && !expandedAbsorbedNodeIds.has(node.id)) return false;
    if (isAgentProjectionNode(node)) return staffing;
    return true;
  });
  const visibleNodeIds = new Set(visibleNodes.map((node) => node.id));

  const expandableNodeIds = new Set<string>();
  const hiddenChildCountByNodeId = new Map<string, number>();
  for (const node of visibleNodes) {
    if (node.expandable) expandableNodeIds.add(node.id);
    hiddenChildCountByNodeId.set(node.id, node.hiddenChildCount);
  }

  const merged: Record<string, string[]> = {};
  for (const [successorId, inputIds] of absorbedInputs) {
    merged[successorId] = [...new Set(inputIds)];
  }

  const territoryById = new Map(
    visibleNodes.flatMap((node) =>
      node.territory ? [[node.territory.branchId, node.territory] as const] : [],
    ),
  );
  const territoryIds = [...territoryById.keys()].sort();

  return {
    graph: {
      session_id: projection.runId,
      graph_version: projection.eventSequence,
      total_node_count: visibleNodes.length,
      nodes: visibleNodes.map((node) => {
        const assignedAgent = assignedAgentByWorkNodeId.get(node.id);
        const rosterSatellite = isAgentProjectionNode(node);
        return {
          id: node.id,
          session_id: projection.runId,
          node_type: node.kind,
          title: node.title ?? node.catalogSummary,
          summary: node.catalogSummary,
          status: rendererStatus(node),
          actor_agent_id:
            assignedAgent?.canonicalRef.id ??
            (rosterSatellite ? node.canonicalRef.id : null),
          payload: {
            canonical_ref: node.canonicalRef,
            branch_ids: node.branchIds,
            territory: node.territory,
            projection_state: node.state,
            projection_tier: node.tier,
            assigned_agent: assignedAgent
              ? {
                  id: assignedAgent.canonicalRef.id,
                  name:
                    assignedAgent.title ?? assignedAgent.catalogSummary,
                }
              : undefined,
            // Keep the Goal root semantically distinct from an XXL result. The
            // legacy graph still uses XXL geometry for the largest circle, but
            // downstream surfaces can now render the canonical Goal role.
            semantic_role:
              node.tier === "GOAL" || node.kind.toLowerCase() === "goal"
                ? "goal"
                : rosterSatellite
                  ? "roster"
                  : undefined,
            absorbed: node.absorbed,
            terminal: node.terminal,
            expandable: node.expandable,
            hidden_child_count: node.hiddenChildCount,
          },
          // Unknown future tiers retain their canonical value in payload while
          // degrading to the neutral M visual instead of breaking the canvas.
          level: rendererLevel(node),
          cluster_id: node.territory?.branchId ?? null,
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
          updated_at: node.updatedAt,
        };
      }),
      edges: projection.edges
        .filter(
          (edge) =>
            visibleNodeIds.has(edge.fromNodeId) &&
            visibleNodeIds.has(edge.toNodeId),
        )
        .map((edge) => ({
          id: edge.id,
          session_id: projection.runId,
          from_node_id: edge.fromNodeId,
          to_node_id: edge.toNodeId,
          edge_type: edge.kind,
          created_at: "",
        })),
      clusters: territoryIds.map((territoryId) => ({
        id: territoryId,
        session_id: projection.runId,
        name: territoryById.get(territoryId)?.label ?? territoryId,
        label: territoryById.get(territoryId)?.label ?? territoryId,
        level: "m",
        cluster_type: "branch",
        goal_version_id: null,
        payload: {
          member_node_ids: visibleNodes
            .filter((node) => node.territory?.branchId === territoryId)
            .map((node) => node.id),
        },
        created_at: "",
        updated_at: "",
      })),
      lineage: {
        derived: {},
        merged,
        superseded: {},
        restarted: {},
        invalidated: {},
        supersedes: {},
      },
    },
    projectionNodeById,
    expandableNodeIds,
    hiddenChildCountByNodeId,
    densityBins: projection.densityBins ?? [],
  };
}

export function adaptResearchV6DirectorSnapshot(
  snapshot: ResearchV6DirectorProjectionSnapshot,
): ResearchV6DirectorCanvasAdapterResult {
  return adaptResearchV6DirectorCanvas({
    runId: snapshot.runId,
    eventSequence: snapshot.throughEventSequence,
    nodes: snapshot.nodes,
    edges: snapshot.edges,
    densityBins: snapshot.densityBins,
  });
}
