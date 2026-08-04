import type { ResearchSession } from "@multica/core/types";
import {
  DONE_STATUSES,
  FAILED_STATUSES,
  matchesTitleQuery,
  type SessionStatusFilter,
} from "./session-list-filter";

export type SessionListStatusCounts = {
  all: number;
  in_progress: number;
  completed: number;
  failed: number;
};

/** Counts for filter chips — search-aware, status-unaware (full corpus for each bucket). */
export function countSessionsByStatus(
  sessions: ResearchSession[],
  titleQuery: string,
): SessionListStatusCounts {
  const matched = sessions.filter((s) => matchesTitleQuery(s, titleQuery));
  let inProgress = 0;
  let completed = 0;
  let failed = 0;
  for (const s of matched) {
    if (FAILED_STATUSES.has(s.status)) failed += 1;
    else if (DONE_STATUSES.has(s.status)) completed += 1;
    else inProgress += 1;
  }
  return {
    all: matched.length,
    in_progress: inProgress,
    completed,
    failed,
  };
}

export function statusBucketLabelKey(
  status: SessionStatusFilter,
): "in_progress" | "completed" | "failed" {
  return status;
}
