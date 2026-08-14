import type { TypedGraphResponse } from "@multica/core/research";

export interface PrematureGateDiagnostic {
  runId: string;
  stage: string;
  sessionStatus: string;
  findingCodes: string[];
  gateNodeIds: string[];
}

export interface GuardedResearchProjection {
  graph: TypedGraphResponse | undefined;
  diagnostic: PrematureGateDiagnostic | null;
}

const PRE_DELIVERY_STAGES = new Set(["s1_plan", "s2_sources"]);

function gateFindingCodes(payload: Record<string, unknown>): string[] {
  const gate = payload.gate;
  if (!gate || typeof gate !== "object" || Array.isArray(gate)) return [];
  const findings = (gate as Record<string, unknown>).findings;
  if (!Array.isArray(findings)) return [];
  return findings.flatMap((finding) => {
    if (!finding || typeof finding !== "object" || Array.isArray(finding)) return [];
    const code = (finding as Record<string, unknown>).code;
    return typeof code === "string" && code.trim() ? [code.trim()] : [];
  });
}

/**
 * Prevents an unevaluated delivery gate from masquerading as a failed result.
 * The mismatch remains explicit through `diagnostic`; task/attempt failures are
 * deliberately preserved because they are real execution state.
 */
export function guardPrematureGateProjection(args: {
  graph: TypedGraphResponse | undefined;
  runId: string;
  stage: string;
  sessionStatus: string;
}): GuardedResearchProjection {
  const { graph, runId, stage, sessionStatus } = args;
  if (!graph || !PRE_DELIVERY_STAGES.has(stage)) {
    return { graph, diagnostic: null };
  }
  if (sessionStatus === "completed" || sessionStatus === "awaiting_user_confirm") {
    return { graph, diagnostic: null };
  }

  const prematureGates = graph.nodes.filter(
    (node) =>
      (node.node_type === "gate" || node.node_type === "stage_gate") &&
      node.status === "failed",
  );
  if (prematureGates.length === 0) return { graph, diagnostic: null };

  const gateNodeIds = prematureGates.map((node) => node.id);
  const hidden = new Set(gateNodeIds);
  const findingCodes = Array.from(
    new Set(
      prematureGates.flatMap((node) =>
        gateFindingCodes(
          node.payload && typeof node.payload === "object" && !Array.isArray(node.payload)
            ? (node.payload as Record<string, unknown>)
            : {},
        ),
      ),
    ),
  ).sort();

  return {
    graph: {
      ...graph,
      total_node_count:
        graph.total_node_count == null
          ? null
          : Math.max(0, graph.total_node_count - gateNodeIds.length),
      nodes: graph.nodes
        .filter((node) => !hidden.has(node.id))
        .map((node) => ({
          ...node,
          child_ids: node.child_ids?.filter((id) => !hidden.has(id)),
          children_of: node.children_of?.filter((id) => !hidden.has(id)),
        })),
      edges: graph.edges.filter(
        (edge) => !hidden.has(edge.from_node_id) && !hidden.has(edge.to_node_id),
      ),
    },
    diagnostic: { runId, stage, sessionStatus, findingCodes, gateNodeIds },
  };
}

export function formatPrematureGateDiagnostic(
  diagnostic: PrematureGateDiagnostic,
): string {
  return [
    "Research V6 projection contract mismatch",
    `run_id=${diagnostic.runId}`,
    `current_stage=${diagnostic.stage}`,
    `session_status=${diagnostic.sessionStatus}`,
    `finding_codes=${diagnostic.findingCodes.join(",") || "unknown"}`,
    `gate_node_ids=${diagnostic.gateNodeIds.join(",")}`,
    "Expected: a failed delivery gate must not be projected during s1_plan or s2_sources.",
    "Backend fix: only project the gate after an explicit quality evaluation or in an awaiting/completed/terminal state.",
  ].join("\n");
}
