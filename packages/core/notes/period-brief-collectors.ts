/**
 * Period Work collector selection helpers (ADR 0019 / K0-T2).
 *
 * Collectors are Agents on human-selected runtimes (local or cloud).
 */

import type { Agent, AgentRuntime } from "../types";
import { isPeriodBriefAgent } from "./period-brief-agent";

export type PeriodBriefCollectorCandidate = Pick<
  Agent,
  "id" | "name" | "runtime_id" | "runtime_mode" | "runtime_status"
>;

/** True when the Agent's bound runtime is currently online (local or cloud). */
export function isPeriodBriefCollectorOnline(
  agent: PeriodBriefCollectorCandidate,
  runtimes: readonly Pick<AgentRuntime, "id" | "status">[] = [],
): boolean {
  if (agent.runtime_status === "online") return true;
  if (agent.runtime_status === "offline") return false;
  const runtime = runtimes.find((item) => item.id === agent.runtime_id);
  return runtime?.status === "online";
}

/**
 * Default collector set: online Agents that are not the Period Brief synthesizer.
 * Cloud and local are both eligible.
 */
export function defaultPeriodBriefCollectorIds(
  agents: readonly PeriodBriefCollectorCandidate[],
  runtimes: readonly Pick<AgentRuntime, "id" | "status">[] = [],
): string[] {
  return agents
    .filter((agent) => !isPeriodBriefAgent(agent) && isPeriodBriefCollectorOnline(agent, runtimes))
    .map((agent) => agent.id);
}

export function togglePeriodBriefCollectorId(
  selected: readonly string[],
  agentId: string,
): string[] {
  if (selected.includes(agentId)) {
    return selected.filter((id) => id !== agentId);
  }
  return [...selected, agentId];
}
