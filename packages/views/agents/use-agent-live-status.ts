"use client";

import { useMemo } from "react";
import { useAgentPresence, useRunnerActivitySummary } from "@multica/core/agents";
import { useT } from "../i18n/use-t";
import { resolveAgentLiveStatus, type AgentLiveStatusView } from "./resolve-agent-live-status";

const RUNNER_TONE_DOT_CLASS: Record<string, string> = {
  neutral: "bg-muted-foreground",
  active: "bg-brand",
  info: "bg-blue-500",
  warning: "bg-amber-500",
  running: "bg-running",
  error: "bg-destructive",
  success: "bg-emerald-500",
};

/** Server-arbitrated Runner summary → compact composer / cue view. */
export function projectRunnerActivitySummary(
  summary: { label: string; tone: string; visibility: string } | null | undefined,
): AgentLiveStatusView | null {
  if (!summary || summary.visibility !== "visible") return null;
  return {
    label: summary.label,
    textClass: "text-foreground",
    dotClass: RUNNER_TONE_DOT_CLASS[summary.tone] ?? "bg-muted-foreground",
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
 * Composer-strip Activity comes only from the server-arbitrated Workspace
 * Runner projection. It intentionally does not inspect Tasks, presence,
 * provider events, sessions, or elapsed time.
 */
export function useAgentActivityProjection(
  wsId: string | undefined,
  agentId: string | undefined,
): AgentLiveStatusView | null {
  const { data: summary } = useRunnerActivitySummary(wsId, agentId);

  return useMemo(() => projectRunnerActivitySummary(summary), [summary]);
}
