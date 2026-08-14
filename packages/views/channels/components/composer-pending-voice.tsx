import { cn } from "@multica/ui/lib/utils";
import type { ChannelMessageQuoteInput } from "@multica/core/types";
import { useT } from "../../i18n/use-t";
import type { VoiceRecordingAttachment } from "../lib/voice-audio";

/**
 * A recording whose upload succeeded but whose message send did not (#838).
 *
 * Keeps the ORIGINAL attachment — retry re-submits this exact one, so the user
 * never re-records and we never re-upload.
 */
export interface PendingVoiceState {
  /**
   * The IMMUTABLE target this recording was meant for — channel id, or
   * `channelId:threadRootId` for a thread reply.
   *
   * #838 H0 (Iris): this state lives on the whole page, which outlives the
   * user's current channel. Keyed only by "there is a pending voice", a failure
   * in channel A would render on channel B's composer and — far worse — retry
   * would send A's recording into B. The record is therefore bound to where it
   * was going, it only renders when that matches the surface on screen, and
   * retry re-sends to the STORED target, never to whatever is active now.
   */
  targetId: string;
  /** Channel the send goes to on retry (never re-read from `active`). */
  channelId: string;
  /** Thread root for a thread reply; absent for a top-level channel send. */
  threadRootId?: string;
  durationMs: number;
  attachment: VoiceRecordingAttachment;
  /** Structured quote captured with the original send, replayed unchanged. */
  quote?: ChannelMessageQuoteInput;
}

/** `0:07` — seconds only; recordings are capped at 60s (MAX_VOICE_RECORDING_MS). */
function formatDuration(durationMs: number): string {
  const total = Math.max(0, Math.round(durationMs / 1000));
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, "0")}`;
}

/**
 * #838 — the durable record of a failed voice send, rendered in the composer's
 * prefix slot (beside the send-error bar / quote preview).
 *
 * Why this exists rather than reusing `ComposerSendErrorBar` (Iris's contract):
 * that bar's Retry is wired to the TEXT send, and a voice send requires an empty
 * composer — so reusing it would show "Retry" and then send nothing. A button
 * that doesn't do what it promises is worse than no button: silence is merely
 * absent, false feedback misleads.
 *
 * The toast that accompanies the failure is the ANNOUNCEMENT; this item is the
 * RECORD. Dismissing the toast (or closing it by keyboard) must not erase the
 * fact that a recording is still unsent — so this clears on exactly two events:
 * a retry that actually commits, or the user explicitly deleting it. Never a
 * timer, and never silently replaced by a newer recording.
 */
export function ComposerPendingVoice({
  pending,
  retrying,
  onRetry,
  onDelete,
}: {
  pending: PendingVoiceState | null;
  /**
   * A retry is in flight — both actions refuse to fire so the item can't be
   * double-sent or dropped mid-send. They stay focusable (LRM-1354): the block
   * is a handler guard plus `aria-disabled`, never a native `disabled`.
   */
  retrying: boolean;
  /** Re-submit THIS recording through the real voice send path. */
  onRetry: () => void;
  /** Explicit abandon — the only way besides success that this disappears. */
  onDelete: () => void;
}) {
  const { t } = useT("channels");
  if (!pending) return null;

  const duration = formatDuration(pending.durationMs);

  return (
    <div
      data-testid="composer-pending-voice"
      className="mx-1 mb-1 flex items-center gap-2 rounded-md bg-destructive/10 px-2 py-1 text-xs text-destructive"
    >
      {/*
       * A live region so the failure is announced, not only drawn.
       *
       * LRM-1354 — the copy has to CHANGE when a retry goes in flight. Holding
       * "not sent" while resending, with only a visual dim to signal the
       * difference, is the SC 4.1.3 failure: a screen reader user presses Retry
       * and hears nothing at all. `aria-busy` marks the region as unsettled so
       * the eventual outcome (success unmount / failure toast) reads as a
       * resolution instead of an unexplained change.
       */}
      <output
        className="min-w-0 flex-1 truncate"
        aria-busy={retrying || undefined}
        data-testid="composer-pending-voice-status"
      >
        {retrying
          ? t(($) => $.composer.voice_unsent_retrying, { duration })
          : t(($) => $.composer.voice_unsent, { duration })}
      </output>
      {/*
       * LRM-1213/1169 frozen pattern (see research-session-interrupt-banner):
       * the pending control must stay a real focus target. A native `disabled`
       * on the button the user just activated drops focus to <body> in Chromium
       * and never gives it back, so keyboard / screen reader users lose their
       * place and never hear the retry outcome. Keep it focusable, guard the
       * handler.
       *
       * Consequence worth spelling out: `disabled:` Tailwind variants compile to
       * the `:disabled` pseudo-class, so they stop matching the moment the
       * native attribute is gone. The dim state therefore has to come from a
       * condition — otherwise the pending affordance silently disappears.
       */}
      <button
        type="button"
        aria-disabled={retrying || undefined}
        onClick={() => {
          if (retrying) return;
          onRetry();
        }}
        data-testid="composer-pending-voice-retry"
        className={cn(
          "shrink-0 font-medium underline underline-offset-2",
          retrying ? "cursor-not-allowed opacity-50" : "hover:no-underline",
        )}
      >
        {t(($) => $.composer.voice_unsent_retry)}
      </button>
      {/*
       * Delete stays reachable while the retry is in flight on purpose. This
       * record only clears on a committed retry or an explicit abandon, so
       * taking the abandon route away mid-send would leave a keyboard user with
       * no exit at all — the guard blocks the ACTION, not the focus target.
       */}
      <button
        type="button"
        aria-disabled={retrying || undefined}
        onClick={() => {
          if (retrying) return;
          onDelete();
        }}
        data-testid="composer-pending-voice-delete"
        className={cn(
          "shrink-0 underline underline-offset-2",
          retrying ? "cursor-not-allowed opacity-50" : "hover:no-underline",
        )}
      >
        {t(($) => $.composer.voice_unsent_delete)}
      </button>
    </div>
  );
}
