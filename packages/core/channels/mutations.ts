import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import { channelKeys } from "./queries";
import { dmKeys } from "../dm/queries";
import type { DMItem } from "../dm/types";

export function useCreateChannel() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (data: { name: string; description?: string; lark_chat_id?: string }) => api.createChannel(data),
    onSuccess: () => qc.invalidateQueries({ queryKey: channelKeys.list(wsId) }),
  });
}

export function useDeleteChannel() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (channelId: string) => api.deleteChannel(channelId),
    onSuccess: () => qc.invalidateQueries({ queryKey: channelKeys.list(wsId) }),
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

export function useSendChannelMessage() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ channelId, content, attachmentIds, replyToMessageId }: { channelId: string; content: string; attachmentIds?: string[]; replyToMessageId?: string }) =>
      api.sendChannelMessage(channelId, content, attachmentIds, replyToMessageId),
    onSuccess: (msg) => {
      qc.invalidateQueries({ queryKey: channelKeys.messages(msg.channel_id) });
      qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
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
            ? { ...dm, unread: 0, manually_unread: false }
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
      // Refresh the list so the composite group avatar reflects the new roster.
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
      qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
    },
  });
}
