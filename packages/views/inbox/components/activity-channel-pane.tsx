"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { ArrowLeft, Hash, Paperclip } from "lucide-react";
import {
  channelMembersOptions,
  channelMessagesFirstItemIndex,
  channelMessagesPageOptions,
  channelsOptions,
  flattenChannelMessagePages,
  useAddChannelReaction,
  useEnsureMessageLoaded,
  useRemoveChannelReaction,
  useSendChannelMessage,
} from "@multica/core/channels";
import { useAuthStore } from "@multica/core/auth";
import { api } from "@multica/core/api";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
import { useWorkspaceId } from "@multica/core/hooks";
import type { ChannelMessage } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { toast } from "sonner";
import { ContentEditor, type ContentEditorRef } from "../../editor/content-editor";
import { useT } from "../../i18n";
import { composePayloadKey } from "../../channels/hooks/use-compose-send-intent";
import { useComposerSend } from "../../channels/hooks/use-composer-send";
import {
  buildChatMessageParts,
  useComposerPendingAttachments,
} from "../../channels/hooks/use-composer-pending-attachments";
import { useEntryAnchor } from "../../channels/hooks/use-entry-around-seq";
import { ChannelMessageList } from "../../channels/components/channel-message-list";
import { Composer, ConversationHeader } from "../../channels/components/conversation-surface";
import { ComposerAttachmentTray } from "../../channels/components/composer-attachment-tray";
import { ComposerQuotePreview } from "../../channels/components/message-quote";
import type { QuoteTarget } from "../../channels/components/message-quote-types";

const HIGHLIGHT_FLASH_MS = 2500;

/**
 * Activity right-pane full channel stream (LRM-388 / LRM-374 ①).
 * Reuses ChannelMessageList virtualization + useEnsureMessageLoaded — no
 * parallel list, no silent miss when the trigger id is outside the window.
 */
export function ActivityChannelPane({
  channelId,
  highlightMessageId,
  channelNameFallback,
  onBack,
  onOpenInMain,
}: {
  channelId: string;
  highlightMessageId: string;
  channelNameFallback?: string | null;
  onBack: () => void;
  onOpenInMain: () => void;
}) {
  const { t } = useT("channels");
  const { t: tInbox } = useT("inbox");
  const isMobile = useIsMobile();
  const wsId = useWorkspaceId();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const currentUserName = useAuthStore((s) => s.user?.name ?? undefined);

  const { data: channels = [], isLoading: channelsLoading } = useQuery(
    channelsOptions(wsId),
  );
  const channel = useMemo(
    () => channels.find((c) => c.id === channelId) ?? null,
    [channels, channelId],
  );
  const channelMissing = !channelsLoading && !channel;

  const entryAnchor = useEntryAnchor(
    channelId,
    channel?.last_read_seq,
    channel?.real_unread_count ?? channel?.unread_count,
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
    channelMessagesPageOptions(channelId, {
      aroundSeq: entryAnchor.aroundSeq,
    }),
  );

  const messages = useMemo(
    () => flattenChannelMessagePages(messagePages),
    [messagePages],
  );
  const messagesFirstItemIndex = useMemo(
    () => channelMessagesFirstItemIndex(messagePages, messages.length > 0),
    [messagePages, messages.length],
  );

  const [activeHighlightId, setActiveHighlightId] = useState<string | null>(
    highlightMessageId,
  );
  useEffect(() => {
    setActiveHighlightId(highlightMessageId);
  }, [highlightMessageId]);

  const jumpTargetLoaded = useMemo(
    () =>
      !!activeHighlightId &&
      messages.some((m) => m.id === activeHighlightId),
    [activeHighlightId, messages],
  );
  const jumpStatus = useEnsureMessageLoaded({
    targetId: activeHighlightId,
    targetLoaded: jumpTargetLoaded,
    hasOlder: !!hasOlderMessages,
    isFetchingOlder: isFetchingOlderMessages,
    fetchOlder: () => {
      void fetchOlderMessages();
    },
  });

  useEffect(() => {
    if (!activeHighlightId || !jumpTargetLoaded) return;
    const timer = window.setTimeout(() => {
      setActiveHighlightId(null);
    }, HIGHLIGHT_FLASH_MS);
    return () => window.clearTimeout(timer);
  }, [activeHighlightId, jumpTargetLoaded]);

  const { data: members = [] } = useQuery(channelMembersOptions(channelId));
  const channelMemberIds = useMemo(
    () => new Set(members.map((m) => m.member_id)),
    [members],
  );
  const channelAgentCandidates = useMemo(
    () => {
      const out: Array<{ id: string; name: string; display_name?: string | null }> = [];
      for (const m of members) {
        if (m.member_type === "agent") {
          out.push({ id: m.member_id, name: m.name, display_name: m.display_name });
        }
      }
      return out;
    },
    [members],
  );

  const sendMessage = useSendChannelMessage();
  const addChannelReaction = useAddChannelReaction();
  const removeChannelReaction = useRemoveChannelReaction();
  const channelSend = useComposerSend();
  const { uploadWithToast } = useFileUpload(api);

  const editorRef = useRef<ContentEditorRef>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const [draftEmpty, setDraftEmpty] = useState(true);
  const [quoteTarget, setQuoteTarget] = useState<QuoteTarget | null>(null);

  const uploadForChannel = useCallback(
    async (file: File) => uploadWithToast(file, { channelId }),
    [channelId, uploadWithToast],
  );
  const channelPending = useComposerPendingAttachments({
    upload: uploadForChannel,
    resetKey: channelId,
  });

  const handleReactToMessage = useCallback(
    (message: ChannelMessage, emoji: string) => {
      const hasReacted = message.reactions?.some(
        (reaction) =>
          reaction.actor_type === "member" &&
          reaction.actor_id === currentUserId &&
          reaction.emoji === emoji,
      );
      const vars = {
        channelId: message.channel_id,
        messageId: message.id,
        emoji,
      };
      if (hasReacted) removeChannelReaction.mutate(vars);
      else addChannelReaction.mutate(vars);
    },
    [addChannelReaction, currentUserId, removeChannelReaction],
  );

  const handleSend = useCallback(() => {
    if (channelPending.hasUploading) return;
    const content = editorRef.current?.getMarkdown()?.trim() ?? "";
    const parts = buildChatMessageParts(
      content,
      channelPending.readyAttachmentParts,
    );
    if (parts.length === 0) return;
    const attachmentIds = channelPending.readyAttachmentParts.map(
      (p) => p.attachment_id,
    );
    const dispatched = channelSend.send({
      payloadKey: composePayloadKey(
        content,
        attachmentIds,
        quoteTarget?.id ?? "",
      ),
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
      editorRef.current?.clearContent();
      channelPending.clear();
      setQuoteTarget(null);
      setDraftEmpty(true);
    }
  }, [
    channelId,
    channelPending,
    channelSend,
    quoteTarget?.id,
    sendMessage.mutate,
    t,
  ]);

  const title =
    channel?.name ?? channelNameFallback ?? tInbox(($) => $.activity.channel_fallback);

  if (channelMissing) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
        <p className="text-sm text-destructive">
          {tInbox(($) => $.activity.open_channel_failed)}
        </p>
        <Button variant="outline" size="sm" onClick={onBack}>
          {tInbox(($) => $.page.back)}
        </Button>
      </div>
    );
  }

  if (channelsLoading && !channel) {
    return (
      <div className="flex h-full flex-col gap-3 p-4">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-4 w-32" />
        <Skeleton className="mt-4 h-24 w-full" />
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <ConversationHeader
        isMobile={isMobile}
        leading={
          <>
            {isMobile ? (
              <Button
                variant="ghost"
                size="icon"
                className="size-10 shrink-0 text-muted-foreground"
                aria-label={t(($) => $.header.back)}
                onClick={onBack}
              >
                <ArrowLeft className="size-5" />
              </Button>
            ) : null}
            <span className="flex size-8 items-center justify-center rounded-md bg-muted text-muted-foreground">
              <Hash className="size-4" />
            </span>
          </>
        }
        title={`#${title}`}
        actions={
          <Button
            variant="ghost"
            size="sm"
            className="h-8 px-2 text-xs text-brand hover:text-brand"
            onClick={onOpenInMain}
          >
            {tInbox(($) => $.activity.open_in_main)}
          </Button>
        }
      />

      {jumpStatus === "exhausted" && (
        <output className="block border-b bg-muted/40 px-5 py-1.5 text-center text-xs text-muted-foreground">
          {t(($) => $.message_loading.jump_not_found)}
        </output>
      )}

      <ChannelMessageList
        key={channelId}
        messages={messages}
        currentUserId={currentUserId}
        ownName={currentUserName}
        highlightMessageId={activeHighlightId}
        lastReadSeq={entryAnchor.aroundSeq ?? undefined}
        unreadCount={
          messagePages?.pages?.[0]?.unread_total ?? entryAnchor.unreadCount
        }
        firstItemIndex={messagesFirstItemIndex}
        loading={messagesLoading}
        loadingOlder={isFetchingOlderMessages}
        hasOlder={!!hasOlderMessages}
        onLoadOlder={() => fetchOlderMessages()}
        loadOlderLabel={t(($) => $.message_loading.load_older)}
        loadingOlderLabel={t(($) => $.message_loading.loading_older)}
        loadErrorLabel={
          messagesError ? t(($) => $.message_loading.load_failed_retry) : undefined
        }
        onRetry={() => refetchMessages()}
        emptyLabel={t(($) => $.thread.empty)}
        onScrollToMessage={setActiveHighlightId}
        onReact={handleReactToMessage}
        onQuoteMessage={setQuoteTarget}
      />

      <Composer
        surface="channel"
        sendLabel={t(($) => $.composer.send)}
        sendDisabled={
          (draftEmpty && channelPending.readyAttachmentParts.length === 0) ||
          channelPending.hasUploading
        }
        sending={sendMessage.isPending}
        onSend={handleSend}
        isMobile={isMobile}
        prefix={
          quoteTarget ? (
            <ComposerQuotePreview
              quote={quoteTarget}
              onCancel={() => setQuoteTarget(null)}
              cancelLabel={t(($) => $.quote.cancel)}
            />
          ) : undefined
        }
        tray={
          <ComposerAttachmentTray
            pending={channelPending.pending}
            onRemove={channelPending.remove}
            onRetry={channelPending.retry}
            isMobile={isMobile}
          />
        }
        editor={
          <ContentEditor
            key={channelId}
            ref={editorRef}
            plainUrls
            placeholder={t(($) => $.composer.placeholder)}
            onUpdate={(md) => setDraftEmpty(!md.trim())}
            debounceMs={0}
            onSubmit={handleSend}
            mediaMode="external"
            onExternalFiles={channelPending.addFiles}
            submitOnEnter
            showBubbleMenu={false}
            mentionAllowedActorIds={channelMemberIds}
            scopedMentionAgents={channelAgentCandidates}
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
                if (e.target.files?.length) {
                  channelPending.addFiles(Array.from(e.target.files));
                }
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
          </>
        }
      />
    </div>
  );
}
