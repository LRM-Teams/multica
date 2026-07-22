"use client";

import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useRef,
  useState,
  type PointerEvent,
} from "react";
import { Copy, MessageSquare, MoreHorizontal, Pencil, Quote, Trash2, SmilePlus } from "lucide-react";
import { toast } from "sonner";
import { ReactionBar } from "@multica/ui/components/common/reaction-bar";
import { QuickEmojiPicker } from "@multica/ui/components/common/quick-emoji-picker";
import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import { copyText } from "@multica/ui/lib/clipboard";
import { cn } from "@multica/ui/lib/utils";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from "@multica/ui/components/ui/context-menu";
import { useActorName } from "@multica/core/workspace/hooks";
import type { ChannelMessage } from "@multica/core/types";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { avatarGlyph } from "../../common/initials";
import { InlineReferenceContent } from "../../common/inline-reference-content";
import { useT } from "../../i18n/use-t";
import { useMessageTime } from "../../i18n/use-message-time";
import {
  authorAvatarCacheKey,
  resolveCachedAuthorAvatarUrl,
} from "./author-avatar-cache";
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
import { MessageQuoteCard } from "./message-quote";
import { isLegacyRuntimeSystemNotice } from "./runtime-system-notice";
import {
  parseMemberSystemEvent,
  parseIssueSystemEvent,
  parseProjectSystemEvent,
} from "./channel-system-event";
import {
  MemberSystemEventContent,
  IssueSystemEventContent,
  ProjectSystemEventContent,
} from "./channel-system-event-content";
import { messageMentionsViewer } from "../../common/content-mentions-viewer";
import { SELF_MENTION_ROW_CLASS } from "../../common/mention-token";

const FullEmojiPicker = lazy(() =>
  import("@multica/ui/components/common/emoji-picker").then((m) => ({
    default: m.EmojiPicker,
  })),
);

const LONG_PRESS_MS = 450;
const TOUCH_MOVE_CANCEL_PX = 8;
const MOBILE_THREAD_TAP_FEEDBACK_MS = 120;
const HISTORY_MESSAGE_COLLAPSE_HEIGHT_CLASS = "max-h-[min(260px,55vh)] md:max-h-[360px]";
const HISTORY_MESSAGE_COLLAPSE_MIN_CHARS = 800;
const HISTORY_MESSAGE_COLLAPSE_MIN_LINES = 12;

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

function isLongHistoryMessageText(text: string) {
  return (
    text.length >= HISTORY_MESSAGE_COLLAPSE_MIN_CHARS ||
    text.split(/\r?\n/).length >= HISTORY_MESSAGE_COLLAPSE_MIN_LINES
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
  const issueEvent = parseIssueSystemEvent(message);
  // Channel↔project association events (#610): bind/change/unbind, projected into
  // a localized row whose sole clickable object is the project name.
  const projectEvent = parseProjectSystemEvent(message);
  // Older backflow rows without the `system_event` part still carry an anchored
  // `reference`, so project those into tokens rather than the raw string (#469).
  const hasReferenceParts = message.parts?.some((part) => part.type === "reference") ?? false;
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
      title={issueEvent ? undefined : messageTime.full(message.created_at)}
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
        {issueEvent ? (
          <IssueSystemEventContent event={issueEvent} sourceMessageId={message.id} />
        ) : memberEvent ? (
          <MemberSystemEventContent event={memberEvent} />
        ) : projectEvent ? (
          <ProjectSystemEventContent event={projectEvent} />
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
      {issueEvent ? (
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
 * Inline single-message editor. Enter (without Shift) saves, Escape cancels —
 * a save calls back into the bubble's onEdit (a PATCH), never a re-send, so an
 * edit can never produce a new agent wake (H5).
 */
function MessageInlineEditor({
  value,
  onChange,
  onSave,
  onCancel,
  editLabel,
  saveLabel,
  cancelLabel,
}: {
  value: string;
  onChange: (next: string) => void;
  onSave: () => void;
  onCancel: () => void;
  editLabel: string;
  saveLabel: string;
  cancelLabel: string;
}) {
  // Move focus into the editor the user just opened (the Edit trigger it
  // replaced has unmounted). A stable ref callback focuses once on mount —
  // no autoFocus prop, no effect.
  const focusOnMount = useCallback((node: HTMLTextAreaElement | null) => {
    node?.focus();
  }, []);
  return (
    <div data-testid="message-editor" className="mt-0.5">
      <textarea
        ref={focusOnMount}
        aria-label={editLabel}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter" && !event.shiftKey) {
            event.preventDefault();
            onSave();
          } else if (event.key === "Escape") {
            event.preventDefault();
            onCancel();
          }
        }}
        rows={2}
        className="w-full resize-none rounded-md border border-input bg-card px-2 py-1.5 text-sm leading-6 text-ink outline-none focus-visible:ring-1 focus-visible:ring-ring"
      />
      <div className="mt-1.5 flex items-center gap-2">
        <button
          type="button"
          onClick={onSave}
          className="inline-flex h-7 items-center rounded-md bg-primary px-3 text-xs font-medium text-primary-foreground transition-colors hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        >
          {saveLabel}
        </button>
        <button
          type="button"
          onClick={onCancel}
          className="inline-flex h-7 items-center rounded-md px-3 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        >
          {cancelLabel}
        </button>
      </div>
    </div>
  );
}

/**
 * One message in the shared Channel/DM/Thread timeline. Ordinary text renders
 * as an IM-style message item, while quote/attachment/code-like content keeps
 * local structure inside the shared Markdown pipeline.
 */
export function ChannelMessageBubble({
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
  searchHighlighted = false,
  searchQuery,
  collapseLongContent = false,
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
  /** Opens the side agent file/public-info panel for agent-authored messages. */
  onOpenAgent?: (agentId: string) => void;
  /** Search hit: marks matching visible text while search is open. */
  searchHighlighted?: boolean;
  /** Trimmed conversation search phrase to mark inside this hit's visible text. */
  searchQuery?: string;
  /** Visually clamp already-read long history while keeping the full DOM/copy payload intact. */
  collapseLongContent?: boolean;
}) {
  const { t } = useT("channels");
  const { getActorName, getActorAvatarUrl } = useActorName();
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
  const [expandedContentKey, setExpandedContentKey] = useState<string | null>(null);
  const [mobileThreadTapActive, setMobileThreadTapActive] = useState(false);
  const longPressTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const tapFeedbackTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const mobileActionsDialogRef = useRef<HTMLDialogElement | null>(null);
  const mobileReactionDialogRef = useRef<HTMLDialogElement | null>(null);
  const touchStartRef = useRef<{ x: number; y: number } | null>(null);
  const touchCancelledRef = useRef(false);


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
  // Avatar resolution (LRM-202 / LRM-218 / LRM-221): payload → sticky
  // same-author cache → actor directory (same source as the profile card).
  // WS upserts also preserve URLs in `withPreservedAuthorAvatar` so consecutive
  // bubbles don't regress to glyph placeholders when a publish path omits
  // `author_avatar_url` (Frank: realtime payloads need not include the face).
  const directoryActorType =
    message.type === "agent" ? "agent" : message.type === "user" ? "member" : null;
  const directoryAvatarUrl =
    directoryActorType && message.author_id
      ? getActorAvatarUrl(directoryActorType, message.author_id)
      : null;
  const avatarUrl = resolveCachedAuthorAvatarUrl(message, directoryAvatarUrl);
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
  // The 2px baseline nudge (`mt-0.5`) lives on the outer wrapper, not the
  // avatar itself, so that when the avatar sits inside the fixed-size presence
  // box the box hugs the avatar exactly (a margin on the inner avatar would
  // overflow the box and lift the dot off the avatar's bottom edge).
  const avatarSeed = authorAvatarCacheKey(message) ?? displayName;
  const avatarNode = (
    <ActorAvatar
      name={displayName}
      initials={avatarGlyph(displayName)}
      avatarUrl={avatarUrl}
      isAgent={isAgent}
      isSystem={false}
      size={28}
      toneSeed={avatarSeed}
      className="select-none"
    />
  );
  // Message rows carry NO live presence dot. A message is history, so pinning
  // "online right now" onto a historical row is both the noisiest column in the
  // view (Frank's screenshot) and semantically wrong (#477 principle: "presence
  // 每视图只一次、且不进消息历史" — Parker/Iris). Live presence lives on directory
  // surfaces (sidebar / member list) and the header status word, not the stream.
  const avatar = <span className="mt-0.5 inline-flex shrink-0">{avatarNode}</span>;
  const nameLabel = (
    <span className="truncate font-bold text-ink">{displayName}</span>
  );

  const canOpenThread = !!onOpenThread && !message.thread_root_message_id;
  const threadReplyCount = message.thread_reply_count ?? 0;
  const threadUnreadCount = message.thread_unread_count ?? 0;
  const hasThreadActivity = threadReplyCount > 0 || threadUnreadCount > 0;
  const hasFeedback = (message.reactions?.length ?? 0) > 0 || hasThreadActivity;
  const quickReactionEmojis = ["👍", "👎", "😄", "🎉", "😕", "❤️", "🚀", "👀"];
  // Resolved once here for copy; `MessageBody` resolves the same way to render
  // the body (see resolveMessageParts for the envelope-unwrap rationale).
  // Non-null for real parts / historical envelopes, null for ordinary content.
  const effectiveParts = resolveMessageParts(message.content, message.parts);
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
  const canDelete = isOwn && !!onDelete;
  const isEdited = !!message.edited_at;
  const collapseText =
    projectReferencesToText(message.content, message.parts, resolveMentionPreview) ??
    formatMessagePartsCopyText(effectiveParts) ??
    unwrapStructuredPreviewContent(message.content) ??
    message.content;
  const contentCollapseKey = `${message.id}:${collapseLongContent ? "collapsed" : "open"}`;
  const canCollapseContent = collapseLongContent && isLongHistoryMessageText(collapseText);
  const isContentCollapsed = canCollapseContent && expandedContentKey !== contentCollapseKey;
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
      onOpenAgent?.(message.author_id);
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
    touchStartRef.current = { x: event.clientX, y: event.clientY };
    clearLongPressTimer();
    longPressTimerRef.current = setTimeout(() => {
      longPressTimerRef.current = null;
      touchCancelledRef.current = true;
      if (!hasActiveTextSelection()) openMobileActions();
    }, LONG_PRESS_MS);
  };
  const handlePointerMove = (event: PointerEvent<HTMLDivElement>) => {
    const start = touchStartRef.current;
    if (!start) return;
    const moved = Math.hypot(event.clientX - start.x, event.clientY - start.y);
    if (moved > TOUCH_MOVE_CANCEL_PX) {
      touchCancelledRef.current = true;
      clearLongPressTimer();
    }
  };
  const handlePointerEnd = (event: PointerEvent<HTMLDivElement>) => {
    const wasTouch = event.pointerType === "touch" && touchStartRef.current;
    touchStartRef.current = null;
    clearLongPressTimer();

    if (!wasTouch || touchCancelledRef.current || !isMobileActionViewport()) {
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

  const bubble = (
    <div
      id={`message-${message.id}`}
      data-testid="message-bubble"
      data-own={isOwn}
      data-self-mentioned={selfMentioned ? "true" : undefined}
      className={cn(
        // Coarse pointers get a dedicated 44px action column (Parker #568:
        // an absolutely-positioned More button compensated by body padding
        // was a layout hack that still clipped link/mention/attachment
        // hitboxes in the first line) — content stays in its own track so
        // nothing overlaps regardless of grouped-message author-row state.
        "group relative grid grid-cols-[28px_minmax(0,1fr)] gap-2.5 rounded-lg px-2 py-1.5 outline-none transition-colors duration-1000 hover:bg-muted/35 focus-within:bg-muted/35 [@media(pointer:coarse)]:grid-cols-[28px_minmax(0,1fr)_44px]",
        selfMentioned && SELF_MENTION_ROW_CLASS,
        highlighted && "bg-primary/10 ring-1 ring-primary/25 duration-0 hover:bg-primary/10 focus-within:bg-primary/10",
        mobileThreadTapActive && "bg-primary/[0.04] ring-1 ring-primary/45 duration-75",
      )}
      onPointerDown={handlePointerDown}
      onPointerMove={handlePointerMove}
      onPointerUp={handlePointerEnd}
      onPointerCancel={cancelTouchGesture}
      onPointerLeave={cancelTouchGesture}
    >
      {profileActorType && profileActorId ? (
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
        <div className="mb-0.5 flex select-none items-baseline gap-2 pr-40 text-sm md:pr-24">
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
          {isAgent && (
            <span className="shrink-0 rounded-full border border-primary/20 bg-primary/[0.08] px-2 py-0.5 text-[11px] font-normal leading-none text-primary">
              {t(($) => $.message.agent_badge)}
            </span>
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
        {!isEditing && (
          <div
            data-testid="message-action-bar"
            data-message-action-surface="true"
            className="pointer-events-none absolute right-3 top-2 z-10 hidden items-center gap-0.5 text-muted-foreground opacity-0 transition-opacity [@media(pointer:fine)]:flex [@media(pointer:fine)]:group-hover:pointer-events-auto [@media(pointer:fine)]:group-hover:opacity-100 [@media(pointer:fine)]:group-focus-within:pointer-events-auto [@media(pointer:fine)]:group-focus-within:opacity-100"
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
            className={cn(
              "message-surface relative min-w-0 max-w-full select-text break-words [overflow-wrap:anywhere] text-sm leading-6 text-ink",
              isContentCollapsed && "overflow-hidden",
              isContentCollapsed ? HISTORY_MESSAGE_COLLAPSE_HEIGHT_CLASS : "overflow-visible",
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
            />
            {isContentCollapsed && (
              <div
                className="pointer-events-none absolute inset-x-0 bottom-0 flex justify-center bg-gradient-to-t from-background via-background/95 to-transparent pb-1.5 pt-12"
                data-testid="message-collapse-fade"
              >
                <button
                  type="button"
                  className="pointer-events-auto inline-flex min-h-11 items-center rounded-full border border-border bg-background px-3 text-xs font-medium text-foreground shadow-sm transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring md:min-h-8"
                  onClick={() => setExpandedContentKey(contentCollapseKey)}
                >
                  {t(($) => $.message.expand_action)}
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
                getActorName={getActorName}
                hideAddButton
                showQuickReactions={false}
              />
            )}
          </div>
        )}
      </div>
      {!isEditing && (onReact || onQuote || canOpenThread) && (
        <button
          type="button"
          data-testid="message-mobile-more-trigger"
          data-message-action-surface="true"
          onClick={openMobileActions}
          aria-label={t(($) => $.message.more_actions)}
          title={t(($) => $.message.more_actions)}
          className="hidden h-11 w-11 shrink-0 items-center justify-center self-start justify-self-end rounded-full text-muted-foreground/50 transition-colors [@media(pointer:coarse)]:flex hover:bg-background/70 hover:text-foreground active:bg-background/70"
        >
          <MoreHorizontal className="size-[18px]" />
        </button>
      )}
    </div>
  );

  if (!onQuote || isEditing) return bubble;

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
}
