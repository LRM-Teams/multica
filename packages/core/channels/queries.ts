import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const channelKeys = {
  all: (wsId: string) => ["channels", wsId] as const,
  list: (wsId: string) => [...channelKeys.all(wsId), "list"] as const,
  archivedList: (wsId: string) => [...channelKeys.all(wsId), "archived-list"] as const,
  messages: (channelId: string) => ["channel-messages", channelId] as const,
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
