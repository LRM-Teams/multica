"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ArrowLeft, ChevronDown, ChevronUp, FileText, Paperclip, Search, Send, X } from "lucide-react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  activeChannelTasksKeys,
  activeChannelTasksOptions,
  channelKeys,
  channelMessagesOptions,
  useMarkChannelRead,
  useSendChannelMessage,
  useSetChannelTyping,
} from "@multica/core/channels";
import {
  chatKeys,
  chatMessagesPageOptions,
  pendingChatTaskOptions,
} from "@multica/core/chat/queries";
import { useMarkChatSessionRead } from "@multica/core/chat/mutations";
import { useChatStore } from "@multica/core/chat";
import { dmKeys } from "@multica/core/dm";
import type { DMItem } from "@multica/core/dm";
import { useAgentPresenceDetail } from "@multica/core/agents";
import { useAuthStore } from "@multica/core/auth";
import { api } from "@multica/core/api";
import { useFileUpload, type UploadResult } from "@multica/core/hooks/use-file-upload";
import { useWorkspaceId } from "@multica/core/hooks";
import { useCurrentWorkspace } from "@multica/core/paths";
import { useWSEvent } from "@multica/core/realtime";
import type { ChannelMessageSearchResult, ChannelTypingPayload, ChatMessage } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { toast } from "sonner";
import { ContentEditor, type ContentEditorRef } from "../../editor";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n";
import { ChatMessageList } from "../../chat/components/chat-message-list";
import { ChatInput } from "../../chat/components/chat-input";
import { ChannelMessageList } from "./channel-message-list";
import { ChannelFilesPanel } from "./channel-files-panel";
import {
  AgentWorkingIndicator,
  TypingIndicator,
  type TypingActor,
} from "./channels-page";

/**
 * DM detail pane. A DM routes entirely by `source`:
 *  - `dm_channel`    — the DM IS a kind='dm' channel, so we reuse the exact
 *    channel conversation stack (ChannelMessageBubble + ContentEditor composer
 *    + AgentWorkingIndicator + TypingIndicator + channel queries/mutations +
 *    channel:message / channel:typing WS).
 *  - `legacy_session` — a pre-existing standalone chat_session, so we reuse the
 *    chat-window internals (ChatMessageList + ChatInput + chat queries +
 *    chat:message WS, driven through the chat store).
 *
 * The DM header chrome differs from the group header: peer avatar + name (+
 * agent presence dot) and Files only — no stats, no share, no member
 * management, no delete.
 */
export function DmConversation({ dm, onBack }: { dm: DMItem; onBack: () => void }) {
  if (dm.source === "legacy_session") {
    return <DmLegacyConversation dm={dm} onBack={onBack} />;
  }
  return <DmChannelConversation dm={dm} onBack={onBack} />;
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

  return (
    <header
      className={cn(
        "flex items-center justify-between gap-3 border-b py-2.5",
        isMobile ? "px-2" : "px-5",
      )}
    >
      <div className={cn("flex min-w-0 items-center", isMobile ? "gap-2" : "gap-3")}>
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
        <ActorAvatar
          actorType={actorType}
          actorId={dm.peer.id}
          size={40}
          showStatusDot={dm.peer.type === "agent"}
          profileLink={false}
        />
        <div className="min-w-0">
          <div className="truncate font-semibold">{dm.peer.name}</div>
        </div>
      </div>
      <div className="flex shrink-0 items-center gap-1 text-muted-foreground">
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
      </div>
    </header>
  );
}

// ─── dm_channel: reuse the channel conversation stack ──────────────────────

function DmChannelConversation({ dm, onBack }: { dm: DMItem; onBack: () => void }) {
  const { t } = useT("channels");
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const isMobile = useIsMobile();
  const channelId = dm.id;
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const currentUserName = useAuthStore((s) => s.user?.name ?? null);

  const { mutate: markChannelRead } = useMarkChannelRead();
  const sendMessage = useSendChannelMessage();
  const setTyping = useSetChannelTyping();
  const { uploadWithToast } = useFileUpload(api);

  const { data: messages = [] } = useQuery(channelMessagesOptions(channelId));
  const { data: activeTasks = [] } = useQuery(activeChannelTasksOptions(channelId));

  const editorRef = useRef<ContentEditorRef>(null);
  const [draftEmpty, setDraftEmpty] = useState(true);
  const [convSearchOpen, setConvSearchOpen] = useState(false);
  const [convSearchQuery, setConvSearchQuery] = useState("");
  const [convSearchResults, setConvSearchResults] = useState<ChannelMessageSearchResult[]>([]);
  const [convSearchTotal, setConvSearchTotal] = useState(0);
  const [convSearchIndex, setConvSearchIndex] = useState(0);
  const [typingActors, setTypingActors] = useState<Record<string, TypingActor>>({});
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const uploadMapRef = useRef<Map<string, string>>(new Map());
  const typingStartedRef = useRef(false);
  const typingStopTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const typingPulseTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Agents surface lifecycle via the query-driven working indicator, so filter
  // them out of the transient typing render (same rule as the group thread).
  const activeTypingActors = useMemo(
    () => Object.values(typingActors).filter((a) => a.actorType !== "agent"),
    [typingActors],
  );

  // 1-on-1 DM: scope the composer's @-mention picker to the peer only. Without
  // an allowlist the picker defaults to the whole workspace, which is wrong for
  // a 1-on-1 (only the peer is reachable here).
  const mentionAllowedActorIds = useMemo(() => new Set([dm.peer.id]), [dm.peer.id]);

  const searchHitIds = useMemo(
    () =>
      convSearchOpen && convSearchResults.length > 0
        ? new Set(convSearchResults.map((r) => r.message_id))
        : undefined,
    [convSearchOpen, convSearchResults],
  );
  const searchHighlightQuery = convSearchOpen ? convSearchQuery.trim() : "";
  const searchHighlightId = convSearchOpen
    ? (convSearchResults[convSearchIndex]?.message_id ?? null)
    : null;

  // Debounced in-conversation search.
  useEffect(() => {
    if (!convSearchOpen) return;
    const q = convSearchQuery.trim();
    if (!q) {
      setConvSearchResults([]);
      setConvSearchTotal(0);
      setConvSearchIndex(0);
      return;
    }
    const timer = setTimeout(async () => {
      try {
        const res = await api.searchChannelMessages(channelId, q);
        setConvSearchResults(res.results);
        setConvSearchTotal(res.total);
        setConvSearchIndex(0);
      } catch {
        toast.error(t(($) => $.conv_search.error));
      }
    }, 300);
    return () => clearTimeout(timer);
  }, [convSearchQuery, channelId, convSearchOpen]);

  // Bottom-stick on new messages and open-at-latest on switch are handled by
  // ChannelMessageList (react-virtuoso). No manual scrollIntoView needed.

  // Mark read on open and keep it read while viewing.
  useEffect(() => {
    if (channelId) markChannelRead(channelId);
  }, [channelId, markChannelRead]);

  // Expire stale typing pulses.
  useEffect(() => {
    const timer = window.setInterval(() => {
      const now = Date.now();
      setTypingActors((current) => {
        const next = Object.fromEntries(
          Object.entries(current).filter(([, a]) => a.expiresAt > now),
        );
        return Object.keys(next).length === Object.keys(current).length ? current : next;
      });
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
    qc.invalidateQueries({ queryKey: channelKeys.messages(channelId) });
    qc.invalidateQueries({ queryKey: activeChannelTasksKeys.all(channelId) });
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
    markChannelRead(channelId);
  });

  useWSEvent("channel:typing", (payload) => {
    const event = payload as ChannelTypingPayload;
    if (!event.channel_id || event.channel_id !== channelId) return;
    qc.invalidateQueries({ queryKey: activeChannelTasksKeys.all(channelId) });
    const actorKey = `${event.actor_type}:${event.actor_id ?? event.actor_name}`;
    if (event.actor_type === "user" && event.actor_id && event.actor_id === currentUserId) return;
    setTypingActors((current) => {
      if (!event.is_typing) {
        const next = { ...current };
        delete next[actorKey];
        return next;
      }
      return {
        ...current,
        [actorKey]: {
          key: actorKey,
          channelId: event.channel_id,
          actorName: event.actor_name,
          actorType: event.actor_type,
          expiresAt: Date.now() + (event.expires_in_ms ?? 5000),
        },
      };
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
    setDraftEmpty(!value.trim());
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

  const handleSend = () => {
    const content = editorRef.current?.getMarkdown()?.trim();
    if (!content) return;
    if (editorRef.current?.hasActiveUploads()) return;
    const attachmentIds: string[] = [];
    for (const [url, id] of uploadMapRef.current) {
      if (content.includes(url)) attachmentIds.push(id);
    }
    if (typingStartedRef.current) {
      typingStartedRef.current = false;
      publishTyping(false);
    }
    sendMessage.mutate(
      {
        channelId,
        content,
        attachmentIds: attachmentIds.length > 0 ? attachmentIds : undefined,
      },
      {
        onSuccess: () => {
          editorRef.current?.clearContent();
          uploadMapRef.current.clear();
          setDraftEmpty(true);
        },
      },
    );
  };

  return (
    <main className="flex flex-1 min-h-0 min-w-0 flex-col">
      <DmHeader
        dm={dm}
        onBack={onBack}
        filesChannelId={channelId}
        onSearchOpen={() => setConvSearchOpen(true)}
      />
      {convSearchOpen && (
        <div
          className={cn(
            "flex items-center gap-2 border-b bg-muted/20 py-2",
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
            value={convSearchQuery}
            onChange={(e) => setConvSearchQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") {
                setConvSearchOpen(false);
                setConvSearchQuery("");
                setConvSearchResults([]);
                setConvSearchTotal(0);
                setConvSearchIndex(0);
              }
            }}
            placeholder={t(($) => $.conv_search.dm_placeholder, { name: dm.peer.name })}
            className="h-8 min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
          />
          {convSearchQuery.trim() && (
            <span className="shrink-0 text-xs text-muted-foreground">
              {convSearchTotal === 0
                ? t(($) => $.conv_search.no_results)
                : t(($) => $.conv_search.result_count, {
                    current: convSearchIndex + 1,
                    total: convSearchTotal,
                  })}
            </span>
          )}
          <Button
            variant="ghost"
            size="icon"
            className="size-7"
            aria-label={t(($) => $.conv_search.prev_aria)}
            onClick={() => setConvSearchIndex((i) => Math.max(0, i - 1))}
            disabled={convSearchTotal === 0 || convSearchIndex === 0}
          >
            <ChevronUp className="size-4" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="size-7"
            aria-label={t(($) => $.conv_search.next_aria)}
            onClick={() => setConvSearchIndex((i) => Math.min(convSearchTotal - 1, i + 1))}
            disabled={convSearchTotal === 0 || convSearchIndex >= convSearchTotal - 1}
          >
            <ChevronDown className="size-4" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="size-7"
            aria-label={t(($) => $.conv_search.close_aria)}
            onClick={() => {
              setConvSearchOpen(false);
              setConvSearchQuery("");
              setConvSearchResults([]);
              setConvSearchTotal(0);
              setConvSearchIndex(0);
            }}
          >
            <X className="size-4" />
          </Button>
        </div>
      )}
      <ChannelMessageList
        key={channelId}
        messages={messages}
        currentUserId={currentUserId}
        ownName={currentUserName ?? undefined}
        highlightMessageId={searchHighlightId}
        searchHitIds={searchHitIds}
        searchQuery={searchHighlightQuery}
        emptyLabel={t(($) => $.dm.thread_empty)}
        footer={<TypingIndicator actors={activeTypingActors} />}
      />
      <div className="px-4 pb-4">
        <AgentWorkingIndicator tasks={activeTasks} />
        <div className="rounded-xl border bg-card shadow-sm">
          <div className="max-h-40 min-h-16 overflow-y-auto px-4 pt-3">
            <ContentEditor
              key={channelId}
              ref={editorRef}
              placeholder={t(($) => $.composer.placeholder)}
              onUpdate={handleEditorUpdate}
              onSubmit={handleSend}
              onUploadFile={handleUpload}
              mentionAllowedActorIds={mentionAllowedActorIds}
              submitOnEnter
              showBubbleMenu={false}
            />
          </div>
          <div className="flex items-center justify-between px-2 pb-2">
            <div className="flex items-center gap-0.5 text-muted-foreground">
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
            </div>
            <Button
              onClick={handleSend}
              disabled={draftEmpty || sendMessage.isPending}
              size="sm"
              className={cn(isMobile && "min-h-10 px-4")}
            >
              <Send className="size-4" /> {t(($) => $.composer.send)}
            </Button>
          </div>
        </div>
      </div>
    </main>
  );
}

// ─── legacy_session: reuse the chat-window internals via the chat store ────

function DmLegacyConversation({ dm, onBack }: { dm: DMItem; onBack: () => void }) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const ws = useCurrentWorkspace();
  const sessionId = dm.id;
  const agentId = dm.peer.id;

  const setActiveSession = useChatStore((s) => s.setActiveSession);
  const setSelectedAgentId = useChatStore((s) => s.setSelectedAgentId);
  const { mutate: markRead } = useMarkChatSessionRead();

  // Drive the chat store so ChatInput's per-session draft keying and send path
  // target this legacy session. The chat store is the single source of truth
  // these reused pieces read from. We restore nothing on unmount — the chat
  // window is no longer mounted as an open surface, so leaving activeSessionId
  // set is harmless and keeps the legacy draft scoped to the session.
  useEffect(() => {
    setSelectedAgentId(agentId);
    setActiveSession(sessionId);
  }, [agentId, sessionId, setActiveSession, setSelectedAgentId]);

  const {
    data: rawMessagePages,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
  } = useInfiniteQuery(chatMessagesPageOptions(sessionId));
  const messagePages = rawMessagePages?.pages ?? [];
  const messages: ChatMessage[] = [...messagePages]
    .reverse()
    .flatMap((page) => page.messages);

  const { data: pendingTask } = useQuery(pendingChatTaskOptions(sessionId));
  const pendingTaskId = pendingTask?.task_id ?? null;

  // Agent presence drives the header dot and could feed the status pill; pass
  // undefined while loading so reused copy stays neutral.
  const presenceDetail = useAgentPresenceDetail(ws?.id, agentId);
  const availability =
    presenceDetail === "loading" ? undefined : presenceDetail.availability;

  // Mark read on open and on inbound replies.
  useEffect(() => {
    markRead(sessionId);
  }, [sessionId, markRead]);

  useWSEvent("chat:message", (payload) => {
    const e = payload as { chat_session_id?: string };
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
    if (e.chat_session_id === sessionId) {
      qc.invalidateQueries({ queryKey: chatKeys.messagesPage(sessionId) });
      qc.invalidateQueries({ queryKey: chatKeys.pendingTask(sessionId) });
      markRead(sessionId);
    }
  });

  const { uploadWithToast } = useFileUpload(api);
  const handleUploadFile = useCallback(
    (file: File) => uploadWithToast(file, { chatSessionId: sessionId }),
    [sessionId, uploadWithToast],
  );

  const handleSend = useCallback(
    async (content: string, attachmentIds?: string[]): Promise<boolean> => {
      try {
        await api.sendChatMessage(sessionId, content, attachmentIds);
      } catch {
        toast.error("Failed to send");
        return false;
      }
      qc.invalidateQueries({ queryKey: chatKeys.messagesPage(sessionId) });
      qc.invalidateQueries({ queryKey: chatKeys.pendingTask(sessionId) });
      qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
      return true;
    },
    [sessionId, qc, wsId],
  );

  return (
    <main className="flex flex-1 min-h-0 min-w-0 flex-col">
      <DmHeader dm={dm} onBack={onBack} />
      <ChatMessageList
        key={sessionId}
        messages={messages}
        pendingTask={pendingTask}
        availability={availability}
        hasOlderMessages={!!hasNextPage}
        isFetchingOlderMessages={isFetchingNextPage}
        onLoadOlderMessages={() => void fetchNextPage()}
      />
      <ChatInput
        onSend={handleSend}
        onUploadFile={handleUploadFile}
        isRunning={!!pendingTaskId}
        wsId={wsId}
        sessionId={sessionId}
        agentName={dm.peer.name}
      />
    </main>
  );
}
