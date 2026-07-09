import { useMemo } from "react";
import type { ActivityEvent } from "./activity-event";

/**
 * Read-model source for an agent's Activity event stream (#351, Option A).
 *
 * Per the #267 `ActivityEvent` contract, the BE (#302 / `agent_activity_event`)
 * supplies each event already tagged with `label` / `subtext` / `tone` /
 * `visibility` — the FE never derives these from raw tool/command/output text
 * (the P1-8 heuristic trap). So this hook's only job is to read that BE stream
 * for one agent and hand `ActivityEvent[]` to `ActivityTimeline`.
 *
 * The BE query (`api.listAgentActivityEvents(agentId)` or equivalent — signature
 * pending #302, Barry/Ronan) is not wired yet, so this returns an empty stream
 * for now: the tab renders `ActivityTimeline`'s empty state and lights up the
 * moment #302 lands. Swap the body for the real query when the endpoint exists;
 * `ActivityTimeline` + the whole tab wiring stay unchanged.
 */
export function useAgentActivityEvents(_agentId: string): {
  events: ActivityEvent[];
  isLoading: boolean;
} {
  // TODO(#302): replace with `useQuery(agentActivityEventsOptions(agentId))`
  // once the BE emits run- + agent-level events as tagged ActivityEvents.
  const events = useMemo<ActivityEvent[]>(() => [], []);
  return { events, isLoading: false };
}
