"use client";

import { useWorkspaceId } from "@multica/core/hooks";
import { cn } from "@multica/ui/lib/utils";
import { useAgentActivityProjection } from "../../agents/use-agent-live-status";

/**
 * LRM-650 / LRM-647 — Activity Compact (EN state type only).
 * Dot + `ACTIVITY_LABEL_EN` / projection label; never command/params/steps/logs.
 * Idle / offline / no active task → null (presence is avatar status dot only).
 */
export function AgentActivityCompact({
  agentId,
  className,
}: {
  agentId: string;
  className?: string;
}) {
  const wsId = useWorkspaceId();
  const projection = useAgentActivityProjection(wsId, agentId);
  if (!projection) return null;

  return (
    <div
      className={cn(
        "flex min-w-0 items-center gap-1.5 text-xs font-medium text-muted-foreground",
        className,
      )}
      data-testid="agent-activity-compact"
      aria-live="polite"
    >
      <span
        aria-hidden
        className={cn("size-1.5 shrink-0 rounded-full", projection.dotClass)}
      />
      <span className="min-w-0 truncate">{projection.label}</span>
    </div>
  );
}
