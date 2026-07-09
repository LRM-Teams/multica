"use client";

import { useMemo, type ReactNode } from "react";
import { ArrowLeft, Bot, MessageSquare, X } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import type { ChannelMessage } from "@multica/core/types";
import { useT } from "../../i18n/use-t";
import { ChannelMessageList } from "./channel-message-list";
import { ThreadRootPreview } from "./thread-root-preview";
import { Composer } from "./composer";
import { ReadOnlyConversationBanner } from "./read-only-conversation-banner";
import { ConversationHeader } from "./conversation-surface";
import type { ThreadMemberType } from "./thread-participants";

export type ThreadWakeState = "pending" | "replied" | "acked" | "delivered" | "no_reply";

export interface ThreadWakeAnnotation {
  /** Participant identity `${memberType}:${memberId}`. */
  key: string;
  displayName: string;
  memberType: ThreadMemberType;
  /**
   * A known wake state, or an unknown/future value the BE may add (the
   * `(string & {})` escape hatch). Unknown states carry no vetted copy, so the
   * strip drops them rather than surfacing a raw token.
   */
  state: ThreadWakeState | (string & {});
  /** "Why no reply" — surfaced next to a `no_reply` record. */
  reason?: string;
}

function ThreadWakeStrip({ annotations }: { annotations: ThreadWakeAnnotation[] }) {
  const { t } = useT("channels");
  const wakeLabel = (state: ThreadWakeState | (string & {})): string | null => {
    switch (state) {
      case "pending":
        return t(($) => $.thread.wake_pending);
      case "replied":
        return t(($) => $.thread.wake_replied);
      case "acked":
        return t(($) => $.thread.wake_acked);
      case "delivered":
        return t(($) => $.thread.wake_delivered);
      case "no_reply":
        return t(($) => $.thread.wake_no_reply);
      default:
        // Unknown/future state — no vetted copy, so stay silent rather than
        // surface a raw token or read as a refusal.
        return null;
    }
  };
  // Iris UX: only agent participants are ever woken, so a human record (or an
  // unknown-state record with no label) is dropped — never shown as "refused".
  const visible = annotations.flatMap((annotation) => {
    if (annotation.memberType !== "agent") return [];
    const label = wakeLabel(annotation.state);
    return label ? [{ annotation, label }] : [];
  });
  if (visible.length === 0) return null;
  return (
    <div
      data-testid="thread-wake-strip"
      className="flex shrink-0 flex-col gap-1 border-t border-border/30 px-5 py-2 text-xs"
    >
      {visible.map(({ annotation, label }) => (
        <div
          key={annotation.key}
          data-wake-state={annotation.state}
          className="flex items-center gap-2"
        >
          <Bot className="size-3 shrink-0 text-primary" aria-hidden="true" />
          <span className="font-medium text-foreground/90">{annotation.displayName}</span>
          <span
            className={cn(
              "rounded-full px-1.5 py-0.5 text-[11px] leading-none",
              // `no_reply` reads NEUTRAL ("received, no reply needed"), not as a
              // refusal — same muted treatment as an informational chip.
              annotation.state === "no_reply"
                ? "bg-muted text-muted-foreground"
                : "bg-primary/[0.08] text-primary",
            )}
          >
            {label}
          </span>
          {annotation.reason ? (
            <span className="truncate text-muted-foreground">{annotation.reason}</span>
          ) : null}
        </div>
      ))}
    </div>
  );
}

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
  /**
   * Per-participant wake/ack/no_reply records (read-model #235). Presentational
   * only — undefined until the read-model is wired; a non-participant is simply
   * never included here, so it is never shown as woken.
   */
  wakeAnnotations?: ThreadWakeAnnotation[];
  isMobile: boolean;
  /** Return to the parent conversation (mobile back / desktop close). */
  onBack: () => void;
  /** Jump to the root in the main timeline. */
  onViewParent?: () => void;
  loading?: boolean;
  loadError?: boolean;
  onRetry?: () => void;
  onReact?: (message: ChannelMessage, emoji: string) => void;
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
 * projection), and — when the read-model wake state is supplied — a
 * per-participant wake strip. Following is implicit (replying follows the
 * thread); there is no explicit follow toggle.
 */
export function ThreadPanel({
  root,
  replies,
  currentUserId,
  currentUserName,
  showInChannel = false,
  onShowInChannelChange,
  wakeAnnotations,
  isMobile,
  onBack,
  onViewParent,
  loading,
  loadError,
  onRetry,
  onReact,
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
      />

      {wakeAnnotations ? <ThreadWakeStrip annotations={wakeAnnotations} /> : null}

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
            leadingActions={composerActions}
          />
        </>
      )}
    </div>
  );
}
