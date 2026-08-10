import type { QueryClient } from "@tanstack/react-query";
import type { AgentTask } from "../types";
import { agentTaskSnapshotKeys } from "./queries";

const TERMINAL_STATUSES: ReadonlySet<AgentTask["status"]> = new Set([
  "completed",
  "failed",
  "cancelled",
]);

/**
 * step② activity WS-delta — in-place patch of the workspace
 * `agent-task-snapshot` cache from a single task lifecycle WS event.
 *
 * Replaces the previous amplifier where EVERY task event invalidated the whole
 * query and triggered a full-workspace refetch (×every connected client). The
 * snapshot is a raw `AgentTask[]` over which Workload counts are derived
 * client-side — `deriveWorkloadDetail` reads only `t.status` — so updating the
 * matching row's status is sufficient and O(1), with zero network traffic.
 *
 * Returns `true` when the event was fully handled in-place (row patched, no-op,
 * or an untracked terminal that never needed to appear). Returns `false` only
 * for a brand-new non-terminal task not yet in the cache: `AgentTask` carries
 * required fields (`runtime_id`, `priority`, `created_at`, …) the event payload
 * can't supply, so a bare insert isn't possible — the caller coalesces a single
 * snapshot refetch per burst instead of one refetch per event. Drift from any
 * missed event self-heals via the query's 30s staleTime + refetchOnWindowFocus.
 *
 * GUARDRAIL (Wren review, #1280): patching only `status` is correct because the
 * sole snapshot consumer — `deriveWorkloadDetail` — reads only `t.status`. If a
 * future consumer starts reading a MUTABLE non-status field off the snapshot
 * array, a status-only patch would leave that field stale until the 30s
 * staleTime; such a consumer must either patch that field here too or read it
 * from its own live source.
 */
export function patchAgentTaskSnapshotStatus(
  qc: QueryClient,
  wsId: string,
  taskId: string,
  status: AgentTask["status"],
): boolean {
  const key = agentTaskSnapshotKeys.list(wsId);
  const existing = qc.getQueryData<AgentTask[]>(key);
  // Not cached / query never mounted → the next mount fetches fresh; nothing to
  // patch and no refetch to schedule.
  if (!existing) return true;

  const idx = existing.findIndex((t) => t.id === taskId);
  const current = idx === -1 ? undefined : existing[idx];
  if (!current) {
    // Unknown task. A terminal event for a task we never tracked changes no
    // active count, so ignore it; a non-terminal one must appear → refetch.
    return TERMINAL_STATUSES.has(status);
  }

  if (current.status === status) return true; // already up to date

  const next = existing.slice();
  next[idx] = { ...current, status };
  qc.setQueryData(key, next);
  return true;
}
