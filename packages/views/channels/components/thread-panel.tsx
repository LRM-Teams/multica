"use client";

import { useMemo, useRef, type ReactNode } from "react";
import { ArrowLeft, X } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import type { ChannelMessage } from "@multica/core/types";
import type { OpenAgentPanelFn } from "@multica/core/agents";
import { useT } from "../../i18n/use-t";
import { ChannelMessageList } from "./channel-message-list";
import { useSelectionQuoteMenu } from "../lib/selection-quote-menu";
import type { ResolvedMessageSelection } from "../lib/selection-quote";
import {
  ComposerSendErrorBar,
  type ComposerSendErrorState,
} from "./composer-send-error-bar";
import { ThreadRootPreview } from "./thread-root-preview";
import { Composer, type ComposerProps } from "./composer";
import { ReadOnlyConversationBanner } from "./read-only-conversation-banner";
import { ConversationHeader } from "./conversation-surface";
import { ThreadFollowButton } from "./thread-follow-button";
import type { VoiceRecordingAttachment } from "../lib/voice-audio";

export interface ThreadPanelProps {
  root: ChannelMessage;
  replies: ChannelMessage[];
  currentUserId: string | null;
  currentUserName?: string;
  isMobile: boolean;
  /** Return to the parent conversation (mobile back / desktop close). */
  onBack: () => void;
  /** Current human viewer's explicit/automatic thread subscription state. */
  followed: boolean;
  followDisabled?: boolean;
  onFollowChange: (followed: boolean) => void;
  /** Jump to the root in the main timeline (closes the thread surface). */
  onViewParent?: () => void;
  /**
   * LRM-572 — parent surface for the header “View in …” link.
   * `channel` →「在 #name 查看」; `dm` →「在对话中查看」.
   */
  parentContext?: "channel" | "dm";
  /** Channel display name for `parentContext="channel"` (no `#` prefix). */
  parentChannelName?: string | null;
  loading?: boolean;
  loadError?: boolean;
  onRetry?: () => void;
  onReact?: (message: ChannelMessage, emoji: string) => void;
  onQuoteMessage?: (message: ChannelMessage) => void;
  /** Set a structured quote target from a selection in this thread. */
  onQuoteSelection?: (selection: ResolvedMessageSelection) => void;
  /** Retry a failed optimistic send (reuses `client_message_id`). */
  onRetrySend?: (message: ChannelMessage) => void;
  /** Click an agent author's avatar/name → open the agent side panel (parity
   *  with the main channel list, which passes the same handler). Without it,
   *  thread avatars only show the hover card, never open the panel (#488).
   *  LRM-292: id + optional identity snapshot from the message row. */
  onOpenAgent?: OpenAgentPanelFn;
  /** Click a human author's avatar/name → open the LRM-619 member Profile
   *  dock (same parity; without it member avatar clicks are dead). */
  onOpenMember?: (userId: string) => void;
  /** #772 inline send-failure bar for the thread composer (surface-owned). */
  sendError?: ComposerSendErrorState | null;
  onRestorePrevious?: () => void;
  // Composer (surface="thread") wiring — the surface owns the editor + send.
  editor: ReactNode;
  onSend: () => void;
  sendDisabled: boolean;
  sending?: boolean;
  voicePlaybackScope?: string;
  /** #858 — raw block conditions; the composer derives both disabled + reason. */
  voiceBlock?: ComposerProps["voiceBlock"];
  onVoiceSend?: (
    durationMs: number,
    attachment: VoiceRecordingAttachment,
  ) => boolean;
  composerLeadingActions?: ReactNode;
  /** Slack-style attachment tray above the editor (Composer `tray` slot). */
  composerTray?: ReactNode;
  /**
   * #838 — extra durable state rendered in the composer prefix, above the send
   * error bar (currently the unsent-voice item). Owned by the caller because
   * the failure record lives with the send handler, not the panel.
   */
  composerPrefixExtra?: ReactNode;
  /** Read-only surface (archived channel) → banner instead of composer. */
  readOnly?: boolean;
  readOnlyContent?: ReactNode;
  /** Activity strip rendered between the reply list and the composer. */
  activitySlot?: ReactNode;
  /** Deep-link target reply id (e.g. from a Reminder anchor) - scrolls to and ring-highlights that bubble in the reply list. */
  highlightMessageId?: string | null;
}

/**
 * The thread panel: a pinned read-only root, a FLAT reply list (no nesting —
 * the reply list is never given an open-thread affordance), the reused
 * `<Composer surface="thread">`. A thread reply stays in that thread; the
 * header exposes the same minimal explicit follow control for group and DM
 * threads while reply/mention auto-follow remains a server responsibility.
 *
 * LRM-572 / LRM-568 — Slack-style header: no Maximize2; subtitle carries a
 * clickable「在 #频道 查看」/「在对话中查看」that shares `onViewParent` with
 * the root「查看原消息」link.
 */
export function ThreadPanel({
  root,
  replies,
  currentUserId,
  currentUserName,
  isMobile,
  onBack,
  followed,
  followDisabled,
  onFollowChange,
  onViewParent,
  parentContext = "channel",
  parentChannelName,
  loading,
  loadError,
  onRetry,
  onReact,
  onQuoteMessage,
  onQuoteSelection,
  onRetrySend,
  onOpenAgent,
  onOpenMember,
  sendError,
  onRestorePrevious,
  editor,
  onSend,
  sendDisabled,
  sending,
  voicePlaybackScope,
  voiceBlock,
  onVoiceSend,
  composerLeadingActions,
  composerTray,
  composerPrefixExtra,
  readOnly = false,
  readOnlyContent,
  activitySlot,
  highlightMessageId,
}: ThreadPanelProps) {
  const { t } = useT("channels");
  // LRM-695 — text-selection Quote/Copy mini-menu over the thread message area.
  const threadMessageAreaRef = useRef<HTMLDivElement>(null);
  const threadSelectionMenu = useSelectionQuoteMenu({
    containerRef: threadMessageAreaRef,
    onQuote: (selection) => onQuoteSelection?.(selection),
  });

  // `leading` / `actions` on ConversationHeader and `leadingActions` on
  // Composer are non-allowlisted slot props, so their inline JSX is memoized to
  // keep a stable element identity across renders (react-doctor jsx-as-prop).
  // LRM-572 — desktop AFTER drops the MessageSquare tile; mobile keeps ← back.
  const headerLeading = useMemo(
    () =>
      isMobile ? (
        <Button
          variant="ghost"
          size="icon"
          className="size-9"
          aria-label={t(($) => $.thread.back_to_conversation)}
          onClick={onBack}
        >
          <ArrowLeft className="size-5" />
        </Button>
      ) : null,
    [isMobile, onBack, t],
  );

  // LRM-572 — subtitle:「N 条回复 · 在 #频道 查看」(DM:「在对话中查看」).
  // Same handler as root「查看原消息」; omit the link when onViewParent is absent.
  const headerMeta = useMemo(() => {
    if (loading) {
      return t(($) => $.thread.meta_loading);
    }
    if (loadError) {
      return t(($) => $.thread.meta_load_failed);
    }

    const countLabel =
      replies.length > 0
        ? t(($) => $.thread.meta_count, { count: replies.length })
        : t(($) => $.thread.meta_empty);

    if (!onViewParent) {
      return countLabel;
    }

    const viewLabel =
      parentContext === "dm"
        ? t(($) => $.thread.view_in_conversation)
        : t(($) => $.thread.view_in_channel, {
            name: parentChannelName?.trim() || "…",
          });

    return (
      <span className="inline-flex min-w-0 max-w-full flex-wrap items-center gap-x-1">
        <span className="truncate">{countLabel}</span>
        <span aria-hidden className="text-muted-foreground/50">
          ·
        </span>
        <button
          type="button"
          className="min-h-8 shrink-0 rounded-sm font-medium text-primary underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          onClick={onViewParent}
        >
          {viewLabel}
        </button>
      </span>
    );
  }, [
    loadError,
    loading,
    onViewParent,
    parentChannelName,
    parentContext,
    replies.length,
    t,
  ]);

  // LRM-384 / LRM-572 — no dark floating Maximize+Download capsule; no Maximize2
  // icon. Desktop: Follow + ✕. Mobile: Follow only (← is in leading).
  const headerActions = useMemo(
    () => (
      <>
        <ThreadFollowButton
          followed={followed}
          disabled={followDisabled}
          onFollowChange={onFollowChange}
        />
        {!isMobile && (
          <Button
            variant="ghost"
            size="icon"
            className="size-8"
            aria-label={t(($) => $.thread.close_aria)}
            onClick={onBack}
          >
            <X className="size-4" />
          </Button>
        )}
      </>
    ),
    [followDisabled, followed, isMobile, onBack, onFollowChange, t],
  );

  const composerActions = useMemo(() => composerLeadingActions, [composerLeadingActions]);

  return (
    <div className="flex h-full min-h-0 min-w-0 flex-col bg-background">
      <ConversationHeader
        isMobile={isMobile}
        leading={headerLeading}
        title={t(($) => $.thread.title)}
        meta={headerMeta}
        actions={headerActions}
      />

      <div ref={threadMessageAreaRef} className="contents">
      <ChannelMessageList
        key={`thread:${root.id}`}
        messages={replies}
        currentUserId={currentUserId}
        ownName={currentUserName}
        emptyLabel={t(($) => $.thread.empty_replies)}
        highlightMessageId={highlightMessageId}
        header={
          <ThreadRootPreview
            message={root}
            currentUserId={currentUserId}
            ownName={currentUserName}
            onViewParent={onViewParent}
            onOpenAgent={onOpenAgent}
            onOpenMember={onOpenMember}
          />
        }
        loading={loading}
        loadErrorLabel={loadError ? t(($) => $.thread.load_failed) : undefined}
        onRetry={onRetry}
        onReact={onReact}
        onQuoteMessage={onQuoteMessage}
        onRetrySend={onRetrySend}
        onOpenAgent={onOpenAgent}
        onOpenMember={onOpenMember}
      />
      </div>
      {threadSelectionMenu.menu}

      {readOnly ? (
        <ReadOnlyConversationBanner>{readOnlyContent}</ReadOnlyConversationBanner>
      ) : (
        <>
          {activitySlot}
          <Composer
            surface="thread"
            editor={editor}
            sendLabel={t(($) => $.composer.send)}
            sendDisabled={sendDisabled}
            sending={sending}
            onSend={onSend}
            voiceChannelId={root.channel_id}
            voicePlaybackScope={voicePlaybackScope}
            voiceBlock={voiceBlock}
            onVoiceSend={onVoiceSend}
            isMobile={isMobile}
            // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- Composer prefix slot; identity is not memo-sensitive
            prefix={sendError || composerPrefixExtra ? (
              <>
                {composerPrefixExtra}
                <ComposerSendErrorBar
                  error={sendError ?? null}
                  onRetry={onSend}
                  onRestore={onRestorePrevious ?? (() => {})}
                />
              </>
            ) : undefined}
            tray={composerTray}
            leadingActions={composerActions}
          />
        </>
      )}
    </div>
  );
}
