import { useCallback } from "react";
import { ApiError } from "@multica/core/api";
import { useComposeSendIntent } from "./use-compose-send-intent";

/**
 * Which visible-error branch a failed send takes. The third branch of the
 * send-lock 3-way — a 200-dedup success — is silent (no error at all), so it is
 * not represented here; it flows through the commit path instead.
 */
export type SendFailureKind = "conflict" | "retry" | "too_long";

/**
 * Classify a failed send into its recovery branch (#207 send-lock 3-way).
 *
 * - `conflict` (409): the same `client_message_id` already committed a
 *   *different* payload. Retrying the same id would soft-lock the composer, so
 *   the intent must be abandoned and a fresh id minted.
 * - `retry` (net / 5xx / permission / validation): the id never committed, so
 *   the same id can be retried safely — an eventual landing dedupes to a 200
 *   upsert rather than a duplicate row.
 */
export function classifySendFailure(err: unknown): SendFailureKind {
  if (err instanceof ApiError && err.status === 409) return "conflict";
  // #1276 413 fast-follow: the payload is too large — a plain retry of the same
  // text just fails again, so the recovery guides the user to shorten it (the
  // text is preserved + editable like every non-success outcome).
  if (err instanceof ApiError && err.status === 413) return "too_long";
  return "retry";
}

export interface ComposerSendRun<TVars> {
  /**
   * Stable key for the current draft (content + bound attachments + scope).
   * Build it with `composePayloadKey` so the intent id is re-minted whenever the
   * draft changes.
   */
  payloadKey: string;
  /** Build the surface's mutation vars from the minted `client_message_id`. */
  buildVars: (clientMessageId: string) => TVars;
  /** The surface's react-query `mutate` (channel / thread / dm send). */
  mutate: (
    vars: TVars,
    callbacks: {
      onSuccess: () => void;
      onError: (err: unknown) => void;
      onSettled: () => void;
    },
  ) => void;
  /**
   * Commit side effects for a landed send (clear the editor, drop the draft).
   * Runs on a 200 — including the silent idempotent dedup of an unacked landing.
   */
  onCommitted: () => void;
  /**
   * Surface the visible error for a failed send. Called for BOTH the `conflict`
   * and `retry` branches so a failure is never silent — a silent 409 reads to
   * the user as a sent message.
   */
  onVisibleError: (kind: SendFailureKind) => void;
}

export interface ComposerSend {
  /**
   * Run one send. Applies the synchronous send lock (N rapid Enter/click in one
   * burst → exactly one request) and then the 3-way outcome:
   *
   * 1. 200-dedup success → `onCommitted`, silent (no error).
   * 2. 409 conflict → `onVisibleError("conflict")` + fresh-id recovery.
   * 3. net / 5xx / perm / validation → `onVisibleError("retry")` (same id retryable).
   *
   * `onSettled` always releases the lock. Returns `true` when a request was
   * dispatched, `false` when the lock aborted a held/auto-repeat trigger.
   */
  send: <TVars>(run: ComposerSendRun<TVars>) => boolean;
}

/**
 * Owns one composer entry's send lifecycle: the send lock + payload-bound
 * `client_message_id` (via {@link useComposeSendIntent}) wrapped in the 3-way
 * outcome handling that every surface (channel / dm_channel / legacy_dm /
 * thread) shares. Consolidates the previously-duplicated `handleSend` bodies so
 * a bare mutation can never slip past the lock or the visible-error contract.
 *
 * Instantiate one per composer entry (top-level vs. thread reply) so an
 * in-flight send on one never locks the other.
 */
export function useComposerSend(): ComposerSend {
  const { beginSend, finishSend, resetIntent, settleSend } = useComposeSendIntent();

  const send = useCallback(
    <TVars>(run: ComposerSendRun<TVars>): boolean => {
      // Send lock + payload-bound client_message_id in one step. `null` means a
      // send is already running (held / auto-repeat trigger) → abort.
      const clientMessageId = beginSend(run.payloadKey);
      if (clientMessageId === null) return false;

      // The request carries its own `AbortSignal.timeout` (see ApiClient), so a
      // stalled send aborts into a real `onError` instead of hanging — no
      // manual timer / settled-flag race guard is needed here. A timeout/abort
      // classifies as `retry` (never a 409), so the same id is reused and
      // dedupes if the original actually landed.
      run.mutate(run.buildVars(clientMessageId), {
        onSuccess: () => {
          run.onCommitted();
          finishSend();
        },
        onError: (err) => {
          const kind = classifySendFailure(err);
          if (kind === "conflict") resetIntent();
          run.onVisibleError(kind);
        },
        onSettled: () => {
          settleSend();
        },
      });
      return true;
    },
    [beginSend, finishSend, resetIntent, settleSend],
  );

  return { send };
}
