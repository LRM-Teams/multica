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
