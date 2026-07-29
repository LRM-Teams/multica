import type { WorkspaceSearchResponse, WorkspaceSearchScope } from "@multica/core/types";

/**
 * Global search status state machine (LRM-606 / Lock A).
 *
 * Pure derivation from (query, isFetching, data, error) so it is unit-testable
 * without rendering. The states map 1:1 to the design's locked states:
 *
 *  - idle      → no query: show recent searches + jump-to suggestions
 *  - loading   → skeleton (first fetch for this query; placeholderData keeps
 *                prior rows on re-fetch, so loading only blankets the first hit)
 *  - success   → results render
 *  - empty     → query returned no visible matches (viewer-filtered; AC#3 b)
 *  - error     → fetch failed; retryable, no silent fallback (LRM-238)
 */
export type GlobalSearchStatus =
  | "idle"
  | "loading"
  | "success"
  | "empty"
  | "error";

export interface GlobalSearchStatusInput {
  query: string;
  isFetching: boolean;
  isLoading: boolean;
  isError: boolean;
  data: WorkspaceSearchResponse | undefined;
}

export function deriveGlobalSearchStatus(input: GlobalSearchStatusInput): GlobalSearchStatus {
  const q = input.query.trim();
  if (!q) return "idle";

  // Error wins over stale data — never silently fall back to old results.
  if (input.isError) return "error";

  if (input.isFetching && !input.data) return "loading";

  const data = input.data;
  if (!data) {
    // Not fetching, not error, no data: treat as loading (e.g. between mounts).
    return input.isLoading ? "loading" : "idle";
  }

  const hasAny =
    data.messages.length > 0 ||
    data.channels.length > 0 ||
    data.dms.length > 0 ||
    data.people.length > 0;
  return hasAny ? "success" : "empty";
}

/** Total hits for a scope tab badge, independent of which scope is active. */
export function scopeCount(data: WorkspaceSearchResponse | undefined, scope: WorkspaceSearchScope): number {
  if (!data) return 0;
  switch (scope) {
    case "messages":
      return data.counts.messages;
    case "channels":
      return data.counts.channels;
    case "dms":
      return data.counts.dms;
    case "people":
      return data.counts.people;
    case "all":
    default:
      return (
        data.counts.messages +
        data.counts.channels +
        data.counts.dms +
        data.counts.people
      );
  }
}
