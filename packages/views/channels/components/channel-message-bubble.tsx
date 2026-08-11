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
import { Copy, Loader2, MessageSquare, Pencil, Quote, Trash2, SmilePlus } from "lucide-react";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
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
import { ActorStyledName } from "../../common/actor-styled-name";
import type { ChannelMessage } from "@multica/core/types";
import type { OpenAgentPanelFn } from "@multica/core/agents";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { InlineReferenceContent } from "../../common/inline-reference-content";
import { useT } from "../../i18n/use-t";
import { useMessageTime } from "../../i18n/use-message-time";
import { Time } from "../../i18n/time";
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
import { useMessageContentExpanded } from "../hooks/use-message-content-expanded";
import { ThreadReplyPreview } from "./thread-reply-preview";
import { MessageQuoteCard } from "./message-quote";
import { isLegacyRuntimeSystemNotice } from "./runtime-system-notice";
import {
  parseMemberSystemEvent,
  parseIssueSystemEvent,
  parseIssueAggregateSystemEvent,
  parseProjectSystemEvent,
  parseThreadSystemEvent,
} from "./channel-system-event";
import {
  MemberSystemEventContent,
  IssueSystemEventContent,
  IssueAggregateSystemEventContent,
  ProjectSystemEventContent,
  ThreadSystemEventContent,
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
/** LRM-268, widened ~2x by LRM-750 — Slack collapsed body height. */
export const MESSAGE_COLLAPSE_MAX_HEIGHT_PX = 320;
/** Fade overlay height — light bottom wash only (LRM-302); must not center-cover text. */
export const MESSAGE_COLLAPSE_FADE_HEIGHT_PX = 40;
const MESSAGE_COLLAPSE_HEIGHT_CLASS = "max-h-[320px]";
const MESSAGE_COLLAPSE_OVERFLOW_EPSILON_PX = 2;

/**
 * LRM-1227 — bubble shell, frozen in LRM-1233 as density ① + granularity G.
 *
 * Surface matches the conversation pane (`bg-background`) — Frank 2026-08-04
 * kicked back D1 `bg-muted` (#f6f6f4 grey slab).
 *
 * LRM-1346 (Frank lock ①, 2026-08-04): the shell carries **no visible edge** —
 * the 1px `--line` frame and its hover / `focus-within` `--line-strong`
 * strengthening are gone, together with the per-segment group outline (see
 * below). A border cannot be "strengthened" once it does not exist, so the
 * fine-pointer hover affordance is the action bar alone. The floating action
 * bar keeps its own `border` / `bg-popover` / `shadow-sm`, and deep-link
 * `highlighted` keeps its `ring` — neither is a bubble edge.
 *
 * Density ① is "keep the live rhythm": the shell adds no vertical padding, only
 * the 4px horizontal inset the frozen 660px body width accounts for.
 */
const MESSAGE_SHELL_CLASS = "px-1";

/**
 * LRM-1331 — hover action bar + geometry reserves only on fine pointer AND
 * ≥640px. Narrow fine windows fall back to the coarse long-press / context
 * menu path (360px + 162px reserve left ~9 CJK chars — not worth it).
 *
 * LRM-1360: the gate MUST be written as a literal class on every candidate —
 * `[@media(pointer:fine)_and_(min-width:640px)]:…`. Tailwind v4 extracts
 * candidates by statically scanning source text, so a template-interpolated
 * variant (gate constant + ":group-hover:opacity-100") yields no CSS at all and
 * the bar stays `opacity-0 pointer-events-none` forever — that is exactly how the
 * thread entry became invisible on desktop. `className` unit assertions cannot
 * catch it (the class string is still on the node); the guard test
 * `channel-message-bubble-static-gate.test.ts` does.
 */

/**
 * LRM-1227/G drew a joined group as three bordered segments (head `border-x
 * border-t` + `rounded-t-lg`, middle `border-x`, tail `border-x border-b` +
 * `rounded-b-lg`), because every message is its own virtual row
 * (`channel-message-list`, Virtuoso) and no DOM node wraps a group.
 *
 * LRM-1346 removed all three segments' visible edges (Frank lock ①), so there
 * is no per-segment class left to compute — grouping now shows up only in the
 * `data-group-start` / `data-group-end` attributes and in the LRM-1331 row
 * geometry (author-row reserve vs. continuation float). The corner radii went
 * with the borders: with the shell painted in the pane's own `bg-background`
 * they rounded nothing visible.
 */

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
  // LRM-540: thread unfollow/follow — structured actor token (display_name),
  // never the BE `@handle unfollowed this thread` fallback content.
  const threadEvent = parseThreadSystemEvent(message);
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
        // LRM-561 / LRM-564 lock: left-align into the message body column
        // (avatar 28 + gap ≈ pl-10), no side hairlines, no full-row capsule.
        // LRM-609 SoT A': no ClipboardList; issue refs are unfilled brand text
        // links (title-primary) — not filled ▶ chips / LRM keys.
        "group flex items-baseline px-2 py-1 pl-10 outline-none transition-colors duration-1000",
        "text-xs leading-snug text-muted-foreground/85",
        "[&_.mention]:bg-transparent [&_.mention]:px-0 [&_.mention]:font-medium [&_.mention]:text-inherit [&_.mention]:hover:bg-transparent [&_.mention]:focus-visible:bg-transparent",
        "[&_a:not([data-system-issue-chip])]:text-inherit [&_a:not([data-system-issue-chip])]:font-medium [&_a:not([data-system-issue-chip])]:no-underline hover:[&_a:not([data-system-issue-chip])]:underline",
        highlighted && "rounded-md bg-primary/10 ring-1 ring-primary/25 duration-0",
      )}
    >
      <div className="flex min-w-0 flex-1 flex-wrap items-baseline justify-start gap-x-1.5 gap-y-0.5 text-left">
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
          ) : threadEvent ? (
            <ThreadSystemEventContent event={threadEvent} />
          ) : hasReferenceParts ? (
            // Spans are anchored to the RAW `message.content`; feeding the trimmed
            // `systemText` would shift every offset and misplace the tokens.
            <InlineReferenceContent
              content={message.content}
              parts={message.parts}
              sourceMessageId={message.id}
              issueAppearance="systemChip"
            />
          ) : (
            systemText
          )}
        </span>
        {showIssueTime ? (
          // Simple, always-visible time — "· 10:16" (item #7口径), the bucketed
          // inline clock the rest of the list uses, never a full timestamp.
          <span className="shrink-0 whitespace-nowrap tabular-nums text-muted-foreground/60">
            {"· "}
            <Time kind="message" value={message.created_at} title={false} />
          </span>
        ) : (
          <span
            aria-hidden
            className="shrink-0 whitespace-nowrap tabular-nums text-muted-foreground/50 opacity-0 transition-opacity duration-150 group-hover:opacity-100"
          >
            <Time kind="full" value={message.created_at} title={false} />
          </span>
        )}
      </div>
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
  onOpenAgent,
  onOpenMember,
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
  groupEnd = true,
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
  /** Opens the side agent file/public-info panel for agent-authored messages
   *  (LRM-292: agentId + optional row identity snapshot). */
  onOpenAgent?: OpenAgentPanelFn;
  /** Opens the LRM-619 member Profile dock for human-authored messages —
   *  click parity with agent avatars; without it a member avatar only shows
   *  the hover card and click is dead. */
  onOpenMember?: (userId: string) => void;
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
  /**
   * LRM-1227 / LRM-1233 **G**: true when this row is the last of its visual
   * group, i.e. it draws the joined shell's bottom edge + bottom corners.
   * Defaults to true so a standalone bubble renders a fully enclosed shell.
   */
  groupEnd?: boolean;
// LRM-268 adds contentExpanded/contentOverflows; mobileOverlay already uses a
// union instead of three booleans (#568). Full useReducer consolidation is a
// separate refactor of this ~1100-line component — suppress to unblock CI.
// react-doctor-disable-next-line react-doctor/prefer-useReducer
}) {
  const { t } = useT("channels");
  const {
    getActorName,
    getMemberHonor,
    getAgentFleetRank,
    getAgentHonorLevel,
  } = useActorName();
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
  // LRM-987: Map-backed so Virtuoso remount-after-expand does not flash-collapse.
  const collapseIdentity = `${message.id}\0${message.content ?? ""}\0${message.parts?.length ?? 0}\0${message.attachments?.length ?? 0}`;
  const { contentExpanded, expand, collapse } = useMessageContentExpanded(
    message.id,
    collapseIdentity,
  );
  const [contentOverflows, setContentOverflows] = useState(false);
  const [mobileThreadTapActive, setMobileThreadTapActive] = useState(false);
  // #1276 INV-3: a slow in-flight send must not stay silent (looking exactly like
  // a delivered message). ACK within ~1s stays silent (LRM-271/273 no-flash); once
  // pending crosses 1.0s — the point a user starts to doubt it landed — reveal a
  // low-emphasis "Sending…" (updated in place through the failed terminal so the
  // a11y live region announces the transition). Must sit above the early return
  // below (rules-of-hooks), so it reads local_send_status off the message directly.
  const [sendingRevealed, setSendingRevealed] = useState(false);
  const isPendingSend = (message.local_send_status ?? null) === "pending";
  useEffect(() => {
    if (!isPendingSend) {
      setSendingRevealed(false);
      return;
    }
    const id = setTimeout(() => setSendingRevealed(true), 1000);
    return () => clearTimeout(id);
  }, [isPendingSend]);
  const longPressTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const tapFeedbackTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const mobileActionsDialogRef = useRef<HTMLDialogElement | null>(null);
  const mobileReactionDialogRef = useRef<HTMLDialogElement | null>(null);
  const bubbleRef = useRef<HTMLDivElement | null>(null);
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

  // LRM-1174 (LRM-1173 freeze B): dismissing a top-layer <dialog> leaves
  // activeElement on <body>, so the next keyboard/AT step restarts from the top
  // of the list instead of the message the sheet belonged to. Hand focus back to
  // the bubble that opened it. Only a real dismissal (→ "none") returns focus:
  // the actions → reaction handover (#568) keeps focus inside the overlay stack.
  const mobileOverlayWasOpenRef = useRef(false);
  useEffect(() => {
    const open = mobileOverlay !== "none";
    const wasOpen = mobileOverlayWasOpenRef.current;
    mobileOverlayWasOpenRef.current = open;
    if (!wasOpen || open) return;
    // preventScroll: the bubble is already on screen (it was just long-pressed);
    // a Virtuoso row must not be yanked into view by the focus call.
    bubbleRef.current?.focus({ preventScroll: true });
  }, [mobileOverlay]);

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

  // #689: no per-row ResizeObserver here. It used to exist to catch content
  // growing the body past the collapse threshold after the useLayoutEffect
  // above (which only depends on message.attachments/parts — metadata, not
  // asset bytes) already ran, and re-apply the collapse cap. Two real late-
  // growth paths still exist (Wren's #1146 review, not eliminated, accepted
  // as a tradeoff): a markdown inline `![]()` image resolves through the
  // `kind: "url"` AttachmentInput (markdown.tsx renderImage →
  // attachment.tsx), which never forwards a resolved record's width/height
  // into the aspect-ratio box even when one exists, so it can grow on load;
  // and a sticker's placeholder (`min-h-20`, message-parts-renderer.tsx
  // StickerPlaceholder) sits at a different height than its loaded fixed
  // `size-32/40` box (StickerImage) — the swap depends on the sticker-
  // catalog query settling, which isn't in this effect's deps either. Both
  // are UPLOADED-attachment-only reservations (attachment.tsx
  // ImageAttachmentView aspect-ratio from record width/height): a normal
  // record-backed image/attachment paints at final size and never needed
  // this observer to begin with. The accepted cost of removing it is
  // cosmetic-only for the two paths above — a message may render slightly
  // taller than the cap without the "see more" affordance until the next
  // window resize or content-prop change re-measures it — never a wrong
  // clipped height or a stuck state. Virtuoso's own per-item ResizeObserver
  // still re-settles the surrounding rows regardless of what this component
  // does; keeping this observer double-fires that re-settle on every one of
  // these late-growth events, and doing so while the row is on/near screen
  // during a scroll is exactly the mid-scroll jank #689 reported.
  useEffect(() => {
    const handleOverflow = () => measureContentOverflowRef.current();
    window.addEventListener("resize", handleOverflow);
    return () => window.removeEventListener("resize", handleOverflow);
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
  // LRM-270 (Slack align): message author row — name + time only.
  // No Owner/Admin chrome (Slack has none); no Agent/APP type pill.
  // Feishu functional badge stays. Member-list muted role is unchanged.
  const displayName = resolveChannelAuthorDisplayName(message, {
    currentUserId,
    ownName,
    getActorName,
  });
  const profileActorType =
    message.type === "agent"
      ? "agent"
      : message.type === "user"
        ? "user"
        : null;
  const profileActorId = profileActorType ? message.author_id : null;
  // LRM-224 / LRM-223 option B: identity-first Avatar. Appearance belongs to
  // the live User/Agent Profile, never to the Message read model. Chat `user`
  // maps to directory `member`. Agent status dots are in-scope for bubbles.
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
  const authorFleet =
    isAgent && message.author_id ? getAgentFleetRank(message.author_id) : undefined;
  const authorHonorLevel =
    isAgent && message.author_id ? getAgentHonorLevel(message.author_id) : undefined;
  const avatarNode =
    identityActorType && message.author_id ? (
      <ActorAvatar
        actorType={identityActorType}
        actorId={message.author_id}
        size={28}
        className="select-none"
        name={displayName}
        showStatusDot={isAgent || message.type === "user"}
        showXpBurst={isAgent}
        fleetRank={authorFleet?.fleet_rank}
        profileLink={false}
      />
    ) : null;
  const avatar = avatarNode ? (
    <span className="mt-0.5 inline-flex shrink-0">{avatarNode}</span>
  ) : null;
  const authorHonor =
    message.type === "user" && message.author_id ? getMemberHonor(message.author_id) : undefined;
  // LRM-1126: author name never wraps; role/desc truncates first, time stays
  // shrink-0. Hover action bar occupies a reserved ~184px fine-pointer gutter.
  const nameLabel = (
    <ActorStyledName
      displayName={displayName}
      honor={authorHonor}
      agentHonorLevel={authorHonorLevel}
      className="shrink-0 text-[13.5px] font-semibold text-foreground"
      nameClassName="whitespace-nowrap"
    />
  );

  const localSendStatus = message.local_send_status ?? null;
  const isLocalPending = localSendStatus === "pending";
  const isLocalFailed = localSendStatus === "failed";
  const isLocalSend = isLocalPending || isLocalFailed;
  const canOpenThread = !!onOpenThread && !message.thread_root_message_id && !isLocalSend;
  const threadReplyCount = message.thread_reply_count ?? 0;
  const threadUnreadCount = message.thread_unread_count ?? 0;
  const hasThreadActivity = threadReplyCount > 0 || threadUnreadCount > 0;
  const hasReactions = (message.reactions?.length ?? 0) > 0;
  const hasFeedback = hasReactions;
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
      showErrorToast(t(($) => $.message.copy_failed_toast));
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
  const handleOpenAgent = () => {
    if (isAgent && message.author_id) {
      onOpenAgent?.(message.author_id);
    }
  };
  const handleOpenAgentCapture = isAgent && onOpenAgent ? handleOpenAgent : undefined;
  const handleOpenMember = () => {
    if (message.type === "user" && message.author_id) {
      onOpenMember?.(message.author_id);
    }
  };
  const handleOpenMemberCapture =
    message.type === "user" && onOpenMember ? handleOpenMember : undefined;
  const handleOpenProfileCapture = handleOpenAgentCapture ?? handleOpenMemberCapture;
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
  const groupStart = !compact;
  // LRM-1233: the self-mention wash must *replace* the shell fill, not sit under
  // it — same for the deep-link highlight and the mobile thread-tap flash. Those
  // washes live on the row, so the shell drops its own surface and keeps only its
  // edges, letting the row colour through the group's silhouette.
  const shellFilled = !selfMentioned && !highlighted && !mobileThreadTapActive;

  const bubble = (
    <div
      id={`message-${message.id}`}
      ref={bubbleRef}
      // Programmatic focus target only (never tab-reachable): the mobile sheets
      // return focus here on dismissal (LRM-1174).
      tabIndex={-1}
      data-testid="message-bubble"
      data-message-group={compact ? "compact" : "lead"}
      data-message-id={message.id}
      data-message-author={displayName}
      data-own={isOwn}
      data-self-mentioned={selfMentioned ? "true" : undefined}
      data-local-send={localSendStatus ?? undefined}
      className={cn(
        // LRM-495: no permanent mobile ⋯ column — coarse pointers open actions
        // via long-press / left-swipe; fine pointers keep the hover action bar.
        // LRM-1227 moved the hover signal off the row onto the shell edge;
        // LRM-1346 then deleted that edge (lock ①), so on fine pointers the
        // hover affordance is the floating action bar — the row still must not
        // reintroduce a wash. Selected / highlighted / self-mention states below
        // keep their own backgrounds.
        "group relative grid grid-cols-[28px_minmax(0,1fr)] gap-2.5 rounded-lg px-2 outline-none transition-colors duration-1000",
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
          <Time kind="clock" value={message.created_at} title={false} />
        </span>
      ) : profileActorType && profileActorId ? (
        <ActorProfileTrigger
          memberType={profileActorType}
          memberId={profileActorId}
          side="top"
          sideOffset={8}
          onClickCapture={handleOpenProfileCapture}
        >
          {avatar}
        </ActorProfileTrigger>
      ) : (
        avatar
      )}
      {/* LRM-400: fill the conversation column — a 760px cap left a wide empty
          right band that still read as Frank's "半屏空白" after the PanelGroup
          shell fix (#1154). Soft wrap stays on `.message-surface`.
          LRM-1331: drop the shell-wide fine-pointer `pr-[136px]` gutter (it ate
          10–47% of body width and still failed to clear the 154px bar). Reserves
          move to the author row / continuation float / leading-card inset below.
          LRM-1227/G: this element is also the bubble shell — see
          MESSAGE_SHELL_CLASS. LRM-1346 took its visible border away (lock ①);
          the group segments survive only as data attributes. */}
      <div
        data-testid="message-shell"
        data-group-start={groupStart ? "true" : undefined}
        data-group-end={groupEnd ? "true" : undefined}
        className={cn(
          "min-w-0 max-w-full",
          MESSAGE_SHELL_CLASS,
          // Same token as the message pane — no muted grey slab (LRM-1227 kickback).
          shellFilled && "bg-background",
        )}
      >
        {showAuthor && (
          <div
            data-testid="message-author-row"
            className={cn(
              "mb-0.5 flex min-w-0 select-none items-center gap-1.5 text-[13.5px]",
              // LRM-1331 §2: reserve only on the author line (162 = 154 bar + 8).
              // LRM-1360: literal gate class — see the note on the gate above.
              "[@media(pointer:fine)_and_(min-width:640px)]:pr-[162px]",
            )}
          >
            {profileActorType && profileActorId ? (
              <ActorProfileTrigger
                memberType={profileActorType}
                memberId={profileActorId}
                side="top"
                sideOffset={8}
                className="self-center"
                onClickCapture={handleOpenProfileCapture}
              >
                {nameLabel}
              </ActorProfileTrigger>
            ) : (
              nameLabel
            )}
            {isExternal && (
              <span className="shrink-0 rounded-full border border-border/60 bg-transparent px-2 py-0.5 text-[11px] leading-none text-muted-foreground">
                {t(($) => $.message.feishu_badge)}
              </span>
            )}
            <span
              data-testid="message-author-time"
              className="inline-flex h-5 shrink-0 items-center text-[10px] leading-none tabular-nums text-muted-foreground/50"
              title={messageTime.full(message.created_at)}
            >
              <Time kind="message" value={message.created_at} title={false} />
            </span>
            {isEdited && (
              <span
                data-testid="message-edited"
                className="shrink-0 text-[11px] text-muted-foreground/50"
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
              // LRM-1126: solid popover chrome so icons never punch through body
              // text. Overlay — not a document-flow gutter (LRM-1331).
              // LRM-1227/D2 chrome kept; bar measured ~154×34 with 5 keys.
              // LRM-1331: gate on fine+≥640 — narrow fine uses long-press menu.
              // LRM-1360: literal gate classes — interpolated variants produce no
              // CSS, which left the bar (and the thread entry) permanently
              // `opacity-0 pointer-events-none` on desktop.
              "pointer-events-none absolute right-2 z-10 hidden items-center gap-0.5 rounded-lg border border-line-strong bg-popover p-0.5 text-muted-foreground opacity-0 shadow-sm transition-opacity",
              "[@media(pointer:fine)_and_(min-width:640px)]:flex",
              "[@media(pointer:fine)_and_(min-width:640px)]:group-hover:pointer-events-auto",
              "[@media(pointer:fine)_and_(min-width:640px)]:group-hover:opacity-100",
              "[@media(pointer:fine)_and_(min-width:640px)]:group-focus-within:pointer-events-auto",
              "[@media(pointer:fine)_and_(min-width:640px)]:group-focus-within:opacity-100",
              // Lead row: ride the shell's top edge (bar mid-line == shell top
              // line). D2 froze `top-0 -translate-y-1/2` measured from the shell;
              // the bar is positioned against the row, whose `py-1` offsets the
              // shell by 4px — hence `top-1`, same resulting geometry.
              // C2: inside a joined group a compact row has no edge to ride, so
              // the bar sits fully inside its own row and still clears the text.
              compact ? "top-0.5" : "top-1 -translate-y-1/2",
            )}
          >
            {onReact && (
              <QuickEmojiPicker
                onSelect={(emoji) => onReact(message, emoji)}
                align="end"
                side="bottom"
                className="size-7 rounded-md hover:bg-muted hover:text-foreground focus-visible:bg-muted focus-visible:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                ariaLabel={t(($) => $.message.add_reaction)}
                sideOffset={4}
                emojis={quickReactionEmojis}
                showMore={false}
                loadingLabel={t(($) => $.message.loading_emojis)}
                contentClassName="rounded-md border border-border/70 bg-popover/95 shadow-none ring-0"
              />
            )}
            <button
              type="button"
              onClick={handleCopy}
              className="inline-flex size-7 items-center justify-center rounded-md transition-colors hover:bg-muted hover:text-foreground focus-visible:bg-muted focus-visible:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              aria-label={t(($) => $.message.copy_action)}
              title={t(($) => $.message.copy_action)}
            >
              <Copy className="size-4" />
            </button>
            {onQuote && (
              <button
                type="button"
                onClick={handleQuote}
                className="inline-flex size-7 items-center justify-center rounded-md transition-colors hover:bg-muted hover:text-foreground focus-visible:bg-muted focus-visible:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
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
                className="inline-flex size-7 items-center justify-center rounded-md transition-colors hover:bg-muted hover:text-foreground focus-visible:bg-muted focus-visible:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
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
                className="inline-flex size-7 items-center justify-center rounded-md transition-colors hover:bg-muted hover:text-foreground focus-visible:bg-muted focus-visible:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                aria-label={t(($) => $.message.edit_action)}
                title={t(($) => $.message.edit_action)}
              >
                <Pencil className="size-4" />
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
              "message-surface relative min-w-0 max-w-full select-text break-words [overflow-wrap:anywhere] text-[13.5px] leading-6 text-foreground",
              isContentCollapsed && "overflow-hidden",
              isContentCollapsed ? MESSAGE_COLLAPSE_HEIGHT_CLASS : "overflow-visible",
              searchHighlighted && "rounded-md bg-primary/5",
              // LRM-1331 §3: compact continuations — first-line float safety zone
              // (36×158) instead of shell padding. Skip when a leading card owns
              // its own inset (§4); block boxes slide under floats.
              compact &&
                !(message.quote || message.quote_message_id) &&
                "[@media(pointer:fine)_and_(min-width:640px)]:before:float-right [@media(pointer:fine)_and_(min-width:640px)]:before:h-[36px] [@media(pointer:fine)_and_(min-width:640px)]:before:w-[158px] [@media(pointer:fine)_and_(min-width:640px)]:before:content-['']",
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
                // LRM-1331 §4: leading card inset on compact rows (bar overlays body).
                className={
                  compact
                    ? "[@media(pointer:fine)_and_(min-width:640px)]:pr-[158px]"
                    : undefined
                }
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
              choiceContext={{ channelId: message.channel_id, messageId: message.id }}
            />
            <VoiceMessageAudio
              message={message}
              presentation={voicePresentation}
              highlightQuery={searchHighlighted ? searchQuery : undefined}
            />
            {/* #1276 INV-3: one persistent status live region per local send —
                empty (silent) → "Sending…" (pending ≥1.0s) → "Failed" — updated
                in place so screen readers announce the transition and the body
                is never disguised as a delivered message. */}
            {isLocalSend && (
              <output
                aria-live="polite"
                data-testid="message-send-status"
                className="block"
              >
                {isLocalFailed ? (
                  <span
                    data-testid="message-send-failed"
                    className="mt-1.5 flex flex-wrap items-center gap-2 text-xs text-destructive"
                  >
                    <span>{t(($) => $.message.send_failed)}</span>
                    {onRetrySend && (
                      <button
                        type="button"
                        onClick={() => onRetrySend(message)}
                        className="inline-flex h-7 items-center rounded-md border border-destructive/40 bg-destructive/10 px-2.5 font-medium text-destructive transition-colors hover:bg-destructive/15 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                      >
                        {t(($) => $.message.retry_send)}
                      </button>
                    )}
                  </span>
                ) : sendingRevealed ? (
                  <span
                    data-testid="message-sending"
                    className="mt-1.5 flex items-center gap-1.5 text-xs text-muted-foreground"
                  >
                    <Loader2 className="size-3 animate-spin" aria-hidden />
                    <span>{t(($) => $.message.sending)}</span>
                  </span>
                ) : null}
              </output>
            )}
            {isContentCollapsed && (
              <div
                className={messageCollapseFadeClassName(collapseFadeVariant)}
                data-testid="message-collapse-fade"
              >
                {/* LRM-302: text link, not centered pill — must not cover body. */}
                <button
                  type="button"
                  className="pointer-events-auto inline-flex min-h-8 touch-manipulation items-center px-0 text-sm font-normal text-primary underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  onPointerDown={(event) => event.stopPropagation()}
                  onClick={(event) => {
                    event.stopPropagation();
                    expand();
                    // Virtuoso remeasures after height change (LRM-987).
                    window.requestAnimationFrame(() => {
                      window.dispatchEvent(new Event("resize"));
                    });
                  }}
                >
                  {t(($) => $.message.expand_action)}
                </button>
              </div>
            )}
            {canCollapseContent && !isContentCollapsed && (
              <div className="mt-1 flex justify-start" data-testid="message-collapse-less">
                <button
                  type="button"
                  className="inline-flex min-h-8 touch-manipulation items-center px-0 text-sm font-normal text-primary underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  onPointerDown={(event) => event.stopPropagation()}
                  onClick={(event) => {
                    event.stopPropagation();
                    collapse();
                    window.requestAnimationFrame(() => {
                      window.dispatchEvent(new Event("resize"));
                    });
                  }}
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
            className="fixed inset-0 z-50 m-0 h-dvh max-h-none w-screen max-w-none border-0 bg-transparent p-0 backdrop:bg-black/10"
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
              </div>
            </div>
          </dialog>
        )}
        {!isEditing && onReact && mobileReactionOpen && (
          <dialog
            ref={showMobileReactionDialog}
            className="fixed inset-0 z-50 m-0 h-dvh max-h-none w-screen max-w-none border-0 bg-transparent p-0 backdrop:bg-black/10"
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
            {/* LRM-873: reply count chip replaced by ThreadReplyPreview below. */}
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
        {!isEditing && hasThreadActivity && onOpenThread ? (
          <ThreadReplyPreview message={message} onOpenThread={onOpenThread} />
        ) : null}
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
