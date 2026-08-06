import { useT } from "../../i18n/use-t";

/** Transient send-failure state driving the inline bar (#772). */
export interface ComposerSendErrorState {
  /** Optional human-readable reason appended as `Send failed · {reason}`. */
  reason?: string;
  /**
   * True when the composer already held DIFFERENT new text at failure time, so
   * the failed text was NOT auto-restored — the bar keeps the new text and
   * offers "Restore previous" instead of a plain Retry (Iris edge case #1).
   */
  conflicted: boolean;
  /**
   * #1276 413 fast-follow: the payload was too large. The message guides the
   * user to shorten (a plain retry of the same text just 413s again), and the
   * bar offers no Retry — the preserved text is editable in the composer.
   */
  tooLong?: boolean;
}

/**
 * #772 inline send-failure bar, rendered directly above the composer. Replaces
 * the old permanent "failed" bubble in the transcript: on a failed send the
 * surface restores the text into the composer (or, when the composer already
 * holds new text, keeps it and surfaces `Restore previous`) and shows this bar.
 * It clears on retry / restore / the next successful send. No auto-retry (#772
 * v1 is Raft-layer parity; outbox/auto-retry is a deferred surpass layer).
 */
export function ComposerSendErrorBar({
  error,
  onRetry,
  onRestore,
}: {
  error: ComposerSendErrorState | null;
  /** Re-send the current composer content (Iris: "Retry = 当前文"). */
  onRetry: () => void;
  /** Put the kept-back failed text into the composer (conflicted case). */
  onRestore: () => void;
}) {
  const { t } = useT("channels");
  if (!error) return null;

  const message = error.conflicted
    ? t(($) => $.composer.send_failed_kept_previous)
    : error.tooLong
      ? t(($) => $.composer.send_failed_too_long)
      : error.reason
        ? t(($) => $.composer.send_failed_reason, { reason: error.reason })
        : t(($) => $.composer.send_failed_title);

  return (
    <output
      className="mx-1 mb-1 flex items-center gap-2 rounded-md bg-destructive/10 px-2 py-1 text-xs text-destructive"
    >
      <span className="min-w-0 flex-1 truncate">{message}</span>
      {/* #1276 413: no Retry — the same oversized text just fails again; the user
          shortens the preserved text and sends normally. */}
      {!error.tooLong && (
        <button
          type="button"
          onClick={error.conflicted ? onRestore : onRetry}
          className="shrink-0 font-medium underline underline-offset-2 hover:no-underline"
        >
          {error.conflicted
            ? t(($) => $.composer.restore_previous)
            : t(($) => $.composer.retry)}
        </button>
      )}
    </output>
  );
}
