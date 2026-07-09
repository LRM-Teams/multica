import { useRef } from "react";

/**
 * The `around_seq` anchor for a conversation's cold message load (#340), frozen
 * at entry — the viewer's list read cursor when the conversation first opened.
 *
 * Loading the window centered on this seq means the first render already lands
 * on the unread divider (via `initialTopMostItemIndex`) instead of loading the
 * latest page and scroll-chasing. Returns `null` when there is nothing unread
 * (cursor <= 0 / absent) → the caller keeps the default latest-page load.
 *
 * FROZEN per conversation (a ref, like `useEntryReadCursor`'s payload snapshot),
 * NOT reactive: a value that shifted on the mark-read echo would jitter the
 * anchor. It doesn't need to — the anchor only matters for the very first fetch
 * (the messages query keys on channel id alone and caches under staleTime:
 * Infinity, so reopening reuses the window and never re-anchors), and the list
 * cursor is already the pre-advance value on a warm open. `list_last_read_seq`
 * is `Channel.last_read_seq` / `DMItem.last_read_seq` from the sidebar list.
 */
export function useEntryAroundSeq(
  channelId: string | null | undefined,
  listLastReadSeq: number | null | undefined,
): number | null {
  const key = channelId ?? null;
  const ref = useRef<{ channelId: string | null; seq: number | null }>({
    channelId: null,
    seq: null,
  });
  if (ref.current.channelId !== key) {
    const seq =
      typeof listLastReadSeq === "number" && listLastReadSeq > 0
        ? listLastReadSeq
        : null;
    ref.current = { channelId: key, seq };
  }
  return ref.current.channelId === key ? ref.current.seq : null;
}
