import type { AgentTask } from "../types";
import type { AgentWorkloadDetail, Workload } from "./types";

// Workload is a Task/capacity projection. It never chooses Agent Presence.
export function deriveWorkload(counts: {
  runningCount: number;
  queuedCount: number;
}): Workload {
  if (counts.runningCount > 0) return "working";
  if (counts.queuedCount > 0) return "queued";
  return "idle";
}

export function deriveWorkloadDetail(
  tasks: readonly AgentTask[],
): AgentWorkloadDetail {
  let runningCount = 0;
  let queuedCount = 0;
  for (const task of tasks) {
    if (task.status === "running") {
      runningCount += 1;
    } else if (task.status === "queued" || task.status === "dispatched") {
      queuedCount += 1;
    }
  }
  return {
    workload: deriveWorkload({ runningCount, queuedCount }),
    runningCount,
    queuedCount,
  };
}
