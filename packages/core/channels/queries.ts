import { infiniteQueryOptions, queryOptions, type InfiniteData, type QueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { ChannelMessage, ChannelMessagesPage, ChannelThreadMessagesPage, ListIssuesParams } from "../types";
import { CHANNEL_MESSAGE_GC_TIME_MS } from "./evict-inactive-caches";

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
  /** LRM-872 / LRM-879 — per-row can_remove / role-change gates from BE. */
  memberManagementCapabilities: (channelId: string) =>
    ["channel-member-management-capabilities", channelId] as const,
  inviteCandidates: (channelId: string) => ["channel-invite-candidates", channelId] as const,
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
    queryFn: async ({ client, queryKey, signal }) => {
      const incoming = await api.listChannelMessages(channelId, { signal });
      // Refetch must not wipe in-flight / failed optimistic bubbles (LRM-280).
      if (!client) return incoming;
      const previous = client.getQueryData<ChannelMessage[]>(queryKey);
      return preserveLocalSendMessages(previous, incoming);
    },
    enabled: !!channelId,
    // LRM-1363: retain inactive channel message caches for the session.
    gcTime: CHANNEL_MESSAGE_GC_TIME_MS,
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
    queryFn: async ({ pageParam, client, queryKey, signal }) => {
      const page = await api.listChannelMessagesPage(
        channelId,
        isAroundPageParam(pageParam)
          ? { around: pageParam.around, limit, signal }
          : { before: pageParam, limit, signal },
      );
      // Optimistic sends live on the latest window (page 0). Older-history pages
      // must not re-attach them (LRM-280).
      const isLatestWindow = pageParam == null || isAroundPageParam(pageParam);
      if (!isLatestWindow || !client) return page;
      const previous = client.getQueryData<InfiniteData<ChannelMessagesPage>>(queryKey);
      return {
        ...page,
        messages: preserveLocalSendMessages(previous?.pages[0]?.messages, page.messages),
      };
    },
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
    // LRM-1363: retain inactive channel message caches for the session.
    gcTime: CHANNEL_MESSAGE_GC_TIME_MS,
  });
}

export function flattenChannelMessagePages(data?: InfiniteData<ChannelMessagesPage>): ChannelMessage[] {
  const flat = data
    ? [...data.pages].reverse().flatMap((page) => page.messages ?? []).filter(Boolean)
    : [];
  return normalizeChannelMessages(flat);
}

/**
 * Normalize a channel-message list at the cache seam.
 *
 * Duplicate API pages collapse onto one canonical row. Soft-deleted tombstones
 * stay in the list; only the realtime upsert path decides whether to hide them.
 */
export function normalizeChannelMessages(
  messages: readonly ChannelMessage[],
): ChannelMessage[] {
  const out: ChannelMessage[] = [];
  for (const message of messages) {
    // List/refetch pages (and some test fixtures) can include sparse holes.
    if (!message) continue;
    const normalized = withoutLegacyMessageAvatar(message);
    const idx = out.findIndex((existing) => matchesChannelMessage(existing, normalized));
    if (idx >= 0) {
      out[idx] = normalized;
    } else {
      out.push(normalized);
    }
  }
  return out;
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

function isLocalSendRow(message: ChannelMessage): boolean {
  return message.local_send_status === "pending" || message.local_send_status === "failed";
}

/**
 * Keep client-only pending/failed bubbles across list/thread refetches.
 *
 * `invalidateChannelMessages` / reaction / thread-send side effects replace the
 * cache with server pages. Without this merge, an in-flight send disappears; if
 * HTTP then errors, `markOptimisticChannelMessageFailed` has no row to patch and
 * the message is silently lost (LRM-280).
 */
export function preserveLocalSendMessages(
  previous: readonly ChannelMessage[] | undefined,
  incoming: ChannelMessage[],
): ChannelMessage[] {
  if (!previous?.length) return incoming;
  const orphans: ChannelMessage[] = [];
  for (const row of previous) {
    if (!isLocalSendRow(row)) continue;
    const clientId = row.client_message_id;
    const onServer = incoming.some(
      (m) =>
        m.id === row.id ||
        (!!clientId && (m.client_message_id === clientId || m.id === clientId)),
    );
    if (!onServer) orphans.push(row);
  }
  if (!orphans.length) return incoming;
  return [...incoming, ...orphans];
}

/**
 * Match an existing cache row to an incoming message: same `id`, OR same
 * non-empty `client_message_id` (temp optimistic id → authoritative ACK / WS).
 * If the ACK/WS omits `client_message_id`, fall back to the pending/failed
 * optimistic bubble with the same author + content + thread root (LRM-271/273).
 */
export function findChannelMessageMatchIndex(
  messages: readonly ChannelMessage[],
  message: ChannelMessage,
): number {
  const byId = messages.findIndex((m) => m.id === message.id);
  if (byId >= 0) return byId;
  const clientId = message.client_message_id;
  if (clientId) {
    // Optimistic rows use `client_message_id` as temp `id`; ACK/WS may only carry
    // the client id on the incoming payload.
    const byClient = messages.findIndex(
      (m) => m.client_message_id === clientId || m.id === clientId,
    );
    if (byClient >= 0) return byClient;
  }
  // Authoritative rows use a server-issued id (not `id === client_message_id`).
  // Do not require `local_send_status` to be absent — a buggy ACK merge may
  // leak it, and we still need to retire the temp bubble (LRM-273).
  const isTempOptimistic =
    !!message.client_message_id && message.id === message.client_message_id;
  if (isTempOptimistic || !message.author_id) return -1;
  return messages.findIndex(
    (m) =>
      isLocalSendRow(m) &&
      m.author_id === message.author_id &&
      m.content === message.content &&
      (m.thread_root_message_id ?? null) === (message.thread_root_message_id ?? null),
  );
}

function matchesChannelMessage(existing: ChannelMessage, incoming: ChannelMessage): boolean {
  return findChannelMessageMatchIndex([existing], incoming) === 0;
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
    const enriched = asCacheMessage(message, existing);
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

/**
 * Patch one message's `reactions` array in place (#689 perf audit) instead of
 * invalidating the whole channel message list. A `channel_reaction:added` /
 * `channel_reaction:removed` WS event carries enough data to compute the new
 * reaction row set itself — a full-list refetch on every reaction is a
 * virtualized-list-wide re-render for a one-field change on one row, and was
 * the single largest contributor to mobile scroll jank in the perf audit.
 *
 * Same reach as {@link upsertChannelMessageInCache}: the flat array cache and
 * every loaded page of the infinite cache. Messages not currently cached are
 * a no-op (`setQueryData`'s updater only runs against an existing entry).
 */
export function patchChannelMessageReactionInCache(
  qc: QueryClient,
  channelId: string,
  messageId: string,
  updateReactions: (reactions: ChannelMessage["reactions"]) => ChannelMessage["reactions"],
) {
  const patchOne = (message: ChannelMessage): ChannelMessage =>
    message.id === messageId ? { ...message, reactions: updateReactions(message.reactions) } : message;

  qc.setQueryData<ChannelMessage[]>(channelKeys.messages(channelId), (old) => old?.map(patchOne));
  qc.setQueryData<InfiniteData<ChannelMessagesPage>>(channelKeys.messagesPage(channelId), (old) => {
    if (!old) return old;
    return {
      ...old,
      pages: old.pages.map((page) => ({ ...page, messages: page.messages.map(patchOne) })),
    };
  });
}

function shouldRenderChannelMessage(message: ChannelMessage | null | undefined): boolean {
  if (!message) return false;
  return !message.deleted_at || (message.thread_reply_count ?? 0) > 0;
}

function withoutLegacyMessageAvatar(message: ChannelMessage): ChannelMessage {
  const stripSnakeCaseAvatar = <T extends object>(value: T): T => {
    const { author_avatar_url: _legacyAvatar, ...identityOnly } = value as T & {
      author_avatar_url?: unknown;
    };
    return identityOnly as T;
  };
  const stripCamelCaseAvatar = <T extends object>(value: T): T => {
    const { authorAvatarUrl: _legacyAvatar, ...identityOnly } = value as T & {
      authorAvatarUrl?: unknown;
    };
    return identityOnly as T;
  };
  const identityOnlyMessage = stripSnakeCaseAvatar(message);
  return {
    ...identityOnlyMessage,
    ...(identityOnlyMessage.reply_to
      ? { reply_to: stripSnakeCaseAvatar(identityOnlyMessage.reply_to) }
      : {}),
    ...(identityOnlyMessage.thread_root
      ? { thread_root: stripSnakeCaseAvatar(identityOnlyMessage.thread_root) }
      : {}),
    ...(identityOnlyMessage.quote?.snapshot
      ? {
          quote: {
            ...identityOnlyMessage.quote,
            snapshot: stripCamelCaseAvatar(identityOnlyMessage.quote.snapshot),
          },
        }
      : {}),
  };
}

/**
 * Normalize an API/WS row for the cache: preserve avatar + client_message_id from
 * the optimistic row, and never keep client-only pending/failed state on an
 * authoritative server id (LRM-271/273/280).
 */
function asCacheMessage(
  incoming: ChannelMessage,
  existing: ChannelMessage | undefined,
): ChannelMessage {
  let enriched = withoutLegacyMessageAvatar(incoming);
  // Keep client_message_id so list identity / keys / retry stay stable when ACK omits it.
  if (!enriched.client_message_id && existing?.client_message_id) {
    enriched = { ...enriched, client_message_id: existing.client_message_id };
  }
  // Temp optimistic rows use `id === client_message_id` and may carry
  // `local_send_status`. Authoritative HTTP ACK / WS rows use a server id and
  // must never keep a client-only pending/failed badge (LRM-271/273).
  const clientId = enriched.client_message_id;
  if (clientId && enriched.id === clientId) return enriched;
  if (enriched.local_send_status == null) return enriched;
  const { local_send_status: _drop, ...rest } = enriched;
  return rest;
}

function upsertChannelMessage(old: ChannelMessage[] | undefined, message: ChannelMessage) {
  if (!shouldRenderChannelMessage(message)) {
    return old?.filter((existing) => !matchesChannelMessage(existing, message));
  }
  if (!old) return [asCacheMessage(message, undefined)];
  const index = findChannelMessageMatchIndex(old, message);
  const existing = index >= 0 ? old[index] : undefined;
  const enriched = asCacheMessage(message, existing);
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
    queryFn: async ({ client, queryKey, signal }) => {
      const page = await api.listChannelMessageThread(channelId, messageId, { ...options, signal });
      if (!client) return page;
      const previous = client.getQueryData<ChannelThreadMessagesPage>(queryKey);
      return {
        ...page,
        messages: preserveLocalSendMessages(previous?.messages, page.messages),
      };
    },
    enabled: !!channelId && !!messageId,
    // LRM-1363: retain inactive thread caches for the session.
    gcTime: CHANNEL_MESSAGE_GC_TIME_MS,
  });
}

export function channelMessageSearchOptions(channelId: string, query: string, limit?: number) {
  return queryOptions({
    queryKey: channelKeys.messageSearch(channelId, query, limit),
    queryFn: () => api.searchChannelMessages(channelId, query, limit),
    enabled: !!channelId && query.trim().length > 0,
    gcTime: CHANNEL_MESSAGE_GC_TIME_MS,
  });
}

export function channelMembersOptions(channelId: string) {
  return queryOptions({
    queryKey: channelKeys.members(channelId),
    queryFn: ({ signal }) => api.listChannelMembers(channelId, { signal }),
    enabled: !!channelId,
  });
}

/** LRM-872 / LRM-879 — enable for ordinary group channels only. */
export function channelMemberManagementCapabilitiesOptions(
  channelId: string,
  enabled = true,
) {
  return queryOptions({
    queryKey: channelKeys.memberManagementCapabilities(channelId),
    queryFn: ({ signal }) =>
      api.getChannelMemberManagementCapabilities(channelId, { signal }),
    enabled: !!channelId && enabled,
  });
}

/** Invalidate roster + capability projection together after membership writes. */
export function invalidateChannelMemberRoster(qc: QueryClient, channelId: string): void {
  qc.invalidateQueries({ queryKey: channelKeys.members(channelId) });
  qc.invalidateQueries({ queryKey: channelKeys.memberManagementCapabilities(channelId) });
}

/** LRM-622/623 — invite picker pool; enable only while Add people is open. */
export function channelInviteCandidatesOptions(channelId: string) {
  return queryOptions({
    queryKey: channelKeys.inviteCandidates(channelId),
    queryFn: async () => {
      const res = await api.listChannelInviteCandidates(channelId);
      return res.candidates;
    },
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
