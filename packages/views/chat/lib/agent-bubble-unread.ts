import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { chatSessionsOptions } from "@multica/core/chat/queries";

/**
 * Map agent_id → count of chat_sessions with unread assistant replies.
 * Used to surface bubble completion on the DM sidebar (bubble ≠ dm_channel).
 */
export function useAgentBubbleUnreadByAgent(wsId: string): Map<string, number> {
  const { data: sessions = [] } = useQuery(chatSessionsOptions(wsId));
  return useMemo(() => {
    const counts = new Map<string, number>();
    for (const session of sessions) {
      if (!session.has_unread) continue;
      counts.set(session.agent_id, (counts.get(session.agent_id) ?? 0) + 1);
    }
    return counts;
  }, [sessions]);
}
