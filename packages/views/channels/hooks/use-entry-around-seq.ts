import { useRef } from "react";

export interface EntryAnchor {
  /**
   * Cold-load `around_seq` for the messages query.
   *
   * LRM-1068: always `null` on normal open so the first fetch is the **latest
   * page** (cheap, lands at newest). Mid-history `around_seq` windows were
   * anchoring busy channels on the first-unread row ("今日第一条") and pulling
   * a heavier bidirectional page — the lag + wrong landing jianghp3 reported.
   * Deep-link / search jump paths own their own fetch and are unaffected.
   */
  aroundSeq: number | null;
  /**
   * The TRUE unread count at entry, from the same list response (sidebar-same
   * source), for the "N new messages" divider / pill label. Frozen per visit so
   * mark-read echo + live arrivals don't shrink the entry snapshot (PRD §3.1).
   * `null` when the list didn't provide it → the caller falls back to the
   * window count.
   */
  unreadCount: number | null;
}

/**
 * Entry snapshot for a conversation visit (LRM-1068 / formerly #340).
 *
 * Freezes `unreadCount` from the sidebar list item at the moment the
 * conversation first opens (a ref — same pattern as `useEntryReadCursor`).
 * Does **not** feed `around_seq` anymore: cold open always loads the latest
 * page and the message list bottom-settles to newest. The divider + "N new"
 * pill still use the frozen count so viewers can jump to unread on demand.
 *
 * `listLastReadSeq` is accepted for API stability / future deep-link reuse but
 * is intentionally unused for cold-open fetch anchoring.
 * `listUnreadCount` = `Channel.real_unread_count ?? unread_count` /
 * `DMItem.real_unread ?? unread` (real unread, excluding the manual-unread boost).
 */
export function useEntryAnchor(
  channelId: string | null | undefined,
  _listLastReadSeq: number | null | undefined,
  listUnreadCount: number | null | undefined,
): EntryAnchor {
  const key = channelId ?? null;
  const ref = useRef<{ channelId: string | null; anchor: EntryAnchor }>({
    channelId: null,
    anchor: { aroundSeq: null, unreadCount: null },
  });
  if (ref.current.channelId !== key) {
    const unreadCount =
      typeof listUnreadCount === "number" && listUnreadCount > 0
        ? listUnreadCount
        : null;
    ref.current = {
      channelId: key,
      anchor: { aroundSeq: null, unreadCount },
    };
  }
  return ref.current.channelId === key
    ? ref.current.anchor
    : { aroundSeq: null, unreadCount: null };
}
