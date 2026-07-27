import { queryOptions, keepPreviousData } from "@tanstack/react-query";
import { api } from "../api";
import type { WorkspaceSearchScope } from "../types";

/**
 * Workspace-level global search query keys + options (LRM-605 BE ↔ LRM-606 FE).
 *
 * The scope tab + query string both participate in the key so switching tabs
 * (全部/Messages/Channels/DMs/People) re-issues against the right server scope
 * and is cached independently.
 */
export const workspaceSearchKeys = {
  all: (wsId: string) => ["workspace-search", wsId] as const,
  search: (
    wsId: string,
    query: string,
    scope: WorkspaceSearchScope,
    limit?: number,
  ) => [...workspaceSearchKeys.all(wsId), query.trim().toLowerCase(), scope, limit ?? null] as const,
};

export function workspaceSearchOptions(
  wsId: string,
  query: string,
  scope: WorkspaceSearchScope,
  opts?: { limit?: number; enabled?: boolean },
) {
  const q = query.trim();
  return queryOptions({
    queryKey: workspaceSearchKeys.search(wsId, q, scope, opts?.limit),
    queryFn: ({ signal }) =>
      api.searchWorkspace(wsId, { q, scope, limit: opts?.limit, signal }),
    // Only search on a non-empty query; the idle state (recent + jump-to) does
    // not hit the network.
    enabled: !!wsId && q.length > 0 && (opts?.enabled ?? true),
    // Keep stale results visible while refetching on scope switch / retype so
    // the list does not flash empty between requests.
    placeholderData: keepPreviousData,
    staleTime: 30_000,
  });
}
