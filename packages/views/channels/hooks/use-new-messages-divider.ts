import { useMemo, useRef } from "react";

export interface NewMessagesDivider {
  /** Id of the first loaded message newer than the entry read cursor. */
  anchorMessageId: string;
  /** How many loaded messages sit at/after the anchor — the "N new messages". */
  count: number;
}

interface SeqMessage {
  id: string;
  seq: number;
}

// Pure: the first message whose seq is beyond the read cursor, plus how many
// messages sit at/after it. Null when the cursor is unknown or everything
// currently loaded is already read. The loaded window is contiguous, so when
// the anchor is present every newer message is loaded too — `count` is the
// exact unread total, not an undercount. Exported for exhaustive unit tests.
export function computeNewMessagesDivider(
  messages: readonly SeqMessage[],
  lastReadSeq: number | null,
): NewMessagesDivider | null {
  if (lastReadSeq == null) return null;
  const index = messages.findIndex((m) => m.seq > lastReadSeq);
  if (index < 0) return null;
  const anchor = messages[index];
  if (!anchor) return null;
  return { anchorMessageId: anchor.id, count: messages.length - index };
}

/**
 * The "N new messages" divider anchor for a conversation (#303). Pins to the
 * first message past the viewer's read cursor as captured ON ENTRY, and stays
 * frozen for the visit: opening the conversation fires mark-read, which
 * advances the server cursor and refetches the channel, but the divider must
 * not chase it down the list ("进会话即见、不随手清" — Iris). We therefore
 * snapshot the first known `lastReadSeq` per channel visit and compute against
 * that snapshot; a later (advanced) value is ignored.
 *
 * Degrades to null while the BE hasn't supplied `last_read_seq` yet, so the
 * feature ships dark and lights up once the field lands. Residual race: if the
 * channel query only resolves *after* the entry mark-read round-trips, the
 * first cursor we see is already advanced and the divider stays hidden — a
 * graceful miss, never a misplaced line. (A pre-advance cursor echoed by the
 * mark-read response would remove even that; tracked with BE.)
 */
export function useNewMessagesDivider(
  channelId: string | null | undefined,
  messages: readonly SeqMessage[],
  lastReadSeq: number | null | undefined,
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
  return useMemo(
    () => computeNewMessagesDivider(messages, snapshotSeq),
    [messages, snapshotSeq],
  );
}
