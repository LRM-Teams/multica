import { useCallback, useMemo, useRef, useState, type RefObject } from "react";
import type { VirtuosoHandle } from "react-virtuoso";
import type { ChannelMessage } from "@multica/core/types";
import { maxSeqOrNull } from "./use-new-messages-divider";

export interface NewArrivalsPill {
  /** How many messages from others arrived live after you last caught up. */
  count: number;
  /** Id of the first such message — the "jump to first new" scroll target. */
  firstMessageId: string;
}

interface SeqMessage {
  id: string;
  seq: number;
  author_id?: string | null;
}

// Pure: messages from someone other than the viewer whose seq is beyond
// `seenThroughSeq` — i.e. they arrived live after you last caught up (entry, or
// the last time you were at the bottom, or a pill click). Drives the floating
// "N new messages ↓" jump pill (#303 follow-up), complementary to the inline
// divider (which covers what was already unread on entry). Null when none.
export function computeNewArrivals(
  messages: readonly SeqMessage[],
  seenThroughSeq: number | null,
  currentUserId: string | null,
): NewArrivalsPill | null {
  if (seenThroughSeq == null) return null;
  let count = 0;
  let firstMessageId: string | null = null;
  for (const m of messages) {
    if (
      m.seq > seenThroughSeq &&
      (currentUserId == null || m.author_id !== currentUserId)
    ) {
      if (firstMessageId === null) firstMessageId = m.id;
      count += 1;
    }
  }
  return firstMessageId === null ? null : { count, firstMessageId };
}

/**
 * #325 phase-2 block 1: the floating "N new messages ↓" pill as a self-contained
 * plugin hook. Owns its own boundary state (the per-visit entry high-water + the
 * `caughtUpSeq` bumped at the bottom / on click, set only in event handlers) and
 * its imperative "jump to first new" scroll — the core list just renders `pill`
 * and forwards `onReachedBottom`/`onPillClick`. It only READS `messages` and
 * scrolls via the Virtuoso ref; it never touches the core render/scroll
 * ownership (`isNearBottom` stays in the core, passed in nowhere — the caller
 * gates the pill's visibility on it).
 */
export function useNewMessagesPill({
  messages,
  currentUserId,
  virtuosoRef,
}: {
  messages: readonly ChannelMessage[];
  currentUserId: string | null;
  virtuosoRef: RefObject<VirtuosoHandle | null>;
}): { pill: NewArrivalsPill | null; onReachedBottom: () => void; onPillClick: () => void } {
  const channelId = messages[0]?.channel_id ?? null;
  const entryHighWaterRef = useRef<{ channelId: string | null; seq: number | null }>({
    channelId: null,
    seq: null,
  });
  if (entryHighWaterRef.current.channelId !== channelId) {
    entryHighWaterRef.current = { channelId, seq: maxSeqOrNull(messages) };
  } else if (entryHighWaterRef.current.seq === null && messages.length > 0) {
    entryHighWaterRef.current.seq = maxSeqOrNull(messages);
  }
  const [caughtUpSeq, setCaughtUpSeq] = useState<number | null>(null);
  const arrivalsBoundary = caughtUpSeq ?? entryHighWaterRef.current.seq;
  const pill = useMemo(
    () => computeNewArrivals(messages, arrivalsBoundary, currentUserId),
    [messages, arrivalsBoundary, currentUserId],
  );
  // At the bottom = caught up to the latest; nothing for the pill to show.
  const onReachedBottom = useCallback(() => {
    setCaughtUpSeq(maxSeqOrNull(messages));
  }, [messages]);
  const onPillClick = useCallback(() => {
    const target = pill ? messages.findIndex((m) => m.id === pill.firstMessageId) : -1;
    if (target >= 0) {
      // #689/#1189 index-contract fix: `scrollToIndex` resolves against the
      // LOCAL data array (0..messages.length-1), never offset by
      // `firstItemIndex` — see channel-message-list.tsx's matching comment.
      virtuosoRef.current?.scrollToIndex({
        index: target,
        align: "start",
        behavior: "smooth",
      });
    }
    // Clicking = caught up: dismiss the pill until more arrive.
    setCaughtUpSeq(maxSeqOrNull(messages));
  }, [pill, messages, virtuosoRef]);
  return { pill, onReachedBottom, onPillClick };
}
