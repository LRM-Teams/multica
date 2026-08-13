"use client";

import { useMemo } from "react";
import { useRunnerActivitySummaries } from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import { cn } from "@multica/ui/lib/utils";
import {
  selectComposerAgentActivityRows,
  type ComposerActivityAgent,
} from "./composer-agent-activity-rows";

export type { ComposerActivityAgent };

/**
 * Compact Agent Activity above the conversation composer.
 *
 * DM: pass `agentId` for the single peer.
 * Group: pass channel-member agents; live verbs sort first, idle stays hidden.
 *
 * Uses the workspace-batched Runner summary (one query + WS patch). Never
 * Working/Idle words, never command bodies, never per-agent timeline REST.
 */
export function ComposerAgentActivityStrip({
  agentId,
  agents,
  className,
}: {
  agentId?: string;
  agents?: readonly ComposerActivityAgent[];
  className?: string;
}) {
  const wsId = useWorkspaceId();
  const resolvedAgents = useMemo<readonly ComposerActivityAgent[]>(() => {
    if (agents) return agents;
    if (agentId) return [{ agentId, name: "" }];
    return [];
  }, [agentId, agents]);
  const { data } = useRunnerActivitySummaries(
    resolvedAgents.length > 0 ? wsId : undefined,
  );
  const rows = useMemo(
    () => selectComposerAgentActivityRows(resolvedAgents, data?.items),
    [data?.items, resolvedAgents],
  );
  if (rows.length === 0) return null;

  const showNames = resolvedAgents.length > 1;

  return (
    <div
      className={cn(
        "flex min-h-6 flex-col gap-1 px-5 pb-2 text-xs font-medium text-muted-foreground",
        className,
      )}
      aria-live="polite"
      data-testid="composer-agent-activity-strip"
    >
      {rows.map((row) => (
        <span
          key={row.agentId}
          className="flex min-w-0 items-center gap-1.5"
          data-testid="composer-agent-activity-row"
        >
          <span
            aria-hidden
            className={cn("size-1.5 shrink-0 rounded-full", row.dotClass)}
          />
          <span className="min-w-0 truncate">
            {showNames && row.name ? `${row.name} ${row.label}` : row.label}
          </span>
        </span>
      ))}
    </div>
  );
}
