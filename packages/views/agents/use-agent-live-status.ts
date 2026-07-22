"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  agentTaskSnapshotOptions,
  useAgentPresenceDetail,
} from "@multica/core/agents";
import { useT } from "../i18n/use-t";
import {
  pickPrimaryActiveTask,
  resolveAgentActivityProjection,
  resolveAgentLiveStatus,
  type AgentLiveStatusView,
} from "./resolve-agent-live-status";
import { useAgentActivityEvents } from "./components/tabs/use-agent-activity-events";

// Activity kinds projected onto the composer strip (not live presence).
// LRM-202: exclude `text` — Activity presents those as "Output", which Frank
// does not want above the composer.
const ACTIVITY_STAGE_KINDS = new Set(["thinking", "tool_call"]);

/**
 * Live Online/Offline name-row status (LRM-248). Does not project Activity
 * verbs — those live on `useAgentActivityProjection` / the composer strip.
 */
export function useAgentLiveStatus(
  wsId: string | undefined,
  agentId: string | undefined,
): AgentLiveStatusView | null {
  const { t: tAgents } = useT("agents");
  const presence = useAgentPresenceDetail(wsId, agentId);

  return useMemo(
    () =>
      resolveAgentLiveStatus({
        presence,
        tAgents,
      }),
    [presence, tAgents],
  );
}

/**
 * Composer-strip activity projection (Thinking / Running command…).
 * Non-live event verbs — hidden when idle or offline.
 */
export function useAgentActivityProjection(
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
      if (e.occurred_at >= roundStart && ACTIVITY_STAGE_KINDS.has(e.activity_kind)) {
        latest = e;
      }
    }
    return latest;
  }, [events, roundStart]);

  return useMemo(
    () =>
      resolveAgentActivityProjection({
        presence,
        activeTask,
        latestActivity,
      }),
    [presence, activeTask, latestActivity],
  );
}
