"use client";

import {
  lazy,
  memo,
  Suspense,
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type PointerEvent,
} from "react";
import { Copy, MessageSquare, Pencil, Quote, Trash2, SmilePlus } from "lucide-react";
import { toast } from "sonner";
import { ReactionBar } from "@multica/ui/components/common/reaction-bar";
import { QuickEmojiPicker } from "@multica/ui/components/common/quick-emoji-picker";
import { copyText } from "@multica/ui/lib/clipboard";
import { cn } from "@multica/ui/lib/utils";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from "@multica/ui/components/ui/context-menu";
import { useActorName } from "@multica/core/workspace/hooks";
import { useReactionActorName } from "../../common/use-reaction-actor-name";
import type { ChannelMessage } from "@multica/core/types";
import type { OpenAgentPanelFn } from "@multica/core/agents";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { InlineReferenceContent } from "../../common/inline-reference-content";
import { useT } from "../../i18n/use-t";
import { useMessageTime } from "../../i18n/use-message-time";
import {
  mentionResolverFrom,
  projectReferencesToText,
  resolveChannelAuthorDisplayName,
} from "./message-preview";
import {
  formatMessagePartsCopyText,
  resolveMessageParts,
  unwrapStructuredPreviewContent,
} from "./message-parts-preview";
import { MessageBody } from "./message-body";
import { MessageInlineEditor } from "./message-inline-editor";
import { areChannelMessageBubblePropsEqual } from "./channel-message-render-equality";
import { MessageQuoteCard } from "./message-quote";
import { isLegacyRuntimeSystemNotice } from "./runtime-system-notice";
import {
  parseMemberSystemEvent,
  parseIssueSystemEvent,
  parseIssueAggregateSystemEvent,
  parseProjectSystemEvent,
  parseReminderSystemEvent,
  parseThreadUnfollowedSystemEvent,
} from "./channel-system-event";
import {
  MemberSystemEventContent,
  IssueSystemEventContent,
  IssueAggregateSystemEventContent,
  ProjectSystemEventContent,
  ReminderSystemEventContent,
  ThreadUnfollowedSystemEventContent,
} from "./channel-system-event-content";
import { messageMentionsViewer } from "../../common/content-mentions-viewer";
import {
  messageCollapseFadeClassName,
  resolveMessageCollapseFadeVariant,
  SELF_MENTION_ROW_CLASS,
  SELF_MENTION_ROW_MENTION_CLASS,
} from "../../common/mention-token";
import { VoiceMessageAudio } from "./voice-message-audio";
import { resolveVoiceMessagePresentation } from "../lib/voice-message-presentation";

const FullEmojiPicker = lazy(() =>
  import("@multica/ui/components/common/emoji-picker").then((m) => ({
    default: m.EmojiPicker,
  })),
);

const LONG_PRESS_MS = 450;
const TOUCH_MOVE_CANCEL_PX = 8;
/** LRM-495 — Slack-style left swipe opens the same mobile action sheet as long-press. */
const SWIPE_LEFT_OPEN_PX = 48;
const SWIPE_VERTICAL_CANCEL_PX = 24;
const MOBILE_THREAD_TAP_FEEDBACK_MS = 120;
/** LRM-268 / design-long-message-slack-vs-multica.html — Slack collapsed body height. */
export const MESSAGE_COLLAPSE_MAX_HEIGHT_PX = 160;
/** Fade overlay height — light bottom wash only (LRM-302); must not center-cover text. */
export const MESSAGE_COLLAPSE_FADE_HEIGHT_PX = 40;
const MESSAGE_COLLAPSE_HEIGHT_CLASS = "max-h-[160px]";
const MESSAGE_COLLAPSE_OVERFLOW_EPSILON_PX = 2;

function isInteractiveMessageTarget(target: EventTarget | null) {
  if (!(target instanceof Element)) return false;
  return Boolean(
    target.closest(
      'button,a,input,textarea,select,[role="button"],[role="menuitem"],[contenteditable="true"],[data-message-action-surface="true"]',
    ),
  );
}

function hasActiveTextSelection() {
  const selection = window.getSelection?.();
  return Boolean(selection && !selection.isCollapsed && selection.toString().trim());
}

function isMobileActionViewport() {
  if (typeof window === "undefined") return false;
  return (
    window.innerWidth < 768 ||
    window.matchMedia?.("(max-width: 767px)").matches ||
    window.matchMedia?.("(pointer: coarse)").matches
  );
}

function ChannelSystemMessageRow({
  message,
  highlighted,
  systemText,
}: {
  message: ChannelMessage;
  highlighted: boolean;
  systemText: string;
}) {
  const messageTime = useMessageTime();
  // Member-change events (#450) carry a structured part the FE composes into a
  // Raft/Slack-style row with clickable @username tokens; everything else falls
  // back to the plain canonical text.
  const memberEvent = parseMemberSystemEvent(message);
  // Issue-lifecycle backflow rows (#497, item #7) carry a structured
  // `system_event` part the FE projects into the frozen "任务" copy — a localized
  // action verb with the issue identifier as the sole clickable token — instead
  // of dumping the raw English "moved to In Progress" string.
  // LRM-423: server aggregates (non-empty `items`) win over the single-issue path.
  const issueAggregateEvent = parseIssueAggregateSystemEvent(message);
  const issueEvent = issueAggregateEvent ? null : parseIssueSystemEvent(message);
  // Channel↔project association events (#610): bind/change/unbind, projected into
  // a localized row whose sole clickable object is the project name.
  const projectEvent = parseProjectSystemEvent(message);
  // #656/#655 Reminder-fired receipts: localized ×4 "Reminder fired: <title>"
  // (+ "Anchor unavailable" when the anchored message/thread no longer
  // exists), read-only — never the backend's hard-coded English fallback.
  const reminderEvent = parseReminderSystemEvent(message);
  const threadUnfollowedEvent = parseThreadUnfollowedSystemEvent(message);
  // Older backflow rows without the `system_event` part still carry an anchored
  // `reference`, so project those into tokens rather than the raw string (#469).
  const hasReferenceParts = message.parts?.some((part) => part.type === "reference") ?? false;
  const showIssueTime = Boolean(issueEvent || issueAggregateEvent);
  return (
    <div
      id={`message-${message.id}`}
      data-testid="system-message-row"
      data-message-kind="system"
      // Time is NOT a persistent tail (#369, Iris §8: Frank disliked a trailing
      // timestamp pinned right). Member/notice rows keep the absolute time
      // REVEALED on hover ("系统事件 hover 出时间"), so `title` carries the full
      // local date-time. Issue rows (item #7口径) show a SIMPLE inline time and
      // NEVER a full timestamp, so they opt out of the hover-full treatment.
      title={showIssueTime ? undefined : messageTime.full(message.created_at)}
      className={cn(
        // Lightweight CENTERED service notice (#369, Iris §8): a top-left row
        // reads like "a message that lost its avatar" against Multica's heavy
        // avatar column + loose body — centering separates it as a system event.
        // Quiet by design: small muted text, tight vertical rhythm so consecutive
        // add/remove rows read as one cluster, NO capsule / avatar / bubble.
        "group mx-auto flex max-w-[min(640px,100%)] flex-wrap items-baseline justify-center gap-x-1.5 gap-y-0.5 px-2 py-0.5 text-center text-xs text-muted-foreground outline-none transition-colors duration-1000",
        highlighted && "rounded-md bg-primary/10 ring-1 ring-primary/25 duration-0",
      )}
    >
      <span className="min-w-0 break-words">
        {issueAggregateEvent ? (
          <IssueAggregateSystemEventContent
            event={issueAggregateEvent}
            sourceMessageId={message.id}
          />
        ) : issueEvent ? (
          <IssueSystemEventContent event={issueEvent} sourceMessageId={message.id} />
        ) : memberEvent ? (
          <MemberSystemEventContent event={memberEvent} />
        ) : projectEvent ? (
          <ProjectSystemEventContent event={projectEvent} />
        ) : reminderEvent ? (
          <ReminderSystemEventContent event={reminderEvent} />
        ) : threadUnfollowedEvent ? (
          <ThreadUnfollowedSystemEventContent event={threadUnfollowedEvent} />
        ) : hasReferenceParts ? (
          // Spans are anchored to the RAW `message.content`; feeding the trimmed
          // `systemText` would shift every offset and misplace the tokens.
          <InlineReferenceContent
            content={message.content}
            parts={message.parts}
            sourceMessageId={message.id}
          />
        ) : (
          systemText
        )}
      </span>
      {showIssueTime ? (
        // Simple, always-visible time — "· 10:16" (item #7口径), the bucketed
        // inline clock the rest of the list uses, never a full timestamp.
        <span className="shrink-0 tabular-nums text-muted-foreground/60">
          {"· "}
          {messageTime.format(message.created_at)}
        </span>
      ) : (
        <span
          aria-hidden
          className="shrink-0 tabular-nums text-muted-foreground/50 opacity-0 transition-opacity duration-150 group-hover:opacity-100"
        >
          {messageTime.full(message.created_at)}
        </span>
      )}
    </div>
  );
}

/**
 * One message in the shared Channel/DM/Thread timeline. Ordinary text renders
 * as an IM-style message item, while quote/attachment/code-like content keeps
 * local structure inside the shared Markdown pipeline.
 */
export const ChannelMessageBubble = memo(function ChannelMessageBubble({
  message,
  currentUserId,
  ownName,
  highlighted = false,
  onOpenThread,
  onScrollTo,
  onReact,
  onQuote,
  onEdit,
  onDelete,
  onOpenAgent,
  onRetrySend,
  searchHighlighted = false,
  searchQuery,
  /**
   * Slack parity (LRM-268): long bodies clamp for every message (read + unread).
   * Pass false only in tests that need an uncapped body.
   */
  collapseLongContent = true,
  /** Slack-style continuation: no avatar/name row; gutter shows HH:mm on hover. */
  compact = false,
}: {
  message: ChannelMessage;
  currentUserId: string | null;
  /** Display name for the viewer's own messages. Defaults to the stored name. */
  ownName?: string;
  /** Deep-link target: briefly ring-highlights the message, then fades. */
  highlighted?: boolean;
  /** Called when the user opens the message's side thread. */
  onOpenThread?: (message: ChannelMessage) => void;
  /** Called when the user clicks the inline quote block to jump to the original. */
  onScrollTo?: (messageId: string) => void;
  /** Toggle/add a lightweight emoji reaction on this message. */
  onReact?: (message: ChannelMessage, emoji: string) => void;
  /** Set this message as the composer quote target. */
  onQuote?: (message: ChannelMessage) => void;
  /**
   * Save an inline edit of the viewer's own message. H5: this is an edit, never
   * a re-send — it must not go through a send/dispatch path (no new wake).
   */
  onEdit?: (message: ChannelMessage, content: string) => void;
  /** Soft-delete the viewer's own message; the bubble then renders a tombstone. */
  onDelete?: (message: ChannelMessage) => void;
  /** Opens the side agent file/public-info panel for agent-authored messages
   *  (LRM-292: agentId + optional row identity snapshot). */
  onOpenAgent?: OpenAgentPanelFn;
  /** One-click retry for a failed optimistic send (reuses `client_message_id`). */
  onRetrySend?: (message: ChannelMessage) => void;
  /** Search hit: marks matching visible text while search is open. */
  searchHighlighted?: boolean;
  /** Trimmed conversation search phrase to mark inside this hit's visible text. */
  searchQuery?: string;
  /** When true (default), clamp overflowing bodies to Slack's collapsed height. */
  collapseLongContent?: boolean;
  /** When true, render as a same-author continuation (avatar/name hidden). */
  compact?: boolean;
// LRM-268 adds contentExpanded/contentOverflows; mobileOverlay already uses a
// union instead of three booleans (#568). Full useReducer consolidation is a
// separate refactor of this ~1100-line component — suppress to unblock CI.
// react-doctor-disable-next-line react-doctor/prefer-useReducer
}) {
  const { t } = useT("channels");
  const { getActorName } = useActorName();
  // LRM-364: group managers miss ListAgents → resolve via member-profile, never
  // surface "Unknown Agent" in the reaction hover card.
  const getReactionActorName = useReactionActorName(message.reactions ?? []);
  const resolveMentionPreview = mentionResolverFrom(getActorName);
  const messageTime = useMessageTime();
  const [editDraft, setEditDraft] = useState<string | null>(null);
  // Single state machine for the mobile action/reaction sheets (react-doctor
  // prefer-useReducer + Iris #568: three independent booleans made "only one
  // mobile overlay open at a time" something callers had to remember; a union
  // makes it structurally impossible to have two open together).
  const [mobileOverlay, setMobileOverlay] = useState<
    "none" | "actions" | "reaction" | "reaction-full"
  >("none");
  const mobileActionsOpen = mobileOverlay === "actions";
  const mobileReactionOpen = mobileOverlay === "reaction" || mobileOverlay === "reaction-full";
  const mobileReactionShowFull = mobileOverlay === "reaction-full";
  // Key expansion to the message identity so a recycled row cannot keep another
  // bubble's See-more choice (no prop→state effect; LRM-268 / react-doctor).
  const collapseIdentity = `${message.id}\0${message.content ?? ""}\0${message.parts?.length ?? 0}\0${message.attachments?.length ?? 0}`;
  const [expandedForIdentity, setExpandedForIdentity] = useState<string | null>(null);
  const contentExpanded = expandedForIdentity === collapseIdentity;
  const [contentOverflows, setContentOverflows] = useState(false);
  const [mobileThreadTapActive, setMobileThreadTapActive] = useState(false);
  const longPressTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const tapFeedbackTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const mobileActionsDialogRef = useRef<HTMLDialogElement | null>(null);
  const mobileReactionDialogRef = useRef<HTMLDialogElement | null>(null);
  const messageBodyRef = useRef<HTMLDivElement | null>(null);
  const measureContentOverflowRef = useRef<() => void>(() => {});
  const touchStartRef = useRef<{ x: number; y: number } | null>(null);
  const touchCancelledRef = useRef(false);
  const swipeLeftCandidateRef = useRef(false);


  // react-doctor-disable-next-line react-doctor/exhaustive-deps -- unmount needs to clear whichever touch timers are currently pending.
  useEffect(() => {
    return () => {
      if (longPressTimerRef.current) clearTimeout(longPressTimerRef.current);
      if (tapFeedbackTimerRef.current) clearTimeout(tapFeedbackTimerRef.current);
    };
  }, []);

  const showMobileActionsDialog = useCallback((dialog: HTMLDialogElement | null) => {
    mobileActionsDialogRef.current = dialog;
    if (!dialog || dialog.open) return;
    if (typeof dialog.showModal === "function") dialog.showModal();
    else dialog.setAttribute("open", "");
  }, []);

  const showMobileReactionDialog = useCallback((dialog: HTMLDialogElement | null) => {
    mobileReactionDialogRef.current = dialog;
    if (!dialog || dialog.open) return;
    if (typeof dialog.showModal === "function") dialog.showModal();
    else dialog.setAttribute("open", "");
  }, []);

  const measureContentOverflow = useCallback(() => {
    const body = messageBodyRef.current;
    if (!body || !collapseLongContent) {
      setContentOverflows(false);
      return;
    }
    // scrollHeight stays the full content height even while max-height clips.
    const overflows =
      body.scrollHeight > MESSAGE_COLLAPSE_MAX_HEIGHT_PX + MESSAGE_COLLAPSE_OVERFLOW_EPSILON_PX;
    setContentOverflows((previous) => (previous === overflows ? previous : overflows));
  }, [collapseLongContent]);
  measureContentOverflowRef.current = measureContentOverflow;

  useLayoutEffect(() => {
    if (!collapseLongContent || message.deleted_at || message.type === "system") {
      setContentOverflows(false);
      return;
    }
    measureContentOverflowRef.current();
  }, [
    collapseLongContent,
    contentExpanded,
    message.id,
    message.content,
    message.parts,
    message.attachments,
    message.deleted_at,
    message.type,
    editDraft,
  ]);

  useEffect(() => {
    if (!collapseLongContent || message.deleted_at || message.type === "system") return;
    const body = messageBodyRef.current;
    if (!body) return;

    const handleOverflow = () => measureContentOverflowRef.current();
    const resizeObserver =
      typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(handleOverflow);
    resizeObserver?.observe(body);
    window.addEventListener("resize", handleOverflow);
    return () => {
      resizeObserver?.disconnect();
      window.removeEventListener("resize", handleOverflow);
    };
  }, [collapseLongContent, message.id, message.deleted_at, message.type]);

  if (message.deleted_at) {
    return (
      <div
        id={`message-${message.id}`}
        data-testid="message-tombstone"
        data-message-kind="deleted"
        className={cn(
          "mx-2 flex items-center gap-2 rounded-md px-2 py-1.5 text-sm italic text-muted-foreground outline-none transition-colors duration-1000",
          highlighted && "bg-primary/10 ring-1 ring-primary/25 duration-0",
        )}
      >
        <Trash2 className="size-3.5 shrink-0" />
        <span className="min-w-0 break-words">
          {t(($) => $.message.deleted_placeholder)}
        </span>
      </div>
    );
  }

  if (message.type === "system") {
    if (isLegacyRuntimeSystemNotice(message)) return null;

    const systemText = message.content.trim() || message.author_name || "System";
    return (
      <ChannelSystemMessageRow
        message={message}
        highlighted={highlighted}
        systemText={systemText}
      />
    );
  }

  const isOwn =
    message.type === "user" &&
    message.author_id != null &&
    message.author_id === currentUserId;
  const isAgent = message.type === "agent";
  const isExternal = message.source === "lark";
  // LRM-270 (Slack align): message author row — name + time only.
  // No Owner/Admin chrome (Slack has none); no Agent/APP type pill.
  // Radar / Feishu functional badges stay. Member-list muted role is unchanged.
  const displayName = resolveChannelAuthorDisplayName(message, {
    currentUserId,
    ownName,
    getActorName,
  });
  const isRadarMessage = isAgent && message.content.trimStart().startsWith("主动发现：");
  const profileActorType =
    message.type === "agent"
      ? "agent"
      : message.type === "user"
        ? "user"
        : null;
  const profileActorId = profileActorType ? message.author_id : null;
  // LRM-224 / LRM-223 option B: identity-first Avatar. Message `author_avatar_url`
  // only seeds the sticky cache (via avatarUrlHint); null must not clear a known
  // face. Chat `user` → directory `member`. Agent status dots are in-scope for
  // bubbles per the frozen long-term design (supersedes #477 for this epic).
  const identityActorType =
    message.type === "agent"
      ? "agent"
      : message.type === "user"
        ? "member"
        : null;
  // The 2px baseline nudge (`mt-0.5`) lives on the outer wrapper, not the
  // avatar itself, so that when the avatar sits inside the fixed-size presence
  // box the box hugs the avatar exactly (a margin on the inner avatar would
  // overflow the box and lift the dot off the avatar's bottom edge).
  const avatarNode =
    identityActorType && message.author_id ? (
      <ActorAvatar
        actorType={identityActorType}
        actorId={message.author_id}
        size={28}
        className="select-none"
        name={displayName}
        avatarUrlHint={message.author_avatar_url}
        showStatusDot={isAgent || message.type === "user"}
        showXpBurst={isAgent}
        profileLink={false}
      />
    ) : null;
  const avatar = avatarNode ? (
    <span className="mt-0.5 inline-flex shrink-0">{avatarNode}</span>
  ) : null;
  const nameLabel = (
    <span className="truncate font-bold text-ink">{displayName}</span>
  );

  const localSendStatus = message.local_send_status ?? null;
  const isLocalPending = localSendStatus === "pending";
  const isLocalFailed = localSendStatus === "failed";
  const isLocalSend = isLocalPending || isLocalFailed;
  const canOpenThread = !!onOpenThread && !message.thread_root_message_id && !isLocalSend;
  const threadReplyCount = message.thread_reply_count ?? 0;
  const threadUnreadCount = message.thread_unread_count ?? 0;
  const hasThreadActivity = threadReplyCount > 0 || threadUnreadCount > 0;
  const hasFeedback = (message.reactions?.length ?? 0) > 0 || hasThreadActivity;
  const quickReactionEmojis = ["👍", "👎", "😄", "🎉", "😕", "❤️", "🚀", "👀"];
  // Resolved once here for copy; `MessageBody` resolves the same way to render
  // the body (see resolveMessageParts for the envelope-unwrap rationale).
  // Non-null for real parts / historical envelopes, null for ordinary content.
  const effectiveParts = resolveMessageParts(message.content, message.parts);
  const voicePresentation = resolveVoiceMessagePresentation(message);
  const isAgentVoiceReply = message.type === "agent" && voicePresentation !== null;
  const hidesVoiceTranscript =
    isAgentVoiceReply || voicePresentation?.source === "recording";
  const handleCopy = async () => {
    // Copy = take away what I can see (#530, Iris's ruling). The screen says
    // `@小雅`; a clipboard holding `@actor_14` disagrees with it, and that is its
    // own kind of lying. Round-trip fidelity is not a counter-argument: pasting
    // back into our own composer should go through the mention picker, and
    // anywhere else the internal handle is just noise.
    const copyPayload =
      projectReferencesToText(message.content, message.parts, resolveMentionPreview) ??
      formatMessagePartsCopyText(effectiveParts) ??
      unwrapStructuredPreviewContent(message.content) ??
      message.content;
    if (await copyText(copyPayload)) {
      toast.success(t(($) => $.message.copied_toast));
    } else {
      toast.error(t(($) => $.message.copy_failed_toast));
    }
  };
  const runMobileAction = (action: () => void | Promise<void>) => {
    setMobileOverlay("none");
    void action();
  };
  const handleQuote = () => onQuote?.(message);

  // Edit / delete are viewer-own affordances only. H5: saving an edit routes
  // through onEdit (a PATCH), never a re-send — it cannot produce a new wake.
  const isEditing = editDraft !== null;
  // Edit unshipped 2026-07-05 (Frank/Miles): the inline editor is a plain
  // textarea with no @mention/composer parity; hidden until rebuilt on the
  // unified composer (#258 backlog). onEdit/MessageInlineEditor kept dormant for
  // that rebuild. Delete stays.
  const canEdit = false;
  const canDelete = isOwn && !!onDelete && !isLocalSend;
  const isEdited = !!message.edited_at;
  const canCollapseContent = collapseLongContent && contentOverflows;
  const isContentCollapsed = canCollapseContent && !contentExpanded;
  const handleStartEdit = () => setEditDraft(message.content);
  const handleCancelEdit = () => setEditDraft(null);
  const handleSaveEdit = () => {
    const next = (editDraft ?? "").trim();
    if (next && next !== message.content) {
      onEdit?.(message, next);
    }
    setEditDraft(null);
  };
  const handleDelete = () => onDelete?.(message);
  const handleOpenAgent = () => {
    if (isAgent && message.author_id) {
      onOpenAgent?.(message.author_id, {
        display_name: message.author_name,
        avatar_url: message.author_avatar_url ?? null,
      });
    }
  };
  const handleOpenAgentCapture = isAgent && onOpenAgent ? handleOpenAgent : undefined;
  const clearLongPressTimer = () => {
    if (longPressTimerRef.current) {
      clearTimeout(longPressTimerRef.current);
      longPressTimerRef.current = null;
    }
  };
  const clearTapFeedbackTimer = () => {
    if (tapFeedbackTimerRef.current) {
      clearTimeout(tapFeedbackTimerRef.current);
      tapFeedbackTimerRef.current = null;
    }
  };
  const openMobileActions = () => {
    if (!isEditing && isMobileActionViewport()) {
      clearTapFeedbackTimer();
      setMobileThreadTapActive(false);
      setMobileOverlay("actions");
    }
  };
  // Reply/React overlay lifecycle (Iris #568): only one mobile action layer may
  // be open at a time. Selecting "Add reaction" from the primary sheet closes
  // it first, then opens the dedicated reaction sheet — never both mounted
  // together (was a real double-panel bug: Popover portal + open <dialog>).
  const openMobileReactionFromActions = () => {
    setMobileOverlay("reaction");
  };
  const handleMobileReactionSheetSelect = (emoji: string) => {
    setMobileOverlay("none");
    onReact?.(message, emoji);
  };
  const openThreadAfterMobileTap = () => {
    if (!canOpenThread) return;
    setMobileOverlay("none");
    clearTapFeedbackTimer();
    setMobileThreadTapActive(true);
    tapFeedbackTimerRef.current = setTimeout(() => {
      tapFeedbackTimerRef.current = null;
      setMobileThreadTapActive(false);
      onOpenThread?.(message);
    }, MOBILE_THREAD_TAP_FEEDBACK_MS);
  };
  const handlePointerDown = (event: PointerEvent<HTMLDivElement>) => {
    if (event.pointerType !== "touch" || isInteractiveMessageTarget(event.target)) return;
    touchCancelledRef.current = false;
    swipeLeftCandidateRef.current = false;
    touchStartRef.current = { x: event.clientX, y: event.clientY };
    clearLongPressTimer();
    longPressTimerRef.current = setTimeout(() => {
      longPressTimerRef.current = null;
      touchCancelledRef.current = true;
      swipeLeftCandidateRef.current = false;
      if (!hasActiveTextSelection()) openMobileActions();
    }, LONG_PRESS_MS);
  };
  const handlePointerMove = (event: PointerEvent<HTMLDivElement>) => {
    const start = touchStartRef.current;
    if (!start) return;
    const dx = event.clientX - start.x;
    const dy = event.clientY - start.y;
    const absDx = Math.abs(dx);
    const absDy = Math.abs(dy);
    // Vertical scroll dominates → abandon long-press / swipe.
    if (absDy > TOUCH_MOVE_CANCEL_PX && absDy >= absDx) {
      touchCancelledRef.current = true;
      swipeLeftCandidateRef.current = false;
      clearLongPressTimer();
      return;
    }
    if (absDx > TOUCH_MOVE_CANCEL_PX) {
      clearLongPressTimer();
      if (dx < 0) {
        swipeLeftCandidateRef.current = true;
      } else {
        touchCancelledRef.current = true;
        swipeLeftCandidateRef.current = false;
      }
    }
  };
  const handlePointerEnd = (event: PointerEvent<HTMLDivElement>) => {
    const start = touchStartRef.current;
    const wasTouch = event.pointerType === "touch" && start;
    const dx = start ? event.clientX - start.x : 0;
    const dy = start ? event.clientY - start.y : 0;
    const wasSwipeLeft =
      swipeLeftCandidateRef.current &&
      dx <= -SWIPE_LEFT_OPEN_PX &&
      Math.abs(dy) < SWIPE_VERTICAL_CANCEL_PX;
    touchStartRef.current = null;
    swipeLeftCandidateRef.current = false;
    clearLongPressTimer();

    if (!wasTouch || !isMobileActionViewport()) {
      touchCancelledRef.current = false;
      return;
    }
    // LRM-495: left swipe opens the action sheet (Slack mobile convention).
    if (wasSwipeLeft) {
      touchCancelledRef.current = false;
      if (!isInteractiveMessageTarget(event.target) && !hasActiveTextSelection()) {
        openMobileActions();
      }
      return;
    }
    if (touchCancelledRef.current) {
      touchCancelledRef.current = false;
      return;
    }
    touchCancelledRef.current = false;
    if (isInteractiveMessageTarget(event.target) || hasActiveTextSelection()) return;
    if (canOpenThread) {
      openThreadAfterMobileTap();
      return;
    }
    openMobileActions();
  };
  const cancelTouchGesture = () => {
    touchCancelledRef.current = true;
    swipeLeftCandidateRef.current = false;
    touchStartRef.current = null;
    clearLongPressTimer();
  };

  // Self-mention row wash: cool brand tint when *someone else* addresses the
  // viewer (@me or @all). Own messages never wash — the author already knows
  // they typed the mention; the wash is a scan aid for incoming address.
  // Deep-link `highlighted` keeps its primary ring and wins over the wash;
  // mobile tap feedback also wins.
  const addressedToViewer = messageMentionsViewer(
    message.content,
    currentUserId,
    message.parts,
  );
  const selfMentioned = addressedToViewer && !isOwn;
  const collapseFadeVariant = resolveMessageCollapseFadeVariant({
    selfMentioned,
    highlighted: Boolean(highlighted),
    searchHighlighted: Boolean(searchHighlighted),
  });
  const showAuthor = !compact;

  const bubble = (
    <div
      id={`message-${message.id}`}
      data-testid="message-bubble"
      data-message-group={compact ? "compact" : "lead"}
      data-own={isOwn}
      data-self-mentioned={selfMentioned ? "true" : undefined}
      data-local-send={localSendStatus ?? undefined}
      className={cn(
        // LRM-495: no permanent mobile ⋯ column — coarse pointers open actions
        // via long-press / left-swipe; fine pointers keep the hover action bar.
        "group relative grid grid-cols-[28px_minmax(0,1fr)] gap-2.5 rounded-lg px-2 outline-none transition-colors duration-1000 hover:bg-muted/35 focus-within:bg-muted/35",
        // Tighten lead-row vertical rhythm one notch (py-1.5 → py-1); keep
        // avatar / name / time alignment. Compact continuations stay tighter.
        compact ? "py-0" : "py-1",
        selfMentioned && SELF_MENTION_ROW_CLASS,
        selfMentioned && SELF_MENTION_ROW_MENTION_CLASS,
        highlighted && "bg-primary/10 ring-1 ring-primary/25 duration-0 hover:bg-primary/10 focus-within:bg-primary/10",
        mobileThreadTapActive && "bg-primary/[0.04] ring-1 ring-primary/45 duration-75",
        // Pending is silent (Slack / LRM-271/273): no opacity flash on ACK.
        isLocalFailed && "opacity-90",
      )}
      onPointerDown={handlePointerDown}
      onPointerMove={handlePointerMove}
      onPointerUp={handlePointerEnd}
      onPointerCancel={cancelTouchGesture}
      onPointerLeave={cancelTouchGesture}
    >
      {compact ? (
        <span
          data-testid="message-gutter-time"
          className="mt-0.5 select-none self-start justify-self-end pt-0.5 text-[10px] tabular-nums text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100"
          title={messageTime.full(message.created_at)}
          aria-hidden
        >
          {messageTime.clock(message.created_at)}
        </span>
      ) : profileActorType && profileActorId ? (
        <ActorProfileTrigger
          memberType={profileActorType}
          memberId={profileActorId}
          side="top"
          sideOffset={8}
          onClickCapture={handleOpenAgentCapture}
        >
          {avatar}
        </ActorProfileTrigger>
      ) : (
        avatar
      )}
      <div className="min-w-0 max-w-[min(760px,100%)]">
        {showAuthor && (
          <div className="mb-0.5 flex select-none items-baseline gap-2 md:pr-24 text-sm">
            {profileActorType && profileActorId ? (
              <ActorProfileTrigger
                memberType={profileActorType}
                memberId={profileActorId}
                side="top"
                sideOffset={8}
                onClickCapture={handleOpenAgentCapture}
              >
                {nameLabel}
              </ActorProfileTrigger>
            ) : (
              nameLabel
            )}
            {isRadarMessage && (
              <span className="shrink-0 rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-[11px] font-normal leading-none text-amber-700 dark:text-amber-300">
                {t(($) => $.message.radar_badge)}
              </span>
            )}
            {isExternal && (
              <span className="shrink-0 rounded-full border bg-muted px-2 py-0.5 text-[11px] leading-none text-muted-foreground">
                {t(($) => $.message.feishu_badge)}
              </span>
            )}
            <span
              className="shrink-0 text-[11px] text-ink-3"
              title={messageTime.full(message.created_at)}
            >
              {messageTime.format(message.created_at)}
            </span>
            {isEdited && (
              <span
                data-testid="message-edited"
                className="shrink-0 text-[11px] text-ink-3/70"
              >
                {t(($) => $.message.edited_label)}
              </span>
            )}
          </div>
        )}
        {!isEditing && !isLocalSend && (
          <div
            data-testid="message-action-bar"
            data-message-action-surface="true"
            className={cn(
              "pointer-events-none absolute right-3 z-10 hidden items-center gap-0.5 text-muted-foreground opacity-0 transition-opacity [@media(pointer:fine)]:flex [@media(pointer:fine)]:group-hover:pointer-events-auto [@media(pointer:fine)]:group-hover:opacity-100 [@media(pointer:fine)]:group-focus-within:pointer-events-auto [@media(pointer:fine)]:group-focus-within:opacity-100",
              compact ? "top-0.5" : "top-2",
            )}
          >
            {onReact && (
              <QuickEmojiPicker
                onSelect={(emoji) => onReact(message, emoji)}
                align="end"
                side="bottom"
                className="size-8 rounded-md hover:bg-background/70 hover:text-foreground focus-visible:bg-background/70 focus-visible:text-foreground"
                ariaLabel={t(($) => $.message.add_reaction)}
                sideOffset={4}
                emojis={quickReactionEmojis}
                showMore={false}
                contentClassName="rounded-md border border-border/70 bg-popover/95 shadow-none ring-0"
              />
            )}
            <button
              type="button"
              onClick={handleCopy}
              className="inline-flex size-8 items-center justify-center rounded-md transition-colors hover:bg-background/70 hover:text-foreground focus-visible:bg-background/70 focus-visible:text-foreground"
              aria-label={t(($) => $.message.copy_action)}
              title={t(($) => $.message.copy_action)}
            >
              <Copy className="size-4" />
            </button>
            {onQuote && (
              <button
                type="button"
                onClick={handleQuote}
                className="inline-flex size-8 items-center justify-center rounded-md transition-colors hover:bg-background/70 hover:text-foreground focus-visible:bg-background/70 focus-visible:text-foreground"
                aria-label={t(($) => $.quote.action)}
                title={t(($) => $.quote.action)}
              >
                <Quote className="size-4" />
              </button>
            )}
            {canOpenThread && (
              <button
                type="button"
                onClick={() => onOpenThread?.(message)}
                className="inline-flex size-8 items-center justify-center rounded-md transition-colors hover:bg-background/70 hover:text-foreground focus-visible:bg-background/70 focus-visible:text-foreground"
                aria-label={t(($) => $.thread.reply)}
                title={t(($) => $.thread.reply)}
              >
                <MessageSquare className="size-4" />
              </button>
            )}
            {canEdit && (
              <button
                type="button"
                onClick={handleStartEdit}
                className="inline-flex size-8 items-center justify-center rounded-md transition-colors hover:bg-background/70 hover:text-foreground focus-visible:bg-background/70 focus-visible:text-foreground"
                aria-label={t(($) => $.message.edit_action)}
                title={t(($) => $.message.edit_action)}
              >
                <Pencil className="size-4" />
              </button>
            )}
            {canDelete && (
              <button
                type="button"
                onClick={handleDelete}
                className="inline-flex size-8 items-center justify-center rounded-md transition-colors hover:bg-destructive/10 hover:text-destructive focus-visible:bg-destructive/10 focus-visible:text-destructive"
                aria-label={t(($) => $.message.delete_action)}
                title={t(($) => $.message.delete_action)}
              >
                <Trash2 className="size-4" />
              </button>
            )}
          </div>
        )}
        {isEditing ? (
          <MessageInlineEditor
            value={editDraft ?? ""}
            onChange={setEditDraft}
            onSave={handleSaveEdit}
            onCancel={handleCancelEdit}
            editLabel={t(($) => $.message.edit_action)}
            saveLabel={t(($) => $.message.save_edit)}
            cancelLabel={t(($) => $.message.cancel_edit)}
          />
        ) : (
          <div
            ref={messageBodyRef}
            className={cn(
              "message-surface relative min-w-0 max-w-full select-text break-words [overflow-wrap:anywhere] text-sm leading-6 text-ink",
              isContentCollapsed && "overflow-hidden",
              isContentCollapsed ? MESSAGE_COLLAPSE_HEIGHT_CLASS : "overflow-visible",
              searchHighlighted && "rounded-md bg-primary/5",
            )}
            data-testid="message-body"
            data-collapsed={isContentCollapsed ? "true" : undefined}
            style={{ WebkitTouchCallout: "default" }}
          >
            {(message.quote || message.quote_message_id) && (
              <MessageQuoteCard
                quote={message.quote}
                quoteMessageId={message.quote_message_id}
                currentUserId={currentUserId}
                ownName={ownName}
                onJump={onScrollTo}
              />
            )}
            <MessageBody
              content={message.content}
              parts={message.parts}
              attachments={message.attachments}
              highlightQuery={searchHighlighted ? searchQuery : undefined}
              sourceMessageId={message.id}
              consumedAttachmentIds={voicePresentation?.consumedAttachmentIds}
              contentMode={hidesVoiceTranscript ? "non-transcript" : "all"}
            />
            <VoiceMessageAudio
              message={message}
              presentation={voicePresentation}
              highlightQuery={searchHighlighted ? searchQuery : undefined}
            />
            {isLocalFailed && onRetrySend && (
              <div
                data-testid="message-send-failed"
                className="mt-1.5 flex flex-wrap items-center gap-2 text-xs text-destructive"
              >
                <span>{t(($) => $.message.send_failed)}</span>
                <button
                  type="button"
                  onClick={() => onRetrySend(message)}
                  className="inline-flex h-7 items-center rounded-md border border-destructive/40 bg-destructive/10 px-2.5 font-medium text-destructive transition-colors hover:bg-destructive/15 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                >
                  {t(($) => $.message.retry_send)}
                </button>
              </div>
            )}
            {isContentCollapsed && (
              <div
                className={messageCollapseFadeClassName(collapseFadeVariant)}
                data-testid="message-collapse-fade"
              >
                {/* LRM-302: text link, not centered pill — must not cover body. */}
                <button
                  type="button"
                  className="pointer-events-auto inline-flex min-h-8 items-center px-0 text-sm font-normal text-primary underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  onClick={() => setExpandedForIdentity(collapseIdentity)}
                >
                  {t(($) => $.message.expand_action)}
                </button>
              </div>
            )}
            {canCollapseContent && !isContentCollapsed && (
              <div className="mt-1 flex justify-start" data-testid="message-collapse-less">
                <button
                  type="button"
                  className="inline-flex min-h-8 items-center px-0 text-sm font-normal text-primary underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  onClick={() => setExpandedForIdentity(null)}
                >
                  {t(($) => $.message.collapse_action)}
                </button>
              </div>
            )}
          </div>
        )}
        {!isEditing && mobileActionsOpen && (
          <dialog
            ref={showMobileActionsDialog}
            className="fixed inset-0 z-50 m-0 h-dvh max-h-none w-screen max-w-none border-0 bg-transparent p-0 backdrop:bg-black/10 [@media(pointer:fine)]:hidden"
            aria-label={t(($) => $.message.actions_menu)}
            onCancel={(event) => {
              event.preventDefault();
              setMobileOverlay("none");
            }}
            onClose={() => setMobileOverlay("none")}
          >
            <form method="dialog" className="absolute inset-0">
              <button
                type="submit"
                aria-label={t(($) => $.message.actions_menu)}
                className="h-full w-full cursor-default"
              />
            </form>
            <div
              data-testid="mobile-message-actions"
              data-message-action-surface="true"
              className="absolute inset-x-0 bottom-0 rounded-t-2xl border-t border-border bg-popover p-3 pb-[calc(env(safe-area-inset-bottom)+0.75rem)] text-popover-foreground shadow-2xl"
            >
              <div className="mx-auto mb-2 h-1 w-10 rounded-full bg-muted-foreground/25" />
              <div className="flex flex-col gap-1">
                {onReact && (
                  <button
                    type="button"
                    onClick={openMobileReactionFromActions}
                    className="inline-flex h-11 items-center gap-3 rounded-xl px-3 text-sm text-popover-foreground transition-colors hover:bg-muted focus-visible:bg-muted focus-visible:outline-none"
                  >
                    <SmilePlus className="size-4" />
                    <span>{t(($) => $.message.add_reaction)}</span>
                  </button>
                )}
                <button
                  type="button"
                  onClick={() => runMobileAction(handleCopy)}
                  className="inline-flex h-11 items-center gap-3 rounded-xl px-3 text-sm transition-colors hover:bg-muted focus-visible:bg-muted focus-visible:outline-none"
                >
                  <Copy className="size-4" />
                  <span>{t(($) => $.message.copy_action)}</span>
                </button>
                {onQuote && (
                  <button
                    type="button"
                    onClick={() => runMobileAction(handleQuote)}
                    className="inline-flex h-11 items-center gap-3 rounded-xl px-3 text-sm transition-colors hover:bg-muted focus-visible:bg-muted focus-visible:outline-none"
                  >
                    <Quote className="size-4" />
                    <span>{t(($) => $.quote.action)}</span>
                  </button>
                )}
                {canEdit && (
                  <button
                    type="button"
                    onClick={() => runMobileAction(handleStartEdit)}
                    className="inline-flex h-11 items-center gap-3 rounded-xl px-3 text-sm transition-colors hover:bg-muted focus-visible:bg-muted focus-visible:outline-none"
                  >
                    <Pencil className="size-4" />
                    <span>{t(($) => $.message.edit_action)}</span>
                  </button>
                )}
                {canDelete && (
                  <button
                    type="button"
                    onClick={() => runMobileAction(handleDelete)}
                    className="inline-flex h-11 items-center gap-3 rounded-xl px-3 text-sm text-destructive transition-colors hover:bg-destructive/10 focus-visible:bg-destructive/10 focus-visible:outline-none"
                  >
                    <Trash2 className="size-4" />
                    <span>{t(($) => $.message.delete_action)}</span>
                  </button>
                )}
              </div>
            </div>
          </dialog>
        )}
        {!isEditing && onReact && mobileReactionOpen && (
          <dialog
            ref={showMobileReactionDialog}
            className="fixed inset-0 z-50 m-0 h-dvh max-h-none w-screen max-w-none border-0 bg-transparent p-0 backdrop:bg-black/10 [@media(pointer:fine)]:hidden"
            aria-label={t(($) => $.message.add_reaction)}
            onCancel={(event) => {
              event.preventDefault();
              setMobileOverlay("none");
            }}
            onClose={() => setMobileOverlay("none")}
          >
            <form method="dialog" className="absolute inset-0">
              <button
                type="submit"
                aria-label={t(($) => $.message.add_reaction)}
                className="h-full w-full cursor-default"
              />
            </form>
            <div
              data-testid="mobile-reaction-sheet"
              data-message-action-surface="true"
              className="absolute inset-x-0 bottom-0 rounded-t-2xl border-t border-border bg-popover p-3 pb-[calc(env(safe-area-inset-bottom)+0.75rem)] text-popover-foreground shadow-2xl"
            >
              <div className="mx-auto mb-2 h-1 w-10 rounded-full bg-muted-foreground/25" />
              {mobileReactionShowFull ? (
                <Suspense
                  fallback={
                    <div className="p-4 text-center text-sm text-muted-foreground">
                      {t(($) => $.message.actions_menu)}
                    </div>
                  }
                >
                  <FullEmojiPicker onSelect={handleMobileReactionSheetSelect} emojiButtonSize={44} />
                </Suspense>
              ) : (
                <>
                  <div className="grid grid-cols-4 gap-2 py-1">
                    {quickReactionEmojis.map((emoji) => (
                      <button
                        key={emoji}
                        type="button"
                        onClick={() => handleMobileReactionSheetSelect(emoji)}
                        className="flex h-11 w-11 items-center justify-center rounded-xl text-xl transition-colors hover:bg-muted focus-visible:bg-muted focus-visible:outline-none"
                      >
                        {emoji}
                      </button>
                    ))}
                  </div>
                  <button
                    type="button"
                    onClick={() => setMobileOverlay("reaction-full")}
                    className="mt-1 flex h-11 w-full items-center justify-center rounded-xl text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:bg-muted focus-visible:outline-none"
                  >
                    {t(($) => $.message.more_emojis)}
                  </button>
                </>
              )}
            </div>
          </dialog>
        )}
        {!isEditing && hasFeedback && (
          <div className="mt-2 flex flex-wrap items-center gap-1.5">
            {hasThreadActivity && onOpenThread && (
              <button
                type="button"
                onClick={() => onOpenThread(message)}
                className={cn(
                  "inline-flex h-7 items-center gap-1.5 rounded-md border border-border/80 bg-muted/45 px-2 text-xs font-medium text-foreground transition-colors hover:bg-muted focus-visible:bg-muted focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring md:h-6",
                  threadUnreadCount > 0 && "border-primary/35 bg-primary/10 text-primary",
                )}
              >
                <MessageSquare className="size-3.5" />
                <span>{t(($) => $.thread.reply_count, { count: threadReplyCount })}</span>
                {threadUnreadCount > 0 && (
                  <span className="rounded-full bg-primary px-1.5 py-0.5 text-[10px] leading-none text-primary-foreground">
                    {threadUnreadCount}
                  </span>
                )}
              </button>
            )}
            {onReact && (message.reactions?.length ?? 0) > 0 && (
              <ReactionBar
                reactions={message.reactions ?? []}
                currentUserId={currentUserId ?? undefined}
                onToggle={(emoji) => onReact(message, emoji)}
                getActorName={getReactionActorName}
                hideAddButton
                showQuickReactions={false}
              />
            )}
          </div>
        )}
      </div>
    </div>
  );

  if (!onQuote || isEditing || isLocalSend) return bubble;

  return (
    <ContextMenu>
      <ContextMenuTrigger render={bubble} />
      <ContextMenuContent>
        <ContextMenuItem onClick={handleQuote}>
          <Quote className="mr-2 size-3.5" />
          {t(($) => $.quote.action)}
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}, areChannelMessageBubblePropsEqual);
