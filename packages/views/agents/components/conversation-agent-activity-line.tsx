"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { agentTaskSnapshotOptions } from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import { cn } from "@multica/ui/lib/utils";
import { useAgentLiveStatus } from "../use-agent-live-status";
import { pickPrimaryActiveTask } from "../resolve-agent-live-status";
import { AgentLiveStatusMark } from "./agent-live-status-mark";

/**
 * Quiet one-line "what is this conversation's agent doing right now" strip,
 * rendered directly above the composer.
 *
 * Reuses the Activity latest-row projection verbatim (`useAgentLiveStatus` →
 * `AgentLiveStatusMark`) — the SAME word + type-on-dot the Activity tab shows,
 * no new format. Kind is distinguished by icon/text, never colour (Frank: 靠
 * 图标/字分类型、不靠色; post-reduction every dot is neutral gray except
 * failure). Styling mirrors `ConversationActivityStrip`
 * (`text-xs text-muted-foreground`, `px-5 pb-2`) so it reads as one quiet line.
 *
 * HONEST / hide-when-idle: renders nothing unless the agent actually has an
 * active task on the plate — gated on `pickPrimaryActiveTask`, the SAME
 * active-vs-idle boundary `useAgentLiveStatus` uses internally. No fabricated
 * "Idle" row and no fake activity (Parker: 没活动不显).
 *
 * The caller resolves `agentId` to the single conversation agent (a DM whose
 * peer is an agent). Where a single conversation agent is undefined (a
 * multi-agent channel), the caller renders nothing at all.
 */
export function ConversationAgentActivityLine({
  agentId,
  className,
}: {
  agentId: string;
  className?: string;
}) {
  const wsId = useWorkspaceId();
  const { data: snapshot = [] } = useQuery({
    ...agentTaskSnapshotOptions(wsId ?? ""),
    enabled: !!wsId && !!agentId,
  });
  const activeTask = useMemo(
    () => pickPrimaryActiveTask(snapshot, agentId),
    [snapshot, agentId],
  );
  const status = useAgentLiveStatus(wsId, agentId);

  // Only paint while the agent is actively working. No active task (idle) — or
  // status still resolving — hides the whole line; never a fabricated idle word
  // or fake activity beneath the composer.
  if (!activeTask || !status) return null;

  return (
    <div
      className={cn(
        "flex min-h-6 items-center px-5 pb-2 text-xs text-muted-foreground",
        className,
      )}
      aria-live="polite"
      data-testid="conversation-agent-activity-line"
    >
      <AgentLiveStatusMark status={status} />
    </div>
  );
}
