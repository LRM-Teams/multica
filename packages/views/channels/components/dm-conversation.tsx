"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ArrowLeft, ChevronDown, ChevronUp, FileText, Hash, MessageSquare, Paperclip, Search, X } from "lucide-react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  activeChannelTasksKeys,
  activeChannelTasksOptions,
  channelMessageThreadOptions,
  channelKeys,
  channelMessagesOptions,
  useMarkChannelThreadRead,
  useMarkChannelRead,
  useSendChannelMessage,
  useSendChannelThreadMessage,
  useAddChannelReaction,
  useRemoveChannelReaction,
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
import type { ChannelMessage, ChannelMessageSearchResult, ChannelTypingPayload, ChatMessage } from "@multica/core/types";
import { UnicodeSpinner } from "@multica/ui/components/common/unicode-spinner";
import { Button } from "@multica/ui/components/ui/button";
import {
  Drawer,
  DrawerContent,
} from "@multica/ui/components/ui/drawer";
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
import { ContentEditor, type ContentEditorRef } from "../../editor";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { useT } from "../../i18n";
import { ChatMessageList } from "../../chat/components/chat-message-list";
import { ChatInput } from "../../chat/components/chat-input";
import { ChannelMessageBubble } from "./channel-message-bubble";
import { ChannelMessageList } from "./channel-message-list";
import { ChannelFilesPanel } from "./channel-files-panel";
import { ChannelComposer, ConversationHeader } from "./conversation-surface";
import {
  AgentWorkingIndicator,
  TypingIndicator,
  type TypingActor,
} from "./channels-page";
import { isConversationMuted, MutedIndicator } from "./conversation-muted";

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

function DmChannelConversation({ dm, onBack }: { dm: DMItem; onBack: () => void }) {
  const { t } = useT("channels");
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const isMobile = useIsMobile();
  const channelId = dm.id;
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const currentUserName = useAuthStore((s) => s.user?.name ?? null);

  const { mutate: markChannelRead } = useMarkChannelRead();
  const { mutate: markThreadRead } = useMarkChannelThreadRead();
  const sendMessage = useSendChannelMessage();
  const sendThreadMessage = useSendChannelThreadMessage();
  const addChannelReaction = useAddChannelReaction();
  const removeChannelReaction = useRemoveChannelReaction();
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

  const { data: messages = [] } = useQuery(channelMessagesOptions(channelId));
  const [openThreadRoot, setOpenThreadRoot] = useState<ChannelMessage | null>(null);
  const threadRoot =
    openThreadRoot && openThreadRoot.channel_id === channelId
      ? messages.find((m) => m.id === openThreadRoot.id) ?? openThreadRoot
      : null;
  const { data: threadPage, isLoading: threadLoading, isError: threadError } = useQuery(
    channelMessageThreadOptions(channelId, threadRoot?.id ?? ""),
  );
  const threadMessages = threadPage?.messages ?? [];
  const { data: activeTasks = [] } = useQuery(activeChannelTasksOptions(channelId));

  const editorRef = useRef<ContentEditorRef>(null);
  const threadEditorRef = useRef<ContentEditorRef>(null);
  const [draftEmpty, setDraftEmpty] = useState(true);
  const [threadDraftEmpty, setThreadDraftEmpty] = useState(true);
  const [convSearchOpen, setConvSearchOpen] = useState(false);
  const [convSearchQuery, setConvSearchQuery] = useState("");
  const [convSearchResults, setConvSearchResults] = useState<ChannelMessageSearchResult[]>([]);
  const [convSearchTotal, setConvSearchTotal] = useState(0);
  const [convSearchIndex, setConvSearchIndex] = useState(0);
  const [threadParentHighlightId, setThreadParentHighlightId] = useState<string | null>(null);
  const [typingActors, setTypingActors] = useState<Record<string, TypingActor>>({});
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const threadFileInputRef = useRef<HTMLInputElement | null>(null);
  const uploadMapRef = useRef<Map<string, string>>(new Map());
  const threadUploadMapRef = useRef<Map<string, string>>(new Map());
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
  const highlightMessageId = threadParentHighlightId ?? searchHighlightId;

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

  useEffect(() => {
    setOpenThreadRoot(null);
    setThreadDraftEmpty(true);
    setThreadParentHighlightId(null);
  }, [channelId]);

  useEffect(() => {
    if (!threadRoot) return;
    markThreadRead({ channelId, messageId: threadRoot.id });
  }, [channelId, threadRoot?.id, markThreadRead]);

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

  const handleThreadEditorUpdate = (value: string) => {
    setThreadDraftEmpty(!value.trim());
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

  const handleThreadSend = () => {
    const content = threadEditorRef.current?.getMarkdown()?.trim();
    if (!content || !threadRoot) return;
    if (threadEditorRef.current?.hasActiveUploads()) return;
    const attachmentIds: string[] = [];
    for (const [url, id] of threadUploadMapRef.current) {
      if (content.includes(url)) attachmentIds.push(id);
    }
    sendThreadMessage.mutate(
      {
        channelId,
        messageId: threadRoot.id,
        content,
        attachmentIds: attachmentIds.length > 0 ? attachmentIds : undefined,
      },
      {
        onSuccess: () => {
          threadEditorRef.current?.clearContent();
          threadUploadMapRef.current.clear();
          setThreadDraftEmpty(true);
        },
        onError: () => {
          toast.error(t(($) => $.thread.send_failed));
        },
      },
    );
  };

  const threadPanel =
    threadRoot ? (
      <div className="flex h-full min-h-0 flex-col bg-background">
        <ConversationHeader
          isMobile={isMobile}
          leading={
            <span className="flex size-8 items-center justify-center rounded-md bg-muted text-muted-foreground">
              <MessageSquare className="size-4" />
            </span>
          }
          title={t(($) => $.thread.title)}
          meta={t(($) => $.thread.meta_count, {
            count: threadMessages.length,
          })}
          actions={
            <>
              <Button
                variant="ghost"
                size="sm"
                className="h-8 px-2 text-xs"
                onClick={() => {
                  setThreadParentHighlightId(threadRoot.id);
                  if (isMobile) setOpenThreadRoot(null);
                }}
              >
                {t(($) => $.thread.view_parent)}
              </Button>
              <Button
                variant="ghost"
                size="icon"
                className="size-8"
                aria-label={t(($) => $.thread.close_aria)}
                onClick={() => setOpenThreadRoot(null)}
              >
                <X className="size-4" />
              </Button>
            </>
          }
        />
        <div className="px-5 pt-3">
          <ChannelMessageBubble
            message={threadRoot}
            currentUserId={currentUserId}
            ownName={currentUserName ?? undefined}
            onReact={handleReactToMessage}
            onScrollTo={(messageId) => {
              setThreadParentHighlightId(messageId);
              if (isMobile) setOpenThreadRoot(null);
            }}
          />
        </div>
        {threadError ? (
          <div className="flex flex-1 items-center justify-center px-5 text-sm text-muted-foreground">
            {t(($) => $.thread.load_failed)}
          </div>
        ) : threadLoading ? (
          <div className="flex flex-1 items-center justify-center">
            <UnicodeSpinner className="size-5 text-muted-foreground" />
          </div>
        ) : (
          <ChannelMessageList
            key={`dm-thread:${threadRoot.id}`}
            messages={threadMessages}
            currentUserId={currentUserId}
            ownName={currentUserName ?? undefined}
            emptyLabel={t(($) => $.thread.empty_replies)}
            onReact={handleReactToMessage}
          />
        )}
        <ChannelComposer
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
        onSearchOpen={() => setConvSearchOpen(true)}
      />
      {convSearchOpen && (
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
        highlightMessageId={highlightMessageId}
        searchHitIds={searchHitIds}
        searchQuery={searchHighlightQuery}
        emptyLabel={t(($) => $.dm.thread_empty)}
        footer={<TypingIndicator actors={activeTypingActors} />}
        onOpenThread={setOpenThreadRoot}
        onReact={handleReactToMessage}
      />
      <div className="px-5">
        <AgentWorkingIndicator tasks={activeTasks} />
      </div>
      <ChannelComposer
        sendLabel={t(($) => $.composer.send)}
        sendDisabled={draftEmpty}
        sending={sendMessage.isPending}
        onSend={handleSend}
        isMobile={isMobile}
        editor={
            <ContentEditor
              key={channelId}
              ref={editorRef}
              placeholder={t(($) => $.dm.composer_placeholder, { name: dm.peer.name })}
              onUpdate={handleEditorUpdate}
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
            if (!open) setOpenThreadRoot(null);
          }}
        >
          <DrawerContent className="h-[90vh] p-0">
            {threadPanel}
          </DrawerContent>
        </Drawer>
      )}
    </main>
  );

  if (!isMobile && threadPanel) {
    return (
      <ResizablePanelGroup orientation="horizontal" className="min-h-0 flex-1">
        <ResizablePanel id="dm-conversation" minSize="50%" className="flex min-h-0 flex-col">
          {conversationPane}
        </ResizablePanel>
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
      </ResizablePanelGroup>
    );
  }

  return conversationPane;
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
