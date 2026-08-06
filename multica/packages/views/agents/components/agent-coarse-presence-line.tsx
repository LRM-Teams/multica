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
 * Coarse Online/Offline line for the DM header (LRM-248).
 *
 * Avatar badge carries the round indicator; this chip is plain text only
 * (no second dot). Never Working / Queued / Idle / Unstable / activity verbs.
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
    <AgentLiveStatusMark
      status={status}
      className={className}
      showSkeleton
      showDot={false}
    />
  );
}
