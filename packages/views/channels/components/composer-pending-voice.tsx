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
  /** A retry is in flight — both actions disable so the item can't be double-sent or dropped mid-send. */
  retrying: boolean;
  /** Re-submit THIS recording through the real voice send path. */
  onRetry: () => void;
  /** Explicit abandon — the only way besides success that this disappears. */
  onDelete: () => void;
}) {
  const { t } = useT("channels");
  if (!pending) return null;

  return (
    <div
      data-testid="composer-pending-voice"
      className="mx-1 mb-1 flex items-center gap-2 rounded-md bg-destructive/10 px-2 py-1 text-xs text-destructive"
    >
      {/* A live region so the failure is announced, not only drawn. */}
      <output className="min-w-0 flex-1 truncate">
        {t(($) => $.composer.voice_unsent, { duration: formatDuration(pending.durationMs) })}
      </output>
      <button
        type="button"
        disabled={retrying}
        onClick={onRetry}
        data-testid="composer-pending-voice-retry"
        className="shrink-0 font-medium underline underline-offset-2 hover:no-underline disabled:opacity-50"
      >
        {t(($) => $.composer.voice_unsent_retry)}
      </button>
      <button
        type="button"
        disabled={retrying}
        onClick={onDelete}
        data-testid="composer-pending-voice-delete"
        className="shrink-0 underline underline-offset-2 hover:no-underline disabled:opacity-50"
      >
        {t(($) => $.composer.voice_unsent_delete)}
      </button>
    </div>
  );
}
