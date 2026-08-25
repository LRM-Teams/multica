import { DONE_STATUSES, FAILED_STATUSES } from "./session-list-filter";

/** LRM-832 — session terminal guide surface. */
export type CompletionGuideKind = "done" | "failed";

export function resolveCompletionGuideKind(
  status: string | null | undefined,
): CompletionGuideKind | null {
  if (!status) return null;
  if (DONE_STATUSES.has(status)) return "done";
  if (FAILED_STATUSES.has(status)) return "failed";
  return null;
}
