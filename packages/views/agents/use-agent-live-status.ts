"use client";

import { useMemo } from "react";
import { useAgentPresence, useRunnerActivitySummary } from "@multica/core/agents";
import { useT } from "../i18n/use-t";
import { resolveAgentLiveStatus, type AgentLiveStatusView } from "./resolve-agent-live-status";
import { runnerActivityVisuals } from "./runner-activity-visuals";

/** Runner lifecycle facts → compact composer / cue view. */
export function projectRunnerActivitySummary(
  summary: { label: string; activityKind: string; detailKind: string } | null | undefined,
): AgentLiveStatusView | null {
  if (!summary) return null;
  const visuals = runnerActivityVisuals({ activity_kind: summary.activityKind, detail_kind: summary.detailKind });
  if (!visuals.show) return null;
  return {
    label: summary.label,
    textClass: "text-foreground",
    dotClass: visuals.dotClass,
  };
}

/**
 * Live Online/Offline name-row status (LRM-248). Does not project Activity
 * verbs — those live on `useAgentActivityProjection` / the composer strip.
 */
export function useAgentLiveStatus(
  wsId: string | undefined,
  agentId: string | undefined,
): AgentLiveStatusView | null {
  const { t: tAgents } = useT("agents");
  const presence = useAgentPresence(wsId, agentId);

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
 * Composer-strip Activity comes only from Workspace Runner lifecycle facts.
 * It intentionally does not inspect Tasks, presence,
 * provider events, sessions, or elapsed time.
 */
export function useAgentActivityProjection(
  wsId: string | undefined,
  agentId: string | undefined,
): AgentLiveStatusView | null {
  const { data: summary } = useRunnerActivitySummary(wsId, agentId);

  return useMemo(() => projectRunnerActivitySummary(summary), [summary]);
}
