"use client";

import { useWorkspaceId } from "@multica/core/hooks";
import { cn } from "@multica/ui/lib/utils";
import { ACTIVITY_LABEL_EN } from "../../agents/components/tabs/activity-event";
import { useAgentActivityProjection } from "../../agents/use-agent-live-status";

/**
 * LRM-650 / LRM-647 — Compact Activity under an agent name.
 * EN state type only (Thinking / Running command…); never "Working", never
 * command/path/log detail. Idle → null (presence stays on the avatar dot).
 */
export function isCompactActivityLabel(label: string | null | undefined): boolean {
  if (!label) return false;
  const base = label.replace(/…$/u, "").trim();
  return base !== ACTIVITY_LABEL_EN.working && base !== ACTIVITY_LABEL_EN.idle;
}

export function AgentCompactActivity({
  agentId,
  className,
}: {
  agentId: string;
  className?: string;
}) {
  const wsId = useWorkspaceId();
  const projection = useAgentActivityProjection(wsId, agentId);
  if (!projection || !isCompactActivityLabel(projection.label)) return null;

  return (
    <span
      className={cn(
        "mt-0.5 flex min-w-0 items-center gap-1.5 text-xs font-medium text-muted-foreground",
        className,
      )}
      data-testid="agent-compact-activity"
      aria-live="polite"
    >
      <span
        aria-hidden
        className={cn("size-1.5 shrink-0 rounded-full", projection.dotClass)}
      />
      <span className="min-w-0 truncate">{projection.label}</span>
    </span>
  );
}
