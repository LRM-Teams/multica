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

export function completionGuideStorageKey(sessionId: string): string {
  return `research.completionGuide.dismissed.${sessionId}`;
}

export function isCompletionGuideDismissed(sessionId: string): boolean {
  if (typeof window === "undefined") return false;
  try {
    return window.localStorage.getItem(completionGuideStorageKey(sessionId)) === "1";
  } catch {
    return false;
  }
}

export function dismissCompletionGuide(sessionId: string): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(completionGuideStorageKey(sessionId), "1");
  } catch {
    /* private mode / quota — ignore; in-memory dismiss still works via React state */
  }
}
