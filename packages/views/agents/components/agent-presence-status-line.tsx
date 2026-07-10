"use client";

import { useWorkspaceId } from "@multica/core/hooks";
import { useAgentLiveStatus } from "../use-agent-live-status";
import { AgentLiveStatusMark } from "./agent-live-status-mark";

/**
 * Live name-row status for an agent, wired for header call sites
 * (DM header, side panel, live peek).
 *
 * Data: `useAgentLiveStatus` — stage word when a task is active
 * (Thinking / Running a command / Queued…), coarse presence when idle
 * (Idle / Offline / …). Same source as the profile hover card.
 *
 * Visual: `AgentLiveStatusMark` — coloured dot + word (never a Lucide
 * icon), so headers cannot drift from the hover card.
 *
 * Shows a skeleton while status is still resolving so header height stays
 * stable.
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
    <AgentLiveStatusMark status={status} className={className} showSkeleton />
  );
}
