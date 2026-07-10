import { infiniteQueryOptions, queryOptions, type InfiniteData, type QueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { ChannelMessage, ChannelMessagesPage, ChannelThreadMessagesPage } from "../types";

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
};

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
  return data ? [...data.pages].reverse().flatMap((page) => page.messages) : [];
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
          messages: page.messages.filter((existing) => existing.id !== message.id),
        })),
      };
    }
    const existingPageIndex = old.pages.findIndex((page: ChannelMessagesPage) =>
      page.messages.some((existing: ChannelMessage) => existing.id === message.id),
    );
    const pages = old.pages.map((page, index) => {
      if (existingPageIndex < 0 && index === 0) {
        return { ...page, messages: [...page.messages, message] };
      }
      const messages = page.messages.map((existing: ChannelMessage) => {
        if (existing.id !== message.id) return existing;
        return message;
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

function shouldRenderChannelMessage(message: ChannelMessage): boolean {
  return !message.deleted_at || (message.thread_reply_count ?? 0) > 0;
}

function upsertChannelMessage(old: ChannelMessage[] | undefined, message: ChannelMessage) {
  if (!shouldRenderChannelMessage(message)) {
    return old?.filter((existing) => existing.id !== message.id);
  }
  if (!old) return [message];
  const index = old.findIndex((existing) => existing.id === message.id);
  if (index >= 0) {
    return old.map((existing) => (existing.id === message.id ? message : existing));
  }
  return [...old, message];
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
