import type { ChannelActiveTask } from "@multica/core/types";

/** Terminal outcome rows are history facts — they are not cancelable. */
export function isTerminalChannelActiveTask(task: ChannelActiveTask): boolean {
  return typeof task.outcome === "string";
}

/**
 * LRM-405 / LRM-581 — every non-terminal active inbox row in the channel.
 * Used by the header Stop-all entry so "stop all running agents" covers reply
 * runs and issue-create work alike — no composer-strip subset or legacy task
 * fallback.
 */
export function listStoppableChannelTasks(
  tasks: readonly ChannelActiveTask[],
): ChannelActiveTask[] {
  const next: ChannelActiveTask[] = [];
  for (const task of tasks) {
    if (isTerminalChannelActiveTask(task)) continue;
    next.push(task);
  }
  return next;
}
