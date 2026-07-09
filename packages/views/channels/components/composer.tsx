"use client";

import type { ReactNode } from "react";
import { Send } from "lucide-react";
import type { ChannelMessage } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { ReadOnlyConversationBanner } from "./read-only-conversation-banner";
import { QuoteReplyPreview } from "./quote-reply-preview";

/**
 * The conversation surface a composer belongs to. The composer shell is
 * identical across all four (#46/#49 consolidation); `surface` is threaded so
 * per-surface chrome / analytics can key off it and so tests can assert the one
 * shell renders everywhere.
 */
export type ComposerSurface = "channel" | "dm_channel" | "legacy_dm" | "thread";

export interface ComposerQuotePreviewProps {
  message: ChannelMessage;
  currentUserId: string | null;
  ownName?: string;
  onCancel: () => void;
}

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
  /** Quote target rendered inside the shell without passing JSX through parents. */
  quotePreview?: ComposerQuotePreviewProps;
  /** Action-row controls left of Send (attach, mention, issue-ref, project). */
  leadingActions?: ReactNode;
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
  quotePreview,
  leadingActions,
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
        className="composer-shell min-w-0 rounded-lg border border-border/35 bg-background shadow-none"
        data-slot="composer-shell"
        data-composer-surface={surface}
      >
        {prefix}
        {quotePreview ? <QuoteReplyPreview {...quotePreview} /> : null}
        {tray ? (
          <div className="min-w-0 px-2 pt-2" data-slot="composer-tray">
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
          <div className="flex min-h-8 min-w-0 flex-1 items-center gap-0.5 overflow-x-auto text-muted-foreground">
            {leadingActions}
          </div>
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
  );
}
