"use client";

import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";
import { ArrowLeft, ChevronDown, ChevronUp, FileText, Paperclip, Search, X } from "lucide-react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  channelMessageThreadOptions,
  channelMessagesPageOptions,
  flattenChannelMessagePages,
  enrichChannelMessagesPreservingAvatars,
  channelMessagesFirstItemIndex,
  useEnsureMessageLoaded,
  useMarkChannelThreadRead,
  useMarkChannelRead,
  useSendChannelMessage,
  useSendChannelThreadMessage,
  useAddChannelReaction,
  useRemoveChannelReaction,
  useEditChannelMessage,
  useDeleteChannelMessage,
  useSetChannelThreadFollowed,
  useSetChannelTyping,
  activeChannelTasksOptions,
} from "@multica/core/channels";
import { dmKeys, type DMItem } from "@multica/core/dm";
import { useAuthStore } from "@multica/core/auth";
import { api } from "@multica/core/api";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
import { useWorkspaceId } from "@multica/core/hooks";
import { memberListOptions } from "@multica/core/workspace/queries";
import type {
  AgentPanelIdentitySnapshot,
  OpenAgentPanelFn,
} from "@multica/core/agents";
import { useWSEvent } from "@multica/core/realtime";
import type {
  ChannelMessage,
  ChannelMessageSearchResult,
} from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { toast } from "sonner";
import { ContentEditor, type ContentEditorRef } from "../../editor/content-editor";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { AgentPanelProvider, useOpenAgentPanel } from "../../common/agent-panel-context";
import {
  CHANNEL_DETAIL_SIDE_WIDTH_STORAGE_KEY,
  useProfilePanelWidth,
} from "../../layout/use-profile-panel-width";
import { useT } from "../../i18n/use-t";
import { DmAgentVoiceCall } from "../../voice-calls";
import { composePayloadKey } from "../hooks/use-compose-send-intent";
import { useComposerSend } from "../hooks/use-composer-send";
import {
  buildChatMessageParts,
  useComposerPendingAttachments,
} from "../hooks/use-composer-pending-attachments";
import { useEntryReadCursor } from "../hooks/use-entry-read-cursor";
import { useEntryAnchor } from "../hooks/use-entry-around-seq";
import {
  buildRecordedVoiceMessageParts,
  type VoiceRecordingAttachment,
} from "../lib/voice-audio";
import { prepareVoicePlayback, voicePlaybackScope } from "../lib/voice-playback";
import { ChannelMessageList } from "./channel-message-list";
import { ChannelFilesPanel } from "./channel-files-panel";
import { ResolvedAgentSidePanel } from "../../common/resolved-agent-side-panel";
import { Composer, ConversationHeader } from "./conversation-surface";
import { ComposerAttachmentTray } from "./composer-attachment-tray";
import { ThreadRootPreview } from "./thread-root-preview";
import { ThreadFollowButton } from "./thread-follow-button";
import { ComposerQuotePreview } from "./message-quote";
import type { QuoteTarget } from "./message-quote-types";
import { isConversationMuted, MutedIndicator } from "./conversation-muted";
import { ChannelAgentsLiveCue } from "./channel-agents-live-cue";
import { DmAgentBubble } from "../../chat/components/dm-agent-bubble";

/**
 * DM detail pane. Visible direct messages must use the R2 `dm_channel` stack:
 *  - `dm_channel` — the DM IS a kind='dm' channel, so we reuse the exact
 *    channel conversation stack (ChannelMessageBubble + ContentEditor composer
 *    + channel queries/mutations + channel:message WS).
 *
 * LRM-537: DM composer no longer renders ConversationActivityStrip /
 * ConversationAgentActivityLine (preparing / Thinking / Stop). Status
 * perception redesign is a separate issue.
 *
 * LRM-589: agent DM header keeps the live cue (Thinking / working) only —
 * Stop lives in AgentProfileActions (profile card), not beside the cue.
 * 1:1 has no Stop all.
 *
 * The DM header chrome differs from the group header: peer avatar + name (+
 * agent presence dot) and Files only — no stats, no share, no member
 * management, no delete.
 */
interface DmConversationProps {
  dm: DMItem;
  onBack: () => void;
  draft?: string;
  onDraftChange?: (value: string) => void;
  onDraftClear?: () => void;
  /** ?thread=<rootId> from a Reminder anchor deep-link — opens this DM's thread panel (same shape as channels-page.tsx's group-channel handling). One-shot: consumed on mount only. */
  threadDeepLinkId?: string | null;
  /** ?message=<id> from a Reminder anchor deep-link — highlights this message in the main list, or in the open thread's reply list when threadDeepLinkId is also set. */
  deepLinkMessageId?: string | null;
}

interface ConversationSearchState {
  open: boolean;
  query: string;
  results: ChannelMessageSearchResult[];
  total: number;
  index: number;
}

interface DmChannelState {
  openThreadRoot: ChannelMessage | null;
  threadDraftEmpty: boolean;
  quoteTarget: QuoteTarget | null;
  threadQuoteTarget: QuoteTarget | null;
  convSearch: ConversationSearchState;
  threadParentHighlightId: string | null;
  /** Deep-link target from a Reminder anchor (?message=, routed to the main list or the open thread's reply list depending on whether ?thread= is also present). Distinct from threadParentHighlightId, which is always "jump back to the root in the main list", never a reply. */
  deepLinkHighlightId: string | null;
}

type DmChannelAction =
  | { type: "openThread"; message: ChannelMessage }
  | { type: "closeThread" }
  | { type: "resetForChannel" }
  | { type: "setQuote"; message: QuoteTarget | null }
  | { type: "setThreadQuote"; message: QuoteTarget | null }
  | { type: "setThreadDraftEmpty"; empty: boolean }
  | { type: "setThreadParentHighlightId"; id: string | null }
  | { type: "setDeepLinkHighlightId"; id: string | null }
  | { type: "openSearch" }
  | { type: "closeSearch" }
  | { type: "setSearchQuery"; query: string }
  | { type: "setSearchResults"; query: string; results: ChannelMessageSearchResult[]; total: number }
  | { type: "previousSearchResult" }
  | { type: "nextSearchResult" };

const initialConversationSearchState: ConversationSearchState = {
  open: false,
  query: "",
  results: [],
  total: 0,
  index: 0,
};

const initialDmChannelState: DmChannelState = {
  openThreadRoot: null,
  threadDraftEmpty: true,
  quoteTarget: null,
  threadQuoteTarget: null,
  convSearch: initialConversationSearchState,
  threadParentHighlightId: null,
  deepLinkHighlightId: null,
};

function dmChannelReducer(state: DmChannelState, action: DmChannelAction): DmChannelState {
  switch (action.type) {
    case "openThread":
      return { ...state, openThreadRoot: action.message, threadQuoteTarget: null };
    case "closeThread":
      return { ...state, openThreadRoot: null, threadQuoteTarget: null };
    case "resetForChannel":
      return initialDmChannelState;
    case "setQuote":
      return { ...state, quoteTarget: action.message };
    case "setThreadQuote":
      return { ...state, threadQuoteTarget: action.message };
    case "setThreadDraftEmpty":
      return state.threadDraftEmpty === action.empty ? state : { ...state, threadDraftEmpty: action.empty };
    case "setThreadParentHighlightId":
      return state.threadParentHighlightId === action.id ? state : { ...state, threadParentHighlightId: action.id };
    case "setDeepLinkHighlightId":
      return state.deepLinkHighlightId === action.id ? state : { ...state, deepLinkHighlightId: action.id };
    case "openSearch":
      return { ...state, convSearch: { ...state.convSearch, open: true } };
    case "closeSearch":
      return { ...state, convSearch: initialConversationSearchState };
    case "setSearchQuery": {
      const trimmedQuery = action.query.trim();
      const nextSearch = trimmedQuery
        ? { ...state.convSearch, query: action.query }
        : { ...state.convSearch, query: action.query, results: [], total: 0, index: 0 };
      return { ...state, convSearch: nextSearch };
    }
    case "setSearchResults":
      if (!state.convSearch.open || state.convSearch.query.trim() !== action.query) return state;
      return {
        ...state,
        convSearch: {
          ...state.convSearch,
          results: action.results,
          total: action.total,
          index: 0,
        },
      };
    case "previousSearchResult":
      return {
        ...state,
        convSearch: {
          ...state.convSearch,
          index: Math.max(0, state.convSearch.index - 1),
        },
      };
    case "nextSearchResult":
      return {
        ...state,
        convSearch: {
          ...state.convSearch,
          index: state.convSearch.total === 0
            ? 0
            : Math.min(state.convSearch.total - 1, state.convSearch.index + 1),
        },
      };
  }
}

export function DmConversation({
  dm,
  onBack,
  draft = "",
  onDraftChange,
  onDraftClear,
  threadDeepLinkId,
  deepLinkMessageId,
}: DmConversationProps) {
  return (
    <DmChannelConversation
      dm={dm}
      onBack={onBack}
      draft={draft}
      onDraftChange={onDraftChange}
      onDraftClear={onDraftClear}
      threadDeepLinkId={threadDeepLinkId}
      deepLinkMessageId={deepLinkMessageId}
    />
  );
}

// Shared peer header: avatar (presence dot for agents) + name + optional search + Files popover.
function DmHeader({
  dm,
  onBack,
  filesChannelId,
  onSearchOpen,
  voiceCallAction,
}: {
  dm: DMItem;
  onBack: () => void;
  /** Channel id whose project files to surface. Only dm_channel DMs have one. */
  filesChannelId?: string;
  /** When provided, renders a magnifying-glass button (source gate: dm_channel only). */
  onSearchOpen?: () => void;
  /** Agent-only call control; kept outside the shared header chrome. */
  voiceCallAction?: React.ReactNode;
}) {
  const { t } = useT("channels");
  const isMobile = useIsMobile();
  const openAgentPanel = useOpenAgentPanel();
  const peerId = dm.peer.id;
  const peerType = dm.peer.type;
  const isMuted = isConversationMuted(dm);
  const actorType = peerType === "agent" ? "agent" : "member";
  const memberType = peerType === "agent" ? "agent" : "user";
  const isAgentPeer = peerType === "agent";
  // LRM-581 / LRM-589 — agent DM: live cue beside peer name (status only).
  // Stop is in AgentProfileActions, not outer header chrome. Human peers keep
  // the static "Human" meta.
  const channelIdForTasks = isAgentPeer ? (filesChannelId ?? dm.id) : "";
  const { data: activeTasks = [] } = useQuery(
    activeChannelTasksOptions(channelIdForTasks),
  );
  const agentLiveStatus = isAgentPeer ? (
    <ChannelAgentsLiveCue
      variant="dm"
      agentCount={1}
      tasks={activeTasks}
      canStop={false}
    />
  ) : undefined;
  const meta = isAgentPeer ? undefined : t(($) => $.dm.human_meta);
  const mutedBadge = useMemo(
    () => (isMuted ? <MutedIndicator label={t(($) => $.dm.muted_label)} /> : null),
    [isMuted, t],
  );
  const peerAvatar = (
    <ActorAvatar
      actorType={actorType}
      actorId={peerId}
      // 28px matches the message-row avatar so the header avatar and every
      // message avatar share one size + left edge (see ConversationHeader).
      size={28}
      // LRM-248: avatar badge is the round live indicator; the name-row word
      // is plain Online/Offline text (no second dot).
      showStatusDot
      profileLink={false}
    />
  );
  // #349: for an agent peer, clicking the header avatar/name opens the side
  // panel (same as clicking the avatar in a channel), so the DM header is a
  // panel entry point too — not just the hover-card popover. Human peers keep
  // the popover (no agent side panel exists for them).
  const wrapPeerTrigger = (child: React.ReactNode) =>
    isAgentPeer && openAgentPanel ? (
      <button
        type="button"
        className="inline-flex min-w-0 max-w-full items-center overflow-hidden rounded-md text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        onClick={() => openAgentPanel(peerId)}
      >
        {child}
      </button>
    ) : (
      <ActorProfileTrigger memberType={memberType} memberId={peerId}>
        {child}
      </ActorProfileTrigger>
    );

  // Mobile: put Working under the name (meta line) — long agent names +
  // back/avatar/actions leave too little room for same-row "处理中".
  // Desktop: keep Slack-style cue beside the name (status slot).
  const headerStatus = isMobile ? undefined : agentLiveStatus;
  const headerMeta = isMobile
    ? (agentLiveStatus ?? meta)
    : meta;

  return (
    <ConversationHeader
      isMobile={isMobile}
      leading={
        <>
          {isMobile && (
            <Button
              variant="ghost"
              size="icon"
              className="size-10 shrink-0 text-muted-foreground"
              aria-label={t(($) => $.header.back)}
              onClick={onBack}
            >
              <ArrowLeft className="size-5" />
            </Button>
          )}
          {wrapPeerTrigger(peerAvatar)}
        </>
      }
      title={wrapPeerTrigger(
        <span className="block truncate">{dm.peer.name}</span>,
      )}
      meta={headerMeta}
      // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- ConversationHeader status slot; live cue is not memo-sensitive
      status={headerStatus}
      badges={mutedBadge}
      actions={
        <>
          {voiceCallAction}
          {onSearchOpen && (
            <Button
              variant="ghost"
              size="icon"
              className="size-8"
              aria-label={t(($) => $.conv_search.search_aria)}
              onClick={onSearchOpen}
            >
              <Search className="size-4" />
            </Button>
          )}
          {filesChannelId && (
            <Popover>
              <PopoverTrigger
                className="flex size-8 items-center justify-center rounded-md transition-colors hover:bg-accent"
                aria-label={t(($) => $.dm.files)}
              >
                <FileText className="size-4" />
              </PopoverTrigger>
              <PopoverContent align="end" className="w-80">
                <p className="mb-3 text-sm font-medium">{t(($) => $.dm.files)}</p>
                <ChannelFilesPanel channelId={filesChannelId} />
              </PopoverContent>
            </Popover>
          )}
        </>
      }
    />
  );
}

// ─── dm_channel: reuse the channel conversation stack ──────────────────────

function DmChannelConversation({
  dm,
  onBack,
  draft = "",
  onDraftChange,
  onDraftClear,
  threadDeepLinkId,
  deepLinkMessageId,
}: DmConversationProps) {
  const { t } = useT("channels");
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const isMobile = useIsMobile();
  // LRM-400 — same flex + pixel dock as ChannelsPage (no lone ResizablePanelGroup).
  const {
    width: detailSideWidth,
    onResizePointerDown: onDetailSideResizePointerDown,
  } = useProfilePanelWidth(CHANNEL_DETAIL_SIDE_WIDTH_STORAGE_KEY);
  const channelId = dm.id;
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const currentUserName = useAuthStore((s) => s.user?.name ?? null);
  const [{
    openThreadRoot,
    threadDraftEmpty,
    quoteTarget,
    threadQuoteTarget,
    convSearch,
    threadParentHighlightId,
    deepLinkHighlightId,
  }, dispatch] = useReducer(dmChannelReducer, initialDmChannelState);
  const appliedDeepLinkMessageRef = useRef<string | null>(null);
  const appliedThreadDeepLinkRef = useRef<string | null>(null);
  // #349 agent side panel — same slot as the thread panel (mutually
  // exclusive), matching channels-page.tsx's inline-panel pattern per
  // Frank's direction (replace the slot, don't route away).
  const [selectedAgentPanelId, setSelectedAgentPanelId] = useState<string | null>(null);
  const [selectedAgentPanelSnapshot, setSelectedAgentPanelSnapshot] =
    useState<AgentPanelIdentitySnapshot | null>(null);
  const handleOpenAgentPanel = useCallback<OpenAgentPanelFn>((agentId, snapshot) => {
    dispatch({ type: "closeThread" });
    setSelectedAgentPanelId(agentId);
    setSelectedAgentPanelSnapshot(snapshot ?? null);
  }, []);
  const { data: dmMembers = [] } = useQuery({
    ...memberListOptions(wsId),
    enabled: !!selectedAgentPanelId,
  });
  const setQuoteTarget = useCallback((message: QuoteTarget | null) => {
    dispatch({ type: "setQuote", message });
  }, []);
  const setThreadQuoteTarget = useCallback((message: QuoteTarget | null) => {
    dispatch({ type: "setThreadQuote", message });
  }, []);

  const { mutate: markChannelRead } = useMarkChannelRead();
  const { mutate: markThreadRead } = useMarkChannelThreadRead();
  const sendMessage = useSendChannelMessage();
  const sendThreadMessage = useSendChannelThreadMessage();
  const addChannelReaction = useAddChannelReaction();
  const removeChannelReaction = useRemoveChannelReaction();
  const editChannelMessage = useEditChannelMessage();
  const deleteChannelMessage = useDeleteChannelMessage();
  const setThreadFollowed = useSetChannelThreadFollowed();
  // Edit is a PATCH of an existing message (H5) — it routes through
  // editChannelMessage, never the send path, so it can never produce a new wake.
  // DMs are never archived/closed, so (like onReact) edit/delete are always wired;
  // the bubble still gates the affordance on the message being the viewer's own.
  const handleEditMessage = useCallback((message: ChannelMessage, content: string) => {
    editChannelMessage.mutate(
      { channelId: message.channel_id, messageId: message.id, content },
      { onError: () => toast.error(t(($) => $.message.edit_failed_toast)) },
    );
  }, [editChannelMessage, t]);
  const handleDeleteMessage = useCallback((message: ChannelMessage) => {
    deleteChannelMessage.mutate(
      { channelId: message.channel_id, messageId: message.id },
      { onError: () => toast.error(t(($) => $.message.delete_failed_toast)) },
    );
  }, [deleteChannelMessage, t]);
  const handleReactToMessage = useCallback((message: ChannelMessage, emoji: string) => {
    const hasReacted = message.reactions?.some(
      (reaction) => reaction.actor_type === "member" && reaction.actor_id === currentUserId && reaction.emoji === emoji,
    );
    const vars = { channelId: message.channel_id, messageId: message.id, emoji };
    if (hasReacted) {
      removeChannelReaction.mutate(vars);
    } else {
      addChannelReaction.mutate(vars);
    }
  }, [addChannelReaction, currentUserId, removeChannelReaction]);
  const setTyping = useSetChannelTyping();
  // Throwing `upload` so tray chips show the real API error (LRM-426).
  const { upload } = useFileUpload(api);

  // #340: freeze the entry read cursor + true unread count (sidebar-same source)
  // per DM — anchors the cold load on the unread divider and gives the divider
  // the real "N new" (not the count within the loaded window). See useEntryAnchor.
  const entryAnchor = useEntryAnchor(
    channelId,
    dm.last_read_seq,
    dm.real_unread ?? dm.unread,
  );
  const {
    data: messagePages,
    isLoading: messagesLoading,
    isError: messagesError,
    refetch: refetchMessages,
    fetchNextPage: fetchOlderMessages,
    hasNextPage: hasOlderMessages,
    isFetchingNextPage: isFetchingOlderMessages,
  } = useInfiniteQuery(
    channelMessagesPageOptions(channelId, { aroundSeq: entryAnchor.aroundSeq }),
  );
  const messages = useMemo(() => flattenChannelMessagePages(messagePages), [messagePages]);
  const messagesFirstItemIndex = useMemo(
    () => channelMessagesFirstItemIndex(messagePages, messages.length > 0),
    [messagePages, messages.length],
  );
  const threadRoot = useMemo(
    () =>
      openThreadRoot && openThreadRoot.channel_id === channelId
        ? messages.find((m) => m.id === openThreadRoot.id) ?? openThreadRoot
        : null,
    [channelId, messages, openThreadRoot],
  );
  const { data: threadPage, isLoading: threadLoading, isError: threadError, refetch: refetchThread } = useQuery(
    channelMessageThreadOptions(channelId, threadRoot?.id ?? ""),
  );
  const threadSurfaceRoot = useMemo(
    () => threadPage?.messages.find((message) => message.id === threadRoot?.id) ?? threadRoot,
    [threadPage?.messages, threadRoot],
  );
  const threadReplies = useMemo(
    () => {
      const messages = threadPage?.messages ?? [];
      const filtered = threadRoot ? messages.filter((msg) => msg.id !== threadRoot.id) : messages;
      return enrichChannelMessagesPreservingAvatars(filtered);
    },
    [threadPage?.messages, threadRoot],
  );

  const handleThreadFollowChange = useCallback((followed: boolean) => {
    if (!threadSurfaceRoot) return;
    setThreadFollowed.mutate(
      {
        channelId: threadSurfaceRoot.channel_id,
        messageId: threadSurfaceRoot.id,
        followed,
      },
      {
        onError: () => toast.error(
          t(($) => followed ? $.thread.follow_failed : $.thread.unfollow_failed),
        ),
      },
    );
  }, [setThreadFollowed, t, threadSurfaceRoot]);

  const editorRef = useRef<ContentEditorRef>(null);
  const threadEditorRef = useRef<ContentEditorRef>(null);
  const focusThreadComposerOnOpenRef = useRef(false);
  const draftEmpty = !draft.trim();
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const threadFileInputRef = useRef<HTMLInputElement | null>(null);

  const uploadForDm = useCallback(
    async (file: File) => upload(file, { channelId }),
    [channelId, upload],
  );
  const dmPending = useComposerPendingAttachments({
    upload: uploadForDm,
    resetKey: channelId,
  });
  const threadPending = useComposerPendingAttachments({
    upload: uploadForDm,
    resetKey: openThreadRoot?.id
      ? `${channelId}:${openThreadRoot.id}`
      : channelId,
  });

  const typingStartedRef = useRef(false);
  const typingStopTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const typingPulseTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Send idempotency + send lock (#207) — see use-compose-send-intent. DM
  // top-level and thread reply each own an independent intent.
  const dmSend = useComposerSend();
  const threadSend = useComposerSend();

  // 1-on-1 DM: scope the composer's @-mention picker to the peer only. Without
  // an allowlist the picker defaults to the whole workspace, which is wrong for
  // a 1-on-1 (only the peer is reachable here).
  const mentionAllowedActorIds = useMemo(() => new Set([dm.peer.id]), [dm.peer.id]);

  const searchHitIds = useMemo(
    () =>
      convSearch.open && convSearch.results.length > 0
        ? new Set(convSearch.results.map((r) => r.message_id))
        : undefined,
    [convSearch.open, convSearch.results],
  );
  const searchQuery = convSearch.query.trim();
  const searchHighlightQuery = convSearch.open ? searchQuery : "";
  const searchHighlightId = convSearch.open
    ? (convSearch.results[convSearch.index]?.message_id ?? null)
    : null;
  // deepLinkHighlightId only belongs on the main list when no thread is open
  // — when a thread IS open, the deep-linked id is a REPLY that lives in the
  // thread's own reply list instead (passed to that ChannelMessageList
  // separately below), never the main timeline.
  const highlightMessageId =
    threadParentHighlightId ?? searchHighlightId ?? (openThreadRoot ? null : deepLinkHighlightId);

  // A search hit or "view parent" jump can target a message in an older,
  // not-yet-fetched page. Page older history until it loads (found) or history
  // is exhausted, so the viewport can actually scroll to it instead of the jump
  // silently doing nothing.
  const jumpTargetLoaded = useMemo(
    () => !!highlightMessageId && messages.some((m) => m.id === highlightMessageId),
    [highlightMessageId, messages],
  );
  // `exhausted` (target not anywhere in history) is surfaced declaratively as
  // an inline notice below, so the jump never fails silently.
  const jumpStatus = useEnsureMessageLoaded({
    targetId: highlightMessageId,
    targetLoaded: jumpTargetLoaded,
    hasOlder: !!hasOlderMessages,
    isFetchingOlder: isFetchingOlderMessages,
    fetchOlder: fetchOlderMessages,
  });

  // Debounced in-conversation search.
  useEffect(() => {
    if (!convSearch.open) return;
    if (!searchQuery) return;
    const timer = setTimeout(async () => {
      try {
        const res = await api.searchChannelMessages(channelId, searchQuery);
        dispatch({
          type: "setSearchResults",
          query: searchQuery,
          results: res.results,
          total: res.total,
        });
      } catch {
        toast.error(t(($) => $.conv_search.error));
      }
    }, 300);
    return () => clearTimeout(timer);
  }, [convSearch.open, searchQuery, channelId, t]);

  // Bottom-stick on new messages and open-at-latest on switch are handled by
  // ChannelMessageList (react-virtuoso). No manual scrollIntoView needed.

  // Mark read on open — clears the badge — and expose the pre-advance read
  // cursor from the mark-read response for the race-free "N new messages"
  // divider (#303).
  const dividerLastReadSeq = useEntryReadCursor(channelId, dm.last_read_seq, markChannelRead);

  useEffect(() => {
    dispatch({ type: "resetForChannel" });
  }, [channelId]);

  // Reminder-anchor deep link (?thread=&message=), same shape as
  // channels-page.tsx's group-channel handling. Keyed on the VALUE (not a
  // fired-once boolean): DmConversation remounts fresh when the ACTIVE DM
  // changes (key={source:id} at the call site), but clicking a different
  // Reminder anchor pointing at the SAME DM is a same-pathname AppLink push
  // that does NOT remount this component — a plain one-shot guard would
  // silently ignore that second, different deep link.
  useEffect(() => {
    if (deepLinkMessageId && deepLinkMessageId !== appliedDeepLinkMessageRef.current) {
      appliedDeepLinkMessageRef.current = deepLinkMessageId;
      // react-doctor-disable-next-line react-doctor/no-event-handler -- consumption of an external signal (props sourced from the URL), gated on a ref guard, not a fake event handler; there is no user event to move this into.
      dispatch({ type: "setDeepLinkHighlightId", id: deepLinkMessageId });
    }
    // react-doctor-disable-next-line react-doctor/no-event-handler -- same deep-link consumption as above.
    if (threadDeepLinkId && threadDeepLinkId !== appliedThreadDeepLinkRef.current) {
      appliedThreadDeepLinkRef.current = threadDeepLinkId;
      dispatch({
        type: "openThread",
        message: {
          id: threadDeepLinkId,
          channel_id: channelId,
          workspace_id: wsId,
          seq: 0,
          type: "user",
          author_id: null,
          author_name: "",
          content: "",
          source: "multica",
          external_message_id: null,
          client_message_id: null,
          created_at: new Date(0).toISOString(),
        },
      });
    }
  }, [threadDeepLinkId, deepLinkMessageId, channelId, wsId]);

  useEffect(() => {
    if (!threadRoot) return;
    markThreadRead({ channelId, messageId: threadRoot.id });
  }, [channelId, threadRoot, markThreadRead]);

  useEffect(() => {
    if (!threadRoot || !focusThreadComposerOnOpenRef.current) return;
    focusThreadComposerOnOpenRef.current = false;
    requestAnimationFrame(() => {
      threadEditorRef.current?.focus();
    });
  }, [threadRoot]);

  useEffect(() => {
    typingStartedRef.current = false;
    if (typingStopTimerRef.current) window.clearTimeout(typingStopTimerRef.current);
    if (typingPulseTimerRef.current) window.clearTimeout(typingPulseTimerRef.current);
  }, [channelId]);

  useWSEvent("channel:message", (payload) => {
    const e = payload as { channel_id?: string };
    if (e.channel_id !== channelId) return;
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
    markChannelRead(channelId);
  });

  useWSEvent("task:cancelled", () => {
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
  });

  useWSEvent("task:completed", (payload) => {
    const event = payload as { chat_session_id?: string };
    if (!event.chat_session_id) return;
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
  });

  useWSEvent("task:failed", (payload) => {
    const event = payload as { chat_session_id?: string };
    if (!event.chat_session_id) return;
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
  });

  const publishTyping = (isTyping: boolean) => setTyping.mutate({ channelId, isTyping });

  const scheduleTypingStop = () => {
    if (typingStopTimerRef.current) clearTimeout(typingStopTimerRef.current);
    typingStopTimerRef.current = setTimeout(() => {
      if (typingStartedRef.current) {
        typingStartedRef.current = false;
        publishTyping(false);
      }
    }, 1200);
  };

  const scheduleTypingPulse = () => {
    if (typingPulseTimerRef.current) clearTimeout(typingPulseTimerRef.current);
    typingPulseTimerRef.current = setTimeout(() => {
      if (typingStartedRef.current) {
        publishTyping(true);
        scheduleTypingPulse();
      }
    }, 3500);
  };

  const handleEditorUpdate = (value: string) => {
    onDraftChange?.(value);
    if (value.trim()) {
      if (!typingStartedRef.current) {
        typingStartedRef.current = true;
        publishTyping(true);
        scheduleTypingPulse();
      }
      scheduleTypingStop();
      return;
    }
    if (typingStartedRef.current) {
      typingStartedRef.current = false;
      publishTyping(false);
    }
  };

  const handleThreadEditorUpdate = (value: string) => {
    dispatch({ type: "setThreadDraftEmpty", empty: !value.trim() });
  };

  const handlePickFiles = (files: FileList | null) => {
    if (!files?.length) return;
    dmPending.addFiles(Array.from(files));
  };

  const handlePickThreadFiles = (files: FileList | null) => {
    if (!files?.length) return;
    threadPending.addFiles(Array.from(files));
  };

  const handleSend = () => {
    // Empty-payload early-return before the send lock: after a send succeeds the
    // editor/tray are cleared, so a still-held Enter grabs empty content and stops here.
    if (dmPending.hasUploading) return;
    const content = editorRef.current?.getMarkdown()?.trim() ?? "";
    const parts = buildChatMessageParts(content, dmPending.readyAttachmentParts);
    if (parts.length === 0) return;
    const attachmentIds = dmPending.readyAttachmentParts.map((p) => p.attachment_id);
    // Stop typing before the send lock so a dropped (held-Enter) trigger still
    // clears the indicator.
    const dispatched = dmSend.send({
      payloadKey: composePayloadKey(content, attachmentIds, quoteTarget?.id ?? ""),
      buildVars: (clientMessageId) => ({
        channelId,
        content,
        parts,
        quoteMessageId: quoteTarget?.id ?? undefined,
        clientMessageId,
      }),
      mutate: sendMessage.mutate,
      onCommitted: () => {},
      onVisibleError: (kind) => {
        if (kind === "conflict") toast.error(t(($) => $.composer.send_failed));
      },
    });
    if (dispatched) {
      prepareVoicePlayback(voicePlaybackScope(channelId));
      editorRef.current?.clearContent();
      dmPending.clear();
      setQuoteTarget(null);
      onDraftClear?.();
      if (typingStartedRef.current) {
        typingStartedRef.current = false;
        publishTyping(false);
      }
    }
  };

  const handleVoiceSend = (
    durationMs: number,
    attachment: VoiceRecordingAttachment,
  ): boolean => {
    if (!draftEmpty || dmPending.pending.length > 0) return false;
    const content = "";
    const parts = buildRecordedVoiceMessageParts(durationMs, attachment);
    const dispatched = dmSend.send({
      payloadKey: composePayloadKey(content, [attachment.id], `voice:${quoteTarget?.id ?? ""}`),
      buildVars: (clientMessageId) => ({
        channelId,
        content,
        parts,
        quoteMessageId: quoteTarget?.id ?? undefined,
        clientMessageId,
      }),
      mutate: sendMessage.mutate,
      onCommitted: () => {},
      onVisibleError: (kind) => {
        if (kind === "conflict") toast.error(t(($) => $.composer.send_failed));
      },
    });
    if (dispatched) {
      setQuoteTarget(null);
      onDraftClear?.();
      if (typingStartedRef.current) {
        typingStartedRef.current = false;
        publishTyping(false);
      }
    }
    return dispatched;
  };

  const handleThreadSend = () => {
    if (!threadRoot) return;
    if (threadPending.hasUploading) return;
    const content = threadEditorRef.current?.getMarkdown()?.trim() ?? "";
    const parts = buildChatMessageParts(content, threadPending.readyAttachmentParts);
    if (parts.length === 0) return;
    const attachmentIds = threadPending.readyAttachmentParts.map((p) => p.attachment_id);
    const dispatched = threadSend.send({
      payloadKey: composePayloadKey(
        content,
        attachmentIds,
        `${threadRoot.id}:${threadQuoteTarget?.id ?? ""}`,
      ),
      buildVars: (clientMessageId) => ({
        channelId,
        messageId: threadRoot.id,
        content,
        parts,
        quoteMessageId: threadQuoteTarget?.id ?? undefined,
        clientMessageId,
      }),
      mutate: sendThreadMessage.mutate,
      onCommitted: () => {},
      onVisibleError: (kind) => {
        if (kind === "conflict") toast.error(t(($) => $.thread.send_failed));
      },
    });
    if (dispatched) {
      prepareVoicePlayback(voicePlaybackScope(channelId, threadRoot.id));
      threadEditorRef.current?.clearContent();
      threadPending.clear();
      setThreadQuoteTarget(null);
      dispatch({ type: "setThreadDraftEmpty", empty: true });
    }
  };

  const handleThreadVoiceSend = (
    durationMs: number,
    attachment: VoiceRecordingAttachment,
  ): boolean => {
    if (!threadRoot || !threadDraftEmpty || threadPending.pending.length > 0) return false;
    const content = "";
    const parts = buildRecordedVoiceMessageParts(durationMs, attachment);
    const dispatched = threadSend.send({
      payloadKey: composePayloadKey(
        content,
        [attachment.id],
        `${threadRoot.id}:voice:${threadQuoteTarget?.id ?? ""}`,
      ),
      buildVars: (clientMessageId) => ({
        channelId,
        messageId: threadRoot.id,
        content,
        parts,
        quoteMessageId: threadQuoteTarget?.id ?? undefined,
        clientMessageId,
      }),
      mutate: sendThreadMessage.mutate,
      onCommitted: () => {},
      onVisibleError: (kind) => {
        if (kind === "conflict") toast.error(t(($) => $.thread.send_failed));
      },
    });
    if (dispatched) {
      setThreadQuoteTarget(null);
      dispatch({ type: "setThreadDraftEmpty", empty: true });
    }
    return dispatched;
  };

  const handleRetrySend = useCallback(
    (message: ChannelMessage) => {
      if (!message.client_message_id || message.local_send_status !== "failed") return;
      if (message.thread_root_message_id) {
        sendThreadMessage.mutate({
          channelId,
          messageId: message.thread_root_message_id,
          content: message.content,
          parts: message.parts,
          quoteMessageId: message.quote_message_id ?? undefined,
          clientMessageId: message.client_message_id,
        });
        return;
      }
      sendMessage.mutate({
        channelId,
        content: message.content,
        parts: message.parts,
        quoteMessageId: message.quote_message_id ?? undefined,
        clientMessageId: message.client_message_id,
      });
    },
    [channelId, sendMessage, sendThreadMessage],
  );

  const handleOpenThread = (message: ChannelMessage) => {
    focusThreadComposerOnOpenRef.current = true;
    setSelectedAgentPanelId(null);
    dispatch({ type: "openThread", message });
  };

  const handleThreadViewParent = useCallback(() => {
    if (!threadSurfaceRoot) return;
    dispatch({
      type: "setThreadParentHighlightId",
      id: threadSurfaceRoot.id,
    });
    dispatch({ type: "closeThread" });
  }, [threadSurfaceRoot]);

  const threadPanel =
    threadSurfaceRoot ? (
      <div className="flex h-full min-h-0 min-w-0 flex-col bg-background">
        <ConversationHeader
          isMobile={isMobile}
          // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- ConversationHeader leading slot; identity is not memo-sensitive
          leading={
            isMobile ? (
              <Button
                variant="ghost"
                size="icon"
                className="size-9"
                aria-label={t(($) => $.thread.back_to_conversation)}
                onClick={() => dispatch({ type: "closeThread" })}
              >
                <ArrowLeft className="size-5" />
              </Button>
            ) : null
          }
          title={t(($) => $.thread.title)}
          // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- ConversationHeader meta slot; LRM-572 View-in-conversation link
          meta={
            threadLoading ? (
              t(($) => $.thread.meta_loading)
            ) : threadError ? (
              t(($) => $.thread.meta_load_failed)
            ) : (
              <span className="inline-flex min-w-0 max-w-full flex-wrap items-center gap-x-1">
                <span className="truncate">
                  {threadReplies.length > 0
                    ? t(($) => $.thread.meta_count, { count: threadReplies.length })
                    : t(($) => $.thread.meta_empty)}
                </span>
                <span aria-hidden className="text-muted-foreground/50">
                  ·
                </span>
                <button
                  type="button"
                  className="min-h-8 shrink-0 rounded-sm font-medium text-primary underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  onClick={handleThreadViewParent}
                >
                  {t(($) => $.thread.view_in_conversation)}
                </button>
              </span>
            )
          }
          // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- ConversationHeader actions slot
          actions={
            <>
              <ThreadFollowButton
                followed={threadSurfaceRoot.thread_followed === true}
                disabled={
                  threadLoading ||
                  (setThreadFollowed.isPending &&
                    setThreadFollowed.variables?.messageId === threadSurfaceRoot.id)
                }
                onFollowChange={handleThreadFollowChange}
              />
              {/* LRM-572 — no Maximize2;「在对话中查看」lives in the subtitle meta. */}
              {!isMobile && (
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-8"
                  aria-label={t(($) => $.thread.close_aria)}
                  onClick={() => dispatch({ type: "closeThread" })}
                >
                  <X className="size-4" />
                </Button>
              )}
            </>
          }
        />
        <ChannelMessageList
          key={`thread:${threadSurfaceRoot.id}`}
          messages={threadReplies}
          currentUserId={currentUserId}
          ownName={currentUserName ?? undefined}
          emptyLabel={t(($) => $.thread.empty_replies)}
          initialScroll="top"
          highlightMessageId={deepLinkHighlightId}
          // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- ChannelMessageList header slot
          header={
            <ThreadRootPreview
              message={threadSurfaceRoot}
              currentUserId={currentUserId}
              ownName={currentUserName ?? undefined}
              onViewParent={handleThreadViewParent}
            />
          }
          loading={threadLoading}
          loadErrorLabel={threadError ? t(($) => $.thread.load_failed) : undefined}
          onRetry={() => refetchThread()}
          onReact={handleReactToMessage}
          onQuoteMessage={setThreadQuoteTarget}
          onEditMessage={handleEditMessage}
          onDeleteMessage={handleDeleteMessage}
          onRetrySend={handleRetrySend}
        />
        <Composer
          surface="thread"
          sendLabel={t(($) => $.composer.send)}
          sendDisabled={
            (threadDraftEmpty && threadPending.readyAttachmentParts.length === 0) ||
            threadPending.hasUploading
          }
          sending={sendThreadMessage.isPending}
          onSend={handleThreadSend}
          voiceChannelId={channelId}
          voicePlaybackScope={voicePlaybackScope(channelId, threadSurfaceRoot.id)}
          voiceDisabled={!threadDraftEmpty || threadPending.pending.length > 0}
          onVoiceSend={handleThreadVoiceSend}
          isMobile={isMobile}
          prefix={threadQuoteTarget ? (
            <ComposerQuotePreview
              quote={threadQuoteTarget}
              onCancel={() => setThreadQuoteTarget(null)}
              cancelLabel={t(($) => $.quote.cancel)}
            />
          ) : undefined}
          // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- Composer tray slot; identity is not memo-sensitive
          tray={
            <ComposerAttachmentTray
              pending={threadPending.pending}
              onRemove={threadPending.remove}
              onRetry={threadPending.retry}
              isMobile={isMobile}
            />
          }
          editor={
            <ContentEditor
              key={`dm-thread-editor:${threadSurfaceRoot.id}`}
              ref={threadEditorRef}
              // Bare URLs stay plain text in the composer (#531/#542).
              plainUrls
              placeholder={t(($) => $.thread.composer_placeholder)}
              onUpdate={handleThreadEditorUpdate}
              onSubmit={handleThreadSend}
              mediaMode="external"
              onExternalFiles={threadPending.addFiles}
              submitOnEnter
              showBubbleMenu={false}
              mentionAllowedActorIds={mentionAllowedActorIds}
            />
          }
          leadingActions={
            <>
              <input
                ref={threadFileInputRef}
                type="file"
                multiple
                className="hidden"
                onChange={(e) => {
                  handlePickThreadFiles(e.target.files);
                  e.target.value = "";
                }}
              />
              <Button
                variant="ghost"
                size="icon"
                className={cn(isMobile ? "size-10" : "size-8")}
                aria-label={t(($) => $.composer.attach_aria)}
                onClick={() => threadFileInputRef.current?.click()}
              >
                <Paperclip className={cn(isMobile ? "size-5" : "size-4")} />
              </Button>
            </>
          }
        />
      </div>
    ) : null;

  const conversationPane = (
    <main className="relative flex flex-1 min-h-0 min-w-0 flex-col bg-background">
      <DmHeader
        dm={dm}
        onBack={onBack}
        filesChannelId={channelId}
        onSearchOpen={() => dispatch({ type: "openSearch" })}
        voiceCallAction={
          <DmAgentVoiceCall
            workspaceId={wsId}
            channelId={channelId}
            peer={dm.peer}
          />
        }
      />
      {convSearch.open && (
        <div
          className={cn(
            "flex items-center gap-2 border-b border-border/40 bg-muted/15 py-2",
            isMobile ? "px-2" : "px-5",
          )}
        >
          <span className="shrink-0 rounded-md border bg-background px-2 py-1 text-xs text-muted-foreground">
            {t(($) => $.conv_search.scope_current_messages)}
          </span>
          <Search className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          <input
            type="search"
            autoFocus
            value={convSearch.query}
            onChange={(e) =>
              dispatch({ type: "setSearchQuery", query: e.target.value })
            }
            onKeyDown={(e) => {
              if (e.key === "Escape") {
                dispatch({ type: "closeSearch" });
              }
            }}
            placeholder={t(($) => $.conv_search.dm_placeholder, { name: dm.peer.name })}
            className="h-8 min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
          />
          {searchQuery && (
            <span className="shrink-0 text-xs text-muted-foreground">
              {convSearch.total === 0
                ? t(($) => $.conv_search.no_results)
                : t(($) => $.conv_search.result_count, {
                    current: convSearch.index + 1,
                    total: convSearch.total,
                  })}
            </span>
          )}
          <Button
            variant="ghost"
            size="icon"
            className="size-7"
            aria-label={t(($) => $.conv_search.prev_aria)}
            onClick={() => dispatch({ type: "previousSearchResult" })}
            disabled={convSearch.total === 0 || convSearch.index === 0}
          >
            <ChevronUp className="size-4" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="size-7"
            aria-label={t(($) => $.conv_search.next_aria)}
            onClick={() => dispatch({ type: "nextSearchResult" })}
            disabled={convSearch.total === 0 || convSearch.index >= convSearch.total - 1}
          >
            <ChevronDown className="size-4" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="size-7"
            aria-label={t(($) => $.conv_search.close_aria)}
            onClick={() => {
              dispatch({ type: "closeSearch" });
            }}
          >
            <X className="size-4" />
          </Button>
        </div>
      )}
      {jumpStatus === "exhausted" && (
        <output className="block border-b bg-muted/40 px-5 py-1.5 text-center text-xs text-muted-foreground">
          {t(($) => $.message_loading.jump_not_found)}
        </output>
      )}
      <ChannelMessageList
        key={channelId}
        messages={messages}
        currentUserId={currentUserId}
        ownName={currentUserName ?? undefined}
        highlightMessageId={highlightMessageId}
        lastReadSeq={dividerLastReadSeq}
        // #340 divider count, most-authoritative first: around response total →
        // entry-frozen list count → (in MessageViewport) loaded-window count.
        unreadCount={messagePages?.pages?.[0]?.unread_total ?? entryAnchor.unreadCount}
        firstItemIndex={messagesFirstItemIndex}
        searchHitIds={searchHitIds}
        searchQuery={searchHighlightQuery}
        loading={messagesLoading}
        loadingOlder={isFetchingOlderMessages}
        hasOlder={!!hasOlderMessages}
        onLoadOlder={() => fetchOlderMessages()}
        loadOlderLabel={t(($) => $.message_loading.load_older)}
        loadingOlderLabel={t(($) => $.message_loading.loading_older)}
        loadErrorLabel={messagesError ? t(($) => $.message_loading.load_failed_retry) : undefined}
        onRetry={() => refetchMessages()}
        emptyLabel={t(($) => $.dm.thread_empty)}
        onOpenThread={handleOpenThread}
        onOpenAgent={handleOpenAgentPanel}
        onReact={handleReactToMessage}
        onQuoteMessage={setQuoteTarget}
        onEditMessage={handleEditMessage}
        onDeleteMessage={handleDeleteMessage}
        onRetrySend={handleRetrySend}
      />
      <Composer
        surface="dm_channel"
        sendLabel={t(($) => $.composer.send)}
        sendDisabled={
          (draftEmpty && dmPending.readyAttachmentParts.length === 0) ||
          dmPending.hasUploading
        }
        sending={sendMessage.isPending}
        onSend={handleSend}
        voiceChannelId={channelId}
        voicePlaybackScope={voicePlaybackScope(channelId)}
        voiceDisabled={!draftEmpty || dmPending.pending.length > 0}
        onVoiceSend={handleVoiceSend}
        isMobile={isMobile}
        prefix={quoteTarget ? (
          <ComposerQuotePreview
            quote={quoteTarget}
            onCancel={() => setQuoteTarget(null)}
            cancelLabel={t(($) => $.quote.cancel)}
          />
        ) : undefined}
        // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- Composer tray slot; identity is not memo-sensitive
        tray={
          <ComposerAttachmentTray
            pending={dmPending.pending}
            onRemove={dmPending.remove}
            onRetry={dmPending.retry}
            isMobile={isMobile}
          />
        }
        editor={
            <ContentEditor
              key={channelId}
              ref={editorRef}
              // Chat composer: typed/loaded bare URLs stay plain text
              // (#531/#542) — made clickable on the read side, not here.
              plainUrls
              defaultValue={draft}
              placeholder={t(($) => $.dm.composer_placeholder, { name: dm.peer.name })}
              onUpdate={handleEditorUpdate}
              debounceMs={0}
              onSubmit={handleSend}
              mediaMode="external"
              onExternalFiles={dmPending.addFiles}
              disableMentions
              enableIssueReferences
              submitOnEnter
              showBubbleMenu={false}
            />
        }
        leadingActions={
          <>
              <input
                ref={fileInputRef}
                type="file"
                multiple
                className="hidden"
                onChange={(e) => {
                  handlePickFiles(e.target.files);
                  e.target.value = "";
                }}
              />
              <Button
                variant="ghost"
                size="icon"
                className={cn(isMobile ? "size-10" : "size-8")}
                aria-label={t(($) => $.composer.attach_aria)}
                onClick={() => fileInputRef.current?.click()}
              >
                <Paperclip className={cn(isMobile ? "size-5" : "size-4")} />
              </Button>
              {/* LRM-205 — drop composer # issue-ref button. */}
          </>
        }
      />
      {dm.peer.type === "agent" ? (
        <DmAgentBubble agentId={dm.peer.id} agentName={dm.peer.name} />
      ) : null}
    </main>
  );

  // #349: the agent side panel shares the thread-panel slot (opening one
  // closes the other — see handleOpenThread / handleOpenAgentPanel).
  const agentPanel =
    selectedAgentPanelId ? (
      <ResolvedAgentSidePanel
        agentId={selectedAgentPanelId}
        identitySnapshot={selectedAgentPanelSnapshot}
        currentUserId={currentUserId}
        members={dmMembers}
        onClose={() => {
          setSelectedAgentPanelId(null);
          setSelectedAgentPanelSnapshot(null);
        }}
      />
    ) : null;
  const detailPanel = threadPanel ?? agentPanel;

  const withProvider = (node: React.ReactNode) => (
    <AgentPanelProvider onOpenAgent={handleOpenAgentPanel}>{node}</AgentPanelProvider>
  );

  if (!isMobile) {
    return withProvider(
      <div className="flex min-h-0 min-w-0 flex-1" data-testid="dm-detail-row">
        <div
          className="flex min-h-0 min-w-0 flex-1 flex-col"
          data-testid="dm-conversation-column"
        >
          {conversationPane}
        </div>
        {detailPanel ? (
          <div
            data-testid="dm-thread-side-slot"
            className="relative flex shrink-0 flex-col border-l border-border/30 bg-background"
            style={{ width: detailSideWidth }}
          >
            <button
              type="button"
              data-testid="dm-detail-side-resize"
              aria-label={t(($) => $.details.resize_side_aria)}
              className="absolute inset-y-0 left-0 z-10 w-1.5 cursor-col-resize border-0 bg-transparent p-0 hover:bg-foreground/10"
              onPointerDown={onDetailSideResizePointerDown}
            />
            {detailPanel}
          </div>
        ) : null}
      </div>,
    );
  }

  return withProvider(detailPanel ?? conversationPane);
}
