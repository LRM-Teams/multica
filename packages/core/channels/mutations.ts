import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { ApiError } from "../api/client";
import { useAuthStore } from "../auth";
import { useWorkspaceId } from "../hooks";
import { channelKeys, invalidateChannelMessages, upsertChannelMessageInCache, upsertChannelMessageThreadInCache } from "./queries";
import {
  buildOptimisticChannelMessage,
  insertOptimisticChannelMessage,
  insertOptimisticThreadMessage,
  markOptimisticChannelMessageFailed,
  removeOptimisticChannelMessage,
  resolveOptimisticSiblings,
} from "./optimistic-send";
import { dmKeys } from "../dm/queries";
import type { DMItem } from "../dm/types";
import type { ChannelThreadMessagesPage, MessagePart } from "../types";
import { userActivityKeys } from "../user-activity/queries";
import {
  optimisticallyMarkActivityThreadRead,
  restoreActivityQueries,
} from "../user-activity/mutations";

function isConflictError(err: unknown): boolean {
  return err instanceof ApiError && err.status === 409;
}

function viewerAuthorFields() {
  const user = useAuthStore.getState().user;
  return {
    authorId: user?.id ?? "",
    authorName: user?.display_name || user?.name || "You",
    authorAvatarUrl: user?.avatar_url ?? null,
  };
}

export function useCreateChannel() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (data: { name: string; description?: string; lark_chat_id?: string; project_id?: string | null }) =>
      api.createChannel(data),
    onSuccess: () => qc.invalidateQueries({ queryKey: channelKeys.list(wsId) }),
  });
}

export function useUpdateChannel() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({
      channelId,
      ...data
    }: {
      channelId: string;
      name?: string;
      description?: string | null;
      lark_chat_id?: string | null;
    }) => api.updateChannel(channelId, data),
    onSuccess: () => qc.invalidateQueries({ queryKey: channelKeys.all(wsId) }),
  });
}

export function useDeleteChannel() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (channelId: string) => api.deleteChannel(channelId),
    // LRM-485: hard-delete removes the row — drop active + archived caches
    // immediately, then invalidate. Invalidate-only left ghosts until refetch.
    onSuccess: (_data, channelId) => {
      const drop = <T extends { id: string }>(prev: T[] | undefined) =>
        prev ? prev.filter((c) => c.id !== channelId) : prev;
      qc.setQueryData(channelKeys.list(wsId), drop);
      qc.setQueryData(channelKeys.archivedList(wsId), drop);
      qc.invalidateQueries({ queryKey: channelKeys.all(wsId) });
    },
  });
}

export function useArchiveChannel() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (channelId: string) => api.archiveChannel(channelId),
    onSuccess: () => qc.invalidateQueries({ queryKey: channelKeys.all(wsId) }),
  });
}

export function useRestoreChannel() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (channelId: string) => api.restoreChannel(channelId),
    onSuccess: () => qc.invalidateQueries({ queryKey: channelKeys.all(wsId) }),
  });
}

export function useSetChannelPin() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ channelId, pinned }: { channelId: string; pinned: boolean }) =>
      pinned ? api.pinChannel(channelId) : api.unpinChannel(channelId),
    onSuccess: () => qc.invalidateQueries({ queryKey: channelKeys.list(wsId) }),
  });
}

export function useMuteChannel() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ channelId, muted }: { channelId: string; muted: boolean }) =>
      muted ? api.muteChannel(channelId) : api.unmuteChannel(channelId),
    onSuccess: () => qc.invalidateQueries({ queryKey: channelKeys.list(wsId) }),
  });
}

export const useSetChannelMuted = useMuteChannel;

export function useSendChannelMessage() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({
      channelId,
      content,
      replyToMessageId,
      parts,
      clientMessageId,
      quoteMessageId,
    }: {
      channelId: string;
      content: string;
      replyToMessageId?: string | null;
      /** Structured parts; attachment bind uses `{ type: "attachment", attachment_id }`. */
      parts?: MessagePart[];
      clientMessageId?: string | null;
      quoteMessageId?: string | null;
    }) =>
      api.sendChannelMessage(channelId, {
        content,
        parts,
        replyToMessageId,
        clientMessageId,
        quoteMessageId,
      }),
    onMutate: (vars) => {
      if (!vars.clientMessageId) return;
      const author = viewerAuthorFields();
      if (!author.authorId) return;
      const siblings = resolveOptimisticSiblings(qc, vars.channelId);
      const optimistic = buildOptimisticChannelMessage({
        channelId: vars.channelId,
        workspaceId: wsId,
        clientMessageId: vars.clientMessageId,
        content: vars.content,
        parts: vars.parts,
        authorId: author.authorId,
        authorName: author.authorName,
        authorAvatarUrl: author.authorAvatarUrl,
        quoteMessageId: vars.quoteMessageId,
        siblings,
        status: "pending",
      });
      insertOptimisticChannelMessage(qc, optimistic);
    },
    onError: (err, vars) => {
      if (!vars.clientMessageId) return;
      if (isConflictError(err)) {
        removeOptimisticChannelMessage(qc, vars.channelId, vars.clientMessageId);
        return;
      }
      // Real transport / server failure only — never auto-fail while the request
      // is still in flight (LRM-280; API abort budget is SEND_TIMEOUT_MS).
      markOptimisticChannelMessageFailed(qc, vars.channelId, vars.clientMessageId);
    },
    onSuccess: (msg) => {
      upsertChannelMessageInCache(qc, msg);
      qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
    },
  });
}

export function useEditChannelMessage() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      channelId,
      messageId,
      content,
      parts,
    }: {
      channelId: string;
      messageId: string;
      content: string;
      parts?: MessagePart[];
    }) => api.editChannelMessage(channelId, messageId, content, parts),
    onSuccess: (msg) => {
      upsertChannelMessageInCache(qc, msg);
      invalidateChannelMessages(qc, msg.channel_id);
    },
  });
}

export function useDeleteChannelMessage() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ channelId, messageId }: { channelId: string; messageId: string }) =>
      api.deleteChannelMessage(channelId, messageId),
    onSuccess: (_data, vars) => {
      invalidateChannelMessages(qc, vars.channelId);
    },
  });
}

export function useAddChannelReaction() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ channelId, messageId, emoji }: { channelId: string; messageId: string; emoji: string }) =>
      api.addChannelReaction(channelId, messageId, emoji),
    onSuccess: (reaction) => {
      invalidateChannelMessages(qc, reaction.channel_id);
    },
  });
}

export function useRemoveChannelReaction() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ channelId, messageId, emoji }: { channelId: string; messageId: string; emoji: string }) =>
      api.removeChannelReaction(channelId, messageId, emoji),
    onSuccess: (_data, vars) => {
      invalidateChannelMessages(qc, vars.channelId);
    },
  });
}

export function useSendChannelThreadMessage() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({
      channelId,
      messageId,
      content,
      replyToMessageId,
      parts,
      clientMessageId,
      quoteMessageId,
    }: {
      channelId: string;
      messageId: string;
      content: string;
      replyToMessageId?: string | null;
      /** Structured parts; attachment bind uses `{ type: "attachment", attachment_id }`. */
      parts?: MessagePart[];
      clientMessageId?: string | null;
      quoteMessageId?: string | null;
    }) =>
      api.sendChannelThreadMessage(channelId, messageId, {
        content,
        parts,
        replyToMessageId,
        clientMessageId,
        quoteMessageId,
      }),
    onMutate: (vars) => {
      if (!vars.clientMessageId) return;
      const author = viewerAuthorFields();
      if (!author.authorId) return;
      const siblings = resolveOptimisticSiblings(qc, vars.channelId, vars.messageId);
      const optimistic = buildOptimisticChannelMessage({
        channelId: vars.channelId,
        workspaceId: wsId,
        clientMessageId: vars.clientMessageId,
        content: vars.content,
        parts: vars.parts,
        authorId: author.authorId,
        authorName: author.authorName,
        authorAvatarUrl: author.authorAvatarUrl,
        quoteMessageId: vars.quoteMessageId,
        threadRootMessageId: vars.messageId,
        siblings,
        status: "pending",
      });
      insertOptimisticThreadMessage(qc, optimistic, vars.messageId);
    },
    onError: (err, vars) => {
      if (!vars.clientMessageId) return;
      if (isConflictError(err)) {
        removeOptimisticChannelMessage(qc, vars.channelId, vars.clientMessageId, vars.messageId);
        return;
      }
      markOptimisticChannelMessageFailed(qc, vars.channelId, vars.clientMessageId, vars.messageId);
    },
    onSuccess: (msg, vars) => {
      // Prefer the mutation's thread root — ACK payloads occasionally omit
      // `thread_root_message_id`, which previously left the optimistic row pending forever.
      const rootId = msg.thread_root_message_id ?? vars.messageId;
      const authoritative = msg.thread_root_message_id
        ? msg
        : { ...msg, thread_root_message_id: vars.messageId };
      upsertChannelMessageThreadInCache(qc, authoritative, rootId);
      // Upsert is authoritative for this send; avoid an immediate thread refetch
      // that can race the ACK / flash the list (LRM-271/273). Channel-list
      // invalidate preserves still-in-flight local sends (LRM-280).
      invalidateChannelMessages(qc, msg.channel_id);
      qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
      qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
    },
  });
}

export function useMarkChannelRead() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (channelId: string) => api.markChannelRead(channelId),
    onMutate: async (channelId) => {
      await qc.cancelQueries({ queryKey: dmKeys.list(wsId) });
      const prevDms = qc.getQueryData<DMItem[]>(dmKeys.list(wsId));
      qc.setQueryData<DMItem[]>(dmKeys.list(wsId), (old) =>
        old?.map((dm) =>
          dm.id === channelId && dm.source === "dm_channel"
            ? { ...dm, unread: 0, real_unread: 0, manually_unread: false }
            : dm,
        ),
      );
      return { prevDms };
    },
    onError: (_err, _channelId, ctx) => {
      if (ctx?.prevDms) qc.setQueryData(dmKeys.list(wsId), ctx.prevDms);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
      // DM channels (kind='dm') also clear manual_unread_at in dm_peer_state.
      // Always invalidate dmKeys so the DM list badge stays in sync.
      qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
    },
  });
}

export function useMarkChannelThreadRead() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ channelId, messageId }: { channelId: string; messageId: string }) =>
      api.markChannelThreadRead(channelId, messageId),
    onMutate: async (vars) => {
      // Activity Unread must clear when a thread is opened (LRM-379) — do not
      // wait for the channel ThreadPanel path alone; optimistic patch first.
      const prevActivity = await optimisticallyMarkActivityThreadRead(
        qc,
        wsId,
        vars.messageId,
      );
      return { prevActivity };
    },
    onError: (_err, _vars, ctx) => {
      restoreActivityQueries(qc, ctx?.prevActivity);
    },
    onSettled: (_result, _error, vars) => {
      qc.invalidateQueries({ queryKey: channelKeys.messageThread(vars.channelId, vars.messageId) });
      invalidateChannelMessages(qc, vars.channelId);
      qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
      qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
      qc.invalidateQueries({ queryKey: userActivityKeys.all(wsId) });
    },
  });
}

export function useSetChannelThreadFollowed() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ channelId, messageId, followed }: { channelId: string; messageId: string; followed: boolean }) =>
      followed ? api.followChannelThread(channelId, messageId) : api.unfollowChannelThread(channelId, messageId),
    onMutate: async (vars) => {
      const queryKey = channelKeys.messageThread(vars.channelId, vars.messageId);
      await qc.cancelQueries({ queryKey });
      const previousThreads = qc.getQueriesData<ChannelThreadMessagesPage>({ queryKey });
      qc.setQueriesData<ChannelThreadMessagesPage>({ queryKey }, (old) => {
        if (!old) return old;
        return {
          ...old,
          messages: old.messages.map((message) =>
            message.id === vars.messageId
              ? { ...message, thread_followed: vars.followed }
              : message,
          ),
        };
      });
      return { previousThreads };
    },
    onError: (_error, _vars, context) => {
      for (const [queryKey, data] of context?.previousThreads ?? []) {
        qc.setQueryData(queryKey, data);
      }
    },
    onSettled: (_result, _error, vars) => {
      qc.invalidateQueries({ queryKey: channelKeys.messageThread(vars.channelId, vars.messageId) });
      invalidateChannelMessages(qc, vars.channelId);
    },
  });
}

export function useMarkChannelUnread() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (channelId: string) => api.markChannelUnread(channelId),
    onSuccess: () => qc.invalidateQueries({ queryKey: channelKeys.list(wsId) }),
  });
}

export function useSetChannelTyping() {
  return useMutation({
    mutationFn: ({ channelId, isTyping }: { channelId: string; isTyping: boolean }) => api.setChannelTyping(channelId, isTyping),
  });
}

export function useAddChannelMember() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ channelId, memberType, memberId }: { channelId: string; memberType: "user" | "agent"; memberId: string }) =>
      api.addChannelMember(channelId, { member_type: memberType, member_id: memberId }),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: channelKeys.members(vars.channelId) });
      qc.invalidateQueries({ queryKey: channelKeys.inviteCandidates(vars.channelId) });
      // Refresh the list so channel member briefs stay current after roster changes.
      qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
    },
  });
}

export function useAddChannelMembers() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({
      channelId,
      members,
    }: {
      channelId: string;
      members: { member_type: "user" | "agent"; member_id: string }[];
    }) => api.addChannelMembers(channelId, members),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: channelKeys.members(vars.channelId) });
      qc.invalidateQueries({ queryKey: channelKeys.inviteCandidates(vars.channelId) });
      qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
    },
  });
}

export function useRemoveChannelMember() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ channelId, memberType, memberId }: { channelId: string; memberType: "user" | "agent"; memberId: string }) =>
      api.removeChannelMember(channelId, memberType, memberId),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: channelKeys.members(vars.channelId) });
      qc.invalidateQueries({ queryKey: channelKeys.inviteCandidates(vars.channelId) });
      qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
    },
  });
}
