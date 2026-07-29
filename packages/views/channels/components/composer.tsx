"use client";

import { useCallback, useId, useState, type ReactNode } from "react";
import { Send } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { ReadOnlyConversationBanner } from "./read-only-conversation-banner";
import { VoiceInputButton } from "./voice-input-button";
import type { VoiceRecordingAttachment } from "../lib/voice-audio";
import {
  resolveVoiceBlockReason,
  type VoiceBlockReason,
  type VoiceCapturePhase,
} from "./voice-block-reason";
import { useT } from "../../i18n/use-t";

/**
 * The conversation surface a composer belongs to. The composer shell is
 * identical across all four (#46/#49 consolidation); `surface` is threaded so
 * per-surface chrome / analytics can key off it and so tests can assert the one
 * shell renders everywhere.
 */
export type ComposerSurface = "channel" | "dm_channel" | "legacy_dm" | "thread";

export interface ComposerProps {
  /** Which of the four surfaces this composer is mounted in. */
  surface: ComposerSurface;
  /** The rich-text editor node (surface owns the ref / handlers). */
  editor: ReactNode;
  sendLabel: string;
  sendDisabled: boolean;
  /** In-flight guard from the send lock — also disables Send. */
  sending?: boolean;
  onSend: () => void;
  isMobile: boolean;
  /** Optional content pinned above the editor (e.g. a reply-to preview). */
  prefix?: ReactNode;
  /** Action-row controls left of Send (attach, mention, issue-ref, project). */
  leadingActions?: ReactNode;
  /** Optional speech input shared by channel, DM, and thread composers. */
  voiceChannelId?: string;
  voicePlaybackScope?: string;
  /**
   * #858 — the conditions that block voice recording, so the composer can both
   * disable the mic AND say why in one place. Deliberately the raw conditions
   * rather than a `disabled` boolean: deriving both from one source is what
   * makes "disabled but unexplained" unrepresentable, which was the whole bug.
   *
   * Omit entirely on surfaces with no mic.
   */
  voiceBlock?: {
    /** An unsent recording waits for THIS target. DMs pass nothing (#838 only wired channels/threads). */
    pendingVoice?: boolean;
    hasTextDraft: boolean;
    /** Any tray item, uploaded or not — `pending.length > 0`, not `hasUploading`. */
    hasAttachmentDraft: boolean;
  };
  onVoiceSend?: (
    durationMs: number,
    attachment: VoiceRecordingAttachment,
  ) => boolean;
  /**
   * Attachment tray mount point (#151/#154). Rendered above the input and never
   * over the Send control, so touch targets stay reachable; the Attachment lane
   * (#237) fills it.
   */
  tray?: ReactNode;
  /** Read-only surface (archived channel, closed DM) → banner, no input. */
  readOnly?: boolean;
  readOnlyContent?: ReactNode;
}

/**
 * LRM-353 — composer chrome uses semantic tokens only (no light-only hex fills).
 * Light: border = `--input` (line-strong); surface = `--card` (slightly raised).
 * Dark: same classes; hover/focus stay on muted / brand — never a light gray hex wash.
 * Focus ring: brand/30 so both themes keep a readable focus cue.
 */
export const COMPOSER_SHELL_CLASSNAME =
  "composer-shell min-w-0 rounded-lg border border-input bg-card text-foreground shadow-none " +
  "focus-within:border-ring focus-within:ring-1 focus-within:ring-brand/30";

/**
 * The one composer shell reused across channel / dm_channel / legacy_dm /
 * thread. Owns layout only: input scroll area, tray mount point, action row and
 * the Send control (disabled while empty or a send is in flight). The send
 * lifecycle (send-lock 3-way, @mention payload) lives in the surface via
 * `useComposerSend`; this component stays presentational.
 */
export function Composer({
  surface,
  editor,
  sendLabel,
  sendDisabled,
  sending = false,
  onSend,
  isMobile,
  prefix,
  leadingActions,
  voiceChannelId,
  voicePlaybackScope,
  voiceBlock,
  onVoiceSend,
  tray,
  readOnly = false,
  readOnlyContent,
}: ComposerProps) {
  const { t } = useT("channels");
  const voiceStatusId = useId();
  // The mic's OWN upload — only this may resolve to "uploading". An attachment
  // upload arrives as `hasAttachmentDraft` and gets the attachment sentence
  // (Iris, #858): calling a PDF upload "uploading your voice message" would be
  // the same class of lie this ticket removes.
  const [capturePhase, setCapturePhase] = useState<VoiceCapturePhase>("idle");
  const handleCapturePhaseChange = useCallback((phase: VoiceCapturePhase) => setCapturePhase(phase), []);

  const voiceBlockReason: VoiceBlockReason | null = voiceBlock
    ? resolveVoiceBlockReason({
        capturePhase,
        pendingVoice: voiceBlock.pendingVoice ?? false,
        sending,
        hasTextDraft: voiceBlock.hasTextDraft,
        hasAttachmentDraft: voiceBlock.hasAttachmentDraft,
      })
    : null;
  // One source for both. A separate `disabled` prop could drift out of step with
  // the reason and put us back to a mic that is grey for an unstated cause.
  const voiceBlocked = voiceBlockReason !== null;
  // One sentence per reason, no shared fallback. A single string covering
  // several causes is true for at most one of them — that is exactly how the
  // retired `composer.voice_blocked` came to tell users with an empty composer
  // to clear text and attachments.
  let voiceBlockText: string | null = null;
  switch (voiceBlockReason) {
    case "starting":
      voiceBlockText = t(($) => $.composer.voice_blocked_starting);
      break;
    case "uploading":
      voiceBlockText = t(($) => $.composer.voice_blocked_uploading);
      break;
    case "pending_voice":
      voiceBlockText = t(($) => $.composer.voice_blocked_pending_voice);
      break;
    case "sending":
      voiceBlockText = t(($) => $.composer.voice_blocked_sending);
      break;
    // LRM-702 — "clear text to record" hints removed from the composer (Frank:
    // composer 内的文字删掉). The mic still disables while the composer holds
    // text/attachments (voiceBlocked stays true); we just no longer render the
    // sentence inline. Falls through to `default` (null) → no <output>.
    case "attachment_draft":
      voiceBlockText = t(($) => $.composer.voice_blocked_attachment_draft);
      break;
    case "text_draft":
    case "text_and_attachment_draft":
    default:
      voiceBlockText = null;
  }

  if (readOnly) {
    return <ReadOnlyConversationBanner>{readOnlyContent}</ReadOnlyConversationBanner>;
  }
  return (
    <div
      className={cn(
        "shrink-0",
        isMobile ? "px-3 pb-[max(0.75rem,env(safe-area-inset-bottom))]" : "px-5 pb-4",
      )}
    >
      <div
        className={COMPOSER_SHELL_CLASSNAME}
        data-slot="composer-shell"
        data-composer-surface={surface}
      >
        {prefix}
        {tray ? (
          // min-w-0 is required so the horizontal tray strip can scroll inside
          // the shell instead of expanding the composer or stacking chips.
          <div className="min-w-0 overflow-hidden px-2 pt-2" data-slot="composer-tray">
            {tray}
          </div>
        ) : null}
        <div
          className={cn(
            // LRM-491: shorter empty min-height so a one-line Slack-style
            // placeholder is not propped up by a 64px box (was min-h-16).
            "composer-editor-scroll overflow-y-auto px-4 pt-3 overscroll-contain",
            isMobile ? "min-h-11 max-h-[28dvh]" : "min-h-12 max-h-40",
          )}
          data-slot="composer-editor-scroll"
        >
          {editor}
        </div>
        {/* #858 — the explanation is VISIBLE and lives in the shell, not in a
            `title`. A native-disabled button fires no hover events, so a tooltip
            reaches nobody; and a visible line is the same explanation for
            touch, mouse, keyboard and screen readers rather than only one of
            them. Rendered ONLY when a cause exists — "recordable" must show
            nothing at all. */}
        {voiceBlockText ? (
          <output
            id={voiceStatusId}
            // `<output>` already maps to role="status" — an explicit attribute
            // is redundant (react-doctor prefer-tag-over-role). The tests assert
            // the COMPUTED role via getByRole("status"), which is the stronger
            // check anyway: it verifies what assistive tech resolves, not that a
            // literal string is present.
            className="px-4 pb-1 text-xs text-muted-foreground"
            data-slot="composer-voice-block-status"
            data-voice-block-reason={voiceBlockReason}
          >
            {voiceBlockText}
          </output>
        ) : null}
        <div
          className={cn("flex items-center justify-between px-2 pb-2", isMobile && "gap-2")}
          data-slot="composer-action-row"
        >
          <div
            className="flex min-h-8 min-w-0 flex-1 items-center gap-0.5 overflow-x-auto text-muted-foreground [&_svg]:text-current"
            data-slot="composer-leading-actions"
          >
            {leadingActions}
          </div>
          <div
            className="flex shrink-0 items-center gap-1.5 text-muted-foreground"
            data-slot="composer-submit-actions"
          >
            {voiceChannelId && voicePlaybackScope && onVoiceSend ? (
              <VoiceInputButton
                channelId={voiceChannelId}
                disabled={voiceBlocked}
                describedById={voiceBlockText ? voiceStatusId : undefined}
                onCapturePhaseChange={handleCapturePhaseChange}
                isMobile={isMobile}
                playbackScope={voicePlaybackScope}
                onVoiceSend={onVoiceSend}
              />
            ) : null}
            {/* Primary Send owns primary-foreground; parent muted only tints mic chrome. */}
            <Button
              onClick={onSend}
              disabled={sendDisabled || sending}
              size="sm"
              className={cn("shrink-0", isMobile && "min-h-10 px-4")}
            >
              <Send className="size-4" /> {sendLabel}
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}
