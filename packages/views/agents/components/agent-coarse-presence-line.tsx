"use client";

import { useMemo } from "react";
import { useWorkspaceId } from "@multica/core/hooks";
import { useAgentPresenceDetail } from "@multica/core/agents";
import { useT } from "../../i18n";
import {
  formatPresenceStatus,
  presenceStatusDotClass,
  presenceStatusVisual,
} from "../presence";
import type { AgentLiveStatusView } from "../resolve-agent-live-status";
import { AgentLiveStatusMark } from "./agent-live-status-mark";

/**
 * Coarse presence line for the DM header: the agent name row shows only "is
 * the agent around" — Online / Working / Queued / Offline / … — never the
 * FINE live action verb (Running command… / Reading… / Writing…).
 *
 * The fine verb lives on exactly ONE surface: the `ConversationAgentActivityLine`
 * above the composer. Splitting the granularity this way stops the header and
 * that line from echoing the same word twice (Iris split-semantics 2026-07-17):
 * header = presence ("is she around"), composer line = live action ("what is
 * she doing this second").
 *
 * Reuses the shared #288 presence-token helpers (`formatPresenceStatus` /
 * `presenceStatusVisual` / `presenceStatusDotClass`) so the coarse word always
 * agrees with the avatar presence dot, and the `AgentLiveStatusMark` visual
 * (coloured dot + text-xs word + width-stable skeleton) so this chip matches
 * every other presence chip.
 */
export function AgentCoarsePresenceLine({
  agentId,
  className,
}: {
  agentId: string;
  className?: string;
}) {
  const wsId = useWorkspaceId();
  const { t } = useT("agents");
  const presence = useAgentPresenceDetail(wsId, agentId);
  const status = useMemo<AgentLiveStatusView | null>(() => {
    const label = formatPresenceStatus(presence, t);
    const visual = presenceStatusVisual(presence);
    const dotClass = presenceStatusDotClass(presence);
    if (!label || !visual || !dotClass) return null;
    return { label, textClass: visual.textClass, dotClass };
  }, [presence, t]);

  return (
    <AgentLiveStatusMark status={status} className={className} showSkeleton />
  );
}
