import type { TypedGraphNode, TypedGraphResponse } from "@multica/core/research";
import { MOTION_D5_STAGGER_CAP } from "../motion/tokens";
import type { ProjectionTransitionEvent } from "../motion/transition-queue";

const RETIRED_STATUSES = new Set([
  "abandoned",
  "deprecated",
  "retired",
  "superseded",
  "archived",
  "obsolete",
]);

function nodeMap(nodes: readonly TypedGraphNode[]): Map<string, TypedGraphNode> {
  return new Map(nodes.map((node) => [node.id, node]));
}

function pushUnique(events: ProjectionTransitionEvent[], event: ProjectionTransitionEvent) {
  const key = `${event.transition_kind}:${event.related_ids.join(",")}:${event.anchor_id ?? ""}`;
  if (events.some((existing) => `${existing.transition_kind}:${existing.related_ids.join(",")}:${existing.anchor_id ?? ""}` === key)) {
    return;
  }
  events.push(event);
}

/**
 * Diff two typed graph snapshots and emit semantic motion events. Pure — never
 * mutates graph data; only reads canonical fields.
 */
export function buildTypedGraphMotionEvents(
  previous: TypedGraphResponse | undefined,
  next: TypedGraphResponse | undefined,
): ProjectionTransitionEvent[] {
  if (!next || next.nodes.length === 0) return [];
  if (!previous || previous.graph_version === next.graph_version) return [];

  const prevById = nodeMap(previous.nodes);
  const events: ProjectionTransitionEvent[] = [];

  for (const node of next.nodes) {
    const prior = prevById.get(node.id);
    if (!prior) {
      if (node.merged_from?.length) {
        pushUnique(events, {
          transition_kind: "integration_formed",
          related_ids: [node.id, ...node.merged_from],
          anchor_id: node.merged_from[0] ?? null,
        });
      } else if (node.restart_of) {
        pushUnique(events, {
          transition_kind: "task_restarted",
          related_ids: [node.id, node.restart_of],
          anchor_id: node.restart_of,
        });
      } else {
        pushUnique(events, {
          transition_kind: "branch_spawned",
          related_ids: [node.id],
          anchor_id: node.parent_id ?? node.derived_from ?? null,
        });
      }
      continue;
    }

    const prevStatus = (prior.status || "").toLowerCase();
    const nextStatus = (node.status || "").toLowerCase();
    if (prevStatus !== nextStatus && RETIRED_STATUSES.has(nextStatus)) {
      pushUnique(events, {
        transition_kind: "node_retired",
        related_ids: [node.id],
        anchor_id: node.parent_id ?? null,
        status: nextStatus,
      });
    }
  }

  if (previous.graph_version !== next.graph_version) {
    const prevGoalVersions = new Set(
      previous.nodes.map((node) => node.goal_version_id).filter(Boolean),
    );
    const impacted = next.nodes
      .filter((node) => node.goal_version_id && !prevGoalVersions.has(node.goal_version_id))
      .map((node) => node.id);
    if (impacted.length > 0) {
      pushUnique(events, {
        transition_kind: "goal_modified",
        related_ids: impacted.slice(0, 12),
        anchor_id: null,
      });
    }
  }

  return events;
}

/**
 * Slice F · resync catch-up guard — do not replay a burst of historical motion
 * when the client missed live deltas (background restore, pagination reset, etc.).
 */
export function shouldSkipTypedGraphMotionCatchUp(
  previous: TypedGraphResponse,
  next: TypedGraphResponse,
  events: readonly ProjectionTransitionEvent[],
): boolean {
  if (events.length === 0) return false;

  const versionDelta = (next.graph_version ?? 0) - (previous.graph_version ?? 0);
  if (versionDelta > 1) return true;

  const prevIds = new Set(previous.nodes.map((node) => node.id));
  let newNodeCount = 0;
  for (const node of next.nodes) {
    if (!prevIds.has(node.id)) newNodeCount += 1;
  }
  if (newNodeCount > MOTION_D5_STAGGER_CAP) return true;
  if (events.length > MOTION_D5_STAGGER_CAP) return true;

  return false;
}
