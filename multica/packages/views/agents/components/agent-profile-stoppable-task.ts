import type { ChannelActiveTask } from "@multica/core/types";

/**
 * LRM-589 — pick the DM Stop target for AgentProfileActions.
 * Prefer a running task; otherwise the first queued/non-terminal row.
 * Terminal outcome rows are history — not cancelable.
 */
export function pickStoppableDmTask(
  tasks: readonly ChannelActiveTask[],
  agentId: string,
): ChannelActiveTask | null {
  let queued: ChannelActiveTask | null = null;
  for (const task of tasks) {
    if (task.agent_id !== agentId) continue;
    // Same gate as conversation-activity-tasks.isTerminalChannelActiveTask.
    if (typeof task.outcome === "string") continue;
    if (task.status === "running") return task;
    if (!queued) queued = task;
  }
  return queued;
}
