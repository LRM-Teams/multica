"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  agentTaskSnapshotOptions,
  useAgentPresenceDetail,
} from "@multica/core/agents";
import {
  pickPrimaryActiveTask,
  resolveAgentActivityHeader,
  type AgentLiveStatusView,
} from "./resolve-agent-live-status";
import { useAgentActivityEvents } from "./components/tabs/use-agent-activity-events";

const HEADER_STAGE_KINDS = new Set(["thinking", "tool_call"]);

/**
 * Latest Activity work-row projection for the composer strip (LRM-248).
 * Not a live presence word — keeps Thinking / Running command… vocabulary.
 */
export function useAgentActivityHeader(
  wsId: string | undefined,
  agentId: string | undefined,
): AgentLiveStatusView | null {
  const presence = useAgentPresenceDetail(wsId, agentId);
  const { data: snapshot = [] } = useQuery({
    ...agentTaskSnapshotOptions(wsId ?? ""),
    enabled: !!wsId && !!agentId,
  });
  const activeTask = useMemo(
    () => pickPrimaryActiveTask(snapshot, agentId),
    [snapshot, agentId],
  );
  const { events } = useAgentActivityEvents(activeTask ? (agentId ?? "") : "");
  const roundStart = activeTask?.started_at ?? activeTask?.dispatched_at ?? null;
  const latestActivity = useMemo(() => {
    if (!roundStart) return null;
    let latest = null;
    for (const e of events) {
      if (e.occurred_at >= roundStart && HEADER_STAGE_KINDS.has(e.activity_kind)) {
        latest = e;
      }
    }
    return latest;
  }, [events, roundStart]);

  return useMemo(
    () =>
      resolveAgentActivityHeader({
        presence,
        activeTask,
        latestActivity,
      }),
    [presence, activeTask, latestActivity],
  );
}
