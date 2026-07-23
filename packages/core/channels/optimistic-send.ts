import type { InfiniteData, QueryClient } from "@tanstack/react-query";
import type { Attachment, ChannelMessage, ChannelMessagesPage, MessagePart } from "../types";
import {
  channelKeys,
  flattenChannelMessagePages,
  upsertChannelMessageInCache,
  upsertChannelMessageThreadInCache,
} from "./queries";

/** Client-only send lifecycle for optimistic bubbles (never from the API). */
export type LocalSendStatus = "pending" | "failed";

export function isOptimisticChannelMessage(message: ChannelMessage): boolean {
  return message.local_send_status === "pending" || message.local_send_status === "failed";
}

/**
 * Stable list identity across optimistic temp id → server ACK (LRM-273).
 * Prefer `client_message_id` so Virtuoso / React keys do not remount the row
 * when the authoritative id lands.
 */
export function channelMessageListItemKey(message: ChannelMessage): string {
  return message.client_message_id || message.id;
}

function nextOptimisticSeq(messages: readonly ChannelMessage[] | undefined): number {
  if (!messages?.length) return 1;
  let max = 0;
  for (const m of messages) {
    if (typeof m.seq === "number" && m.seq > max) max = m.seq;
  }
  return max + 1;
}

function stubAttachmentsFromParts(
  parts: MessagePart[] | undefined,
  workspaceId: string,
  authorId: string,
  createdAt: string,
): Attachment[] | undefined {
  if (!parts?.length) return undefined;
  const stubs: Attachment[] = [];
  for (const part of parts) {
    if ((part.type !== "attachment" && part.type !== "voice") || !part.attachment_id) continue;
    const download = `/api/attachments/${part.attachment_id}/download`;
    stubs.push({
      id: part.attachment_id,
      workspace_id: workspaceId,
      issue_id: null,
      comment_id: null,
      chat_session_id: null,
      chat_message_id: null,
      uploader_type: "user",
      uploader_id: authorId,
      filename: part.filename || "file",
      url: download,
      download_url: download,
      markdown_url: download,
      content_type: part.content_type || "application/octet-stream",
      size_bytes: part.size_bytes || 0,
      created_at: createdAt,
    });
  }
  return stubs.length > 0 ? stubs : undefined;
}

export interface BuildOptimisticChannelMessageArgs {
  channelId: string;
  workspaceId: string;
  clientMessageId: string;
  content: string;
  parts?: MessagePart[];
  authorId: string;
  authorName: string;
  authorAvatarUrl?: string | null;
  quoteMessageId?: string | null;
  threadRootMessageId?: string | null;
  /** Existing cache rows used to pick the next local seq. */
  siblings?: readonly ChannelMessage[];
  status?: LocalSendStatus;
}

/** Build a temp bubble keyed by `client_message_id` (also used as temp `id`). */
export function buildOptimisticChannelMessage(
  args: BuildOptimisticChannelMessageArgs,
): ChannelMessage {
  const createdAt = new Date().toISOString();
  const status = args.status ?? "pending";
  return {
    id: args.clientMessageId,
    channel_id: args.channelId,
    workspace_id: args.workspaceId,
    seq: nextOptimisticSeq(args.siblings),
    type: "user",
    author_id: args.authorId,
    author_name: args.authorName,
    author_avatar_url: args.authorAvatarUrl ?? null,
    content: args.content,
    parts: args.parts,
    source: "multica",
    external_message_id: null,
    client_message_id: args.clientMessageId,
    quote_message_id: args.quoteMessageId ?? null,
    thread_root_message_id: args.threadRootMessageId ?? null,
    attachments: stubAttachmentsFromParts(args.parts, args.workspaceId, args.authorId, createdAt),
    created_at: createdAt,
    local_send_status: status,
  };
}

function listSiblings(qc: QueryClient, channelId: string): ChannelMessage[] {
  const paged = qc.getQueryData<InfiniteData<ChannelMessagesPage>>(channelKeys.messagesPage(channelId));
  if (paged?.pages.length) return flattenChannelMessagePages(paged);
  return qc.getQueryData<ChannelMessage[]>(channelKeys.messages(channelId)) ?? [];
}

function threadSiblings(
  qc: QueryClient,
  channelId: string,
  threadRootMessageId: string,
): ChannelMessage[] {
  const pages = qc.getQueriesData<{ messages: ChannelMessage[] }>({
    queryKey: channelKeys.messageThread(channelId, threadRootMessageId),
  });
  for (const [, data] of pages) {
    if (data?.messages?.length) return data.messages;
  }
  return [];
}

export function insertOptimisticChannelMessage(
  qc: QueryClient,
  message: ChannelMessage,
): void {
  upsertChannelMessageInCache(qc, message);
}

export function insertOptimisticThreadMessage(
  qc: QueryClient,
  message: ChannelMessage,
  threadRootMessageId: string,
): void {
  upsertChannelMessageThreadInCache(qc, message, threadRootMessageId);
}

/** Flip an optimistic bubble to failed (keep content for one-click retry). */
export function markOptimisticChannelMessageFailed(
  qc: QueryClient,
  channelId: string,
  clientMessageId: string,
  threadRootMessageId?: string | null,
): void {
  const patch = (old: ChannelMessage[] | undefined) =>
    old?.map((m) =>
      m.client_message_id === clientMessageId && isOptimisticChannelMessage(m)
        ? { ...m, local_send_status: "failed" as const }
        : m,
    );

  qc.setQueryData<ChannelMessage[]>(channelKeys.messages(channelId), patch);
  qc.setQueryData<InfiniteData<ChannelMessagesPage>>(channelKeys.messagesPage(channelId), (old) => {
    if (!old?.pages.length) return old;
    return {
      ...old,
      pages: old.pages.map((page) => ({
        ...page,
        messages: patch(page.messages) ?? page.messages,
      })),
    };
  });

  if (threadRootMessageId) {
    qc.setQueriesData<{ messages: ChannelMessage[] }>(
      { queryKey: channelKeys.messageThread(channelId, threadRootMessageId) },
      (old) => {
        if (!old) return old;
        return { ...old, messages: patch(old.messages) ?? old.messages };
      },
    );
  }
}

/** Drop a temp bubble (409 conflict / abandon). */
export function removeOptimisticChannelMessage(
  qc: QueryClient,
  channelId: string,
  clientMessageId: string,
  threadRootMessageId?: string | null,
): void {
  const drop = (old: ChannelMessage[] | undefined) =>
    old?.filter((m) => m.client_message_id !== clientMessageId || !isOptimisticChannelMessage(m));

  qc.setQueryData<ChannelMessage[]>(channelKeys.messages(channelId), drop);
  qc.setQueryData<InfiniteData<ChannelMessagesPage>>(channelKeys.messagesPage(channelId), (old) => {
    if (!old?.pages.length) return old;
    return {
      ...old,
      pages: old.pages.map((page) => ({
        ...page,
        messages: drop(page.messages) ?? page.messages,
      })),
    };
  });

  if (threadRootMessageId) {
    qc.setQueriesData<{ messages: ChannelMessage[] }>(
      { queryKey: channelKeys.messageThread(channelId, threadRootMessageId) },
      (old) => {
        if (!old) return old;
        return { ...old, messages: drop(old.messages) ?? old.messages };
      },
    );
  }
}

export function resolveOptimisticSiblings(
  qc: QueryClient,
  channelId: string,
  threadRootMessageId?: string | null,
): ChannelMessage[] {
  if (threadRootMessageId) {
    return threadSiblings(qc, channelId, threadRootMessageId);
  }
  return listSiblings(qc, channelId);
}

/** Strip client-only fields when an authoritative message replaces a temp row. */
export function stripLocalSendStatus(message: ChannelMessage): ChannelMessage {
  if (message.local_send_status == null) return message;
  const { local_send_status: _drop, ...rest } = message;
  return rest;
}
