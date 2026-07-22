"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { agentTaskSnapshotOptions } from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import { cn } from "@multica/ui/lib/utils";
import { useAgentActivityHeader } from "../use-agent-activity-header";
import { pickPrimaryActiveTask } from "../resolve-agent-live-status";
import { AgentLiveStatusMark } from "./agent-live-status-mark";

/**
 * Quiet one-line "what is this conversation's agent doing right now" strip,
 * rendered directly above the composer.
 *
 * Projects the latest Activity work row (Thinking / Running command…) — not
 * live Online/Offline presence (LRM-248). Hide when idle.
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
  const status = useAgentActivityHeader(wsId, agentId);

  if (!activeTask || !status) return null;
  if (status.label === "Output") return null;

  return (
    <div
      className={cn(
        "flex min-h-6 items-center px-5 pb-2 text-xs text-muted-foreground",
        className,
      )}
      aria-live="polite"
      data-testid="conversation-agent-activity-line"
    >
      <AgentLiveStatusMark status={status} showDot />
    </div>
  );
}
