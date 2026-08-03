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

/** Collapse whitespace for equality / prefix checks (LRM-1104). */
export function normalizeListText(text: string): string {
  return text.trim().replace(/\s+/g, " ");
}

/**
 * True when two texts are equal or either is a prefix of the other
 * (after whitespace collapse). Used to drop redundant goal chips.
 */
export function textsAreEqualOrMutualPrefix(a: string, b: string): boolean {
  const left = normalizeListText(a);
  const right = normalizeListText(b);
  if (!left || !right) return left === right;
  return left === right || left.startsWith(right) || right.startsWith(left);
}

/**
 * LRM-1104: hide goal chip when it duplicates the row title.
 * Compare raw title (or goal when title is empty) against goal — not the
 * truncated chip/title display strings, which break mutual-prefix checks
 * once ellipsis is appended at different lengths.
 */
export function shouldShowSessionGoalChip(
  session: Pick<ResearchSession, "title" | "goal">,
): boolean {
  const goal = normalizeListText(session.goal || "");
  if (!goal) return false;
  const title = normalizeListText(session.title || "");
  const titleSource = title || goal;
  return !textsAreEqualOrMutualPrefix(titleSource, goal);
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
