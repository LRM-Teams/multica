import { useEffect, useRef, useState } from "react";
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
 * Two pre-advance sources, so the line can never be misplaced:
 *  - a SNAPSHOT of the list cursor taken at the conversation's first render
 *    (before mark-read runs). On a warm/soft-nav open the list is cached, so
 *    this is the real pre-advance cursor and the divider pins at first render
 *    with no round-trip wait.
 *  - the echoed `previous_last_read_seq`, for a cold/deep-link open where the
 *    list hasn't resolved yet.
 *
 * The list cursor is snapshotted, NOT read live: on a cold/deep-link load the
 * live payload resolves to the ALREADY-advanced cursor (mark-read has run by the
 * time the list arrives), which would beat the echo and hide the divider — the
 * exact gap deep-linking into an unread conversation hit. The first-render
 * snapshot is null on a cold load, so the echo wins there.
 *
 * The echoed value is captured once per conversation visit — re-marks (e.g. new
 * messages arriving while you're already here) don't move the frozen divider.
 */
export function useEntryReadCursor(
  channelId: string | null | undefined,
  payloadLastReadSeq: number | null | undefined,
  markRead: MarkRead,
): number | null {
  const key = channelId ?? null;

  const payloadSnapRef = useRef<{ channelId: string | null; seq: number | null }>({
    channelId: null,
    seq: null,
  });
  if (payloadSnapRef.current.channelId !== key) {
    // First render for this conversation — freeze the (pre-advance) list cursor.
    payloadSnapRef.current = { channelId: key, seq: payloadLastReadSeq ?? null };
  }

  const [echoed, setEchoed] = useState<{ channelId: string; seq: number | null } | null>(
    null,
  );

  useEffect(() => {
    if (!channelId) return;
    // TEMP DIAGNOSTIC (#348 branch A/B split) — remove after the s89 verify.
    // eslint-disable-next-line no-console
    console.log("[#348 diag] markRead firing on entry", { channelId, payloadLastReadSeq });
    markRead(channelId, {
      onSuccess: (result) => {
        // TEMP DIAGNOSTIC (#348 branch A/B split) — remove after the s89 verify.
        // eslint-disable-next-line no-console
        console.log("[#348 diag] markRead onSuccess", { channelId, result });
        setEchoed((prev) =>
          prev?.channelId === channelId
            ? prev
            : { channelId, seq: result?.previous_last_read_seq ?? null },
        );
      },
    });
    // `markRead` (react-query mutate) is referentially stable.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelId]);

  const payloadSnap =
    payloadSnapRef.current.channelId === key ? payloadSnapRef.current.seq : null;
  const echoedSeq = echoed && echoed.channelId === channelId ? echoed.seq : null;
  const result = payloadSnap ?? echoedSeq;
  // TEMP DIAGNOSTIC (#348 branch A/B split) — remove after the s89 verify.
  // eslint-disable-next-line no-console
  console.log("[#348 diag] useEntryReadCursor result", {
    channelId,
    payloadLastReadSeq,
    payloadSnap,
    echoedSeq,
    result,
  });
  return result;
}
