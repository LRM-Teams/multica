"use client";

import { useMemo, type ReactNode } from "react";
import { ArrowLeft, Maximize2, MessageSquare, X } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import type { ChannelMessage } from "@multica/core/types";
import type { OpenAgentPanelFn } from "@multica/core/agents";
import { useT } from "../../i18n/use-t";
import { ChannelMessageList } from "./channel-message-list";
import { ComposerQuotePreview } from "./message-quote";
import type { QuoteTarget } from "./message-quote-types";
import { ThreadRootPreview } from "./thread-root-preview";
import { Composer } from "./composer";
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
  /** Jump to the root in the main timeline. */
  onViewParent?: () => void;
  loading?: boolean;
  loadError?: boolean;
  onRetry?: () => void;
  onReact?: (message: ChannelMessage, emoji: string) => void;
  onQuoteMessage?: (message: ChannelMessage) => void;
  /** Retry a failed optimistic send (reuses `client_message_id`). */
  onRetrySend?: (message: ChannelMessage) => void;
  /** Click an agent author's avatar/name → open the agent side panel (parity
   *  with the main channel list, which passes the same handler). Without it,
   *  thread avatars only show the hover card, never open the panel (#488).
   *  LRM-292: id + optional identity snapshot from the message row. */
  onOpenAgent?: OpenAgentPanelFn;
  quoteTarget?: QuoteTarget | null;
  onClearQuote?: () => void;
  // Composer (surface="thread") wiring — the surface owns the editor + send.
  editor: ReactNode;
  onSend: () => void;
  sendDisabled: boolean;
  sending?: boolean;
  voicePlaybackScope?: string;
  voiceDisabled?: boolean;
  onVoiceSend?: (
    transcript: string,
    durationMs: number,
    attachment: VoiceRecordingAttachment,
  ) => boolean;
  composerLeadingActions?: ReactNode;
  /** Slack-style attachment tray above the editor (Composer `tray` slot). */
  composerTray?: ReactNode;
  /** Read-only surface (archived channel) → banner instead of composer. */
  readOnly?: boolean;
  readOnlyContent?: ReactNode;
  /** Activity strip rendered between the reply list and the composer. */
  activitySlot?: ReactNode;
}

/**
 * The thread panel: a pinned read-only root, a FLAT reply list (no nesting —
 * the reply list is never given an open-thread affordance), the reused
 * `<Composer surface="thread">`. A thread reply stays in that thread; the
 * header exposes the same minimal explicit follow control for group and DM
 * threads while reply/mention auto-follow remains a server responsibility.
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
  loading,
  loadError,
  onRetry,
  onReact,
  onQuoteMessage,
  onRetrySend,
  onOpenAgent,
  quoteTarget,
  onClearQuote,
  editor,
  onSend,
  sendDisabled,
  sending,
  voicePlaybackScope,
  voiceDisabled,
  onVoiceSend,
  composerLeadingActions,
  composerTray,
  readOnly = false,
  readOnlyContent,
  activitySlot,
}: ThreadPanelProps) {
  const { t } = useT("channels");

  // `leading` / `actions` on ConversationHeader and `leadingActions` on
  // Composer are non-allowlisted slot props, so their inline JSX is memoized to
  // keep a stable element identity across renders (react-doctor jsx-as-prop).
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
      ) : (
        <span className="flex size-8 items-center justify-center rounded-md bg-muted text-muted-foreground">
          <MessageSquare className="size-4" />
        </span>
      ),
    [isMobile, onBack, t],
  );

  // LRM-384 / scheme A — no dark floating Maximize+Download capsule on the
  // thread surface. Desktop keeps a 28px ghost "open in main" control in the
  // header; download stays out of the main UI (no ⋯ export entry yet).
  const headerActions = useMemo(
    () => (
      <>
        <ThreadFollowButton
          followed={followed}
          disabled={followDisabled}
          onFollowChange={onFollowChange}
        />
        {!isMobile && onViewParent && (
          <Button
            variant="ghost"
            size="icon"
            className="size-7 text-muted-foreground hover:bg-muted hover:text-foreground"
            aria-label={t(($) => $.thread.open_in_main_aria)}
            onClick={onViewParent}
          >
            <Maximize2 className="size-3.5" />
          </Button>
        )}
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
    [followDisabled, followed, isMobile, onBack, onFollowChange, onViewParent, t],
  );

  const composerActions = useMemo(() => composerLeadingActions, [composerLeadingActions]);

  return (
    <div className="flex h-full min-h-0 min-w-0 flex-col bg-background">
      <ConversationHeader
        isMobile={isMobile}
        leading={headerLeading}
        title={t(($) => $.thread.title)}
        meta={
          replies.length > 0 ? t(($) => $.thread.meta_count, { count: replies.length }) : undefined
        }
        actions={headerActions}
      />

      <ChannelMessageList
        key={`thread:${root.id}`}
        messages={replies}
        currentUserId={currentUserId}
        ownName={currentUserName}
        emptyLabel={t(($) => $.thread.empty_replies)}
        initialScroll="top"
        header={
          <ThreadRootPreview
            message={root}
            currentUserId={currentUserId}
            ownName={currentUserName}
            onViewParent={onViewParent}
            onOpenAgent={onOpenAgent}
          />
        }
        loading={loading}
        loadErrorLabel={loadError ? t(($) => $.thread.load_failed) : undefined}
        onRetry={onRetry}
        onReact={onReact}
        onQuoteMessage={onQuoteMessage}
        onRetrySend={onRetrySend}
        onOpenAgent={onOpenAgent}
      />

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
            voiceDisabled={voiceDisabled}
            onVoiceSend={onVoiceSend}
            isMobile={isMobile}
            prefix={quoteTarget ? (
              <ComposerQuotePreview
                quote={quoteTarget}
                onCancel={onClearQuote ?? (() => {})}
                cancelLabel={t(($) => $.quote.cancel)}
              />
            ) : undefined}
            tray={composerTray}
            leadingActions={composerActions}
          />
        </>
      )}
    </div>
  );
}
