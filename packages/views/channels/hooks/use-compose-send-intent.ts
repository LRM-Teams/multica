import { useCallback, useRef } from "react";
import { createSafeId } from "@multica/core/utils";

export interface ComposeSendIntent {
  /**
   * Try to begin sending the current compose `payloadKey`. Returns the
   * `client_message_id` for this intent, or `null` when a send is already in
   * progress — in which case the caller MUST abort.
   *
   * The `null` return is the send lock: it flips synchronously on the first
   * call, so a held / auto-repeating Enter in the same keydown burst fires
   * exactly one send instead of thousands (#207).
   *
   * The id is bound to `payloadKey`, so it stays stable while the draft is
   * unchanged (an unacked-but-landed send dedupes to a 200 upsert on retry) but
   * is re-minted the moment the draft changes (an edited resend is a new intent,
   * not a same-id / new-payload 409 that would soft-lock the composer).
   */
  beginSend: (payloadKey: string) => string | null;
  /** The message committed (onSuccess) — retire this intent so the next send is fresh. */
  finishSend: () => void;
  /**
   * Abandon the current intent without a commit (e.g. the backend returned 409
   * for this id) so the next send mints a fresh id and can recover.
   */
  resetIntent: () => void;
  /** Release the send lock once the request settles (onSettled — success or error). */
  settleSend: () => void;
}

/**
 * Owns one composer's send lifecycle: a synchronous send lock plus a
 * payload-bound compose-intent id (`client_message_id`). `useRef` is only the
 * mechanism for surviving re-renders and blocking a synchronous keydown burst —
 * it never leaks out; callers work in terms of the intent, not refs.
 *
 * Instantiate one per composer entry (channel top-level vs. thread reply) so an
 * in-flight send on one never locks the other.
 */
export function useComposeSendIntent(): ComposeSendIntent {
  const sendInProgress = useRef(false);
  const currentIntentId = useRef<string | null>(null);
  const intentPayloadKey = useRef<string | null>(null);

  const clearIntent = useCallback(() => {
    currentIntentId.current = null;
    intentPayloadKey.current = null;
  }, []);

  const beginSend = useCallback((payloadKey: string): string | null => {
    if (sendInProgress.current) return null;
    if (currentIntentId.current === null || intentPayloadKey.current !== payloadKey) {
      currentIntentId.current = createSafeId();
      intentPayloadKey.current = payloadKey;
    }
    sendInProgress.current = true;
    return currentIntentId.current;
  }, []);

  const settleSend = useCallback(() => {
    sendInProgress.current = false;
  }, []);

  return { beginSend, finishSend: clearIntent, resetIntent: clearIntent, settleSend };
}

/**
 * Stable key for the current compose payload. The intent id is re-minted
 * whenever this changes, so editing the text, the bound attachments, OR the
 * `scope` (e.g. the thread root a reply targets) counts as a new intent (fresh
 * id) rather than a 409-conflicting retry of the previous one — the backend
 * treats a differing reply-thread as a same-id / different-payload 409.
 */
export function composePayloadKey(
  content: string,
  attachmentIds: readonly string[] = [],
  scope = "",
): string {
  // NUL separator so no scope/content/attachment combination can alias another.
  return [scope, content, attachmentIds.join(",")].join("\u0000");
}
