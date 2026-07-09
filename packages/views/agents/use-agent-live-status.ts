"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  agentTaskSnapshotOptions,
  useAgentPresenceDetail,
} from "@multica/core/agents";
import { taskMessagesOptions } from "@multica/core/chat/queries";
import { useT } from "../i18n/use-t";
import {
  pickPrimaryActiveTask,
  resolveAgentLiveStatus,
  type AgentLiveStatusView,
} from "./resolve-agent-live-status";

/**
 * Live name-row status for an agent: stage-detail when a task is active
 * (Thinking / Running a command / …), presence word when idle. Snapshot +
 * task-messages are the same caches the chat pill and presence dots use, so
 * WS task:message / task lifecycle events keep this in lockstep with chat.
 */
export function useAgentLiveStatus(
  wsId: string | undefined,
  agentId: string | undefined,
): AgentLiveStatusView | null {
  const { t: tAgents } = useT("agents");
  const { t: tChat } = useT("chat");
  const presence = useAgentPresenceDetail(wsId, agentId);
  const { data: snapshot = [] } = useQuery({
    ...agentTaskSnapshotOptions(wsId ?? ""),
    enabled: !!wsId && !!agentId,
  });
  const activeTask = useMemo(
    () => pickPrimaryActiveTask(snapshot, agentId),
    [snapshot, agentId],
  );
  const { data: taskMessages = [] } = useQuery({
    ...taskMessagesOptions(activeTask?.id ?? ""),
    enabled: !!activeTask?.id,
  });

  return useMemo(
    () =>
      resolveAgentLiveStatus({
        presence,
        activeTask,
        taskMessages,
        tAgents,
        tChat,
      }),
    [presence, activeTask, taskMessages, tAgents, tChat],
  );
}
