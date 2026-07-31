import type { ResearchSession } from "@multica/core/types";

/** LRM-789 / LRM-818: terminal success group. */
export const DONE_STATUSES = new Set(["completed", "archived"]);

/**
 * LRM-818 "失败" bucket — session status enum has no dedicated failed today;
 * accept common failure strings so the filter is ready when BE emits them.
 */
export const FAILED_STATUSES = new Set(["failed", "error", "cancelled"]);

export type SessionStatusFilter = "in_progress" | "completed" | "failed";

export function sessionDisplayTitle(session: Pick<ResearchSession, "title" | "goal">): string {
  return (session.title || session.goal || "").trim();
}

export function matchesTitleQuery(
  session: Pick<ResearchSession, "title" | "goal">,
  query: string,
): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  // AC: filter by session title; empty title falls back to goal (same as row display).
  return sessionDisplayTitle(session).toLowerCase().includes(q);
}

export function matchesStatusFilter(
  session: Pick<ResearchSession, "status">,
  filter: SessionStatusFilter | null,
): boolean {
  if (!filter) return true;
  const status = session.status;
  switch (filter) {
    case "completed":
      return DONE_STATUSES.has(status);
    case "failed":
      return FAILED_STATUSES.has(status);
    case "in_progress":
      return !DONE_STATUSES.has(status) && !FAILED_STATUSES.has(status);
  }
}

export function filterSessions(
  sessions: ResearchSession[],
  query: string,
  status: SessionStatusFilter | null,
): ResearchSession[] {
  return sessions.filter(
    (s) => matchesTitleQuery(s, query) && matchesStatusFilter(s, status),
  );
}

export function isSessionListFilterActive(
  query: string,
  status: SessionStatusFilter | null,
): boolean {
  return query.trim().length > 0 || status != null;
}
