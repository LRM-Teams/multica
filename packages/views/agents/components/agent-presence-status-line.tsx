"use client";

import { useWorkspaceId } from "@multica/core/hooks";
import { useAgentLiveStatus } from "../use-agent-live-status";
import { AgentLiveStatusMark } from "./agent-live-status-mark";

/**
 * Live Online/Offline name-row status for header call sites
 * (DM header, side panel, live peek, profile card).
 *
 * LRM-248: plain text word only — the avatar badge is the round indicator;
 * no second dot next to the word.
 */
export function AgentPresenceStatusLine({
  agentId,
  className,
}: {
  agentId: string;
  className?: string;
}) {
  const wsId = useWorkspaceId();
  const status = useAgentLiveStatus(wsId, agentId);
  return (
    <AgentLiveStatusMark
      status={status}
      className={className}
      showSkeleton
      showDot={false}
    />
  );
}
