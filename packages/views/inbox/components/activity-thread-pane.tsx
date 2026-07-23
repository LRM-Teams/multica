"use client";

import { useCallback, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Paperclip } from "lucide-react";
import {
  channelMembersOptions,
  channelMessageThreadOptions,
  enrichChannelMessagesPreservingAvatars,
  useAddChannelReaction,
  useRemoveChannelReaction,
  useSendChannelThreadMessage,
  useSetChannelThreadFollowed,
} from "@multica/core/channels";
import { useAuthStore } from "@multica/core/auth";
import { api } from "@multica/core/api";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
import type { ChannelMessage } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
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
import { ThreadPanel } from "../../channels/components/thread-panel";
import { ComposerAttachmentTray } from "../../channels/components/composer-attachment-tray";
import type { QuoteTarget } from "../../channels/components/message-quote-types";

function stubThreadRoot(
  channelId: string,
  workspaceId: string,
  rootId: string,
): ChannelMessage {
  return {
    id: rootId,
    channel_id: channelId,
    workspace_id: workspaceId,
    seq: 0,
    type: "user",
    author_id: null,
    author_name: "",
    content: "",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: new Date(0).toISOString(),
  };
}

/**
 * Activity right-pane Thread shell (LRM-388). Reuses `<ThreadPanel>` — same
 * composer / follow / reply list as Channels — without mounting ChannelsPage.
 */
export function ActivityThreadPane({
  channelId,
  threadRootId,
  workspaceId,
  onBack,
  onViewInChannel,
}: {
  channelId: string;
  threadRootId: string;
  workspaceId: string;
  onBack: () => void;
  onViewInChannel: () => void;
}) {
  const { t } = useT("channels");
  const isMobile = useIsMobile();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const currentUserName = useAuthStore((s) => s.user?.name ?? undefined);

  const stubRoot = useMemo(
    () => stubThreadRoot(channelId, workspaceId, threadRootId),
    [channelId, workspaceId, threadRootId],
  );

  const {
    data: threadPage,
    isLoading: threadLoading,
    isError: threadError,
    refetch: refetchThread,
  } = useQuery(channelMessageThreadOptions(channelId, threadRootId));

  const threadSurfaceRoot = useMemo(
    () =>
      threadPage?.messages.find((message) => message.id === threadRootId) ??
      stubRoot,
    [threadPage?.messages, threadRootId, stubRoot],
  );

  const threadReplies = useMemo(() => {
    const messages = threadPage?.messages ?? [];
    const filtered = messages.filter((msg) => msg.id !== threadRootId);
    return enrichChannelMessagesPreservingAvatars(filtered);
  }, [threadPage?.messages, threadRootId]);

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

  const sendThreadMessage = useSendChannelThreadMessage();
  const setThreadFollowed = useSetChannelThreadFollowed();
  const addChannelReaction = useAddChannelReaction();
  const removeChannelReaction = useRemoveChannelReaction();
  const threadSend = useComposerSend();
  const { uploadWithToast } = useFileUpload(api);

  const threadEditorRef = useRef<ContentEditorRef>(null);
  const threadFileInputRef = useRef<HTMLInputElement | null>(null);
  const [threadDraftEmpty, setThreadDraftEmpty] = useState(true);
  const [threadQuoteTarget, setThreadQuoteTarget] = useState<QuoteTarget | null>(
    null,
  );

  const uploadForThread = useCallback(
    async (file: File) => uploadWithToast(file, { channelId }),
    [channelId, uploadWithToast],
  );
  const threadPending = useComposerPendingAttachments({
    upload: uploadForThread,
    resetKey: `${channelId}:${threadRootId}`,
  });

  const handleThreadFollowChange = useCallback(
    (followed: boolean) => {
      setThreadFollowed.mutate(
        {
          channelId,
          messageId: threadRootId,
          followed,
        },
        {
          onError: () =>
            toast.error(
              t(($) =>
                followed ? $.thread.follow_failed : $.thread.unfollow_failed,
              ),
            ),
        },
      );
    },
    [channelId, setThreadFollowed, t, threadRootId],
  );

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

  const handleThreadSend = useCallback(() => {
    if (threadPending.hasUploading) return;
    const content = threadEditorRef.current?.getMarkdown()?.trim() ?? "";
    const parts = buildChatMessageParts(
      content,
      threadPending.readyAttachmentParts,
    );
    if (parts.length === 0) return;
    const attachmentIds = threadPending.readyAttachmentParts.map(
      (p) => p.attachment_id,
    );
    const dispatched = threadSend.send({
      payloadKey: composePayloadKey(
        content,
        attachmentIds,
        `${threadRootId}:${threadQuoteTarget?.id ?? ""}`,
      ),
      buildVars: (clientMessageId) => ({
        channelId,
        messageId: threadRootId,
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
      threadEditorRef.current?.clearContent();
      threadPending.clear();
      setThreadQuoteTarget(null);
      setThreadDraftEmpty(true);
    }
  }, [
    channelId,
    sendThreadMessage.mutate,
    t,
    threadPending,
    threadQuoteTarget?.id,
    threadRootId,
    threadSend,
  ]);

  const handlePickThreadFiles = useCallback(
    (files: FileList | null) => {
      if (!files || files.length === 0) return;
      threadPending.addFiles(Array.from(files));
    },
    [threadPending],
  );

  return (
    <div className="flex h-full min-h-0 flex-col">
      <ThreadPanel
        root={threadSurfaceRoot}
        replies={threadReplies}
        currentUserId={currentUserId}
        currentUserName={currentUserName}
        isMobile={isMobile}
        onBack={onBack}
        followed={threadSurfaceRoot.thread_followed === true}
        followDisabled={
          threadLoading ||
          (setThreadFollowed.isPending &&
            setThreadFollowed.variables?.messageId === threadRootId)
        }
        onFollowChange={handleThreadFollowChange}
        onViewParent={onViewInChannel}
        loading={threadLoading && !threadPage}
        loadError={threadError}
        onRetry={() => refetchThread()}
        onReact={handleReactToMessage}
        onQuoteMessage={setThreadQuoteTarget}
        quoteTarget={threadQuoteTarget}
        onClearQuote={() => setThreadQuoteTarget(null)}
        editor={
          <ContentEditor
            key={`activity-thread-editor:${threadRootId}`}
            ref={threadEditorRef}
            plainUrls
            placeholder={t(($) => $.thread.composer_placeholder)}
            onUpdate={(md) => setThreadDraftEmpty(!md.trim())}
            onSubmit={handleThreadSend}
            mediaMode="external"
            onExternalFiles={threadPending.addFiles}
            submitOnEnter
            showBubbleMenu={false}
            mentionAllowedActorIds={channelMemberIds}
            scopedMentionAgents={channelAgentCandidates}
          />
        }
        onSend={handleThreadSend}
        sendDisabled={
          (threadDraftEmpty &&
            threadPending.readyAttachmentParts.length === 0) ||
          threadPending.hasUploading
        }
        sending={sendThreadMessage.isPending}
        composerTray={
          <ComposerAttachmentTray
            pending={threadPending.pending}
            onRemove={threadPending.remove}
            onRetry={threadPending.retry}
            isMobile={isMobile}
          />
        }
        composerLeadingActions={
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
  );
}
