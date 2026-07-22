import { infiniteQueryOptions, queryOptions, type InfiniteData, type QueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { ChannelMessage, ChannelMessagesPage, ChannelThreadMessagesPage, ListIssuesParams } from "../types";

/**
 * Query params for the group-local Tasks projection (#562). Mirrors the API
 * method's accepted subset — the channel is a discussion context, not an issue
 * owner, so this is a read-only projection over issues anchored to a source
 * message here (1:1 — one issue has at most one source channel).
 */
export type ChannelIssuesParams = Pick<ListIssuesParams, "status" | "assignee_id" | "limit" | "offset">;

export const channelKeys = {
  all: (wsId: string) => ["channels", wsId] as const,
  list: (wsId: string) => [...channelKeys.all(wsId), "list"] as const,
  archivedList: (wsId: string) => [...channelKeys.all(wsId), "archived-list"] as const,
  messages: (channelId: string) => ["channel-messages", channelId] as const,
  messagesPage: (channelId: string) => ["channel-messages-page", channelId] as const,
  messageThread: (channelId: string, messageId: string) => ["channel-message-thread", channelId, messageId] as const,
  messageSearch: (channelId: string, query: string, limit?: number) => ["channel-message-search", channelId, query, limit] as const,
  members: (channelId: string) => ["channel-members", channelId] as const,
  attachments: (channelId: string) => ["channel-attachments", channelId] as const,
  stats: (channelId: string) => ["channel-stats", channelId] as const,
  projectFiles: (channelId: string) => ["channel-project-files", channelId] as const,
  issues: (channelId: string, params?: ChannelIssuesParams) =>
    ["channel-issues", channelId, params ?? {}] as const,
  issuesInfinite: (channelId: string, limit: number) =>
    ["channel-issues", channelId, "infinite", limit] as const,
  // Prefix covering every channel Tasks board query (`issues` + `issuesInfinite`,
  // all channels / page params) — the invalidation target for the issues CRUD /
  // WS-event / reconnect path so a task change refreshes the channel board.
  issuesRoot: () => ["channel-issues"] as const,
};

/**
 * Invalidate every channel Tasks board query (#562), regardless of channel or
 * page params. Wired into the issues CRUD mutations, the issue WS-event
 * updaters, and the reconnect resync so the read-only channel Tasks board
 * refetches whenever a task it shows is created / updated / status-changed /
 * assigned / deleted. The board reads a separate `channel-issues` key family
 * that the workspace-scoped `issueKeys` invalidations never reach, so it needs
 * this explicit hook to stay fresh.
 */
export function invalidateChannelIssues(qc: QueryClient): void {
  qc.invalidateQueries({ queryKey: channelKeys.issuesRoot() });
}

export function channelsOptions(wsId: string) {
  return queryOptions({
    queryKey: channelKeys.list(wsId),
    queryFn: () => api.listChannels(),
    enabled: !!wsId,
  });
}

export function archivedChannelsOptions(wsId: string) {
  return queryOptions({
    queryKey: channelKeys.archivedList(wsId),
    queryFn: () => api.listChannels({ archived: true }),
    enabled: !!wsId,
  });
}

export function channelMessagesOptions(channelId: string) {
  return queryOptions({
    queryKey: channelKeys.messages(channelId),
    queryFn: () => api.listChannelMessages(channelId),
    enabled: !!channelId,
  });
}

/**
 * Page cursor for the messages infinite query. Either the centered anchor for a
 * cold "around" load (task #340), or a `before` cursor for walking OLDER history
 * (the pre-existing backward path). NEWER-direction paging (after_cursor) is a
 * separate follow-up (#340 B2) and not modeled here yet.
 */
export type ChannelMessagesPageParam =
  | { around: number }
  | { seq?: number; created_at: string; id: string }
  | null;

function isAroundPageParam(
  p: ChannelMessagesPageParam,
): p is { around: number } {
  return !!p && "around" in p;
}

export function channelMessagesPageOptions(
  channelId: string,
  options: { limit?: number; aroundSeq?: number | null } = {},
) {
  const limit = options.limit ?? 50;
  const aroundSeq = options.aroundSeq ?? null;
  return infiniteQueryOptions({
    queryKey: channelKeys.messagesPage(channelId),
    queryFn: ({ pageParam }) =>
      api.listChannelMessagesPage(
        channelId,
        isAroundPageParam(pageParam)
          ? { around: pageParam.around, limit }
          : { before: pageParam, limit },
      ),
    // `around_seq` anchors ONLY the cold first fetch: `initialPageParam` is used
    // only when there is no cached data, so reopening a channel (a cache hit
    // under staleTime:Infinity) reuses the existing window and never
    // re-anchors — and the query key stays channel-only so the cache is shared
    // across visits. Callers MUST fix `aroundSeq` at entry (a ref / entry-time
    // memo), never a value that updates on the mark-read echo, or a jittering
    // param would fight this stability.
    initialPageParam: (aroundSeq != null
      ? { around: aroundSeq }
      : null) as ChannelMessagesPageParam,
    // Older direction (unchanged): next_cursor is a `before` cursor, so the next
    // fetched page walks back in time and appends to the pages tail.
    getNextPageParam: (lastPage) =>
      lastPage.has_more ? lastPage.next_cursor ?? undefined : undefined,
    enabled: !!channelId,
    staleTime: Infinity,
  });
}

export function flattenChannelMessagePages(data?: InfiniteData<ChannelMessagesPage>): ChannelMessage[] {
  const flat = data
    ? [...data.pages].reverse().flatMap((page) => page.messages ?? []).filter(Boolean)
    : [];
  return enrichChannelMessagesPreservingAvatars(flat);
}

/**
 * Backfill `author_avatar_url` across a read-model list (API refetch / flatten)
 * using the same preserve/sibling rules as WS upserts. Refetches and list
 * payloads that omit avatars must not regress bubbles to glyph placeholders
 * when an earlier row or optimistic ACK already had the face (LRM-218).
 */
export function enrichChannelMessagesPreservingAvatars(
  messages: readonly ChannelMessage[],
): ChannelMessage[] {
  let acc: ChannelMessage[] | undefined;
  for (const message of messages) {
    // List/refetch pages (and some test fixtures) can include sparse holes;
    // never call render/upsert helpers with undefined (LRM-218 CI crash).
    if (!message) continue;
    if (!shouldRenderChannelMessage(message)) {
      acc = acc?.filter((existing) => !matchesChannelMessage(existing, message));
      continue;
    }
    acc = upsertChannelMessage(acc, message) ?? acc;
  }
  return acc ?? [];
}

// Virtuoso needs a stable, monotonically-decreasing `firstItemIndex` to prepend
// older pages without a visual scroll jump (see react-virtuoso's prepend
// pattern). Page 0 is the latest chronological window; later pages are older,
// so everything past page 0 is "already-loaded older history" whose count we
// subtract from a high stable base — mirrors chat's identical convention in
// chat-window.tsx.
export const CHANNEL_MESSAGES_VIRTUOSO_BASE_INDEX = 1_000_000;

export function channelMessagesFirstItemIndex(data: InfiniteData<ChannelMessagesPage> | undefined, hasMessages: boolean): number {
  if (!hasMessages) return 0;
  const olderCount = (data?.pages ?? []).slice(1).reduce((sum, page) => sum + page.messages.length, 0);
  return CHANNEL_MESSAGES_VIRTUOSO_BASE_INDEX - olderCount;
}

/**
 * Match an existing cache row to an incoming message: same `id`, OR same
 * non-empty `client_message_id` (temp optimistic id → authoritative ACK / WS).
 */
export function findChannelMessageMatchIndex(
  messages: readonly ChannelMessage[],
  message: ChannelMessage,
): number {
  const byId = messages.findIndex((m) => m.id === message.id);
  if (byId >= 0) return byId;
  const clientId = message.client_message_id;
  if (!clientId) return -1;
  // Optimistic rows use `client_message_id` as temp `id`; ACK/WS may only carry
  // the client id on the incoming payload.
  return messages.findIndex(
    (m) => m.client_message_id === clientId || m.id === clientId,
  );
}

function matchesChannelMessage(existing: ChannelMessage, incoming: ChannelMessage): boolean {
  if (existing.id === incoming.id) return true;
  const clientId = incoming.client_message_id;
  if (!clientId) return false;
  return existing.client_message_id === clientId || existing.id === clientId;
}

export function upsertChannelMessageInCache(qc: QueryClient, message: ChannelMessage) {
  qc.setQueryData<ChannelMessage[]>(channelKeys.messages(message.channel_id), (old) => upsertChannelMessage(old, message));
  qc.setQueryData<InfiniteData<ChannelMessagesPage>>(channelKeys.messagesPage(message.channel_id), (old) => {
    // A channel the user has never opened has no cached page to append to. Seeding one
    // here would mark it "fresh" under staleTime: Infinity, so opening the channel later
    // skips the real fetch and only ever shows the messages caught by this upsert.
    if (!old?.pages.length) return old;
    if (!shouldRenderChannelMessage(message)) {
      return {
        ...old,
        pages: old.pages.map((page) => ({
          ...page,
          messages: page.messages.filter(
            (existing) => !matchesChannelMessage(existing, message),
          ),
        })),
      };
    }
    const siblings = flattenChannelMessagePages(old);
    const matchIndex = findChannelMessageMatchIndex(siblings, message);
    const existing = matchIndex >= 0 ? siblings[matchIndex] : undefined;
    const enriched = withPreservedAuthorAvatar(message, existing, siblings);
    const existingPageIndex = old.pages.findIndex((page: ChannelMessagesPage) =>
      page.messages.some((m: ChannelMessage) => matchesChannelMessage(m, enriched)),
    );
    const pages = old.pages.map((page, index) => {
      if (existingPageIndex < 0 && index === 0) {
        return { ...page, messages: [...page.messages, enriched] };
      }
      const messages = page.messages.map((m: ChannelMessage) => {
        if (!matchesChannelMessage(m, enriched)) return m;
        return enriched;
      });
      return { ...page, messages };
    });
    return { ...old, pages };
  });
}

export function upsertChannelMessageThreadInCache(qc: QueryClient, message: ChannelMessage, rootMessageId: string) {
  qc.setQueriesData<ChannelThreadMessagesPage>(
    { queryKey: channelKeys.messageThread(message.channel_id, rootMessageId) },
    (old) => {
      if (!old) return old;
      return {
        ...old,
        messages: upsertChannelMessage(old.messages, message) ?? [],
      };
    },
  );
}

export function invalidateChannelMessages(qc: QueryClient, channelId: string) {
  qc.invalidateQueries({ queryKey: channelKeys.messages(channelId) });
  qc.invalidateQueries({ queryKey: channelKeys.messagesPage(channelId) });
}

function shouldRenderChannelMessage(message: ChannelMessage | null | undefined): boolean {
  if (!message) return false;
  return !message.deleted_at || (message.thread_reply_count ?? 0) > 0;
}

/**
 * WS channel:message payloads sometimes omit `author_avatar_url` (publish path
 * forgot to attach it) while list fetches include it. Prefer the incoming URL,
 * else keep the cached row's, else copy from another same-author bubble already
 * in the thread — so consecutive agent messages don't flicker to initials.
 */
export function withPreservedAuthorAvatar(
  incoming: ChannelMessage,
  existing: ChannelMessage | undefined,
  siblings: readonly ChannelMessage[] | undefined,
): ChannelMessage {
  if (incoming.author_avatar_url) return incoming;
  if (existing?.author_avatar_url) {
    return { ...incoming, author_avatar_url: existing.author_avatar_url };
  }
  if (!incoming.author_id || !siblings?.length) return incoming;
  const fromSibling = siblings.find(
    (m) =>
      !matchesChannelMessage(m, incoming) &&
      m.author_id === incoming.author_id &&
      m.type === incoming.type &&
      !!m.author_avatar_url,
  )?.author_avatar_url;
  if (!fromSibling) return incoming;
  return { ...incoming, author_avatar_url: fromSibling };
}

function upsertChannelMessage(old: ChannelMessage[] | undefined, message: ChannelMessage) {
  if (!shouldRenderChannelMessage(message)) {
    return old?.filter((existing) => !matchesChannelMessage(existing, message));
  }
  if (!old) return [message];
  const index = findChannelMessageMatchIndex(old, message);
  const existing = index >= 0 ? old[index] : undefined;
  const enriched = withPreservedAuthorAvatar(message, existing, old);
  if (index >= 0) {
    return old.map((m, i) => (i === index ? enriched : m));
  }
  return [...old, enriched];
}

export function channelMessageThreadOptions(
  channelId: string,
  messageId: string,
  options?: { limit?: number; beforeSeq?: number; before?: string; beforeId?: string },
) {
  return queryOptions({
    queryKey: [
      ...channelKeys.messageThread(channelId, messageId),
      options?.limit,
      options?.beforeSeq,
      options?.before,
      options?.beforeId,
    ] as const,
    queryFn: () => api.listChannelMessageThread(channelId, messageId, options),
    enabled: !!channelId && !!messageId,
  });
}

export function channelMessageSearchOptions(channelId: string, query: string, limit?: number) {
  return queryOptions({
    queryKey: channelKeys.messageSearch(channelId, query, limit),
    queryFn: () => api.searchChannelMessages(channelId, query, limit),
    enabled: !!channelId && query.trim().length > 0,
  });
}

export function channelMembersOptions(channelId: string) {
  return queryOptions({
    queryKey: channelKeys.members(channelId),
    queryFn: () => api.listChannelMembers(channelId),
    enabled: !!channelId,
  });
}

export function channelAttachmentsOptions(channelId: string) {
  return queryOptions({
    queryKey: channelKeys.attachments(channelId),
    queryFn: () => api.listChannelAttachments(channelId),
    enabled: !!channelId,
  });
}

export function channelStatsOptions(channelId: string) {
  return queryOptions({
    queryKey: channelKeys.stats(channelId),
    queryFn: () => api.getChannelStats(channelId),
    enabled: !!channelId,
  });
}

export function channelProjectFilesOptions(channelId: string) {
  return queryOptions({
    queryKey: channelKeys.projectFiles(channelId),
    queryFn: () => api.listChannelProjectFiles(channelId),
    enabled: !!channelId,
  });
}

/**
 * Group-local Tasks projection (#562): the issues created from a source message
 * in THIS channel. Read-only — the channel doesn't own these issues, it's just
 * the discussion context they came from, so this never restores a global
 * channel filter or duplicates issue data. Single source of truth is the query.
 */
export function channelIssuesOptions(channelId: string, params?: ChannelIssuesParams) {
  return queryOptions({
    queryKey: channelKeys.issues(channelId, params),
    queryFn: () => api.listChannelSourceIssues(channelId, params),
    enabled: !!channelId,
  });
}

/**
 * The #684 endpoint caps a page at 100 issues and returns the `total`, so the
 * group Tasks board must page rather than silently truncate. Offset pagination
 * over the SAME single-source projection: each page fetches `limit`/`offset` of
 * `channelIssuesOptions`' underlying method, and the caller flattens the pages
 * into one loaded set to group by status client-side — never a parallel
 * per-column query, never a restored global filter. `getNextPageParam` returns
 * the next offset until the accumulated count reaches `total`.
 */
export const CHANNEL_ISSUES_PAGE_SIZE = 100;

export function channelIssuesInfiniteOptions(
  channelId: string,
  options: { limit?: number } = {},
) {
  const limit = options.limit ?? CHANNEL_ISSUES_PAGE_SIZE;
  return infiniteQueryOptions({
    queryKey: channelKeys.issuesInfinite(channelId, limit),
    queryFn: ({ pageParam }) =>
      api.listChannelSourceIssues(channelId, { limit, offset: pageParam }),
    initialPageParam: 0,
    getNextPageParam: (_lastPage, allPages) => {
      const loaded = allPages.reduce((sum, page) => sum + page.issues.length, 0);
      const total = allPages[0]?.total ?? 0;
      return loaded < total ? loaded : undefined;
    },
    enabled: !!channelId,
  });
}
