import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { chatSessionsOptions } from "@multica/core/chat/queries";
import type { DMItem } from "@multica/core/dm";
import { excludeChannelShellSessions } from "./exclude-channel-shell-sessions";

export interface AgentBubbleActivity {
  unreadCount: number;
  latestUpdatedAt: string | null;
}

/**
 * LRM-762: bubble (independent chat_session) activity only belongs on a 1:1
 * human↔agent DM. Supervised agent↔agent rows (`agent_pair`) project an agent
 * peer for identity, but their sidebar time/unread must follow the DM channel
 * last_message — never the unrelated bubble "刚刚" / sticky unread path.
 */
export function dmAgentBubbleActivity(
  dm: Pick<DMItem, "mode" | "peer">,
  byAgent: Map<string, AgentBubbleActivity>,
): AgentBubbleActivity | null {
  if (dm.mode === "agent_pair" || dm.peer.type !== "agent") return null;
  return byAgent.get(dm.peer.id) ?? null;
}

/**
 * Map agent_id → independent chat_session activity for DM bubble surfacing.
 * Used to surface bubble completion on the DM sidebar (bubble ≠ dm_channel).
 * Channel "#name" shells are excluded so group wakes do not badge the bubble.
 */
export function useAgentBubbleActivityByAgent(wsId: string): Map<string, AgentBubbleActivity> {
  const { data: sessions = [] } = useQuery(chatSessionsOptions(wsId));
  return useMemo(() => {
    const activity = new Map<string, AgentBubbleActivity>();
    for (const session of excludeChannelShellSessions(sessions)) {
      const current = activity.get(session.agent_id) ?? {
        unreadCount: 0,
        latestUpdatedAt: null,
      };
      const latestUpdatedAt =
        !current.latestUpdatedAt || session.updated_at > current.latestUpdatedAt
          ? session.updated_at
          : current.latestUpdatedAt;
      activity.set(session.agent_id, {
        unreadCount: current.unreadCount + (session.has_unread ? 1 : 0),
        latestUpdatedAt,
      });
    }
    return activity;
  }, [sessions]);
}

/** Map agent_id → count of chat_sessions with unread assistant replies. */
export function useAgentBubbleUnreadByAgent(wsId: string): Map<string, number> {
  const activity = useAgentBubbleActivityByAgent(wsId);
  return useMemo(() => {
    const counts = new Map<string, number>();
    for (const [agentId, item] of activity) {
      if (item.unreadCount > 0) counts.set(agentId, item.unreadCount);
    }
    return counts;
  }, [activity]);
}
