import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { createLogger } from "../logger";
import { invalidateChannelMessages } from "./queries";

const logger = createLogger("channel.project");

export const channelProjectKeys = {
  all: (wsId: string) => ["channel-project", wsId] as const,
  detail: (wsId: string, channelId: string) =>
    [...channelProjectKeys.all(wsId), channelId] as const,
};

/**
 * The project a channel is bound to. Returns the project id, or "" when the
 * channel is unbound. Binding makes every agent in the channel work in that
 * project's directory.
 */
export function channelProjectOptions(wsId: string, channelId: string) {
  return queryOptions({
    queryKey: channelProjectKeys.detail(wsId, channelId),
    queryFn: async () => {
      const res = await api.getChannelProject(channelId);
      return res.project_id;
    },
    enabled: !!wsId && !!channelId,
  });
}

/**
 * Binds (or unbinds) a channel to a project. Optimistically patches the cached
 * channel-project value so the composer's project picker reflects the new
 * selection immediately; rolls back on error and re-fetches on settle. Pass
 * `projectId: null` to clear the binding.
 */
export function useSetChannelProject(wsId: string, channelId: string) {
  const qc = useQueryClient();
  const key = channelProjectKeys.detail(wsId, channelId);

  return useMutation({
    mutationFn: (projectId: string | null) => {
      logger.info("setChannelProject.start", { channelId, projectId });
      return api.setChannelProject(channelId, projectId);
    },
    onMutate: async (projectId) => {
      await qc.cancelQueries({ queryKey: key });
      const prev = qc.getQueryData<string>(key);
      qc.setQueryData<string>(key, projectId ?? "");
      return { prev };
    },
    onError: (err, _projectId, ctx) => {
      logger.error("setChannelProject.error.rollback", { channelId, err });
      if (ctx?.prev !== undefined) qc.setQueryData(key, ctx.prev);
    },
    onSuccess: () => {
      // A successful bind/change/unbind makes the server emit a
      // `channel_project_*` system row. The server broadcasts it to channel
      // members INCLUDING the originator, but the originator must not depend on
      // that WS echo to see their own action (#615) — the socket may be
      // reconnecting, the tab backgrounded, etc. Invalidate the channel timeline
      // so the confirmation row is fetched and shown immediately, exactly like
      // sending a message refreshes the sender's own list.
      invalidateChannelMessages(qc, channelId);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: key });
    },
  });
}

export const projectChannelKeys = {
  channels: (wsId: string, projectId: string) =>
    ["project-channels", wsId, projectId] as const,
};

/**
 * The group channels attached to a project (#574 / #629). Read-only; drives the
 * "Associated groups" row on the project detail page. Reverse of
 * `channelProjectOptions` (which resolves a single channel's bound project).
 */
export function projectChannelsOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: projectChannelKeys.channels(wsId, projectId),
    queryFn: () => api.listProjectChannels(projectId, wsId),
    enabled: !!wsId && !!projectId,
  });
}
