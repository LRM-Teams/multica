"use client";

import { useWorkspaceId } from "@multica/core/hooks";
import { cn } from "@multica/ui/lib/utils";
import { useAgentActivityProjection } from "../../agents/use-agent-live-status";
import { isCompactActivityLabel } from "./is-compact-activity-label";

/**
 * LRM-650 / LRM-647 — DM header Compact under the peer name.
 * EN Activity projection only (dot + type); no Online/Working words, no
 * command/path details, no composer-strip duplicate.
 */
export function DmAgentWorkingCue({
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
        "inline-flex min-h-0 max-w-[14rem] items-center gap-1.5 text-xs font-medium text-muted-foreground",
        className,
      )}
      data-testid="dm-agent-working-cue"
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
