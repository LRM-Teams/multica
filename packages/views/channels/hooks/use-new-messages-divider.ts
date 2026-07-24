import { useMemo, useRef } from "react";

export interface NewMessagesDivider {
  /** Id of the first unread message from someone other than the viewer. */
  anchorMessageId: string;
  /** How many unread messages are from others — the "N new messages". */
  count: number;
}

interface SeqMessage {
  id: string;
  seq: number;
  author_id?: string | null;
}

export function maxSeqOrNull(messages: readonly SeqMessage[]): number | null {
  let max: number | null = null;
  for (const m of messages) if (max === null || m.seq > max) max = m.seq;
  return max;
}

// Pure: the first message past the read cursor that the viewer did NOT send,
// plus how many such messages there are, bounded to what was already in the
// conversation ON ENTRY (`entryHighWaterSeq`). Messages that ARRIVE while you're
// actively viewing (seq beyond the entry high-water) are not "new" — you're
// watching them come in, so they must not extend/raise the divider (Parker's
// snapshot-and-pin rule; avoids a "1 new message" line popping up under your
// eyes). The viewer's own messages are also excluded. Null when the cursor is
// unknown or nothing qualifies. Exported for exhaustive unit tests.
export function computeNewMessagesDivider(
  messages: readonly SeqMessage[],
  lastReadSeq: number | null,
  currentUserId: string | null,
  entryHighWaterSeq: number | null,
): NewMessagesDivider | null {
  // #1189 P0: a real `last_read_seq` of 0 (as opposed to null/undefined) is
  // indistinguishable at this layer from "no cursor at all" — real seq
  // values start at 1, so treating 0 as a genuine read position makes
  // `m.seq > lastReadSeq` true for every message in the conversation,
  // anchoring the divider at the very first loaded message instead of
  // degrading to no-divider like a missing cursor does. Caught in production
  // via a real-device channel-opens-at-the-top regression traced to
  // `last_read_seq` coming back as the backend's "never read" sentinel (0)
  // instead of null from the list/mark-read APIs (task #713 fixes that at
  // the API boundary too — this is the FE-side defense regardless of which
  // side the value originates from). Treat <= 0 the same as null — a
  // non-positive cursor can never legitimately mean "read".
  if (lastReadSeq == null || lastReadSeq <= 0) return null;
  const isNewFromOther = (m: SeqMessage) =>
    m.seq > lastReadSeq &&
    (entryHighWaterSeq == null || m.seq <= entryHighWaterSeq) &&
    (currentUserId == null || m.author_id !== currentUserId);
  const index = messages.findIndex(isNewFromOther);
  if (index < 0) return null;
  const anchor = messages[index];
  if (!anchor) return null;
  let count = 0;
  for (const m of messages) if (isNewFromOther(m)) count += 1;
  return { anchorMessageId: anchor.id, count };
}

/**
 * The "N new messages" divider anchor for a conversation (#303). Pins to the
 * first unread message from someone else as captured ON ENTRY, and stays frozen
 * for the visit: opening the conversation fires mark-read, which advances the
 * server cursor and refetches the channel, but the divider must not chase it
 * down the list ("进会话即见、不随手清" — Iris). We therefore snapshot the first
 * known `lastReadSeq` per channel visit and compute against that snapshot; a
 * later (advanced) value is ignored.
 *
 * We also snapshot the entry HIGH-WATER seq (the latest message present when the
 * conversation opened). Messages that arrive live while you're in the
 * conversation land beyond it and never raise or grow the divider — you're
 * watching them arrive, so they carry no "unread" meaning (Parker's口径). Older
 * messages loaded via pagination sit below the high-water, so they don't affect
 * it.
 *
 * The viewer's own messages are excluded — a message you send is not "new".
 *
 * Degrades to null while the BE hasn't supplied `last_read_seq` yet.
 */
export function useNewMessagesDivider(
  channelId: string | null | undefined,
  messages: readonly SeqMessage[],
  lastReadSeq: number | null | undefined,
  currentUserId: string | null | undefined,
): NewMessagesDivider | null {
  const snapshotRef = useRef<{
    channelId: string | null;
    seq: number | null;
    highWater: number | null;
  }>({ channelId: null, seq: null, highWater: null });
  const key = channelId ?? null;
  const snap = snapshotRef.current;
  if (snap.channelId !== key) {
    // New conversation visit — fresh snapshots (either may be null until the
    // channel query / first message page resolves; captured once below).
    snap.channelId = key;
    snap.seq = lastReadSeq ?? null;
    snap.highWater = maxSeqOrNull(messages);
  } else {
    if (snap.seq === null && lastReadSeq != null) snap.seq = lastReadSeq;
    if (snap.highWater === null && messages.length > 0) {
      snap.highWater = maxSeqOrNull(messages);
    }
  }
  const snapshotSeq = snap.seq;
  const highWater = snap.highWater;
  const viewerId = currentUserId ?? null;
  return useMemo(
    () => computeNewMessagesDivider(messages, snapshotSeq, viewerId, highWater),
    [messages, snapshotSeq, viewerId, highWater],
  );
}
