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

/** Collapse whitespace and ellipsize for single-line list UI (LRM-906 L1/L2). */
export function truncateOneLine(text: string, maxChars: number): string {
  const t = text.trim().replace(/\s+/g, " ");
  if (t.length <= maxChars) return t;
  if (maxChars <= 1) return "…";
  return `${t.slice(0, maxChars - 1)}…`;
}

/** Short row title: prefer title; else truncate goal — never dual-write full goal. */
export function sessionShortTitle(
  session: Pick<ResearchSession, "title" | "goal">,
  maxChars = 48,
): string {
  const title = session.title?.trim();
  if (title) return truncateOneLine(title, maxChars);
  return truncateOneLine(session.goal || "", maxChars);
}

/** Colored goal chip summary (LRM-906 L2). */
export function sessionGoalSummary(
  session: Pick<ResearchSession, "goal">,
  maxChars = 36,
): string {
  return truncateOneLine(session.goal || "", maxChars);
}

/**
 * LRM-1104: hide the goal chip when it adds no information beyond the row title.
 * Compares display strings after stripping a trailing ellipsis so truncated
 * title/goal fallbacks (equal or mutual prefix) collapse to a single line.
 */
export function isRedundantGoalChip(title: string, goalSummary: string): boolean {
  const chip = goalSummary.trim();
  if (!chip) return true;
  const rowTitle = title.trim();
  if (!rowTitle) return false;

  const stripEllipsis = (value: string) =>
    value.endsWith("…") ? value.slice(0, -1) : value;
  const a = stripEllipsis(rowTitle);
  const b = stripEllipsis(chip);
  if (!a || !b) return a === b;
  return a === b || a.startsWith(b) || b.startsWith(a);
}

/** Goal chip text when it is non-empty and not redundant with the row title. */
export function sessionGoalChipSummary(
  session: Pick<ResearchSession, "title" | "goal">,
  titleMaxChars = 48,
  goalMaxChars = 36,
): string | null {
  const title = sessionShortTitle(session, titleMaxChars);
  const goalSummary = sessionGoalSummary(session, goalMaxChars);
  if (isRedundantGoalChip(title, goalSummary)) return null;
  return goalSummary;
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
