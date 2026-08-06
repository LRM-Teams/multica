import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { agentRemindersKeys } from "@multica/core/agents";
import { useWSEvent, useWSReconnect } from "@multica/core/realtime";
import type { AgentReminderChangedPayload } from "@multica/core/types";

/**
 * `agent_reminder:changed` is a pure invalidate signal (no event object to
 * merge, unlike Activity's `agent_activity:event`) — schedule/snooze/update/
 * cancel/fire/terminalize all just tell the FE "refetch", never what changed.
 * So this hook has no live buffer, just an invalidate on event + an
 * invalidate on reconnect (the global workspace-scoped reconnect handler
 * doesn't reach this per-agent query key, same as Activity's own hook).
 */
export function useAgentRemindersRealtime(agentId: string): void {
  const queryClient = useQueryClient();

  useWSEvent(
    "agent_reminder:changed",
    useCallback(
      (payload: unknown) => {
        const p = payload as AgentReminderChangedPayload;
        if (!agentId || p.agent_id !== agentId) return;
        queryClient.invalidateQueries({ queryKey: agentRemindersKeys.all(agentId) });
      },
      [agentId, queryClient],
    ),
  );

  useWSReconnect(
    useCallback(() => {
      if (!agentId) return;
      queryClient.invalidateQueries({ queryKey: agentRemindersKeys.all(agentId) });
    }, [agentId, queryClient]),
  );
}
