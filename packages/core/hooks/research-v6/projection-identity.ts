import type {
  ResearchV6Delta,
  ResearchV6Snapshot,
} from "../../types/research-v6";

function belongsToRun(id: string, runId: string): boolean {
  return id.startsWith(`${runId}:`);
}

/** Validate every identity-bearing fact in a run-scoped snapshot. */
export function isResearchV6SnapshotForRun(
  snapshot: ResearchV6Snapshot,
  runId: string,
): boolean {
  return (
    snapshot.run_id === runId &&
    snapshot.nodes.every((node) => node.run_id === runId) &&
    snapshot.edges.every((edge) => edge.run_id === runId)
  );
}

/**
 * A workspace WS bus carries events for many runs. Upserts carry run_id and
 * tombstones carry stable run-prefixed IDs. A mutation-free frame needs an
 * explicit passthrough run_id; otherwise it cannot be routed safely.
 */
export function isResearchV6DeltaForRun(
  delta: ResearchV6Delta,
  runId: string,
): boolean {
  const explicitRunId = (delta as ResearchV6Delta & { run_id?: unknown }).run_id;
  if (typeof explicitRunId === "string" && explicitRunId !== runId) return false;
  if (delta.node_upserts.some((node) => node.run_id !== runId)) return false;
  if (delta.edge_upserts.some((edge) => edge.run_id !== runId)) return false;
  if (delta.node_tombstones.some((id) => !belongsToRun(id, runId))) return false;
  if (delta.edge_tombstones.some((id) => !belongsToRun(id, runId))) return false;

  const hasRoutableMutation =
    delta.node_upserts.length > 0 ||
    delta.edge_upserts.length > 0 ||
    delta.node_tombstones.length > 0 ||
    delta.edge_tombstones.length > 0;
  return hasRoutableMutation || explicitRunId === runId;
}
