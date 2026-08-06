export type StoppableAgentTask = {
  agent_id: string;
  task_id: string;
  status: string;
  outcome?: string | null;
};

/**
 * LRM-589 — pick the DM Stop target for AgentProfileActions.
 * Prefer a running task; otherwise the first queued/non-terminal row.
 * Terminal outcome rows are history — not cancelable.
 */
export function pickStoppableDmTask(
  tasks: readonly StoppableAgentTask[],
  agentId: string,
): StoppableAgentTask | null {
  let queued: StoppableAgentTask | null = null;
  for (const task of tasks) {
    if (task.agent_id !== agentId) continue;
    if (typeof task.outcome === "string") continue;
    if (task.status === "running") return task;
    if (!queued) queued = task;
  }
  return queued;
}
