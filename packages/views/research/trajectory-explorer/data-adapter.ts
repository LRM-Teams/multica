import type {
  ResearchGraphEdge,
  ResearchGraphNode,
} from "@multica/core/types";
import type { TrajectoryCommit } from "@multica/core/research";
import { resolveLogicStatus } from "../lib/logic-lanes";

/**
 * LRM-1480 / UI-06: read-only consumption adapter.
 *
 * Maps the session snapshot (ResearchGraphNode[] + ResearchGraphEdge[]) to the
 * `TrajectoryCommit[]` shape consumed by `buildTrajectoryLaneLayout`. The view
 * never infers canonical research state from summary/chat/animation — status is
 * derived only through `resolveLogicStatus` (existing logic-lanes semantics),
 * parentage only from typed edges, and branch identity only from the node's
 * own lane/actor projection. Anything the server did not provide stays absent;
 * missing parents surface through lane-layout `issues`, never synthetic edges.
 */

/** Branch identity used for lane assignment. */
function branchKeyFor(
  node: ResearchGraphNode,
  actorIds: ReadonlySet<string>,
): string {
  const theme = node.theme_key?.trim();
  if (theme) return theme;
  if (node.actor_agent_id && actorIds.has(node.actor_agent_id)) {
    return node.actor_agent_id;
  }
  return `type:${node.node_type}`;
}

/**
 * Deterministic read-only adapter: nodes + typed edges → TrajectoryCommit[].
 * Commits are ordered by `created_at` (stable within a slice), which keeps
 * lane assignment deterministic for the same input.
 */
export function deriveTrajectoryCommits(
  nodes: readonly ResearchGraphNode[],
  edges: readonly ResearchGraphEdge[],
): TrajectoryCommit[] {
  const actorIds = new Set<string>();
  for (const n of nodes) if (n.actor_agent_id) actorIds.add(n.actor_agent_id);

  const byId = new Map(nodes.map((n) => [n.id, n]));
  const parentByChild = new Map<string, string[]>();
  const allowed = new Set([
    "leads_to",
    "derived_from",
    "derives_from",
    "integrates",
    "merged_from",
    "refines",
    "restart_of",
  ]);
  for (const e of edges) {
    if (!allowed.has(e.edge_type)) continue;
    if (!byId.has(e.from_node_id) || !byId.has(e.to_node_id)) continue;
    const list = parentByChild.get(e.to_node_id) ?? [];
    if (!list.includes(e.from_node_id)) list.push(e.from_node_id);
    parentByChild.set(e.to_node_id, list);
  }

  const commits: TrajectoryCommit[] = nodes
    .filter((n) => n.id && n.id !== "__research_logic_start__" && n.id !== "__research_logic_end__")
    .map((n) => {
      const normalizedStatus = (n.status || "").trim().toLowerCase();
      const status =
        n.node_type === "agent" &&
        (normalizedStatus === "idle" || normalizedStatus === "offline")
          ? normalizedStatus
          : resolveLogicStatus(n).tone;
      return {
        id: n.id,
        branchKey: branchKeyFor(n, actorIds),
        parentIds: parentByChild.get(n.id) ?? [],
        status,
        label: n.title || n.id,
      };
    })
    // Stable deterministic order: created_at, then id for ties. This keeps the
    // 8+ branch fixture traceable and reflow stable under identical filters.
    .sort((a, b) => {
      const na = byId.get(a.id);
      const nb = byId.get(b.id);
      const ta = na?.created_at ?? "";
      const tb = nb?.created_at ?? "";
      if (ta !== tb) return ta < tb ? -1 : 1;
      return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
    });

  return commits;
}

export interface TrajectoryFilters {
  /** branchKey values to show; empty set = all. */
  branches: ReadonlySet<string>;
  /** actorAgentId values to show; empty set = all. */
  agents: ReadonlySet<string>;
  /** relations to hide (main/branch/merge/abandoned). */
  hiddenRelations: ReadonlySet<string>;
}

export const EMPTY_FILTERS: TrajectoryFilters = {
  branches: new Set(),
  agents: new Set(),
  hiddenRelations: new Set(),
};

/**
 * Apply display-only filters over the source nodes before layout derivation.
 * Rebuilding layout from the *filtered* commit set gives deterministic,
 * stable lane reflow (AC3): identical filter params → identical lane order.
 */
export function filterNodesForTrajectory(
  nodes: readonly ResearchGraphNode[],
  filters: TrajectoryFilters,
): ResearchGraphNode[] {
  const branchFiltered: ResearchGraphNode[] =
    filters.branches.size === 0
      ? [...nodes]
      : nodes.filter((n) =>
          filters.branches.has(n.theme_key?.trim() ?? ""),
        );
  if (filters.agents.size === 0) return branchFiltered;
  return branchFiltered.filter(
    (n) => n.actor_agent_id && filters.agents.has(n.actor_agent_id),
  );
}
