import type { ChannelMessageSearchParams } from "../types";
import { queryOptions, keepPreviousData } from "@tanstack/react-query";
import { api } from "../api";
import type { WorkspaceSearchScope } from "../types";

/**
 * Workspace-level global search query keys + options (LRM-605 BE ↔ LRM-606 FE).
 *
 * LRM-874/873: author_type + author_id (from:@) and include_thread participate
 * in the key so chip / thread-scope toggles refetch independently.
 */
export const workspaceSearchKeys = {
  all: (wsId: string) => ["workspace-search", wsId] as const,
  search: (
    wsId: string,
    query: string,
    scope: WorkspaceSearchScope,
    limit?: number,
    author?: { author_type?: string; author_id?: string; include_thread?: boolean },
  ) =>
    [
      ...workspaceSearchKeys.all(wsId),
      query.trim().toLowerCase(),
      scope,
      limit ?? null,
      author?.author_type ?? null,
      author?.author_id ?? null,
      author?.include_thread ?? null,
    ] as const,
};

export function workspaceSearchOptions(
  wsId: string,
  query: string,
  scope: WorkspaceSearchScope,
  opts?: {
    limit?: number;
    enabled?: boolean;
    author_type?: "user" | "agent";
    author_id?: string;
    include_thread?: boolean;
  },
) {
  const q = query.trim();
  const hasAuthor = !!(opts?.author_type && opts?.author_id);
  return queryOptions({
    queryKey: workspaceSearchKeys.search(wsId, q, scope, opts?.limit, {
      author_type: opts?.author_type,
      author_id: opts?.author_id,
      include_thread: opts?.include_thread,
    }),
    queryFn: ({ signal }) =>
      api.searchWorkspace(wsId, {
        q: q || undefined,
        scope,
        limit: opts?.limit,
        author_type: opts?.author_type,
        author_id: opts?.author_id,
        include_thread: opts?.include_thread,
        signal,
      }),
    // Author-only from:@ may omit q (LRM-874). Keyword search still requires q.
    enabled: !!wsId && (q.length > 0 || hasAuthor) && (opts?.enabled ?? true),
    placeholderData: keepPreviousData,
    staleTime: 30_000,
  });
}

/** Channel-scoped message search with optional from:@ (LRM-873). */
export function channelAuthorMessageSearchOptions(
  channelId: string,
  params: ChannelMessageSearchParams,
) {
  const q = (params.q ?? "").trim();
  const hasAuthor = !!(params.author_type && params.author_id);
  return queryOptions({
    queryKey: [
      "channel-message-search",
      channelId,
      q,
      params.author_type ?? null,
      params.author_id ?? null,
      params.include_thread ?? null,
      params.limit ?? null,
      params.offset ?? null,
    ] as const,
    queryFn: () => api.searchChannelMessages(channelId, params),
    enabled: !!channelId && (q.length > 0 || hasAuthor),
    placeholderData: keepPreviousData,
    staleTime: 30_000,
  });
}
