import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { agentRemindersKeys } from "@multica/core/agents";
import { useWSEvent, useWSReconnect } from "@multica/core/realtime";
import type { AgentReminderChangedPayload } from "@multica/core/types";

export function useAgentRemindersRealtime(agentId: string): void {
  const queryClient = useQueryClient();

  useWSEvent(
    "agent_reminder:changed",
    useCallback(
      (payload: unknown) => {
        const reminder = payload as AgentReminderChangedPayload;
        if (!agentId || reminder.agentId !== agentId) return;
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
