"use client";

import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";
import { ArrowLeft, ChevronDown, ChevronUp, FileText, Hash, MessageSquare, Paperclip, Search, X } from "lucide-react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  activeChannelTasksKeys,
  activeChannelTasksOptions,
  channelMessageThreadOptions,
  channelMessagesPageOptions,
  flattenChannelMessagePages,
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
  useSetChannelTyping,
} from "@multica/core/channels";
import { dmKeys } from "@multica/core/dm";
import type { DMItem } from "@multica/core/dm";
import { useAuthStore } from "@multica/core/auth";
import { api } from "@multica/core/api";
import { useFileUpload, type UploadResult } from "@multica/core/hooks/use-file-upload";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWSEvent } from "@multica/core/realtime";
import type { ChannelActiveTask, ChannelMessage, ChannelMessageSearchResult, ChannelTypingPayload } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Drawer } from "@multica/ui/components/ui/drawer";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@multica/ui/components/ui/resizable";
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
import { useT } from "../../i18n/use-t";
import { composePayloadKey } from "../hooks/use-compose-send-intent";
import { useComposerSend } from "../hooks/use-composer-send";
import { ChannelMessageList } from "./channel-message-list";
import { ChannelFilesPanel } from "./channel-files-panel";
import { Composer, ConversationHeader, MobileThreadDrawerContent } from "./conversation-surface";
import { ThreadRootPreview } from "./thread-root-preview";
import {
  ConversationActivityStrip,
  type TypingActor,
} from "./channels-page";
import { isConversationMuted, MutedIndicator } from "./conversation-muted";
import { isTypingActorVisible } from "./conversation-typing";

/**
 * DM detail pane. Visible direct messages must use the R2 `dm_channel` stack:
 *  - `dm_channel` — the DM IS a kind='dm' channel, so we reuse the exact
 *    channel conversation stack (ChannelMessageBubble + ContentEditor composer
 *    + ConversationActivityStrip + channel queries/mutations +
 *    channel:message / channel:typing WS).
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
  convSearch: ConversationSearchState;
  threadParentHighlightId: string | null;
  typingActors: Record<string, TypingActor>;
}

type DmChannelAction =
  | { type: "openThread"; message: ChannelMessage }
  | { type: "closeThread" }
  | { type: "resetForChannel" }
  | { type: "setThreadDraftEmpty"; empty: boolean }
  | { type: "setThreadParentHighlightId"; id: string | null }
  | { type: "openSearch" }
  | { type: "closeSearch" }
  | { type: "setSearchQuery"; query: string }
  | { type: "setSearchResults"; query: string; results: ChannelMessageSearchResult[]; total: number }
  | { type: "previousSearchResult" }
  | { type: "nextSearchResult" }
  | { type: "expireTypingActors"; now: number }
  | { type: "removeTypingActor"; actorKey: string }
  | { type: "upsertTypingActor"; actor: TypingActor };

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
  convSearch: initialConversationSearchState,
  threadParentHighlightId: null,
  typingActors: {},
};

function dmChannelReducer(state: DmChannelState, action: DmChannelAction): DmChannelState {
  switch (action.type) {
    case "openThread":
      return { ...state, openThreadRoot: action.message };
    case "closeThread":
      return { ...state, openThreadRoot: null };
    case "resetForChannel":
      return initialDmChannelState;
    case "setThreadDraftEmpty":
      return state.threadDraftEmpty === action.empty ? state : { ...state, threadDraftEmpty: action.empty };
    case "setThreadParentHighlightId":
      return state.threadParentHighlightId === action.id ? state : { ...state, threadParentHighlightId: action.id };
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
    case "expireTypingActors": {
      const next = Object.fromEntries(
        Object.entries(state.typingActors).filter(([, actor]) => actor.expiresAt > action.now),
      );
      return Object.keys(next).length === Object.keys(state.typingActors).length
        ? state
        : { ...state, typingActors: next };
    }
    case "removeTypingActor": {
      if (!state.typingActors[action.actorKey]) return state;
      const next = { ...state.typingActors };
      delete next[action.actorKey];
      return { ...state, typingActors: next };
    }
    case "upsertTypingActor":
      return {
        ...state,
        typingActors: {
          ...state.typingActors,
          [action.actor.key]: action.actor,
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
}: DmConversationProps) {
  return (
    <DmChannelConversation
      dm={dm}
      onBack={onBack}
      draft={draft}
      onDraftChange={onDraftChange}
      onDraftClear={onDraftClear}
    />
  );
}

// Shared peer header: avatar (presence dot for agents) + name + optional search + Files popover.
function DmHeader({
  dm,
  onBack,
  filesChannelId,
  onSearchOpen,
}: {
  dm: DMItem;
  onBack: () => void;
  /** Channel id whose project files to surface. Only dm_channel DMs have one. */
  filesChannelId?: string;
  /** When provided, renders a magnifying-glass button (source gate: dm_channel only). */
  onSearchOpen?: () => void;
}) {
  const { t } = useT("channels");
  const isMobile = useIsMobile();
  const actorType = dm.peer.type === "agent" ? "agent" : "member";
  const memberType = dm.peer.type === "agent" ? "agent" : "user";
  const meta = dm.peer.type === "agent" ? t(($) => $.dm.agent_meta) : t(($) => $.dm.human_meta);
  const peerAvatar = (
    <ActorAvatar
      actorType={actorType}
      actorId={dm.peer.id}
      size={34}
      showStatusDot={dm.peer.type === "agent"}
      profileLink={false}
    />
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
          <ActorProfileTrigger memberType={memberType} memberId={dm.peer.id}>
            {peerAvatar}
          </ActorProfileTrigger>
        </>
      }
      title={
        <ActorProfileTrigger memberType={memberType} memberId={dm.peer.id}>
          <span className="truncate">{dm.peer.name}</span>
        </ActorProfileTrigger>
      }
      meta={meta}
      badges={
        isConversationMuted(dm) ? (
          <MutedIndicator label={t(($) => $.dm.muted_label)} />
        ) : null
      }
      actions={
        <>
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
}: DmConversationProps) {
  const { t } = useT("channels");
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const isMobile = useIsMobile();
  const channelId = dm.id;
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const currentUserName = useAuthStore((s) => s.user?.name ?? null);
  const [{
    openThreadRoot,
    threadDraftEmpty,
    convSearch,
    threadParentHighlightId,
    typingActors,
  }, dispatch] = useReducer(dmChannelReducer, initialDmChannelState);
  const [stoppingTaskId, setStoppingTaskId] = useState<string | null>(null);

  const { mutate: markChannelRead } = useMarkChannelRead();
  const { mutate: markThreadRead } = useMarkChannelThreadRead();
  const sendMessage = useSendChannelMessage();
  const sendThreadMessage = useSendChannelThreadMessage();
  const addChannelReaction = useAddChannelReaction();
  const removeChannelReaction = useRemoveChannelReaction();
  const editChannelMessage = useEditChannelMessage();
  const deleteChannelMessage = useDeleteChannelMessage();
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
  const { uploadWithToast } = useFileUpload(api);

  const {
    data: messagePages,
    isLoading: messagesLoading,
    isError: messagesError,
    refetch: refetchMessages,
    fetchNextPage: fetchOlderMessages,
    hasNextPage: hasOlderMessages,
    isFetchingNextPage: isFetchingOlderMessages,
  } = useInfiniteQuery(channelMessagesPageOptions(channelId));
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
  const threadReplies = useMemo(
    () => {
      const messages = threadPage?.messages ?? [];
      return threadRoot ? messages.filter((msg) => msg.id !== threadRoot.id) : messages;
    },
    [threadPage?.messages, threadRoot],
  );
  const { data: activeTasks = [] } = useQuery(activeChannelTasksOptions(channelId));

  const editorRef = useRef<ContentEditorRef>(null);
  const threadEditorRef = useRef<ContentEditorRef>(null);
  const focusThreadComposerOnOpenRef = useRef(false);
  const draftEmpty = !draft.trim();
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const threadFileInputRef = useRef<HTMLInputElement | null>(null);
  const uploadMapRef = useRef<Map<string, string>>(new Map());
  const threadUploadMapRef = useRef<Map<string, string>>(new Map());
  const typingStartedRef = useRef(false);
  const typingStopTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const typingPulseTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Send idempotency + send lock (#207) — see use-compose-send-intent. DM
  // top-level and thread reply each own an independent intent.
  const dmSend = useComposerSend();
  const threadSend = useComposerSend();

  // Agents surface lifecycle via the query-driven working indicator, so filter
  // them out of the transient typing render (same rule as the group thread).
  const activeTypingActors = useMemo(
    () => Object.values(typingActors).filter((a) => isTypingActorVisible(a.actorType)),
    [typingActors],
  );

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
  const highlightMessageId = threadParentHighlightId ?? searchHighlightId;

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

  // Mark read on open and keep it read while viewing.
  useEffect(() => {
    if (channelId) markChannelRead(channelId);
  }, [channelId, markChannelRead]);

  useEffect(() => {
    dispatch({ type: "resetForChannel" });
  }, [channelId]);

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

  // Expire stale typing pulses.
  useEffect(() => {
    const timer = window.setInterval(() => {
      const now = Date.now();
      dispatch({ type: "expireTypingActors", now });
    }, 1000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    typingStartedRef.current = false;
    if (typingStopTimerRef.current) window.clearTimeout(typingStopTimerRef.current);
    if (typingPulseTimerRef.current) window.clearTimeout(typingPulseTimerRef.current);
  }, [channelId]);

  useWSEvent("channel:message", (payload) => {
    const e = payload as { channel_id?: string };
    if (e.channel_id !== channelId) return;
    qc.invalidateQueries({ queryKey: activeChannelTasksKeys.all(channelId) });
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
    markChannelRead(channelId);
  });

  useWSEvent("task:cancelled", (payload) => {
    const e = payload as { chat_session_id?: string };
    if (!e.chat_session_id) return;
    qc.invalidateQueries({ queryKey: activeChannelTasksKeys.all(channelId) });
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
  });

  useWSEvent("channel:typing", (payload) => {
    const event = payload as ChannelTypingPayload;
    if (!event.channel_id || event.channel_id !== channelId) return;
    qc.invalidateQueries({ queryKey: activeChannelTasksKeys.all(channelId) });
    const actorKey = `${event.actor_type}:${event.actor_id ?? event.actor_name}`;
    if (event.actor_type === "user" && event.actor_id && event.actor_id === currentUserId) return;
    if (!event.is_typing) {
      dispatch({ type: "removeTypingActor", actorKey });
      return;
    }
    dispatch({
      type: "upsertTypingActor",
      actor: {
        key: actorKey,
        channelId: event.channel_id,
        actorName: event.actor_name,
        actorType: event.actor_type,
        expiresAt: Date.now() + (event.expires_in_ms ?? 5000),
      },
    });
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

  const handleStopTask = useCallback(async (task: ChannelActiveTask) => {
    setStoppingTaskId(task.task_id);
    try {
      await api.cancelTaskById(task.task_id);
      toast.success(t(($) => $.agent_status.stop_success, { name: task.agent_name }));
      qc.invalidateQueries({ queryKey: activeChannelTasksKeys.all(channelId) });
      qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
    } catch {
      toast.error(t(($) => $.agent_status.stop_failed));
    } finally {
      setStoppingTaskId((current) => (current === task.task_id ? null : current));
    }
  }, [channelId, qc, t, wsId]);

  const handleUpload = useCallback(
    async (file: File): Promise<UploadResult | null> => {
      const result = await uploadWithToast(file, { channelId });
      if (result) {
        uploadMapRef.current.set(result.markdownLink || result.link, result.id);
      }
      return result;
    },
    [channelId, uploadWithToast],
  );

  const handlePickFiles = (files: FileList | null) => {
    if (!files) return;
    for (const file of Array.from(files)) editorRef.current?.uploadFile(file);
  };

  const handleThreadUpload = useCallback(
    async (file: File): Promise<UploadResult | null> => {
      const result = await uploadWithToast(file, { channelId });
      if (result) {
        threadUploadMapRef.current.set(result.markdownLink || result.link, result.id);
      }
      return result;
    },
    [channelId, uploadWithToast],
  );

  const handlePickThreadFiles = (files: FileList | null) => {
    if (!files) return;
    for (const file of Array.from(files)) threadEditorRef.current?.uploadFile(file);
  };

  const handleSend = () => {
    const content = editorRef.current?.getMarkdown()?.trim();
    // Empty-content early-return before the send lock: after a send succeeds the
    // editor is cleared, so a still-held Enter grabs empty content and stops here.
    if (!content) return;
    if (editorRef.current?.hasActiveUploads()) return;
    const attachmentIds: string[] = [];
    for (const [url, id] of uploadMapRef.current) {
      if (content.includes(url)) attachmentIds.push(id);
    }
    // Stop typing before the send lock so a dropped (held-Enter) trigger still
    // clears the indicator.
    const dispatched = dmSend.send({
      payloadKey: composePayloadKey(content, attachmentIds),
      buildVars: (clientMessageId) => ({
        channelId,
        content,
        attachmentIds: attachmentIds.length > 0 ? attachmentIds : undefined,
        clientMessageId,
      }),
      mutate: sendMessage.mutate,
      onCommitted: () => {
        editorRef.current?.clearContent();
        uploadMapRef.current.clear();
        onDraftClear?.();
      },
      // 200-dedup is silent (handled by onCommitted); 409/other always surface,
      // so a silent conflict isn't mistaken for a sent message.
      onVisibleError: () => toast.error(t(($) => $.composer.send_failed)),
    });
    if (dispatched && typingStartedRef.current) {
      typingStartedRef.current = false;
      publishTyping(false);
    }
  };

  const handleThreadSend = () => {
    const content = threadEditorRef.current?.getMarkdown()?.trim();
    if (!content || !threadRoot) return;
    if (threadEditorRef.current?.hasActiveUploads()) return;
    const attachmentIds: string[] = [];
    for (const [url, id] of threadUploadMapRef.current) {
      if (content.includes(url)) attachmentIds.push(id);
    }
    threadSend.send({
      payloadKey: composePayloadKey(content, attachmentIds, threadRoot.id),
      buildVars: (clientMessageId) => ({
        channelId,
        messageId: threadRoot.id,
        content,
        attachmentIds: attachmentIds.length > 0 ? attachmentIds : undefined,
        clientMessageId,
      }),
      mutate: sendThreadMessage.mutate,
      onCommitted: () => {
        threadEditorRef.current?.clearContent();
        threadUploadMapRef.current.clear();
        dispatch({ type: "setThreadDraftEmpty", empty: true });
      },
      onVisibleError: () => toast.error(t(($) => $.thread.send_failed)),
    });
  };

  const handleOpenThread = (message: ChannelMessage) => {
    focusThreadComposerOnOpenRef.current = true;
    dispatch({ type: "openThread", message });
  };

  const threadPanel =
    threadRoot ? (
      <div className="flex h-full min-h-0 min-w-0 flex-col bg-background">
        <ConversationHeader
          isMobile={isMobile}
          leading={
            <span className="flex size-8 items-center justify-center rounded-md bg-muted text-muted-foreground">
              <MessageSquare className="size-4" />
            </span>
          }
          title={t(($) => $.thread.title)}
          meta={
            threadReplies.length > 0
              ? t(($) => $.thread.meta_count, {
                  count: threadReplies.length,
                })
              : undefined
          }
          actions={
            <>
              <Button
                variant="ghost"
                size="icon"
                className="size-8"
                aria-label={t(($) => $.thread.close_aria)}
                onClick={() => dispatch({ type: "closeThread" })}
              >
                <X className="size-4" />
              </Button>
            </>
          }
        />
        <ChannelMessageList
          key={`dm-thread:${threadRoot.id}:${threadLoading ? "loading" : "ready"}`}
          messages={threadReplies}
          currentUserId={currentUserId}
          ownName={currentUserName ?? undefined}
          emptyLabel={t(($) => $.thread.empty_replies)}
          initialScroll="top"
          header={
            <ThreadRootPreview
              message={threadRoot}
              currentUserId={currentUserId}
              ownName={currentUserName ?? undefined}
              onViewParent={() => {
                dispatch({ type: "setThreadParentHighlightId", id: threadRoot.id });
                if (isMobile) dispatch({ type: "closeThread" });
              }}
            />
          }
          loading={threadLoading}
          loadErrorLabel={threadError ? t(($) => $.thread.load_failed) : undefined}
          onRetry={() => refetchThread()}
          onReact={handleReactToMessage}
          onEditMessage={handleEditMessage}
          onDeleteMessage={handleDeleteMessage}
        />
        <ConversationActivityStrip
          tasks={activeTasks}
          stoppingTaskId={stoppingTaskId}
          onStopTask={handleStopTask}
        />
        <Composer
          surface="thread"
          sendLabel={t(($) => $.composer.send)}
          sendDisabled={threadDraftEmpty}
          sending={sendThreadMessage.isPending}
          onSend={handleThreadSend}
          isMobile={isMobile}
          editor={
            <ContentEditor
              key={`dm-thread-editor:${threadRoot.id}`}
              ref={threadEditorRef}
              placeholder={t(($) => $.thread.composer_placeholder)}
              onUpdate={handleThreadEditorUpdate}
              onSubmit={handleThreadSend}
              onUploadFile={handleThreadUpload}
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
        onReact={handleReactToMessage}
        onEditMessage={handleEditMessage}
        onDeleteMessage={handleDeleteMessage}
      />
      <ConversationActivityStrip
        typingActors={activeTypingActors}
        tasks={activeTasks}
        stoppingTaskId={stoppingTaskId}
        onStopTask={handleStopTask}
      />
      <Composer
        surface="dm_channel"
        sendLabel={t(($) => $.composer.send)}
        sendDisabled={draftEmpty}
        sending={sendMessage.isPending}
        onSend={handleSend}
        isMobile={isMobile}
        editor={
            <ContentEditor
              key={channelId}
              ref={editorRef}
              defaultValue={draft}
              placeholder={t(($) => $.dm.composer_placeholder, { name: dm.peer.name })}
              onUpdate={handleEditorUpdate}
              debounceMs={0}
              onSubmit={handleSend}
              onUploadFile={handleUpload}
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
              <Button
                variant="ghost"
                size="icon"
                className={cn(isMobile ? "size-10" : "size-8")}
                aria-label={t(($) => $.composer.issue_ref_aria)}
                onClick={() => editorRef.current?.openIssueReferences()}
              >
                <Hash className={cn(isMobile ? "size-5" : "size-4")} />
              </Button>
          </>
        }
      />
      {isMobile && (
        <Drawer
          open={!!threadPanel}
          onOpenChange={(open) => {
            if (!open) dispatch({ type: "closeThread" });
          }}
        >
          <MobileThreadDrawerContent open={!!threadPanel}>
            {threadPanel}
          </MobileThreadDrawerContent>
        </Drawer>
      )}
    </main>
  );

  if (!isMobile) {
    return (
      <ResizablePanelGroup orientation="horizontal" className="min-h-0 flex-1">
        <ResizablePanel id="dm-conversation" minSize="50%" className="flex min-h-0 flex-col">
          {conversationPane}
        </ResizablePanel>
        {threadPanel ? (
          <>
            <ResizableHandle />
            <ResizablePanel
              id="dm-thread"
              defaultSize={440}
              minSize={360}
              maxSize={640}
              groupResizeBehavior="preserve-pixel-size"
              className="border-l border-border/30 bg-background"
            >
              {threadPanel}
            </ResizablePanel>
          </>
        ) : null}
      </ResizablePanelGroup>
    );
  }

  return conversationPane;
}
