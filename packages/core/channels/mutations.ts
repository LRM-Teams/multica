import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { classifyRoleChangeFailure } from "./role-change-failure";
import { useAuthStore } from "../auth";
import { useWorkspaceId } from "../hooks";
import {
  channelKeys,
  invalidateChannelMemberRoster,
  invalidateChannelMessages,
  upsertChannelMessageInCache,
  upsertChannelMessageThreadInCache,
} from "./queries";
import {
  buildOptimisticChannelMessage,
  insertOptimisticChannelMessage,
  insertOptimisticThreadMessage,
  removeOptimisticChannelMessage,
  resolveOptimisticSiblings,
} from "./optimistic-send";
import { dmKeys } from "../dm/queries";
import type { DMItem } from "../dm/types";
import type { Channel, ChannelNotifyLevel, ChannelThreadMessagesPage, MessagePart } from "../types";
import { userActivityKeys } from "../user-activity/queries";
import {
  optimisticallyMarkActivityThreadRead,
  restoreActivityQueries,
} from "../user-activity/mutations";

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
      avatar_url?: string | null;
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

/** LRM-748 / LRM-769 — set the viewer's per-channel notify level. The server
 *  dual-writes `muted_at` for legacy clients, so invalidating the channel
 *  list refreshes both fields at once. */
export function useSetChannelNotifyPreference() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ channelId, level }: { channelId: string; level: ChannelNotifyLevel }) =>
      api.setChannelNotifyPreference(channelId, level),
    onSuccess: () => qc.invalidateQueries({ queryKey: channelKeys.list(wsId) }),
  });
}

export function useSendChannelMessage() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    // #1276 offline behavior — attempt the send even offline so it fails fast
    // (restore draft + error bar) instead of pausing on a stuck "Sending…" and
    // silently resending on reconnect — now comes from the global
    // `mutations.networkMode: "always"` default in query-client.ts (see there
    // for the full why). Kept implicit here so there is one source of truth.
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
    onError: (_err, vars) => {
      if (!vars.clientMessageId) return;
      // #772: no permanent failed bubble in the transcript. Remove the optimistic
      // row on ANY failure (conflict OR real transport/server failure) — the
      // surface's `onVisibleError` restores the text into the composer and shows
      // an inline error bar, so the transcript stays clean. (LRM-280 still holds:
      // onError only fires after the request truly settles, never in-flight.)
      removeOptimisticChannelMessage(qc, vars.channelId, vars.clientMessageId);
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
    // #1276 offline behavior comes from the global mutations.networkMode
    // "always" default (query-client.ts); see useSendChannelMessage.
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
    onError: (_err, vars) => {
      if (!vars.clientMessageId) return;
      // #772: no permanent failed bubble — remove the optimistic row on ANY
      // failure (conflict OR transport/server); the surface's onVisibleError
      // restores the text into the composer + shows an inline error bar.
      removeOptimisticChannelMessage(qc, vars.channelId, vars.clientMessageId, vars.messageId);
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

/**
 * Read receipt for the conversation the viewer just opened.
 *
 * Fires on EVERY conversation switch (see `useEntryReadCursor`), so its cache
 * work is on the critical path of "switch → readable first paint" (LRM-1296).
 * The receipt only ever zeroes THIS row's unread, and the response carries no
 * new list data, so both lists are patched in place. Invalidating them instead
 * cost two extra full-list refetches per switch — each a server-side per-row
 * unread aggregate + last_message enrichment — racing the message page that
 * actually blocks first paint.
 *
 * `last_read_seq` is deliberately NOT patched: the response echoes the PREVIOUS
 * cursor, not the new one, and the divider consumers freeze it at entry anyway
 * (`useEntryReadCursor` / `useEntryAnchor`). Anything that can raise unread
 * again — a new message — already invalidates both lists over WS, which is what
 * re-syncs the cursor. Guessing a cursor here would misplace the
 * "N new messages" divider.
 */
export function useMarkChannelRead() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (channelId: string) => api.markChannelRead(channelId),
    onMutate: async (channelId) => {
      await qc.cancelQueries({ queryKey: dmKeys.list(wsId) });
      await qc.cancelQueries({ queryKey: channelKeys.list(wsId) });
      const prevDms = qc.getQueryData<DMItem[]>(dmKeys.list(wsId));
      const prevChannels = qc.getQueryData<Channel[]>(channelKeys.list(wsId));
      qc.setQueryData<DMItem[]>(dmKeys.list(wsId), (old) =>
        old?.map((dm) =>
          dm.id === channelId && dm.source === "dm_channel" ? readDmRow(dm) : dm,
        ),
      );
      qc.setQueryData<Channel[]>(channelKeys.list(wsId), (old) =>
        old?.map((channel) => (channel.id === channelId ? readChannelRow(channel) : channel)),
      );
      return { prevDms, prevChannels };
    },
    onError: (_err, _channelId, ctx) => {
      if (ctx?.prevDms) qc.setQueryData(dmKeys.list(wsId), ctx.prevDms);
      if (ctx?.prevChannels) qc.setQueryData(channelKeys.list(wsId), ctx.prevChannels);
    },
  });
}

/** Zero one channel row's unread projection. Cursor left to the WS resync. */
function readChannelRow(channel: Channel): Channel {
  return {
    ...channel,
    unread_count: 0,
    real_unread_count: 0,
    mention_unread_count: 0,
    manually_unread: false,
  };
}

/**
 * Zero one DM row's unread projection. DM channels (kind='dm') also clear
 * `manual_unread_at` in `dm_peer_state`, hence `manually_unread`.
 */
function readDmRow(dm: DMItem): DMItem {
  return { ...dm, unread: 0, real_unread: 0, manually_unread: false, has_mention: false };
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
      invalidateChannelMemberRoster(qc, vars.channelId);
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
      invalidateChannelMemberRoster(qc, vars.channelId);
      qc.invalidateQueries({ queryKey: channelKeys.inviteCandidates(vars.channelId) });
      qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
    },
  });
}

/**
 * Owner-only member role change — promote, demote, or transfer ownership
 * (#832 / #814). The server distinguishes them by the target `role`.
 *
 * Deliberately no optimistic update: the failure modes here are not all
 * retryable, and two of them (`owner_changed`, `conflict`) mean the caller's
 * view of who holds what is already stale. Showing the new role first and
 * rolling it back would flash a state that was never true — the surface would
 * be asserting an outcome it does not have. Every failure kind lands on the
 * caller via `classifyRoleChangeFailure`.
 *
 * `owner_changed` additionally invalidates the roster, because that response
 * means ownership moved underneath us and the list on screen is wrong (#847).
 */
export function useUpdateChannelMemberRole() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({
      channelId,
      memberType,
      memberId,
      role,
    }: {
      channelId: string;
      memberType: "user" | "agent";
      memberId: string;
      // "owner" is deliberately absent: transfer is a different endpoint, not a
      // different role value (the PATCH route 400s on "owner").
      role: "manager" | "member";
    }) => api.updateChannelMemberRole(channelId, memberType, memberId, role),
    onSuccess: (_data, vars) => {
      invalidateChannelMemberRoster(qc, vars.channelId);
      // Ownership transfer changes what the viewer may do everywhere, not just
      // in this row — the channel list carries role-derived affordances too.
      qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
    },
    onError: (error, vars) => {
      // Refresh the roster on the two kinds that mean "your view is stale".
      // Not a retry — the user is told what happened and decides.
      const kind = classifyRoleChangeFailure(error);
      if (kind === "owner_changed" || kind === "gone") {
        invalidateChannelMemberRoster(qc, vars.channelId);
      }
    },
  });
}

/**
 * Ownership transfer (#814). A separate mutation because it is a separate
 * endpoint — the member-role PATCH rejects `role: "owner"` outright. Shares the
 * same failure classification and roster invalidation as a role change.
 */
export function useTransferChannelOwnership() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({
      channelId,
      memberType,
      memberId,
    }: {
      channelId: string;
      memberType: "user" | "agent";
      memberId: string;
    }) => api.transferChannelOwnership(channelId, memberType, memberId),
    onSuccess: (_data, vars) => {
      invalidateChannelMemberRoster(qc, vars.channelId);
      qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
    },
    onError: (error, vars) => {
      const kind = classifyRoleChangeFailure(error);
      if (kind === "owner_changed" || kind === "gone") {
        invalidateChannelMemberRoster(qc, vars.channelId);
      }
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
      invalidateChannelMemberRoster(qc, vars.channelId);
      qc.invalidateQueries({ queryKey: channelKeys.inviteCandidates(vars.channelId) });
      qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
    },
  });
}
