import type { SessionStatusFilter } from "./session-list-filter";

/** LRM-1115 D-IX — tab-session restore of list filters + scroll + focus row. */
export const RESEARCH_LIST_FILTER_STORAGE_KEY = "research.list.filter.v1";

export type ResearchListPersistState = {
  q: string;
  status: SessionStatusFilter | null;
  scroll: number;
  sessionId: string | null;
};

const STATUS_VALUES = new Set<SessionStatusFilter>([
  "in_progress",
  "completed",
  "failed",
]);

export function readResearchListPersist(): ResearchListPersistState | null {
  if (typeof sessionStorage === "undefined") return null;
  try {
    const raw = sessionStorage.getItem(RESEARCH_LIST_FILTER_STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<ResearchListPersistState>;
    const status =
      parsed.status == null
        ? null
        : STATUS_VALUES.has(parsed.status)
          ? parsed.status
          : null;
    return {
      q: typeof parsed.q === "string" ? parsed.q : "",
      status,
      scroll: typeof parsed.scroll === "number" ? parsed.scroll : 0,
      sessionId: typeof parsed.sessionId === "string" ? parsed.sessionId : null,
    };
  } catch {
    return null;
  }
}

export function writeResearchListPersist(state: ResearchListPersistState): void {
  if (typeof sessionStorage === "undefined") return;
  try {
    sessionStorage.setItem(RESEARCH_LIST_FILTER_STORAGE_KEY, JSON.stringify(state));
  } catch {
    // Quota / private mode — drop silently; navigation still works.
  }
}

export function clearResearchListPersist(): void {
  if (typeof sessionStorage === "undefined") return;
  try {
    sessionStorage.removeItem(RESEARCH_LIST_FILTER_STORAGE_KEY);
  } catch {
    // ignore
  }
}
