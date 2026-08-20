"use client";

import { useMemo } from "react";
import { useRunnerActivitySummaries } from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  groupComposerAgentActivityRows,
  selectComposerAgentActivityRows,
  type ComposerActivityAgent,
  type ComposerActivityLine,
} from "./composer-agent-activity-rows";

export type { ComposerActivityAgent };

/**
 * Compact Agent Activity above the conversation composer.
 *
 * DM: pass `agentId` for the single peer.
 * Group: pass channel-member agents; live verbs sort first, idle stays hidden.
 *
 * Agents on the same verb share one line and the rest collapse into a "+N"
 * tail, so a busy channel costs three lines instead of one line per agent.
 *
 * Uses the workspace-batched Runner summary (one query + WS patch). Never
 * Online/Working/Idle words (presence is the avatar), never command bodies,
 * never per-agent timeline REST.
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
  const { t } = useT("channels");
  const wsId = useWorkspaceId();
  const resolvedAgents = useMemo<readonly ComposerActivityAgent[]>(() => {
    if (agents) return agents;
    if (agentId) return [{ agentId, name: "" }];
    return [];
  }, [agentId, agents]);
  const { data } = useRunnerActivitySummaries(
    resolvedAgents.length > 0 ? wsId : undefined,
  );
  const { lines, hiddenAgentCount } = useMemo(
    () =>
      groupComposerAgentActivityRows(
        selectComposerAgentActivityRows(resolvedAgents, data?.items),
      ),
    [data?.items, resolvedAgents],
  );
  if (lines.length === 0) return null;

  return (
    <div
      className={cn(
        "flex min-h-6 flex-col gap-1 px-5 pb-2 text-xs font-medium text-muted-foreground",
        className,
      )}
      aria-live="polite"
      data-testid="composer-agent-activity-strip"
    >
      {lines.map((line) => (
        <span
          key={line.key}
          className="flex min-w-0 items-center gap-1.5"
          data-testid="composer-agent-activity-row"
        >
          <span
            aria-hidden
            className={cn("size-1.5 shrink-0 rounded-full", line.dotClass)}
          />
          <span className="min-w-0 truncate">{lineText(line)}</span>
        </span>
      ))}
      {hiddenAgentCount > 0 && (
        <span
          className="min-w-0 truncate pl-3 text-muted-foreground/70"
          data-testid="composer-agent-activity-more"
        >
          {t(($) => $.conversation_activity.more_agents, {
            count: hiddenAgentCount,
          })}
        </span>
      )}
    </div>
  );
}

/** "leo, owen +2 Running command..." — bare verb when the line has no names. */
function lineText(line: ComposerActivityLine): string {
  if (line.names.length === 0) return line.label;
  const names = line.names.join(", ");
  const more = line.hiddenNameCount > 0 ? ` +${line.hiddenNameCount}` : "";
  return `${names}${more} ${line.label}`;
}
