import type { ResearchSession } from "@multica/core/types/research";

export type ResearchAttentionKind =
  | "user_confirmation"
  | "blocked_tasks"
  | "recoverable_failure"
  | "stalled";

export function knownResearchAttentionKind(
  value: string | null | undefined,
): ResearchAttentionKind | null {
  return value === "user_confirmation" ||
    value === "blocked_tasks" ||
    value === "recoverable_failure" ||
    value === "stalled"
    ? value
    : null;
}

export function isActiveResearchSession(session: ResearchSession): boolean {
  if (["completed", "archived", "cancelled"].includes(session.status)) return false;
  return session.status !== "failed" || session.list_progress?.recoverable === true;
}

function researchSessionRank(session: ResearchSession): number {
  const attention = knownResearchAttentionKind(session.list_progress?.attention_kind);
  if (attention === "user_confirmation") return 0;
  if (attention === "blocked_tasks") return 1;
  if (attention === "recoverable_failure") return 2;
  if (attention === "stalled") return 3;
  if (session.status === "running") return 4;
  if (session.status === "paused") return 5;
  return 6;
}

export function activeResearchSessions(sessions: ResearchSession[]): ResearchSession[] {
  return sessions
    .filter(isActiveResearchSession)
    .sort(
      (a, b) =>
        researchSessionRank(a) - researchSessionRank(b) ||
        Date.parse(b.list_progress?.last_progress_at ?? b.updated_at) -
          Date.parse(a.list_progress?.last_progress_at ?? a.updated_at),
    );
}

export function selectedResearchSession(
  sessions: ResearchSession[],
  selectedId: string | null,
): ResearchSession | null {
  const active = activeResearchSessions(sessions);
  return (
    active.find((session) => session.id === selectedId) ??
    active[0] ??
    sessions.find((session) => session.status === "completed") ??
    null
  );
}
