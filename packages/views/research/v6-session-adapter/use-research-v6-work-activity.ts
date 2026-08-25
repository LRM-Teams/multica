"use client";

import { useEffect, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { RESEARCH_V6_DIRECTOR_DELTA_EVENT } from "@multica/core/research-v6-live/director-controller";
import { researchV6DirectorWorkActivityOptions } from "@multica/core/research-v6/director-queries";
import type {
  ResearchV6DirectorDetailTransport,
  ResearchV6DirectorProjectionNode,
} from "@multica/core/types/research-v6-director";
import type {
  RunnerActivityTimelineRow,
  WSEventType,
} from "@multica/core/types/events";

const WORK_ACTIVITY_EVENTS = [
  "task:running",
  "task:progress",
  "task:message",
  "task:completed",
  "task:failed",
  "task:cancelled",
] as const satisfies readonly WSEventType[];

export interface ResearchV6WorkActivityInput {
  enabled: boolean;
  workspaceId: string;
  runId: string;
  selectedNode: ResearchV6DirectorProjectionNode | null;
  transport: ResearchV6DirectorDetailTransport;
  subscribe: (
    event: WSEventType,
    handler: (payload: unknown) => void,
  ) => () => void;
}

/** Owns the selected Director Work node's durable and live activity timeline. */
export function useResearchV6WorkActivity({
  enabled,
  workspaceId,
  runId,
  selectedNode,
  transport,
  subscribe,
}: ResearchV6WorkActivityInput) {
  const workItemId =
    selectedNode?.kind === "work_s" ? selectedNode.canonicalRef.id : "";
  const query = useQuery({
    ...researchV6DirectorWorkActivityOptions(
      transport,
      workspaceId,
      runId,
      workItemId,
      selectedNode?.updatedAt ?? "unselected",
    ),
    enabled: enabled && Boolean(workItemId),
  });
  const { data, isError, isLoading, refetch } = query;

  useEffect(() => {
    const inboxTaskId = data?.inboxTaskId;
    if (!enabled || !inboxTaskId) return;
    const refetchMatchingActivity = (payload: unknown) => {
      const progress = payload as { task_id?: unknown };
      if (progress.task_id === inboxTaskId) void refetch();
    };
    const unsubscribers = WORK_ACTIVITY_EVENTS.map((event) =>
      subscribe(event, refetchMatchingActivity),
    );
    return () => {
      for (const unsubscribe of unsubscribers) unsubscribe();
    };
  }, [data?.inboxTaskId, enabled, refetch, subscribe]);

  useEffect(() => {
    if (!enabled || !workItemId) return;
    return subscribe(RESEARCH_V6_DIRECTOR_DELTA_EVENT, (payload) => {
      const envelope = payload as { run_id?: unknown };
      if (envelope.run_id === runId) void refetch();
    });
  }, [enabled, refetch, runId, subscribe, workItemId]);

  useEffect(() => {
    const agentId = data?.agentId;
    if (!enabled || !agentId || !workItemId) return;
    return subscribe("research_session:presence", (payload) => {
      const presence = payload as {
        session_id?: unknown;
        agent_id?: unknown;
      };
      if (presence.session_id === runId && presence.agent_id === agentId) {
        void refetch();
      }
    });
  }, [data?.agentId, enabled, refetch, runId, subscribe, workItemId]);

  const timeline = useMemo<RunnerActivityTimelineRow[]>(() => {
    const persistedTimeline = data?.timeline ?? [];
    return [...persistedTimeline]
      .sort(
        (left, right) =>
          Date.parse(right.occurred_at) - Date.parse(left.occurred_at),
      )
      .slice(0, 8);
  }, [data?.timeline]);

  return {
    data,
    isLoading,
    isError,
    refetch,
    timeline,
  };
}
