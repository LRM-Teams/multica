import { useRef } from "react";

export interface EntryAnchor {
  /**
   * The `around_seq` anchor for the cold message load (#340) — the viewer's list
   * read cursor at entry. `null` when nothing is unread → keep the default
   * latest-page load.
   */
  aroundSeq: number | null;
  /**
   * The TRUE unread count at entry, from the same list response (sidebar-same
   * source), for the "N new messages" divider. The around window only holds
   * ~limit/2 messages past the anchor, so counting unread within the loaded
   * window undercounts large-unread conversations (486 → "~25"); this carries
   * the real total. `null` when the list didn't provide it → the caller falls
   * back to the window count.
   */
  unreadCount: number | null;
}

/**
 * The message-list anchor for a conversation, frozen at entry (#340).
 *
 * Both values come from the sidebar list item at the moment the conversation
 * first opens and are FROZEN per conversation (a ref, like `useEntryReadCursor`'s
 * payload snapshot), NOT reactive:
 *  - the anchor only feeds the cold first fetch (the messages query keys on
 *    channel id and caches under staleTime:Infinity), so a shifting value would
 *    only jitter the query key; and
 *  - the divider is a snapshot of the entry moment (PRD §3.1) — its count must
 *    NOT change when new messages arrive after you're already here.
 *
 * `listLastReadSeq` = `Channel.last_read_seq` / `DMItem.last_read_seq`.
 * `listUnreadCount` = `Channel.real_unread_count ?? unread_count` /
 * `DMItem.real_unread ?? unread` (real unread, excluding the manual-unread boost).
 */
export function useEntryAnchor(
  channelId: string | null | undefined,
  listLastReadSeq: number | null | undefined,
  listUnreadCount: number | null | undefined,
): EntryAnchor {
  const key = channelId ?? null;
  const ref = useRef<{ channelId: string | null; anchor: EntryAnchor }>({
    channelId: null,
    anchor: { aroundSeq: null, unreadCount: null },
  });
  if (ref.current.channelId !== key) {
    const aroundSeq =
      typeof listLastReadSeq === "number" && listLastReadSeq > 0
        ? listLastReadSeq
        : null;
    const unreadCount =
      typeof listUnreadCount === "number" && listUnreadCount > 0
        ? listUnreadCount
        : null;
    ref.current = { channelId: key, anchor: { aroundSeq, unreadCount } };
  }
  return ref.current.channelId === key
    ? ref.current.anchor
    : { aroundSeq: null, unreadCount: null };
}
