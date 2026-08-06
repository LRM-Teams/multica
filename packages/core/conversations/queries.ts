import {
  infiniteQueryOptions,
  QueryClient,
  type InfiniteData,
} from "@tanstack/react-query";
import { api } from "../api";
import type { Channel } from "../types";
import type { DMItem } from "../dm/types";
import type { ConversationListItem, ConversationListResponse } from "./types";

/**
 * LRM-1399 — unified active Conversations list query.
 *
 * `GET /api/conversations` returns group channels + DMs in ONE read request
 * with a single global order (pinned → updated_at → id) and cursor. The page
 * starts pinned/unpinned items interleaved; the UI splits them into its
 * familiar regions without re-fetching either side separately.
 *
 * staleTime: Infinity — kept fresh by WS invalidation and mutation
 * invalidation, not polling (mirrors the DM list it replaces).
 */
export const conversationKeys = {
  all: (wsId: string) => ["conversations", wsId] as const,
  list: (wsId: string) => [...conversationKeys.all(wsId), "list"] as const,
};

export function conversationsOptions(wsId: string, pageSize = 50) {
  return infiniteQueryOptions({
    queryKey: conversationKeys.list(wsId),
    queryFn: ({ pageParam }) =>
      api.listConversations({
        limit: pageSize,
        cursor: pageParam ?? undefined,
      }),
    // The first page is loaded unconditionally; subsequent pages load on
    // demand only when the user scrolls (deferred — see the trigger in the
    // sidebar). Keeping it "infinite" preserves the server cursor contract so
    // the next page can be fetched without changing the data source.
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    enabled: !!wsId,
    staleTime: Infinity,
    networkMode: "always",
  });
}

/** Flatten all loaded conversation pages preserving server global order. */
export function flattenConversationPages(
  data: InfiniteData<ConversationListResponse>,
): ConversationListItem[] {
  return data.pages.flatMap((page) => page.items);
}

/** Group-channel slice of the unified list (native Channel shape preserved). */
export function conversationGroupChannels(
  items: ConversationListItem[],
): Channel[] {
  const out: Channel[] = [];
  for (const item of items) {
    if (item.kind === "channel" && item.channel) out.push(item.channel);
  }
  return out;
}

/** DM slice of the unified list (native DMItem shape preserved). */
export function conversationDMs(items: ConversationListItem[]): DMItem[] {
  const out: DMItem[] = [];
  for (const item of items) {
    if (item.kind === "dm" && item.dm) out.push(toDMItem(item.dm));
  }
  return out;
}

/**
 * The unified DM payload is structurally identical to `/api/dm`'s DMItem for
 * every field the UI consumes; this adapter forwards it with the exact type.
 */
function toDMItem(dm: NonNullable<ConversationListItem["dm"]>): DMItem {
  return dm as unknown as DMItem;
}

/** Invalidate the unified Conversations list for a workspace. */
export function invalidateConversations(qc: QueryClient, wsId: string): void {
  qc.invalidateQueries({ queryKey: conversationKeys.list(wsId) });
}
