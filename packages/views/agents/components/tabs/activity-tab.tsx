"use client";

import type { Agent } from "@multica/core/types";
import { ActivityTimeline } from "./activity-timeline";
import { useAgentActivityEvents } from "./use-agent-activity-events";

interface ActivityTabProps {
  agent: Agent;
}

/**
 * Agent Activity tab (#351) — a single, raft-aligned, time-ordered event
 * stream: `time · status dot · human label · optional detail`, newest work
 * flowing down the column. It replaces the old Now / Last-30-days / Recent-work
 * aggregate cards, which contradicted each other ("29 runs" above "nothing
 * finished yet") and mixed "is it reliable" with "what did it deliver".
 *
 * The render is the shared #267 `ActivityTimeline` (which also powers the
 * profile/hover card in `compact` mode — one surface, one source). All rows
 * come from the #302 raw `ActivityEvent` facts; FE projects display label/tone
 * from stable kind/reason fields and never renders raw command output. The
 * default user surface does not expose diagnostics controls.
 */
export function ActivityTab({ agent }: ActivityTabProps) {
  const { events } = useAgentActivityEvents(agent.id);
  return (
    <div className="p-6">
      <ActivityTimeline events={events} />
    </div>
  );
}
