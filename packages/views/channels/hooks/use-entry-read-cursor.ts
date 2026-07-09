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
    markRead(channelId, {
      onSuccess: (result) => {
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
  // NOTE (#340 cold-start race analysis, Felix 2026-07-09): under the CURRENT
  // channels-page / dm-conversation entries this echo fallback is not actually
  // reachable — the message load AND this hook's mark-read both gate on
  // `active?.id` (channels-page) / the DM prop (dm-conversation), which only
  // exist once the list has loaded and contains this conversation. So the
  // payload snapshot is always the pre-advance value and wins; mark-read's
  // effect structurally cannot have run before the freeze render. The echo is
  // kept deliberately: it is harmless, and B3 (deep-link `around_seq`) may add
  // an entry where channelId precedes the list — that path MUST re-run this
  // analysis. Do not delete it as dead code, and do not copy it as "already
  // protected" elsewhere without re-verifying the mount/gating order.
  const echoedSeq = echoed && echoed.channelId === channelId ? echoed.seq : null;
  return payloadSnap ?? echoedSeq;
}
