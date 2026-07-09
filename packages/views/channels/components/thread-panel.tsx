"use client";

import { useMemo, type ReactNode } from "react";
import { ArrowLeft, MessageSquare, X } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import type { ChannelMessage } from "@multica/core/types";
import { useT } from "../../i18n/use-t";
import { ChannelMessageList } from "./channel-message-list";
import { ComposerQuotePreview } from "./message-quote";
import type { QuoteTarget } from "./message-quote-types";
import { ThreadRootPreview } from "./thread-root-preview";
import { Composer } from "./composer";
import { ReadOnlyConversationBanner } from "./read-only-conversation-banner";
import { ConversationHeader } from "./conversation-surface";

/**
 * The "from thread" marker for a reply that is also visible in the main
 * timeline. Keeps the mainstream surface from reading as a bare duplicate: it
 * is explicitly labelled as originating in a thread.
 */
export function ThreadOriginTag({ className }: { className?: string }) {
  const { t } = useT("channels");
  return (
    <span
      data-testid="thread-origin-tag"
      className={cn(
        "inline-flex items-center gap-1 rounded-full border border-border/40 bg-muted/40 px-1.5 py-0.5 text-[10px] font-normal leading-none text-muted-foreground",
        className,
      )}
    >
      <MessageSquare className="size-3" aria-hidden="true" />
      {t(($) => $.thread.from_thread_badge)}
    </span>
  );
}

export interface ThreadPanelProps {
  root: ChannelMessage;
  replies: ChannelMessage[];
  currentUserId: string | null;
  currentUserName?: string;
  /**
   * Show this reply in the main timeline too. Optional: when
   * `onShowInChannelChange` is omitted the checkbox is not rendered at all —
   * the affordance stays hidden until the #256 projection contract is deployed.
   */
  showInChannel?: boolean;
  onShowInChannelChange?: (next: boolean) => void;
  isMobile: boolean;
  /** Return to the parent conversation (mobile back / desktop close). */
  onBack: () => void;
  /** Jump to the root in the main timeline. */
  onViewParent?: () => void;
  loading?: boolean;
  loadError?: boolean;
  onRetry?: () => void;
  onReact?: (message: ChannelMessage, emoji: string) => void;
  onQuoteMessage?: (message: ChannelMessage) => void;
  quoteTarget?: QuoteTarget | null;
  onClearQuote?: () => void;
  // Composer (surface="thread") wiring — the surface owns the editor + send.
  editor: ReactNode;
  onSend: () => void;
  sendDisabled: boolean;
  sending?: boolean;
  composerLeadingActions?: ReactNode;
  /** Read-only surface (archived channel) → banner instead of composer. */
  readOnly?: boolean;
  readOnlyContent?: ReactNode;
  /** Activity strip rendered between the reply list and the composer. */
  activitySlot?: ReactNode;
}

/**
 * The thread panel: a pinned read-only root, a FLAT reply list (no nesting —
 * the reply list is never given an open-thread affordance), the reused
 * `<Composer surface="thread">` (an optional show-in-channel action, hidden
 * unless a handler is supplied — deferred to the #256 main-timeline
 * projection). Following is implicit (replying follows the
 * thread); there is no explicit follow toggle.
 */
export function ThreadPanel({
  root,
  replies,
  currentUserId,
  currentUserName,
  showInChannel = false,
  onShowInChannelChange,
  isMobile,
  onBack,
  onViewParent,
  loading,
  loadError,
  onRetry,
  onReact,
  onQuoteMessage,
  quoteTarget,
  onClearQuote,
  editor,
  onSend,
  sendDisabled,
  sending,
  composerLeadingActions,
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

  const headerActions = useMemo(
    () =>
      isMobile ? undefined : (
        <Button
          variant="ghost"
          size="icon"
          className="size-8"
          aria-label={t(($) => $.thread.close_aria)}
          onClick={onBack}
        >
          <X className="size-4" />
        </Button>
      ),
    [isMobile, onBack, t],
  );

  const composerActions = useMemo(
    () => (
      <>
        {onShowInChannelChange ? (
          <label
            className="flex shrink-0 items-center gap-1.5 px-1 text-xs text-muted-foreground"
            data-slot="thread-show-in-channel"
          >
            <input
              type="checkbox"
              className="size-3.5 accent-primary"
              checked={showInChannel}
              onChange={(event) => onShowInChannelChange(event.target.checked)}
            />
            {t(($) => $.thread.show_in_channel_label)}
          </label>
        ) : null}
        {composerLeadingActions}
      </>
    ),
    [showInChannel, onShowInChannelChange, composerLeadingActions, t],
  );

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
        key={`thread:${root.id}:${loading ? "loading" : "ready"}`}
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
          />
        }
        loading={loading}
        loadErrorLabel={loadError ? t(($) => $.thread.load_failed) : undefined}
        onRetry={onRetry}
        onReact={onReact}
        onQuoteMessage={onQuoteMessage}
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
            isMobile={isMobile}
            prefix={quoteTarget ? (
              <ComposerQuotePreview
                quote={quoteTarget}
                onCancel={onClearQuote ?? (() => {})}
                cancelLabel={t(($) => $.quote.cancel)}
              />
            ) : undefined}
            leadingActions={composerActions}
          />
        </>
      )}
    </div>
  );
}
