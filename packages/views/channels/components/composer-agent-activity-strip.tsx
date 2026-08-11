"use client";

import { useWorkspaceId } from "@multica/core/hooks";
import { cn } from "@multica/ui/lib/utils";
import { useAgentActivityProjection } from "../../agents/use-agent-live-status";
import { isCompactActivityLabel } from "./is-compact-activity-label";

/**
 * Compact Agent Activity above the DM composer (single peer only).
 *
 * Reuses the server-arbitrated Workspace Runner summary projection
 * (`useAgentActivityProjection` → one workspace-batched query + WS patch).
 * Idle / no observation → null. Never Working/Idle words, never command
 * bodies, never per-agent timeline REST.
 */
export function ComposerAgentActivityStrip({
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
    <div
      className={cn(
        "flex min-h-6 items-center gap-1.5 px-5 pb-2 text-xs font-medium text-muted-foreground",
        className,
      )}
      aria-live="polite"
      data-testid="composer-agent-activity-strip"
    >
      <span
        aria-hidden
        className={cn("size-1.5 shrink-0 rounded-full", projection.dotClass)}
      />
      <span className="min-w-0 truncate">{projection.label}</span>
    </div>
  );
}
