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

// Pure: the first message past the read cursor that the viewer did NOT send,
// plus how many such messages there are. The viewer's own messages are never
// "new" to them, so they're excluded from both the anchor and the count —
// otherwise sending a message would raise a "1 new message" divider above your
// own message. Null when the cursor is unknown or nothing unread is from others.
// Exported for exhaustive unit tests.
export function computeNewMessagesDivider(
  messages: readonly SeqMessage[],
  lastReadSeq: number | null,
  currentUserId: string | null,
): NewMessagesDivider | null {
  if (lastReadSeq == null) return null;
  const isNewFromOther = (m: SeqMessage) =>
    m.seq > lastReadSeq && (currentUserId == null || m.author_id !== currentUserId);
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
 * The viewer's own messages are excluded — a message you send is not "new" to
 * you, so it never raises the divider.
 *
 * Degrades to null while the BE hasn't supplied `last_read_seq` yet, so the
 * feature ships dark and lights up once the field lands.
 */
export function useNewMessagesDivider(
  channelId: string | null | undefined,
  messages: readonly SeqMessage[],
  lastReadSeq: number | null | undefined,
  currentUserId: string | null | undefined,
): NewMessagesDivider | null {
  const snapshotRef = useRef<{ channelId: string | null; seq: number | null }>({
    channelId: null,
    seq: null,
  });
  const key = channelId ?? null;
  const snap = snapshotRef.current;
  if (snap.channelId !== key) {
    // New conversation visit — take a fresh cursor snapshot (may be null until
    // the channel query resolves; captured below once it does).
    snap.channelId = key;
    snap.seq = lastReadSeq ?? null;
  } else if (snap.seq === null && lastReadSeq != null) {
    // Cursor arrived after entry (async channel query) — capture it once.
    snap.seq = lastReadSeq;
  }
  const snapshotSeq = snap.seq;
  const viewerId = currentUserId ?? null;
  return useMemo(
    () => computeNewMessagesDivider(messages, snapshotSeq, viewerId),
    [messages, snapshotSeq, viewerId],
  );
}
