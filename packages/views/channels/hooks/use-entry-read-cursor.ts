import { useEffect, useState } from "react";
import type { MarkChannelReadResult } from "@multica/core/types";

type MarkRead = (
  channelId: string,
  options?: { onSuccess?: (result: MarkChannelReadResult) => void },
) => void;

/**
 * The viewer's read cursor for the active conversation as it was ON ENTRY — the
 * anchor for the "N new messages" divider (#303). Fires mark-read when the
 * conversation opens and captures the response's `previous_last_read_seq`: the
 * cursor value from *before* this visit advanced it, so it's immune to the
 * mark-read → refetch race (Frank's race-free requirement).
 *
 * Prefers the list `payloadLastReadSeq` when present so an already-cached
 * conversation can pin the divider at first render (no round-trip wait); a
 * cold-loaded conversation (no cached cursor) falls back to the echoed response.
 * Both are pre-advance values, so neither can misplace the line.
 *
 * The echoed value is captured once per conversation visit — re-marks (e.g. new
 * messages arriving while you're already here) don't move the frozen divider.
 */
export function useEntryReadCursor(
  channelId: string | null | undefined,
  payloadLastReadSeq: number | null | undefined,
  markRead: MarkRead,
): number | null {
  const [echoed, setEchoed] = useState<{ channelId: string; seq: number | null } | null>(
    null,
  );

  useEffect(() => {
    if (!channelId) return;
    markRead(channelId, {
      onSuccess: (result) =>
        setEchoed((prev) =>
          prev?.channelId === channelId
            ? prev
            : { channelId, seq: result?.previous_last_read_seq ?? null },
        ),
    });
    // `markRead` (react-query mutate) is referentially stable.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelId]);

  const echoedSeq = echoed && echoed.channelId === channelId ? echoed.seq : null;
  return payloadLastReadSeq ?? echoedSeq;
}
