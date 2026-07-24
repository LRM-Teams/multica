import type { ChannelActiveTask } from "@multica/core/types";

/**
 * LRM-287 — composer activity strip shows only in-conversation agent
 * reply/running rows (+ Stop). Issue-creation work (quick-create / issue_create)
 * stays in Activity / system events, not above the composer.
 */
export const COMPOSER_STRIP_EXCLUDED_TASK_KINDS = new Set([
  "quick_create",
  "issue_create",
]);

/** Inbox wake reasons that are not a direct reply/run for this conversation. */
const COMPOSER_STRIP_EXCLUDED_INBOX_REASONS = new Set([
  "ambient",
  "channel_onboarding",
]);

export function isComposerStripReplyTask(task: ChannelActiveTask): boolean {
  const kind = task.kind?.trim();
  if (kind && COMPOSER_STRIP_EXCLUDED_TASK_KINDS.has(kind)) {
    return false;
  }
  const reason = task.reason?.trim();
  if (reason && COMPOSER_STRIP_EXCLUDED_INBOX_REASONS.has(reason)) {
    return false;
  }
  return true;
}

export function filterComposerStripTasks(tasks: readonly ChannelActiveTask[]): ChannelActiveTask[] {
  const next: ChannelActiveTask[] = [];
  for (const task of tasks) {
    if (isComposerStripReplyTask(task)) {
      next.push(task);
    }
  }
  return next;
}

/** Terminal outcome rows are history facts — they are not cancelable. */
export function isTerminalChannelActiveTask(task: ChannelActiveTask): boolean {
  return typeof task.outcome === "string";
}

/**
 * LRM-405 — every non-terminal active task in the channel (all kinds).
 * Used by the header Stop-all entry so "stop all running agents" covers
 * reply runs and issue-create work alike — no silent subset.
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
