"use client";

import {
  createContext,
  use,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useQuery } from "@tanstack/react-query";
import { Virtuoso } from "react-virtuoso";
import { cn } from "@multica/ui/lib/utils";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Button } from "@multica/ui/components/ui/button";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@multica/ui/components/ui/collapsible";
import {
  Tooltip,
  TooltipTrigger,
  TooltipContent,
} from "@multica/ui/components/ui/tooltip";
import { ChevronRight, ChevronDown, ChevronUp, Brain, AlertCircle, AlertTriangle, Copy } from "lucide-react";
import { ChatMessageHoverShell } from "./chat-message-hover-actions";
import { NoteChatInsertActions } from "./note-chat-insert-actions";
import { buildChatNoteWriteConfirmationByMessageId } from "@multica/core/notes/worker-reply-actions";
import { useScrollFade } from "@multica/ui/hooks/use-scroll-fade";
import { chatTranscriptOptions, isStandaloneSessionOutstanding, isTaskMessageTaskId } from "@multica/core/chat/queries";
import { Markdown } from "@multica/views/common/markdown";
import { copyText } from "@multica/ui/lib/clipboard";
import { AttachmentList } from "../../issues/components/comment-card";
import type { AgentPresence } from "@multica/core/agents";
import type { ChatMessage, ChatPendingTask, TaskFailureReason } from "@multica/core/types";
import type { ChatTimelineItem } from "@multica/core/chat";
import { parseStickerMessage } from "@multica/core/chat";
import { failureReasonLabel } from "../../agents/components/tabs/task-failure";
import { buildTimeline } from "../../common/task-transcript";
import { CollapsibleMessageBody } from "../../common/collapsible-message-body";
import { TaskStatusPill } from "./task-status-pill";
import { StickerMessage } from "./sticker-message";
import { MessagePartsRenderer } from "../../channels/components/message-parts-renderer";
import {
  resolveMessageParts,
  unwrapStructuredPreviewContent,
} from "../../channels/components/message-parts-preview";
import { formatElapsedMs } from "../lib/format";
import { splitTimeline, extractCopyText } from "../lib/copy-text";
import { isTransportHangError } from "../lib/timeline-error";
import {
  activeBubbleStepSummary,
  bubbleToolSummary,
  deriveBubbleCursorPanels,
  friendlyBubbleToolLabel,
} from "../lib/bubble-cursor-activity";
import {
  BubblePlanPanel,
  BubbleSubagentPanel,
  BubbleTodoPanel,
} from "./bubble-cursor-panels";
import { shouldPinChatToLatest } from "../lib/pin-chat-to-latest";
import { useT } from "../../i18n";
import type { MessagePart } from "@multica/core/types";

// ─── Virtuoso chrome (stable component types — avoid Footer remount) ─────
//
// Inline `Footer: () => …` recreates a component *type* every parent render.
// Token badge / live timeline updates then remount the live TimelineView and
// wipe Collapsible open state — mobile feels like "details vanished / can't
// expand". Keep Header/Footer identities stable; pass data via context.

interface ChatListChrome {
  isFetchingOlderMessages: boolean;
  showStatusPill: boolean;
  pendingTask: ChatPendingTask | null | undefined;
  availability: AgentPresence | undefined;
  loadingOlderLabel: string;
  trailingSlot: ReactNode;
}

const ChatListChromeContext = createContext<ChatListChrome | null>(null);
const ChatMessageBodyLayoutContext = createContext<(() => void) | null>(null);

function ChatMessageListHeader() {
  const chrome = use(ChatListChromeContext);
  if (!chrome) return null;
  return (
    <div className="mx-auto w-full max-w-4xl px-5 pt-4">
      {chrome.isFetchingOlderMessages && (
        <div className="text-center text-xs text-muted-foreground">{chrome.loadingOlderLabel}</div>
      )}
    </div>
  );
}

function ChatMessageListFooter() {
  const chrome = use(ChatListChromeContext);
  if (!chrome) return null;
  if (!chrome.trailingSlot && !(chrome.showStatusPill && chrome.pendingTask)) return null;
  return (
    <div className="mx-auto w-full max-w-4xl px-5 pb-4 space-y-4">
      {chrome.trailingSlot}
      {chrome.showStatusPill && chrome.pendingTask && (
        <TaskStatusPill
          pendingTask={chrome.pendingTask}
          availability={chrome.availability}
        />
      )}
    </div>
  );
}

const chatMessageListVirtuosoComponents = {
  Header: ChatMessageListHeader,
  Footer: ChatMessageListFooter,
};

// ─── Public component ────────────────────────────────────────────────────

interface ChatMessageListProps {
  /** Chat session id — scopes the execution-transcript fetch (#414). */
  sessionId: string;
  messages: ChatMessage[];
  /**
   * Server-authoritative pending-task snapshot. `null` / undefined means
   * no in-flight task — list renders without StatusPill.
   */
  pendingTask: ChatPendingTask | null | undefined;
  /** Resolved presence; pass `undefined` while loading to keep the pill copy neutral. */
  availability: AgentPresence | undefined;
  firstItemIndex?: number;
  hasOlderMessages?: boolean;
  isFetchingOlderMessages?: boolean;
  onLoadOlderMessages?: () => void;
  /**
   * DM agent bubble only: Cursor-like Plan / Todo / Subagent cards + richer
   * tool steps. Global FAB / non-bubble chat keeps the compact fold.
   */
  isDmBubble?: boolean;
  /**
   * Notes bubble: copy lives on the Messages-style hover overlay, not a
   * fixed footer slot under every reply.
   */
  hoverMessageActions?: boolean;
  /** Notes page id for hover insert-below / insert-child. */
  noteInsertPageId?: string | null;
  /** Local turn rendered after persisted messages (e.g. 写汇报 confirm). */
  trailingSlot?: ReactNode;
}

export function ChatMessageList({
  sessionId,
  messages,
  pendingTask,
  availability,
  firstItemIndex = 0,
  hasOlderMessages = false,
  isFetchingOlderMessages = false,
  onLoadOlderMessages,
  isDmBubble = false,
  hoverMessageActions = false,
  noteInsertPageId,
  trailingSlot,
}: ChatMessageListProps) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const lastTailIdRef = useRef<string | undefined>(undefined);
  const pinToLatestRef = useRef(true);
  const [scrollContainerEl, setScrollContainerEl] = useState<HTMLDivElement | null>(null);
  const [isNearTop, setIsNearTop] = useState(true);
  const [isNearBottom, setIsNearBottom] = useState(true);
  const setScrollContainerRef = useCallback((node: HTMLDivElement | null) => {
    scrollRef.current = node;
    setScrollContainerEl(node);
  }, []);
  const fadeStyle = useScrollFade(scrollRef);
  const { t } = useT("chat");

  const noteInsertOffers = useMemo(
    () =>
      noteInsertPageId?.trim()
        ? buildChatNoteWriteConfirmationByMessageId(messages)
        : new Map<string, { mode: string }>(),
    [messages, noteInsertPageId],
  );

  const turnOutstanding = isStandaloneSessionOutstanding(pendingTask);
  const lastMessage = messages[messages.length - 1];
  const pendingAlreadyPersisted = turnOutstanding && lastMessage?.role === "assistant";
  // Standalone Raft deliver has no live task:message stream — StatusPill alone
  // covers the outstanding wait until chat:done lands the assistant message.
  const showStatusPill = turnOutstanding && !pendingAlreadyPersisted && !!pendingTask;

  const totalCount = messages.length + (showStatusPill ? 1 : 0);
  const firstIndex = totalCount > 0 ? firstItemIndex : 0;
  const showScrollControls = totalCount > 0 && (!isNearTop || !isNearBottom);

  const updateScrollPosition = useCallback(() => {
    const scrollEl = scrollRef.current;
    if (!scrollEl) return;

    const distanceFromBottom = scrollEl.scrollHeight - scrollEl.scrollTop - scrollEl.clientHeight;
    const nearBottom = distanceFromBottom <= 120;
    setIsNearTop(scrollEl.scrollTop <= 120);
    setIsNearBottom(nearBottom);
    if (!nearBottom) pinToLatestRef.current = false;
  }, []);

  const scrollToBoundary = useCallback((top: number) => {
    scrollRef.current?.scrollTo({ top, behavior: "smooth" });
  }, []);

  const scrollToLatest = useCallback((behavior: ScrollBehavior) => {
    const el = scrollRef.current;
    if (!el) return;
    pinToLatestRef.current = true;
    setIsNearBottom(true);
    const jump = () => {
      el.scrollTo({ top: el.scrollHeight, behavior });
    };
    jump();
    // Virtuoso may grow the scroller after the first paint of a new row
    // (status pill / optimistic bubble). A second frame lands on the real end.
    window.requestAnimationFrame(jump);
  }, []);

  useEffect(() => {
    if (!scrollContainerEl) return;

    updateScrollPosition();
    scrollContainerEl.addEventListener("scroll", updateScrollPosition, { passive: true });
    return () => scrollContainerEl.removeEventListener("scroll", updateScrollPosition);
  }, [scrollContainerEl, updateScrollPosition]);

  useEffect(() => {
    const tail = messages[messages.length - 1];
    const previousTailId = lastTailIdRef.current;
    if (shouldPinChatToLatest(previousTailId, tail)) {
      scrollToLatest(previousTailId === undefined ? "auto" : "smooth");
    }
    lastTailIdRef.current = tail?.id;
  }, [messages, scrollToLatest]);

  useEffect(() => {
    updateScrollPosition();
    if (pinToLatestRef.current && showStatusPill) {
      scrollToLatest("smooth");
    }
  }, [totalCount, showStatusPill, updateScrollPosition, scrollToLatest]);

  const hasTrailingSlot = Boolean(trailingSlot);
  useEffect(() => {
    if (hasTrailingSlot) scrollToLatest("smooth");
  }, [hasTrailingSlot, scrollToLatest]);

  const handleMessageBodyLayoutChange = useCallback(() => {
    const shouldKeepBottom = isNearBottom;
    window.requestAnimationFrame(() => {
      window.dispatchEvent(new Event("resize"));
      updateScrollPosition();
      if (shouldKeepBottom) {
        const scrollEl = scrollRef.current;
        if (scrollEl) scrollEl.scrollTop = scrollEl.scrollHeight;
      }
    });
  }, [isNearBottom, updateScrollPosition]);

  const chromeValue = useMemo<ChatListChrome>(
    () => ({
      isFetchingOlderMessages,
      showStatusPill,
      pendingTask,
      availability,
      loadingOlderLabel: t(($) => $.message_list.loading_older),
      trailingSlot: trailingSlot ?? null,
    }),
    [
      isFetchingOlderMessages,
      showStatusPill,
      pendingTask,
      availability,
      t,
      trailingSlot,
    ],
  );

  return (
    <ChatListChromeContext.Provider value={chromeValue}>
    <ChatMessageBodyLayoutContext.Provider value={handleMessageBodyLayoutChange}>
    <div className="relative flex-1 min-h-0">
      <div
        ref={setScrollContainerRef}
        data-tab-scroll-root
        style={fadeStyle}
        className="h-full overflow-y-auto"
      >
        {!scrollContainerEl ? (
          <div className="mx-auto w-full max-w-4xl px-5 pt-4 space-y-3">
            <ChatMessageSkeleton />
          </div>
        ) : (
          <Virtuoso
            customScrollParent={scrollContainerEl}
            data={messages}
            firstItemIndex={firstIndex}
            increaseViewportBy={{ top: 400, bottom: 600 }}
            atBottomThreshold={120}
            atBottomStateChange={(atBottom) => {
              setIsNearBottom(atBottom);
              if (!atBottom) pinToLatestRef.current = false;
            }}
            followOutput={() => {
              if (isFetchingOlderMessages) return false;
              if (pinToLatestRef.current || isNearBottom) return "smooth";
              return false;
            }}
            startReached={() => {
              if (hasOlderMessages && !isFetchingOlderMessages) {
                onLoadOlderMessages?.();
              }
            }}
            computeItemKey={(_, msg) => msg.id}
            components={chatMessageListVirtuosoComponents}
            itemContent={(_, msg) => (
              <div className="mx-auto w-full max-w-4xl px-5 py-2">
                <MessageBubble
                  sessionId={sessionId}
                  message={msg}
                  isPending={false}
                  enhanced={isDmBubble}
                  hoverMessageActions={hoverMessageActions}
                  noteInsertPageId={noteInsertPageId}
                  offerNoteInsert={noteInsertOffers.has(msg.id)}
                />
              </div>
            )}
          />
        )}
      </div>
      {showScrollControls && (
        // Anchor bottom-right (not vertical center) so the FABs don't sit on
        // top of the process-fold / tool rows on phone web.
        <div className="pointer-events-none absolute bottom-20 right-3 z-10 flex flex-col gap-2 sm:bottom-24 sm:right-4">
          <Button
            type="button"
            variant="secondary"
            size="icon"
            className={cn(
              "pointer-events-auto size-9 rounded-full border bg-background/90 shadow-lg backdrop-blur transition-opacity hover:bg-accent sm:size-8",
              isNearTop && "opacity-40",
            )}
            aria-label={t(($) => $.message_list.scroll_to_top)}
            title={t(($) => $.message_list.scroll_to_top)}
            disabled={isNearTop}
            onClick={() => scrollToBoundary(0)}
          >
            <ChevronUp className="size-4" />
          </Button>
          <Button
            type="button"
            variant="secondary"
            size="icon"
            className={cn(
              "pointer-events-auto size-9 rounded-full border bg-background/90 shadow-lg backdrop-blur transition-opacity hover:bg-accent sm:size-8",
              isNearBottom && "opacity-40",
            )}
            aria-label={t(($) => $.message_list.scroll_to_bottom)}
            title={t(($) => $.message_list.scroll_to_bottom)}
            disabled={isNearBottom}
            onClick={() => scrollToBoundary(scrollRef.current?.scrollHeight ?? 0)}
          >
            <ChevronDown className="size-4" />
          </Button>
        </div>
      )}
    </div>
    </ChatMessageBodyLayoutContext.Provider>
    </ChatListChromeContext.Provider>
  );
}

/**
 * Placeholder shown while `chat_message` for a session is being fetched
 * (initial refresh, or switching to an un-cached session). Shape roughly
 * mirrors an assistant → user → assistant exchange so the window doesn't
 * shift under the user when real messages arrive.
 */
export function ChatMessageSkeleton() {
  return (
    <div className="flex-1 overflow-hidden">
      <div className="mx-auto w-full max-w-4xl px-5 py-4 space-y-5">
        <div className="space-y-2">
          <Skeleton className="h-3.5 w-3/4" />
          <Skeleton className="h-3.5 w-1/2" />
        </div>
        <div className="flex justify-end">
          <Skeleton className="h-8 w-48 rounded-2xl" />
        </div>
        <div className="space-y-2">
          <Skeleton className="h-3.5 w-2/3" />
          <Skeleton className="h-3.5 w-5/6" />
          <Skeleton className="h-3.5 w-1/3" />
        </div>
      </div>
    </div>
  );
}

// ─── Message bubbles ─────────────────────────────────────────────────────

const selectableMessageTextClass = "select-text [-webkit-user-select:text] [-webkit-touch-callout:default]";

function MessageBubble({
  sessionId,
  message,
  isPending,
  enhanced,
  hoverMessageActions,
  noteInsertPageId,
  offerNoteInsert,
}: {
  sessionId: string;
  message: ChatMessage;
  isPending: boolean;
  enhanced?: boolean;
  hoverMessageActions?: boolean;
  noteInsertPageId?: string | null;
  offerNoteInsert?: boolean;
}) {
  if (message.role === "user") {
    return (
      <div className="flex justify-end">
        <ChatMessageHoverShell
          enabled={!!hoverMessageActions}
          copyTextValue={extractCopyText(message, [])}
          noteInsertPageId={noteInsertPageId}
        >
          <div className="max-w-[80%] space-y-1">
            <div className={cn("rounded-2xl bg-muted px-3.5 py-2 text-sm break-words", selectableMessageTextClass)}>
              {/* User messages are authored as markdown in ContentEditor, so
               * render them through the same pipeline as assistant replies.
               * Neutralise prose's leading/trailing margin so single-line
               * bubbles stay as compact as the plain-text version used to. */}
              <ChatCollapsibleBody
                contentKey={`user:${message.id}:${message.content.length}`}
                fadeVariant="muted"
              >
                <div className="prose prose-sm dark:prose-invert max-w-none [&>*:first-child]:mt-0 [&>*:last-child]:mb-0">
                  <Markdown attachments={message.attachments} mentionVariant="plain">{message.content}</Markdown>
                </div>
              </ChatCollapsibleBody>
              <AttachmentList
                attachments={message.attachments}
                content={message.content}
                className="mt-1.5"
              />
            </div>
            {!hoverMessageActions && (
              <div className="flex justify-end">
                <MessageCopyButton message={message} timeline={[]} />
              </div>
            )}
          </div>
        </ChatMessageHoverShell>
      </div>
    );
  }

  return (
    <AssistantMessage
      sessionId={sessionId}
      message={message}
      isPending={isPending}
      enhanced={enhanced}
      hoverMessageActions={hoverMessageActions}
      noteInsertPageId={noteInsertPageId}
      offerNoteInsert={offerNoteInsert}
    />
  );
}

function AssistantMessage({
  sessionId,
  message,
  isPending,
  enhanced,
  hoverMessageActions,
  noteInsertPageId,
  offerNoteInsert,
}: {
  sessionId: string;
  message: ChatMessage;
  isPending: boolean;
  enhanced?: boolean;
  hoverMessageActions?: boolean;
  noteInsertPageId?: string | null;
  offerNoteInsert?: boolean;
}) {
  const taskId = message.task_id;
  const canFetchTaskMessages = !!sessionId && isTaskMessageTaskId(taskId);

  // Session-scoped execution transcript (#414) — same cache key as the live
  // timeline, so this reads the entry WS already seeded during execution (zero
  // refetch when the task finishes) while fetching off the new endpoint.
  const { data: taskMessages } = useQuery({
    ...chatTranscriptOptions(sessionId, taskId ?? ""),
    enabled: canFetchTaskMessages,
  });

  const timeline: ChatTimelineItem[] = buildTimeline(taskMessages ?? []);

  // Failure bubble path: when the server's FailTask wrote a failure
  // chat_message (failure_reason set), render a destructive bubble with the
  // human-readable reason label + collapsible raw errMsg + the same timeline
  // so the user can see exactly where the run broke.
  if (message.failure_reason) {
    return (
      <ChatMessageHoverShell
        enabled={!!hoverMessageActions}
        copyTextValue={extractCopyText(message, timeline)}
        noteInsertPageId={noteInsertPageId}
      >
        <FailureBubble
          reason={message.failure_reason}
          rawError={message.content}
          timeline={timeline}
          elapsedMs={message.elapsed_ms}
          enhanced={enhanced}
        />
      </ChatMessageHoverShell>
    );
  }

  return (
    <ChatMessageHoverShell
      enabled={!!hoverMessageActions && !isPending}
      copyTextValue={extractCopyText(message, timeline)}
      noteInsertPageId={noteInsertPageId}
    >
      <div className="w-full space-y-1.5">
        {timeline.length > 0 ? (
          <TimelineView
            items={timeline}
            attachments={message.attachments}
            enhanced={enhanced}
            messageParts={message.parts}
            messageContent={message.content}
            foldKey={taskId ? `task:${taskId}` : `msg:${message.id}`}
          />
        ) : (
          <MessageProse
            content={message.content}
            parts={message.parts}
            attachments={message.attachments}
          />
        )}
        <AttachmentList
          attachments={message.attachments}
          content={message.content}
        />
        <MessageFooter
          message={message}
          timeline={timeline}
          isPending={isPending}
          hideCopy={hoverMessageActions}
        />
        {offerNoteInsert && noteInsertPageId && !isPending ? (
          <NoteChatInsertActions
            pageId={noteInsertPageId}
            text={extractCopyText(message, timeline)}
          />
        ) : null}
      </div>
    </ChatMessageHoverShell>
  );
}

// Inline footer row beneath the assistant reply: "Replied in 38s · [Copy]".
// Action icons live here (not as a hover-floating overlay) so they're
// discoverable on first read and don't shift content. Buttons stay quiet
// (muted) until hover. Copy is suppressed during streaming because the
// final text is still being appended.
function MessageFooter({
  message,
  timeline,
  isPending,
  hideCopy,
}: {
  message: ChatMessage;
  timeline: ChatTimelineItem[];
  isPending: boolean;
  hideCopy?: boolean;
}) {
  const showCopy = !isPending && !hideCopy;
  if (message.elapsed_ms == null && !showCopy) return null;
  return (
    <div className="flex items-center gap-1.5">
      {message.elapsed_ms != null && (
        <ElapsedCaption variant="replied" elapsedMs={message.elapsed_ms} />
      )}
      {showCopy && <MessageCopyButton message={message} timeline={timeline} />}
    </div>
  );
}

function MessageCopyButton({
  message,
  timeline,
}: {
  message: ChatMessage;
  timeline: ChatTimelineItem[];
}) {
  const { t } = useT("chat");
  const handleCopy = async () => {
    if (await copyText(extractCopyText(message, timeline))) {
      toast.success(t(($) => $.message_list.copied_toast));
    } else {
      showErrorToast(t(($) => $.message_list.copy_failed_toast));
    }
  };
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            variant="ghost"
            size="icon-xs"
            className="text-muted-foreground/70 hover:text-foreground"
            onClick={handleCopy}
            aria-label={t(($) => $.message_list.copy_action)}
          />
        }
      >
        <Copy />
      </TooltipTrigger>
      <TooltipContent side="top">
        {t(($) => $.message_list.copy_action)}
      </TooltipContent>
    </Tooltip>
  );
}

// Persisted "Replied in 38s" / "Failed after 12s" line under the assistant
// bubble. Reads `elapsed_ms` straight off the chat_message — server computes
// it once at task completion, so this caption is identical across reloads
// and devices. Skipped silently when null (legacy messages predating
// migration 063 + user messages).
function ElapsedCaption({
  variant,
  elapsedMs,
  className,
}: {
  variant: "replied" | "failed";
  elapsedMs: number;
  className?: string;
}) {
  const { t } = useT("chat");
  const text =
    variant === "replied"
      ? t(($) => $.message_list.replied_in, { elapsed: formatElapsedMs(elapsedMs) })
      : t(($) => $.message_list.failed_after, { elapsed: formatElapsedMs(elapsedMs) });
  return (
    <div className={cn("text-xs text-muted-foreground/80", className)}>
      {text}
    </div>
  );
}

function FailureBubble({
  reason,
  rawError,
  timeline,
  elapsedMs,
  enhanced,
}: {
  reason: string;
  rawError: string;
  timeline: ChatTimelineItem[];
  elapsedMs?: number | null;
  enhanced?: boolean;
}) {
  const { t } = useT("chat");
  const [open, setOpen] = useState(false);
  // Map the back-end enum to copy via the shared label table; an unknown
  // reason (e.g. a future enum value the front-end doesn't ship yet)
  // falls back to a generic translated label.
  const label =
    failureReasonLabel[reason as TaskFailureReason] ??
    t(($) => $.message_list.task_failed_fallback);

  return (
    <div className="w-full space-y-1.5">
      {/* Failure read as an inline, low-key note — not a destructive
       *  alert. Intentionally borderless / no background tint: a chat
       *  failure is informational ("this didn't work"), not a system
       *  error. The icon + muted destructive text are signal enough,
       *  the rest stays in the normal reply rhythm. */}
      <div className="flex items-start gap-1.5 text-sm">
        <AlertTriangle className="size-3.5 shrink-0 text-destructive/80 mt-0.5" />
        <div className="flex-1 min-w-0">
          <div className="text-destructive/90">{label}</div>
          {rawError.trim() && (
            <Collapsible open={open} onOpenChange={setOpen}>
              <CollapsibleTrigger className="mt-0.5 flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors">
                {open ? (
                  <ChevronDown className="size-3" />
                ) : (
                  <ChevronRight className="size-3" />
                )}
                <span>{t(($) => $.message_list.show_details)}</span>
              </CollapsibleTrigger>
              <CollapsibleContent>
                <pre className={cn("mt-1 max-h-40 overflow-auto rounded bg-muted/40 p-2 text-xs text-muted-foreground whitespace-pre-wrap break-all", selectableMessageTextClass)}>
                  {rawError}
                </pre>
              </CollapsibleContent>
            </Collapsible>
          )}
        </div>
      </div>
      {timeline.length > 0 && (
        <TimelineView
          items={timeline}
          enhanced={enhanced}
          foldKey={`fail:${reason}:${timeline[0]?.seq ?? 0}`}
        />
      )}
      {elapsedMs != null && (
        <ElapsedCaption variant="failed" elapsedMs={elapsedMs} />
      )}
    </div>
  );
}

/** FAB / chat-session long bodies — same Slack clamp + 查看更多 as channel bubbles. */
function ChatCollapsibleBody({
  contentKey,
  children,
  enabled = true,
  fadeVariant = "default",
}: {
  contentKey: string;
  children: ReactNode;
  enabled?: boolean;
  fadeVariant?: import("../../common/mention-token").MessageCollapseFadeVariant;
}) {
  const { t } = useT("chat");
  const onBodyLayoutChange = use(ChatMessageBodyLayoutContext);
  return (
    <CollapsibleMessageBody
      contentKey={contentKey}
      enabled={enabled}
      expandLabel={t(($) => $.message_list.expand_action)}
      collapseLabel={t(($) => $.message_list.collapse_action)}
      fadeVariant={fadeVariant}
      onExpandedChange={onBodyLayoutChange ? () => onBodyLayoutChange() : undefined}
    >
      {children}
    </CollapsibleMessageBody>
  );
}

/**
 * Renders assistant/user message body. Prefers denormalized `parts`, otherwise
 * unwraps a historical `action:message_send` envelope from `content` (same
 * contract as channel bubbles) so protocol JSON never paints as markdown.
 */
function MessageProse({
  content,
  parts,
  attachments,
}: {
  content: string;
  parts?: MessagePart[] | null;
  attachments?: import("@multica/core/types").Attachment[];
}) {
  if (isTransportHangError(content) && !(parts && parts.length > 0)) {
    return <ErrorRow item={{ seq: 0, type: "error", content }} />;
  }
  const contentKey = `prose:${content.length}:${parts?.length ?? 0}:${content.slice(0, 48)}`;
  const resolved = resolveMessageParts(content, parts);
  if (resolved?.length) {
    const stickerOnly = resolved.every((p) => p.type === "sticker");
    if (stickerOnly) {
      return (
        <div className="flex flex-wrap gap-1.5 py-0.5">
          {resolved.map((part, i) =>
            part.type === "sticker" ? (
              <StickerMessage key={`${part.sticker_id}-${i}`} id={part.sticker_id} />
            ) : null,
          )}
        </div>
      );
    }
    return (
      <ChatCollapsibleBody contentKey={contentKey}>
        <div className={cn("text-sm leading-relaxed max-w-none", selectableMessageTextClass)}>
          <MessagePartsRenderer parts={resolved} />
        </div>
      </ChatCollapsibleBody>
    );
  }

  // Legacy sticker-only `{"parts":[...]}` without action (LRM-84).
  const stickerIds = parseStickerMessage(content);
  if (stickerIds) {
    return (
      <div className="flex flex-wrap gap-1.5 py-0.5">
        {stickerIds.map((id, i) => (
          <StickerMessage key={`${id}-${i}`} id={id} />
        ))}
      </div>
    );
  }

  const unwrapped = unwrapStructuredPreviewContent(content);
  return (
    <ChatCollapsibleBody contentKey={contentKey}>
      <div className={cn("text-sm leading-relaxed prose prose-sm dark:prose-invert max-w-none", selectableMessageTextClass)}>
        <Markdown attachments={attachments}>{unwrapped ?? content}</Markdown>
      </div>
    </ChatCollapsibleBody>
  );
}

// ─── Timeline: outer process fold + final text (Conductor-style) ─────────
//
// splitTimeline (lib/copy-text.ts) carves the items into:
//   preface — text before the first thinking/tool item
//   middle  — process rows (thinking/tool/error); sandwiched text peeled out
//   final   — peeled narration + text after the last non-text item
//
// We render preface + final outside an outer Collapsible ("X steps") that
// wraps middle. The inner row Collapsibles (ThinkingRow / ToolCallRow /
// ToolResultRow) are unchanged — clicking them toggles independently of
// the outer fold. Copy mirrors what's visible when the outer fold is
// closed: preface + final, never middle. See extractCopyText for the
// authoritative copy logic.

/** Survives Virtuoso Footer / live→persisted remounts within a task (LRM-690). */
const processFoldOpenByKey = new Map<string, boolean>();

function TimelineView({
  items,
  isStreaming,
  attachments,
  enhanced,
  messageParts,
  messageContent,
  foldKey,
}: {
  items: ChatTimelineItem[];
  isStreaming?: boolean;
  attachments?: import("@multica/core/types").Attachment[];
  /** DM bubble Cursor-like panels + richer tool labels. */
  enhanced?: boolean;
  /** Assistant message parts — used when the final region is an envelope. */
  messageParts?: MessagePart[] | null;
  messageContent?: string;
  /** Persist fold open across remounts for the same live/persisted task. */
  foldKey?: string;
}) {
  const { preface, middle, final } = splitTimeline(items);
  const panels = enhanced ? deriveBubbleCursorPanels(middle) : null;
  const finalPieces: string[] = [];
  for (const t of final) {
    const c = t.content ?? "";
    if (c.length > 0) finalPieces.push(c);
  }
  const finalText = finalPieces.join("\n\n");
  // Prefer timeline final text; if empty (e.g. sticker-only completion), fall
  // back to the persisted chat_message body so envelopes still unwrap.
  const proseContent = finalText || messageContent || "";

  return (
    <>
      {preface.length > 0 && (
        <ChatCollapsibleBody
          contentKey={`preface:${preface.map((t) => t.content ?? "").join("\n\n").length}`}
        >
          <div className={cn("text-sm leading-relaxed prose prose-sm dark:prose-invert max-w-none", selectableMessageTextClass)}>
            <Markdown attachments={attachments}>
              {preface.map((t) => t.content ?? "").join("\n\n")}
            </Markdown>
          </div>
        </ChatCollapsibleBody>
      )}
      {panels && (panels.plan || panels.todos.length > 0 || panels.subagents.length > 0) && (
        <div className="space-y-1.5">
          {panels.plan ? <BubblePlanPanel plan={panels.plan} /> : null}
          {panels.todos.length > 0 ? <BubbleTodoPanel todos={panels.todos} /> : null}
          {panels.subagents.length > 0 ? (
            <BubbleSubagentPanel items={panels.subagents} />
          ) : null}
        </div>
      )}
      {middle.length > 0 && (
        <OuterProcessFold
          items={middle}
          foldKey={foldKey}
          // Always start collapsed (product: tap to expand). Streaming still
          // surfaces the active step on the collapsed header via activeSummary.
          // foldKey restores a prior open choice across remounts.
          defaultOpen={false}
          attachments={attachments}
          enhanced={enhanced}
          isStreaming={isStreaming}
        />
      )}
      {proseContent.length > 0 && (
        <MessageProse
          content={proseContent}
          parts={finalText ? undefined : messageParts}
          attachments={attachments}
        />
      )}
    </>
  );
}

function OuterProcessFold({
  items,
  foldKey,
  defaultOpen,
  attachments,
  enhanced,
  isStreaming,
}: {
  items: ChatTimelineItem[];
  foldKey?: string;
  defaultOpen?: boolean;
  attachments?: import("@multica/core/types").Attachment[];
  enhanced?: boolean;
  isStreaming?: boolean;
}) {
  const { t } = useT("chat");
  // Seed from the task-scoped map when present so live Footer remounts and
  // the live→persisted handoff keep the user's expand choice (LRM-690).
  const [open, setOpen] = useState(() => {
    if (foldKey && processFoldOpenByKey.has(foldKey)) {
      return processFoldOpenByKey.get(foldKey)!;
    }
    return defaultOpen ?? false;
  });
  const handleOpenChange = (next: boolean) => {
    if (foldKey) processFoldOpenByKey.set(foldKey, next);
    setOpen(next);
  };
  const stepCount = items.length;
  const activeSummary = enhanced ? activeBubbleStepSummary(items) : null;

  return (
    <Collapsible open={open} onOpenChange={handleOpenChange}>
      <CollapsibleTrigger
        className={cn(
          // ≥32px touch target; collapsed state reads as a real control, not a
          // pale caption that looks like the steps vanished after completion.
          "flex min-h-8 max-w-full items-center gap-1.5 rounded-md px-2 py-1.5 text-left text-xs transition-colors",
          open
            ? "text-muted-foreground hover:text-foreground"
            : "border border-border/70 bg-muted/40 text-foreground/90 hover:bg-muted/60 hover:text-foreground",
        )}
        aria-label={
          open
            ? t(($) => $.message_list.process_steps_hide, { count: stepCount })
            : t(($) => $.message_list.process_steps_show, { count: stepCount })
        }
        onPointerDown={(e) => e.stopPropagation()}
      >
        {open ? <ChevronDown className="size-3.5 shrink-0" /> : <ChevronRight className="size-3.5 shrink-0" />}
        <span className="shrink-0 font-medium">
          {open
            ? t(($) => $.message_list.process_steps, { count: stepCount })
            : t(($) => $.message_list.process_steps_show, { count: stepCount })}
        </span>
        {enhanced && !open && activeSummary ? (
          <span
            className={cn(
              "min-w-0 truncate text-muted-foreground",
              isStreaming && "animate-chat-text-shimmer text-foreground/80",
            )}
          >
            · {activeSummary}
          </span>
        ) : null}
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div
          className={cn(
            "mt-1 rounded-lg border bg-muted/20 p-2 space-y-0.5",
            enhanced && "border-border/70",
          )}
        >
          {items.map((item, index) =>
            item.type === "text" ? (
              <MiddleTextRow key={item.seq} item={item} attachments={attachments} />
            ) : (
              <ItemRow
                key={item.seq}
                item={item}
                enhanced={enhanced}
                active={!!isStreaming && index === items.length - 1}
              />
            ),
          )}
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}

// Intermediate text segment rendered inside the outer fold. Visually
// down-shifted (xs / muted) so it reads as part of the agent's process,
// not the final answer — the final answer renders below the fold at full
// prose size.
function MiddleTextRow({
  item,
  attachments,
}: {
  item: ChatTimelineItem;
  attachments?: import("@multica/core/types").Attachment[];
}) {
  return (
    <div className={cn("py-0.5 text-xs text-muted-foreground prose prose-sm dark:prose-invert max-w-none [&>*:first-child]:mt-0 [&>*:last-child]:mb-0", selectableMessageTextClass)}>
      <Markdown attachments={attachments}>{item.content ?? ""}</Markdown>
    </div>
  );
}

// ─── Individual item rows ────────────────────────────────────────────────

function ItemRow({
  item,
  enhanced,
  active,
}: {
  item: ChatTimelineItem;
  enhanced?: boolean;
  active?: boolean;
}) {
  switch (item.type) {
    case "tool_use":
      return <ToolCallRow item={item} enhanced={enhanced} active={active} />;
    case "tool_result":
      return <ToolResultRow item={item} />;
    case "thinking":
      return <ThinkingRow item={item} />;
    case "error":
      return <ErrorRow item={item} />;
    default:
      return null;
  }
}

function shortenPath(p: string): string {
  const parts = p.split("/");
  if (parts.length <= 3) return p;
  return ".../" + parts.slice(-2).join("/");
}

function getToolSummary(item: ChatTimelineItem): string {
  if (!item.input) return "";
  const inp = item.input as Record<string, string>;
  if (inp.query) return inp.query;
  if (inp.file_path) return shortenPath(inp.file_path);
  if (inp.path) return shortenPath(inp.path);
  if (inp.target_file) return shortenPath(inp.target_file);
  if (inp.pattern) return inp.pattern;
  if (inp.description) return String(inp.description);
  if (inp.command) {
    const cmd = String(inp.command);
    return cmd.length > 100 ? cmd.slice(0, 100) + "..." : cmd;
  }
  if (inp.cmd) {
    const cmd = String(inp.cmd);
    return cmd.length > 100 ? cmd.slice(0, 100) + "..." : cmd;
  }
  if (inp.prompt) {
    const p = String(inp.prompt);
    return p.length > 100 ? p.slice(0, 100) + "..." : p;
  }
  if (inp.skill) return String(inp.skill);
  for (const v of Object.values(inp)) {
    if (typeof v === "string" && v.length > 0 && v.length < 120) return v;
  }
  return "";
}

function ToolCallRow({
  item,
  enhanced,
  active,
}: {
  item: ChatTimelineItem;
  enhanced?: boolean;
  active?: boolean;
}) {
  const { t } = useT("chat");
  const [open, setOpen] = useState(false);
  const summary = enhanced ? bubbleToolSummary(item) : getToolSummary(item);
  const hasInput = !!(item.input && Object.keys(item.input).length > 0);
  const label = enhanced ? friendlyBubbleToolLabel(item.tool) : item.tool;

  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <CollapsibleTrigger
        // Never disable: empty-args tool_use still needs a ≥32px tap target on
        // phone web (disabled rows look like dead labels — "啥也点不了").
        className={cn(
          "flex min-h-8 w-full items-center gap-1.5 rounded-md px-1.5 -mx-1 py-1.5 text-xs transition-colors hover:bg-accent/30",
          enhanced && active && "bg-accent/40",
        )}
        onPointerDown={(e) => e.stopPropagation()}
        onClick={(e) => e.stopPropagation()}
      >
        <ChevronRight
          className={cn(
            "size-3.5 shrink-0 text-muted-foreground transition-transform",
            open && "rotate-90",
          )}
        />
        <span
          className={cn(
            "font-medium shrink-0",
            enhanced ? "text-foreground/90" : "text-foreground",
            enhanced && active && "animate-chat-text-shimmer",
          )}
        >
          {label}
        </span>
        {summary ? (
          <span className="min-w-0 flex-1 truncate text-left text-muted-foreground">{summary}</span>
        ) : null}
      </CollapsibleTrigger>
      <CollapsibleContent>
        {hasInput ? (
          <pre className={cn("ml-[18px] mt-0.5 max-h-32 overflow-auto rounded bg-muted/50 p-2 text-xs text-muted-foreground whitespace-pre-wrap break-all", selectableMessageTextClass)}>
            {JSON.stringify(item.input, null, 2)}
          </pre>
        ) : (
          <p className="ml-[18px] mt-0.5 px-1.5 py-1 text-xs text-muted-foreground">
            {t(($) => $.message_list.tool_no_args)}
          </p>
        )}
      </CollapsibleContent>
    </Collapsible>
  );
}

function ToolResultRow({ item }: { item: ChatTimelineItem }) {
  const { t } = useT("chat");
  const [open, setOpen] = useState(false);
  const output = item.output ?? "";
  if (!output) return null;

  const preview = output.length > 120 ? output.slice(0, 120) + "..." : output;
  const labelPrefix = item.tool
    ? t(($) => $.message_list.tool_result_named, { tool: item.tool })
    : t(($) => $.message_list.tool_result_unnamed);

  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <CollapsibleTrigger className="flex min-h-8 w-full items-start gap-1.5 rounded-md px-1.5 -mx-1 py-1.5 text-xs hover:bg-accent/30 transition-colors">
        <ChevronRight
          className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform mt-0.5", open && "rotate-90")}
        />
        <span className="min-w-0 flex-1 text-left text-muted-foreground/70 truncate">
          {labelPrefix}{preview}
        </span>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <pre className={cn("ml-[18px] mt-0.5 max-h-40 overflow-auto rounded bg-muted/50 p-2 text-xs text-muted-foreground whitespace-pre-wrap break-all", selectableMessageTextClass)}>
          {output.length > 4000 ? output.slice(0, 4000) + "\n... (truncated)" : output}
        </pre>
      </CollapsibleContent>
    </Collapsible>
  );
}

function ThinkingRow({ item }: { item: ChatTimelineItem }) {
  const [open, setOpen] = useState(false);
  const text = item.content ?? "";
  if (!text) return null;

  const preview = text.length > 150 ? text.slice(0, 150) + "..." : text;

  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <CollapsibleTrigger className="flex min-h-8 w-full items-start gap-1.5 rounded-md px-1.5 -mx-1 py-1.5 text-xs hover:bg-accent/30 transition-colors">
        <Brain className="size-3.5 shrink-0 text-muted-foreground/60 mt-0.5" />
        <span className="min-w-0 flex-1 text-left text-muted-foreground italic truncate">{preview}</span>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <pre className={cn("ml-[18px] mt-0.5 max-h-40 overflow-auto rounded bg-muted/30 p-2 text-xs text-muted-foreground whitespace-pre-wrap break-words", selectableMessageTextClass)}>
          {text}
        </pre>
      </CollapsibleContent>
    </Collapsible>
  );
}

function ErrorRow({ item }: { item: ChatTimelineItem }) {
  const { t } = useT("chat");
  const label = isTransportHangError(item.content)
    ? t(($) => $.message_list.connection_lost)
    : (item.content ?? "");
  return (
    <div className="flex items-start gap-1.5 px-1 -mx-1 py-0.5 text-xs">
      <AlertCircle className="h-3 w-3 shrink-0 text-destructive mt-0.5" />
      <span className={cn("text-destructive", selectableMessageTextClass)}>{label}</span>
    </div>
  );
}

// ─── Shared ──────────────────────────────────────────────────────────────
