"use client";

import type { ReactNode } from "react";
import { Send } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { ReadOnlyConversationBanner } from "./read-only-conversation-banner";
import { VoiceInputButton } from "./voice-input-button";
import type { VoiceRecordingAttachment } from "../lib/voice-audio";

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
  voiceDisabled?: boolean;
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
  voiceDisabled = false,
  onVoiceSend,
  tray,
  readOnly = false,
  readOnlyContent,
}: ComposerProps) {
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
            "composer-editor-scroll min-h-16 overflow-y-auto px-4 pt-3 overscroll-contain",
            isMobile ? "max-h-[28dvh]" : "max-h-40",
          )}
          data-slot="composer-editor-scroll"
        >
          {editor}
        </div>
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
                disabled={voiceDisabled || sending}
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
