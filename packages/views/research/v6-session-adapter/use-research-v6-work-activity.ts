"use client";

import { useEffect, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { useRunnerActivity } from "@multica/core/agents";
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

  useEffect(() => {
    const inboxTaskId = query.data?.inboxTaskId;
    if (!enabled || !inboxTaskId) return;
    const refetchMatchingActivity = (payload: unknown) => {
      const progress = payload as { task_id?: unknown };
      if (progress.task_id === inboxTaskId) void query.refetch();
    };
    const unsubscribers = WORK_ACTIVITY_EVENTS.map((event) =>
      subscribe(event, refetchMatchingActivity),
    );
    return () => {
      for (const unsubscribe of unsubscribers) unsubscribe();
    };
  }, [enabled, query.data?.inboxTaskId, query.refetch, subscribe]);

  const runnerActivity = useRunnerActivity(
    enabled ? workspaceId : undefined,
    query.data?.agentId || undefined,
  );
  const timeline = useMemo<RunnerActivityTimelineRow[]>(() => {
    const startedAt = Date.parse(query.data?.startedAt ?? "");
    const persistedTimeline = query.data?.timeline ?? [];
    if (!Number.isFinite(startedAt)) return persistedTimeline.slice(0, 8);
    const completedAt = Date.parse(query.data?.completedAt ?? "");
    const upperBound = Number.isFinite(completedAt)
      ? completedAt
      : Number.POSITIVE_INFINITY;
    const timelineById = new Map(
      persistedTimeline.map((row) => [row.id, row] as const),
    );
    for (const row of runnerActivity.data?.timeline ?? []) {
      timelineById.set(row.id, row);
    }
    return [...timelineById.values()]
      .filter((row) => {
        const occurredAt = Date.parse(row.occurred_at);
        return occurredAt >= startedAt && occurredAt <= upperBound;
      })
      .sort(
        (left, right) =>
          Date.parse(right.occurred_at) - Date.parse(left.occurred_at),
      )
      .slice(0, 8);
  }, [
    query.data?.completedAt,
    query.data?.startedAt,
    query.data?.timeline,
    runnerActivity.data?.timeline,
  ]);

  return {
    data: query.data,
    isLoading: query.isLoading,
    isError: query.isError,
    refetch: query.refetch,
    timeline,
    refetchRunnerActivity: runnerActivity.refetch,
  };
}
