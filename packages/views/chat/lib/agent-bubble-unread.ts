import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { chatSessionsOptions } from "@multica/core/chat/queries";
import { channelsOptions } from "@multica/core/channels/queries";
import { excludeChannelShellSessions } from "./exclude-channel-shell-sessions";

/**
 * Map agent_id → count of chat_sessions with unread assistant replies.
 * Used to surface bubble completion on the DM sidebar (bubble ≠ dm_channel).
 * Channel "#name" shells are excluded so group wakes do not badge the bubble.
 */
export function useAgentBubbleUnreadByAgent(wsId: string): Map<string, number> {
  const { data: sessions = [] } = useQuery(chatSessionsOptions(wsId));
  const { data: channels = [] } = useQuery(channelsOptions(wsId));
  const channelNames = useMemo(() => channels.map((channel) => channel.name), [channels]);
  return useMemo(() => {
    const counts = new Map<string, number>();
    for (const session of excludeChannelShellSessions(sessions, channelNames)) {
      if (!session.has_unread) continue;
      counts.set(session.agent_id, (counts.get(session.agent_id) ?? 0) + 1);
    }
    return counts;
  }, [sessions, channelNames]);
}
