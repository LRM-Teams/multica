import type { TaskMessagePayload } from "@multica/core/types";

/** Chat session title used by research fleet wakes (`research:<sessionUUID>`). */
export function researchWakeChatTitle(sessionId: string): string {
  return `research:${sessionId}`;
}

const STOPPABLE = new Set(["drafting", "running", "awaiting_user_confirm"]);

export function isResearchSessionStoppable(status: string): boolean {
  return STOPPABLE.has(status);
}

/** Coalesce live task transcript text the same way chat TimelineView does. */
export function coalesceStreamText(messages: readonly TaskMessagePayload[]): string {
  let out = "";
  for (const m of messages) {
    if (m.type !== "text") continue;
    if (m.visibility === "diagnostic_only") continue;
    const chunk = m.content ?? "";
    if (!chunk) continue;
    out += chunk;
  }
  return out;
}
