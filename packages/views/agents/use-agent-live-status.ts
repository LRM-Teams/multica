"use client";

import { useMemo } from "react";
import { useAgentPresenceDetail } from "@multica/core/agents";
import { useT } from "../i18n/use-t";
import {
  resolveAgentLiveStatus,
  type AgentLiveStatusView,
} from "./resolve-agent-live-status";

/**
 * Live Online/Offline word for profile / name-row surfaces (LRM-248).
 * Activity verbs (Thinking / Running command) live on
 * `useAgentActivityHeader` / the composer strip — not here.
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
