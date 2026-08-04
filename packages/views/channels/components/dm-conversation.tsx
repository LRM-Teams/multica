"use client";

import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useReducer,
  useRef,
  useState,
} from "react";
import { Archive, ArrowLeft, ChevronDown, ChevronUp, Eye, Paperclip, Search, X } from "lucide-react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  channelMessageThreadOptions,
  channelMessagesPageOptions,
  channelKeys,
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
  useSetChannelThreadFollowed,
  useSetChannelTyping,
  useComposerDraftStore,
} from "@multica/core/channels";
import { dmKeys, type DMItem } from "@multica/core/dm";
import { useAuthStore } from "@multica/core/auth";
import { api } from "@multica/core/api";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
import { useWorkspaceId } from "@multica/core/hooks";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import {
  computeDuplicatedHandleLabels,
  resolveActorDisplayName,
} from "@multica/core/identity";
import { useActorName } from "@multica/core/workspace/hooks";
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
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import {
  ContentEditor,
  type ContentEditorRef,
} from "../../editor/lazy-content-editor";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { ActorStyledName } from "../../common/actor-styled-name";
import { AgentPanelProvider, useOpenAgentPanel } from "../../common/agent-panel-context";
import { MemberPanelProvider } from "../../common/member-panel-context";
import {
  CHANNEL_DETAIL_SIDE_WIDTH_STORAGE_KEY,
  useProfilePanelWidth,
} from "../../layout/use-profile-panel-width";
import { useT } from "../../i18n/use-t";
import { DmAgentVoiceCall } from "../../voice-calls";
import { composePayloadKey } from "../hooks/use-compose-send-intent";
import { useComposerSend } from "../hooks/use-composer-send";
import { useComposerSendRestore } from "../hooks/use-composer-send-restore";
import { ComposerSendErrorBar } from "./composer-send-error-bar";
import {
  buildChatMessageParts,
  useComposerPendingAttachments,
} from "../hooks/use-composer-pending-attachments";
import { useComposerDraftHydrateSignal } from "../hooks/use-composer-draft-hydrate-signal";
import { useEntryReadCursor } from "../hooks/use-entry-read-cursor";
import { useEntryAnchor } from "../hooks/use-entry-around-seq";
import { useJumpNotFoundToast } from "../hooks/use-jump-not-found-toast";
import { usePrefetchThreadPreviews } from "../hooks/use-prefetch-thread-previews";
import {
  buildRecordedVoiceMessageParts,
  type VoiceRecordingAttachment,
} from "../lib/voice-audio";
import { prepareVoicePlayback, voicePlaybackScope } from "../lib/voice-playback";
import {
  handleConvSearchInputKeyDown,
  orderConvSearchResultsNewestFirst,
} from "../lib/conv-search-navigation";
import { ChannelMessageList } from "./channel-message-list";

const ChannelFilesPanel = lazy(() =>
  import("./channel-files-panel").then((m) => ({ default: m.ChannelFilesPanel })),
);
const ResolvedAgentSidePanel = lazy(() =>
  import("../../common/resolved-agent-side-panel").then((m) => ({
    default: m.ResolvedAgentSidePanel,
  })),
);
const MemberSidePanel = lazy(() =>
  import("../../members/member-side-panel").then((m) => ({
    default: m.MemberSidePanel,
  })),
);

function DmLazyPanelFallback() {
  return (
    <div className="flex flex-1 min-h-0 flex-col gap-2 p-4">
      <Skeleton className="h-8 w-1/3" />
      <Skeleton className="h-24 w-full" />
      <Skeleton className="h-24 w-full" />
    </div>
  );
}
import { Composer, ConversationHeader } from "./conversation-surface";
import { ComposerAttachmentTray } from "./composer-attachment-tray";
import { ThreadRootPreview } from "./thread-root-preview";
import { ThreadFollowButton } from "./thread-follow-button";
import { ComposerQuotePreview } from "./message-quote";
import type { QuoteTarget } from "./message-quote-types";
import { isConversationMuted, MutedIndicator } from "./conversation-muted";
import { DmAgentBubble } from "../../chat/components/dm-agent-bubble";
import { DmAgentWorkingCue } from "./dm-agent-working-cue";
import { useSelectionQuoteMenu } from "../lib/selection-quote-menu";

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
 * LRM-594 kept the heavy Working list / Stop chrome out of the 1:1 header.
 * LRM-909: Profile ACTIONS no longer exposes Stop either — stop lives on
 * the DM live cue when present. We still show a bubble-style short cue
 * (思考中 / Edit / Shell + breathe) so entering the DM is not silent
 * while the agent works — no path/command details, no Working list.
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

// Shared peer header: avatar (presence dot for agents) + name + optional search.
// LRM-675 — the Files popover is removed: the channel main-area 「文件」 tab
// is the single Files entry (no duplicate header icon).
function DmHeader({
  dm,
  onBack,
  onSearchOpen,
  voiceCallAction,
}: {
  dm: DMItem;
  onBack: () => void;
  /** When provided, renders a magnifying-glass button (source gate: dm_channel only). */
  onSearchOpen?: () => void;
  /** Agent-only call control; kept outside the shared header chrome. */
  voiceCallAction?: React.ReactNode;
}) {
  const { t } = useT("channels");
  const isMobile = useIsMobile();
  const openAgentPanel = useOpenAgentPanel();
  const { getMemberHonor, getAgentHonorLevel } = useActorName();
  const peerId = dm.peer.id;
  const peerType = dm.peer.type;
  const isMuted = isConversationMuted(dm);
  const actorType = peerType === "agent" ? "agent" : "member";
  const memberType = peerType === "agent" ? "agent" : "user";
  const isAgentPeer = peerType === "agent";
  // #692 supervised agent↔agent DM header: both agents named + the 「智能体私聊」
  // pill, a non-interactive dual avatar (no single-peer agent-panel trigger —
  // the owner supervises the pair, not one agent).
  const [pairA, pairB] = dm.participants ?? [];
  const agentPair =
    dm.mode === "agent_pair" && pairA && pairB ? { a: pairA, b: pairB } : null;
  // Ordinary agent DM: short bubble-style working cue (思考中 / Edit / Shell). A
  // supervised agent_pair is NOT one working agent, so it never shows the
  // single-agent cue — its header is the dual-avatar/pill supervision chrome
  // instead. Human peers keep the static "Human" meta; Stop stays in
  // AgentProfileActions.
  const workingCue = isAgentPeer && !agentPair ? (
    <DmAgentWorkingCue agentId={peerId} />
  ) : undefined;
  const meta = isAgentPeer ? undefined : t(($) => $.dm.human_meta);
  // Mobile: put the cue under the name (meta line) — long agent names +
  // back/avatar/actions leave too little room for same-row status.
  // Desktop: Slack-style cue beside the name (status slot).
  const headerStatus = isMobile ? undefined : workingCue;
  const headerMeta = isMobile ? (workingCue ?? meta) : meta;
  const mutedBadge = useMemo(
    () => (isMuted ? <MutedIndicator label={t(($) => $.dm.muted_label)} /> : null),
    [isMuted, t],
  );
  // #692: the 「智能体私聊」pill sits alongside any muted badge. Memoized (like
  // `mutedBadge`) so the JSX handed to ConversationHeader's `badges` prop keeps a
  // stable identity; keyed on the primitive `isAgentPair`, not the per-render
  // `agentPair` object, so the memo actually holds.
  const isAgentPair = !!agentPair;
  const headerBadges = useMemo(
    () => (
      <>
        {isAgentPair && (
          <span className="shrink-0 rounded border border-border px-1 text-[10px] font-medium leading-tight text-muted-foreground">
            {t(($) => $.dm.agent_pair.pill)}
          </span>
        )}
        {mutedBadge}
      </>
    ),
    [isAgentPair, mutedBadge, t],
  );
  const peerHonor = !agentPair && peerType === "user" ? getMemberHonor(peerId) : undefined;
  const peerHonorLevel =
    !agentPair && peerType === "agent" ? getAgentHonorLevel(peerId) : undefined;
  // LRM-749/LRM-710: header mirrors the DM list row — weak gray @handle next
  // to the peer name only while the display name collides in this workspace.
  const wsId = useWorkspaceId();
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const dupHandleLabel = useMemo(() => {
    if (dm.mode === "agent_pair" || !isAgentPeer) return null;
    return (
      computeDuplicatedHandleLabels(agents.filter((a) => !a.archived_at)).get(peerId) ?? null
    );
  }, [agents, dm.mode, isAgentPeer, peerId]);
  const peerTitle = (
    <>
      <ActorStyledName
        displayName={dm.peer.name}
        honor={peerHonor}
        agentHonorLevel={peerHonorLevel}
        className="truncate text-sm font-semibold text-foreground"
      />
      {dupHandleLabel && (
        <span className="shrink-0 text-[11px] font-normal text-muted-foreground">
          {dupHandleLabel}
        </span>
      )}
    </>
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
        // overflow-visible on the avatar axis: presence corner dot must not be
        // shaved by this header hit target (LRM-1119). Truncation stays on the
        // name span (`min-w-0 truncate`), not the whole row.
        className="inline-flex min-w-0 max-w-full items-center overflow-visible rounded-md text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        onClick={() => openAgentPanel(peerId)}
      >
        {child}
      </button>
    ) : (
      <ActorProfileTrigger memberType={memberType} memberId={peerId}>
        {child}
      </ActorProfileTrigger>
    );

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
          {agentPair ? (
            <div className="relative size-7 shrink-0" aria-hidden>
              <ActorAvatar
                actorType="agent"
                actorId={agentPair.a.id}
                size={20}
                showStatusDot={false}
                profileLink={false}
              />
              <div className="absolute -bottom-0.5 -right-0.5 rounded-full ring-2 ring-background">
                <ActorAvatar
                  actorType="agent"
                  actorId={agentPair.b.id}
                  size={16}
                  showStatusDot={false}
                  profileLink={false}
                />
              </div>
            </div>
          ) : (
            wrapPeerTrigger(peerAvatar)
          )}
        </>
      }
      title={
        agentPair ? (
          <span className="block truncate">{`${agentPair.a.name} · ${agentPair.b.name}`}</span>
        ) : (
          wrapPeerTrigger(
            <span className="flex min-w-0 max-w-full items-baseline gap-1">{peerTitle}</span>,
          )
        )
      }
      meta={headerMeta}
      // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- ConversationHeader status slot; live cue is not memo-sensitive
      status={headerStatus}
      badges={headerBadges}
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
  const { t: tAgents } = useT("agents");
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
  // LRM-1063: once per deep-link target, drop mid-history cache so jump can
  // walk latest → older.
  const deepLinkMessagesResetKeyRef = useRef<string | null>(null);
  // #349 / LRM-877 — Dock Stack in one state object (react-doctor prefer-useReducer).
  // Agent + optional returnToMember share the thread-panel slot with member Profile.
  type DmDockStack = {
    agentId: string | null;
    agentSnapshot: AgentPanelIdentitySnapshot | null;
    returnToMemberId: string | null;
    memberId: string | null;
    /** LRM-740 — Profile/Agent opened from Thread; close re-opens Thread. */
    returnToThread: ChannelMessage | null;
  };
  const [dock, setDock] = useState<DmDockStack>({
    agentId: null,
    agentSnapshot: null,
    returnToMemberId: null,
    memberId: null,
    returnToThread: null,
  });
  const {
    agentId: selectedAgentPanelId,
    agentSnapshot: selectedAgentPanelSnapshot,
    returnToMemberId: selectedAgentReturnToMemberId,
    memberId: selectedMemberPanelId,
    returnToThread: dockReturnToThread,
  } = dock;
  // LRM-682 — DM main-area view switch: 聊天 | 文件 (no Issues — DMs have no
  // issue context). DmConversation remounts per DM (key={source:id} at the
  // call site), so switching conversations lands back on chat for free.
  const [dmView, setDmView] = useState<"chat" | "files">("chat");
  const handleOpenAgentPanel = useCallback<OpenAgentPanelFn>(
    (agentId, snapshot, options) => {
      // LRM-740 — stash Thread before close so Profile/Agent close can restore it.
      const returnToThread = openThreadRoot;
      dispatch({ type: "closeThread" });
      setDock((prev) => ({
        agentId,
        agentSnapshot: snapshot ?? null,
        returnToMemberId:
          options?.returnToMemberId ?? prev.memberId ?? prev.returnToMemberId,
        memberId: null,
        returnToThread: returnToThread ?? prev.returnToThread,
      }));
    },
    [openThreadRoot],
  );
  const handleOpenMemberPanel = useCallback(
    (userId: string) => {
      const returnToThread = openThreadRoot;
      dispatch({ type: "closeThread" });
      setDock((prev) => ({
        agentId: null,
        agentSnapshot: null,
        returnToMemberId: null,
        memberId: userId,
        returnToThread: returnToThread ?? prev.returnToThread,
      }));
    },
    [openThreadRoot],
  );
  const handlePopAgentToMember = useCallback(() => {
    setDock((prev) => ({
      agentId: null,
      agentSnapshot: null,
      returnToMemberId: null,
      memberId: prev.returnToMemberId,
      returnToThread: prev.returnToThread,
    }));
  }, []);
  const closeDockRestoringThread = useCallback(() => {
    setDock((prev) => {
      const restore = prev.returnToThread;
      if (restore) {
        dispatch({ type: "openThread", message: restore });
      }
      return {
        agentId: null,
        agentSnapshot: null,
        returnToMemberId: null,
        memberId: null,
        returnToThread: null,
      };
    });
  }, []);
  const { data: dmMembers = [] } = useQuery({
    ...memberListOptions(wsId),
    enabled: !!selectedAgentPanelId || !!selectedMemberPanelId,
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
  const setThreadFollowed = useSetChannelThreadFollowed();
  // Edit is a PATCH of an existing message (H5) — it routes through
  // editChannelMessage, never the send path, so it can never produce a new wake.
  // DMs are never archived/closed, so (like onReact) edit is always wired.
  const handleEditMessage = useCallback((message: ChannelMessage, content: string) => {
    editChannelMessage.mutate(
      { channelId: message.channel_id, messageId: message.id, content },
      { onError: () => showErrorToast(t(($) => $.message.edit_failed_toast)) },
    );
  }, [editChannelMessage, t]);
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

  // LRM-1068: freeze entry unread count for the divider/pill. Cold load uses
  // the latest page (no around_seq) — see useEntryAnchor.
  const entryAnchor = useEntryAnchor(
    channelId,
    dm.last_read_seq,
    dm.real_unread ?? dm.unread,
  );
  // LRM-1063: Reminder / notification deep-links must load from latest so
  // ensure-message-loaded can page older toward the target (no newer cursor).
  const hasConversationDeepLink = !!(deepLinkMessageId || threadDeepLinkId);
  const deepLinkAroundSeq = hasConversationDeepLink ? null : entryAnchor.aroundSeq;
  const {
    data: messagePages,
    isLoading: messagesLoading,
    isPending: messagesPending,
    isError: messagesError,
    refetch: refetchMessages,
    fetchNextPage: fetchOlderMessages,
    hasNextPage: hasOlderMessages,
    isFetchingNextPage: isFetchingOlderMessages,
  } = useInfiniteQuery(
    channelMessagesPageOptions(channelId, { aroundSeq: deepLinkAroundSeq }),
  );
  const messages = useMemo(() => flattenChannelMessagePages(messagePages), [messagePages]);
  usePrefetchThreadPreviews(messages);
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
        onError: () => showErrorToast(
          t(($) => followed ? $.thread.follow_failed : $.thread.unfollow_failed),
        ),
      },
    );
  }, [setThreadFollowed, t, threadSurfaceRoot]);

  const editorRef = useRef<ContentEditorRef>(null);
  const threadEditorRef = useRef<ContentEditorRef>(null);
  const dmMessageAreaRef = useRef<HTMLDivElement>(null);
  // LRM-695 — text-selection Quote/Copy over the DM message area (desktop).
  const dmSelectionMenu = useSelectionQuoteMenu({
    containerRef: dmMessageAreaRef,
    onQuote: (md: string) => editorRef.current?.insertMarkdown(md),
  });
  // #772 send-failure → composer-restore (main + thread composers). The failed
  // text is restored into the composer (or kept-back when the composer already
  // holds new text) and an inline bar is shown; the editor is remounted via a
  // nonce bump so it re-reads the restored text. The main composer restores via
  // the persistent draft store; the thread composer has no persistent draft so
  // it restores via the hook-owned `restoreText` → editor `defaultValue`.
  const restore = useComposerSendRestore(onDraftChange);
  const threadRestore = useComposerSendRestore();
  const focusThreadComposerOnOpenRef = useRef(false);
  const draftEmpty = !draft.trim();
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const threadFileInputRef = useRef<HTMLInputElement | null>(null);

  const uploadForDm = useCallback(
    async (file: File) => upload(file, { channelId }),
    [channelId, upload],
  );
  const dmDraftKey = `dm:${channelId}` as const;
  const dmDraftHydrateSignal = useComposerDraftHydrateSignal(dmDraftKey);
  const dmPending = useComposerPendingAttachments({
    upload: uploadForDm,
    resetKey: channelId,
    // LRM-801 — tray rides the same per-DM draft as the text.
    persistence: {
      load: () =>
        useComposerDraftStore.getState().drafts[dmDraftKey]?.attachments,
      save: (attachments) =>
        useComposerDraftStore.getState().setDraftAttachments(dmDraftKey, attachments),
      hydrateSignal: dmDraftHydrateSignal,
    },
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
  // a 1-on-1 (only the peer is reachable here). A supervised agent_pair DM has
  // two agents rather than a single peer (#692); the owner is read-only there so
  // the composer never renders, but keep the allowlist honest to the pair.
  const mentionAllowedActorIds = useMemo(
    () =>
      dm.mode === "agent_pair" && dm.participants && dm.participants.length > 0
        ? new Set(dm.participants.map((p) => p.id))
        : new Set([dm.peer.id]),
    [dm.mode, dm.participants, dm.peer.id],
  );

  // #692: the owner of a supervised agent_pair DM reads it read-only — the
  // server rejects any send/edit/delete/reaction from the supervisor, so the
  // composer becomes a quiet supervision banner (mirrors the archived-channel
  // read-only surface).
  //
  // #692 walkthrough finding: key read-only on `mode === "agent_pair"`, NOT only
  // on the `supervised` flag. A human viewing an agent-pair DM is ALWAYS a
  // read-only supervisor — the two members are the agents, a human can never be
  // a writer here — so an agent_pair view must be read-only even if the BE
  // omitted `supervised` (observed when one owner owns both ends). `supervised`
  // is kept as a redundant signal.
  const supervisedReadOnly = dm.mode === "agent_pair" || !!dm.supervised;
  // Plain const element (like `peerAvatar`), not a memo: a cheap leaf only
  // consumed when `supervisedReadOnly` is true, so memoizing it would just add a
  // JSX-returning hook before the effects' early-returns for no real saving.
  const supervisedReadOnlyContent = (
    <>
      <Eye className="size-4 shrink-0" />
      <span className="flex-1">{t(($) => $.dm.agent_pair.owner_readonly_note)}</span>
    </>
  );

  // 2026-07-31 Wendy DM incident (B1) — the peer agent was archived (the
  // product-facing "delete agent" action is a soft archive; history is never
  // hidden). Same read-only contract as the agent_pair supervision surface
  // above — reusing `readOnly` below for every write gate keeps this from
  // needing its own copy of each handler guard — but the banner content and
  // copy are distinct, and phrased from the user's action ("deleted"), never
  // the internal "archived" term (Parker/Iris, product review).
  const archivedPeerReadOnly = dm.peer.type === "agent" && !!dm.peer.archived;
  const archivedPeerReadOnlyContent = (
    <>
      <Archive className="size-4 shrink-0" />
      <span className="flex-1">{t(($) => $.dm.peer_deleted_notice)}</span>
    </>
  );
  const readOnly = supervisedReadOnly || archivedPeerReadOnly;
  const readOnlyContent = supervisedReadOnly
    ? supervisedReadOnlyContent
    : archivedPeerReadOnlyContent;

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
    // LRM-1063: don't false-exhaust while the first page is pending.
    isPending: !channelId || messagesPending,
  });
  // LRM-736 AC — toast + keep the inline notice (#835 durable record).
  useJumpNotFoundToast({
    missing: jumpStatus === "exhausted",
    targetId: highlightMessageId,
    message: t(($) => $.message_loading.jump_not_found),
  });
  useJumpNotFoundToast({
    missing:
      !!threadDeepLinkId &&
      !!deepLinkHighlightId &&
      !!openThreadRoot &&
      !threadLoading &&
      !threadError &&
      !!threadPage &&
      !threadPage.messages.some((m) => m.id === deepLinkHighlightId),
    targetId: deepLinkHighlightId,
    message: t(($) => $.message_loading.jump_not_found),
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
          // LRM-753 — newest-first so index 0 is「最近命中」.
          results: orderConvSearchResultsNewestFirst(res.results),
          total: res.total,
        });
      } catch {
        showErrorToast(t(($) => $.conv_search.error));
      }
    }, 300);
    return () => clearTimeout(timer);
  }, [convSearch.open, searchQuery, channelId, t]);

  // Bottom-stick on new messages and open-at-latest on switch are handled by
  // ChannelMessageList (react-virtuoso). No manual scrollIntoView needed.

  // Mark read on open — clears the badge — and expose the pre-advance read
  // cursor from the mark-read response for the race-free "N new messages"
  // divider (#303).
  // LRM-762: supervisors may mark-read (BE admits agent_pair owners via
  // channel_read without requiring channel_member). Clears sidebar unread /
  // manual-unread the same way as a speakable DM.
  const dividerLastReadSeq = useEntryReadCursor(
    channelId,
    dm.last_read_seq,
    markChannelRead,
  );

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

  // LRM-1063: reset mid-history message cache for this deep-link target.
  useEffect(() => {
    const target = deepLinkMessageId || threadDeepLinkId;
    if (!target || !channelId) return;
    const key = `${channelId}:${target}`;
    if (deepLinkMessagesResetKeyRef.current === key) return;
    deepLinkMessagesResetKeyRef.current = key;
    qc.removeQueries({ queryKey: channelKeys.messagesPage(channelId) });
  }, [deepLinkMessageId, threadDeepLinkId, channelId, qc]);

  useEffect(() => {
    if (!threadRoot) return;
    // #692 finding 1: supervisor isn't a channel_member — thread mark-read
    // 403s. Same as above: an archived-peer DM's viewer is a normal member,
    // so this stays keyed on `supervisedReadOnly`, not the broader `readOnly`.
    if (supervisedReadOnly) return;
    markThreadRead({ channelId, messageId: threadRoot.id });
  }, [channelId, threadRoot, markThreadRead, supervisedReadOnly]);

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
    // LRM-762: while the supervised pane is open, advance the supervisor's
    // channel_read cursor the same as a speakable DM (write path stays blocked).
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

  // #692 finding 1: a read-only supervisor never types — and typing is a
  // member-only mutation that would 403 — so it's a no-op on that surface.
  const publishTyping = (isTyping: boolean) => {
    // Same member-403 reasoning as the mark-read gates above — kept on
    // `supervisedReadOnly` rather than `readOnly`. In practice unreachable
    // for the archived-peer case anyway (Composer's readOnly banner replaces
    // the editor, so no onUpdate ever fires), but the guard should still say
    // what it means.
    if (supervisedReadOnly) return;
    setTyping.mutate({ channelId, isTyping });
  };

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
    // #692 supervised read-only contract: an agent_pair supervisor must NEVER
    // write to the pair. Reject every submit path at the handler layer (Enter /
    // submitOnEnter / onSubmit / voice / retry), not only by hiding the composer
    // — the composer's readOnly banner (editor not mounted) is defense-in-depth,
    // this handler gate is the contract-level enforcement. (Iris walkthrough
    // finding; Parker: 定性 fix, mandatory.)
    if (readOnly) return;
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
      // #1276 INV-1: clear the persisted draft ONLY on confirmed success, never on
      // optimistic dispatch — otherwise a failure/abort or a reload mid-flight
      // destroys the text before the restore can put it back.
      onCommitted: () => {
        restore.clear();
        onDraftClear?.();
      },
      onVisibleError: (kind) => {
        // #772: no permanent failed bubble. Restore the failed text into the
        // composer (unless it already holds DIFFERENT new text — then keep it +
        // offer Restore-previous) and show the inline error bar. #1276 413: a
        // too-large payload guides shorten-and-retry (no plain Retry).
        restore.onFailed(
          content,
          editorRef.current?.getMarkdown()?.trim() ?? "",
          kind === "too_long",
        );
      },
    });
    if (dispatched) {
      restore.clear();
      prepareVoicePlayback(voicePlaybackScope(channelId));
      editorRef.current?.clearContent();
      dmPending.clear();
      setQuoteTarget(null);
      // NB: persisted draft is cleared in onCommitted (success), not here (#1276 INV-1).
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
    if (readOnly) return false; // read-only (supervisor or archived peer): no voice send
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
        if (kind === "conflict") showErrorToast(t(($) => $.composer.send_failed));
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
    if (readOnly) return; // read-only (supervisor or archived peer): no thread send
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
      onCommitted: () => threadRestore.clear(),
      onVisibleError: (kind) => {
        // #772 (thread): restore the failed text into the thread composer (via
        // the editor's `defaultValue` + remount) unless it already holds new
        // text; show the inline error bar. #1276 413: guide shorten-and-retry.
        threadRestore.onFailed(
          content,
          threadEditorRef.current?.getMarkdown()?.trim() ?? "",
          kind === "too_long",
        );
      },
    });
    if (dispatched) {
      threadRestore.reset();
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
    if (readOnly) return false; // read-only (supervisor or archived peer): no thread voice send
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
        if (kind === "conflict") showErrorToast(t(($) => $.thread.send_failed));
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
      if (readOnly) return; // read-only (supervisor or archived peer): no retry-send
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
    [channelId, sendMessage, sendThreadMessage, readOnly],
  );

  const handleOpenThread = (message: ChannelMessage) => {
    focusThreadComposerOnOpenRef.current = true;
    setDock({
      agentId: null,
      agentSnapshot: null,
      returnToMemberId: null,
      memberId: null,
      returnToThread: null,
    });
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
              {/* #692 finding 1: thread-follow is a member-only mutation — hidden
                  for the read-only supervisor (it would 403). */}
              {!readOnly && (
                <ThreadFollowButton
                  followed={threadSurfaceRoot.thread_followed === true}
                  disabled={
                    threadLoading ||
                    (setThreadFollowed.isPending &&
                      setThreadFollowed.variables?.messageId === threadSurfaceRoot.id)
                  }
                  onFollowChange={handleThreadFollowChange}
                />
              )}
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
          highlightMessageId={deepLinkHighlightId}
          // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- ChannelMessageList header slot
          header={
            <ThreadRootPreview
              message={threadSurfaceRoot}
              currentUserId={currentUserId}
              ownName={currentUserName ?? undefined}
              onViewParent={handleThreadViewParent}
              onOpenAgent={handleOpenAgentPanel}
              onOpenMember={handleOpenMemberPanel}
            />
          }
          loading={threadLoading}
          loadErrorLabel={threadError ? t(($) => $.thread.load_failed) : undefined}
          onRetry={() => refetchThread()}
          // #692 finding 1: the read-only supervisor gets no message-mutation
          // affordances — omitting these handlers makes ChannelMessageBubble drop
          // its reaction / quote / edit / delete / retry controls entirely.
          onReact={readOnly ? undefined : handleReactToMessage}
          onQuoteMessage={readOnly ? undefined : setThreadQuoteTarget}
          onEditMessage={readOnly ? undefined : handleEditMessage}
          onRetrySend={readOnly ? undefined : handleRetrySend}
          onOpenAgent={handleOpenAgentPanel}
          onOpenMember={handleOpenMemberPanel}
        />
        <Composer
          surface="thread"
          readOnly={readOnly}
          readOnlyContent={readOnlyContent}
          sendLabel={t(($) => $.composer.send)}
          sendDisabled={
            (threadDraftEmpty && threadPending.readyAttachmentParts.length === 0) ||
            threadPending.hasUploading
          }
          sending={sendThreadMessage.isPending}
          onSend={handleThreadSend}
          voiceChannelId={channelId}
          voicePlaybackScope={voicePlaybackScope(channelId, threadSurfaceRoot.id)}
          // #858 — DMs do not CURRENTLY produce a pending-voice record, because
          // #838 wired that state for channels and threads only; hence the input
          // is absent here. NOT "a DM can never have an unsent recording": once
          // #849 lands, the record is author-owned and keyed by
          // `channel_id + optional thread_root_id`, and a DM is a channel — so
          // this input becomes reachable and must be wired then. The resolver
          // already handles the branch; only this call site needs revisiting.
          voiceBlock={{
            hasTextDraft: !threadDraftEmpty,
            hasAttachmentDraft: threadPending.pending.length > 0,
          }}
          onVoiceSend={handleThreadVoiceSend}
          isMobile={isMobile}
          // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- Composer prefix slot; identity is not memo-sensitive
          prefix={threadRestore.error || threadQuoteTarget ? (
            <>
              <ComposerSendErrorBar
                error={threadRestore.error}
                onRetry={handleThreadSend}
                onRestore={threadRestore.restorePrevious}
              />
              {threadQuoteTarget ? (
                <ComposerQuotePreview
                  quote={threadQuoteTarget}
                  onCancel={() => setThreadQuoteTarget(null)}
                  cancelLabel={t(($) => $.quote.cancel)}
                />
              ) : null}
            </>
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
              key={`dm-thread-editor:${threadSurfaceRoot.id}:${threadRestore.nonce}`}
              ref={threadEditorRef}
              defaultValue={threadRestore.restoreText}
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
        onSearchOpen={() => dispatch({ type: "openSearch" })}
        voiceCallAction={
          // #692 finding 1: no voice call on the read-only supervision surface —
          // the supervisor owns neither end's session and the projected `peer`
          // is only one of the two agents.
          readOnly ? undefined : (
            <DmAgentVoiceCall
              workspaceId={wsId}
              channelId={channelId}
              peer={dm.peer}
            />
          )
        }
      />
      {/* LRM-682 — DM main-area tab switch: 聊天 (message list + composer) and
          文件 (DM attachments via the shared ChannelFilesPanel), mirroring the
          group-channel tab bar (#562/LRM-675). No Issues tab: a 1:1 has no
          issue context (LRM-681 design lock). The tab bar is the single Files
          entry — the header files icon stays removed (LRM-675). */}
      <Tabs
        value={dmView}
        onValueChange={(value) => setDmView(value as "chat" | "files")}
        className="flex flex-1 min-h-0 flex-col gap-0"
      >
        <div className="shrink-0 border-b border-border/40 px-4">
          <TabsList variant="line" className="h-auto">
            <TabsTrigger value="chat" className="flex-none px-3 py-2">
              {t(($) => $.view_tabs.dm_chat)}
            </TabsTrigger>
            <TabsTrigger value="files" className="flex-none px-3 py-2">
              {t(($) => $.view_tabs.files)}
            </TabsTrigger>
          </TabsList>
        </div>
        <TabsContent value="files" className="flex flex-1 min-h-0 flex-col text-base">
          {dmView === "files" ? (
            <Suspense fallback={<DmLazyPanelFallback />}>
              <ChannelFilesPanel channelId={channelId} wide />
            </Suspense>
          ) : null}
        </TabsContent>
        <TabsContent value="chat" className="flex flex-1 min-h-0 flex-col text-base">
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
              handleConvSearchInputKeyDown(e, {
                total: convSearch.total,
                onClose: () => dispatch({ type: "closeSearch" }),
                onNext: () => dispatch({ type: "nextSearchResult" }),
                onPrev: () => dispatch({ type: "previousSearchResult" }),
              });
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
      <div ref={dmMessageAreaRef} className="contents">
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
        // #692 walkthrough finding: opening a thread to reply is a write entry
        // too — the read-only supervisor gets no "reply in thread" affordance
        // (the thread's own composer is already read-only, so the button led
        // nowhere). Dropping the handler removes the bubble's thread-reply control.
        onOpenThread={readOnly ? undefined : handleOpenThread}
        onOpenAgent={handleOpenAgentPanel}
        onOpenMember={handleOpenMemberPanel}
        // #692 finding 1: read-only supervisor gets no message-mutation
        // affordances — dropping these handlers removes the bubble's
        // reaction / quote / edit / delete / retry controls.
        onReact={readOnly ? undefined : handleReactToMessage}
        onQuoteMessage={readOnly ? undefined : setQuoteTarget}
        onEditMessage={readOnly ? undefined : handleEditMessage}
        onRetrySend={readOnly ? undefined : handleRetrySend}
      />
      {!readOnly ? dmSelectionMenu.menu : null}
      </div>
      <Composer
        surface="dm_channel"
        readOnly={readOnly}
        readOnlyContent={readOnlyContent}
        sendLabel={t(($) => $.composer.send)}
        sendDisabled={
          (draftEmpty && dmPending.readyAttachmentParts.length === 0) ||
          dmPending.hasUploading
        }
        sending={sendMessage.isPending}
        onSend={handleSend}
        voiceChannelId={channelId}
        voicePlaybackScope={voicePlaybackScope(channelId)}
        // #858 — see the DM thread surface above, including why this input is
        // absent today and what makes it reachable after #849.
        voiceBlock={{
          hasTextDraft: !draftEmpty,
          hasAttachmentDraft: dmPending.pending.length > 0,
        }}
        onVoiceSend={handleVoiceSend}
        isMobile={isMobile}
        // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- Composer prefix slot; identity is not memo-sensitive
        prefix={restore.error || quoteTarget ? (
          <>
            <ComposerSendErrorBar
              error={restore.error}
              onRetry={handleSend}
              onRestore={restore.restorePrevious}
            />
            {quoteTarget ? (
              <ComposerQuotePreview
                quote={quoteTarget}
                onCancel={() => setQuoteTarget(null)}
                cancelLabel={t(($) => $.quote.cancel)}
              />
            ) : null}
          </>
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
              key={`${channelId}:${restore.nonce}`}
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
              enableChannelReferences
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
      {/* #692 gate②: DmAgentBubble mounts a SECOND, independent agent-chat
          surface (ChatWindow → its own ProseMirror composer) keyed only on
          peer.type. An agent_pair's projected peer is also "agent", so the
          read-only supervision surface was mounting a live, editable editor
          beside the "can't post here" banner — a write surface the Composer
          readOnly banner never covered. Gate it on !readOnly: a
          supervised agent_pair has no semantically-correct single peer, so no
          single-agent bubble belongs here. Same reasoning covers an archived
          peer (B1) — a second, independent chat session with an agent that
          can no longer take new work is exactly as wrong as it is for a
          supervised pair. (Composer/handler gates unchanged.) */}
      {dm.peer.type === "agent" && !readOnly ? (
        <DmAgentBubble agentId={dm.peer.id} agentName={dm.peer.name} />
      ) : null}
        </TabsContent>
      </Tabs>
    </main>
  );

  // #349: the agent side panel shares the thread-panel slot (opening one
  // closes the other — see handleOpenThread / handleOpenAgentPanel).
  // LRM-877: Agent may sit on a Dock Stack over a human Profile (returnTo).
  const agentPanelBackLabel = selectedAgentReturnToMemberId
    ? resolveActorDisplayName(
        dmMembers.find((m) => m.user_id === selectedAgentReturnToMemberId) ?? null,
        selectedAgentReturnToMemberId,
      )
    : undefined;
  const agentPanel =
    selectedAgentPanelId ? (
      <Suspense fallback={<DmLazyPanelFallback />}>
      <ResolvedAgentSidePanel
        agentId={selectedAgentPanelId}
        identitySnapshot={selectedAgentPanelSnapshot}
        currentUserId={currentUserId}
        members={dmMembers}
        onClose={closeDockRestoringThread}
        variant={isMobile ? "page" : "panel"}
        onBack={
          selectedAgentReturnToMemberId ? handlePopAgentToMember : undefined
        }
        backLabel={agentPanelBackLabel}
      />
      </Suspense>
    ) : null;
  const memberPanel =
    selectedMemberPanelId ? (
      <Suspense fallback={<DmLazyPanelFallback />}>
      <MemberSidePanel
        userId={selectedMemberPanelId}
        onClose={() => {
          if (dockReturnToThread) {
            closeDockRestoringThread();
            return;
          }
          setDock((prev) => ({
            ...prev,
            memberId: null,
            returnToThread: null,
          }));
        }}
        variant={isMobile ? "page" : "panel"}
        doneLabel={
          isMobile ? tAgents(($) => $.side_panel.back_to_messages) : undefined
        }
      />
      </Suspense>
    ) : null;
  const detailPanel = threadPanel ?? agentPanel ?? memberPanel;

  const withProvider = (node: React.ReactNode) => (
    <AgentPanelProvider onOpenAgent={handleOpenAgentPanel}>
      <MemberPanelProvider onOpenMember={handleOpenMemberPanel}>
        {node}
      </MemberPanelProvider>
    </AgentPanelProvider>
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
