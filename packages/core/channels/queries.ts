import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const channelKeys = {
  all: (wsId: string) => ["channels", wsId] as const,
  list: (wsId: string) => [...channelKeys.all(wsId), "list"] as const,
  archivedList: (wsId: string) => [...channelKeys.all(wsId), "archived-list"] as const,
  messages: (channelId: string) => ["channel-messages", channelId] as const,
  messagesPage: (channelId: string, limit?: number, beforeCreatedAt?: string, beforeId?: string) =>
    ["channel-messages-page", channelId, limit, beforeCreatedAt, beforeId] as const,
  messageThread: (channelId: string, messageId: string) => ["channel-message-thread", channelId, messageId] as const,
  messageSearch: (channelId: string, query: string, limit?: number) => ["channel-message-search", channelId, query, limit] as const,
  members: (channelId: string) => ["channel-members", channelId] as const,
  attachments: (channelId: string) => ["channel-attachments", channelId] as const,
  stats: (channelId: string) => ["channel-stats", channelId] as const,
  projectFiles: (channelId: string) => ["channel-project-files", channelId] as const,
};

export function channelsOptions(wsId: string) {
  return queryOptions({
    queryKey: channelKeys.list(wsId),
    queryFn: () => api.listChannels(),
    enabled: !!wsId,
  });
}

export function archivedChannelsOptions(wsId: string) {
  return queryOptions({
    queryKey: channelKeys.archivedList(wsId),
    queryFn: () => api.listChannels({ archived: true }),
    enabled: !!wsId,
  });
}

export function channelMessagesOptions(channelId: string) {
  return queryOptions({
    queryKey: channelKeys.messages(channelId),
    queryFn: () => api.listChannelMessages(channelId),
    enabled: !!channelId,
  });
}

export function channelMessagesPageOptions(channelId: string, options?: { limit?: number; beforeCreatedAt?: string; beforeId?: string }) {
  return queryOptions({
    queryKey: channelKeys.messagesPage(channelId, options?.limit, options?.beforeCreatedAt, options?.beforeId),
    queryFn: () => api.listChannelMessagesPage(channelId, options),
    enabled: !!channelId,
  });
}

export function channelMessageThreadOptions(channelId: string, messageId: string, options?: { limit?: number; before?: string; beforeId?: string }) {
  return queryOptions({
    queryKey: [...channelKeys.messageThread(channelId, messageId), options?.limit, options?.before, options?.beforeId] as const,
    queryFn: () => api.listChannelMessageThread(channelId, messageId, options),
    enabled: !!channelId && !!messageId,
  });
}

export function channelMessageSearchOptions(channelId: string, query: string, limit?: number) {
  return queryOptions({
    queryKey: channelKeys.messageSearch(channelId, query, limit),
    queryFn: () => api.searchChannelMessages(channelId, query, limit),
    enabled: !!channelId && query.trim().length > 0,
  });
}

export function channelMembersOptions(channelId: string) {
  return queryOptions({
    queryKey: channelKeys.members(channelId),
    queryFn: () => api.listChannelMembers(channelId),
    enabled: !!channelId,
  });
}

export function channelAttachmentsOptions(channelId: string) {
  return queryOptions({
    queryKey: channelKeys.attachments(channelId),
    queryFn: () => api.listChannelAttachments(channelId),
    enabled: !!channelId,
  });
}

export function channelStatsOptions(channelId: string) {
  return queryOptions({
    queryKey: channelKeys.stats(channelId),
    queryFn: () => api.getChannelStats(channelId),
    enabled: !!channelId,
  });
}

export function channelProjectFilesOptions(channelId: string) {
  return queryOptions({
    queryKey: channelKeys.projectFiles(channelId),
    queryFn: () => api.listChannelProjectFiles(channelId),
    enabled: !!channelId,
  });
}
