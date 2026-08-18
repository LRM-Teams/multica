/**
 * Period Work collector selection helpers (ADR 0019).
 *
 * Collectors are dedicated Agents provisioned per local Computer (daemon_id),
 * not arbitrary specialty Agents.
 */

import type { Agent, AgentRuntime } from "../types";
import { isPeriodBriefAgent } from "./period-brief-agent";

export const PERIOD_BRIEF_COLLECTOR_NAME_PREFIX = "period-collect-";
export const PERIOD_BRIEF_COLLECTOR_DISPLAY_LEAD = "采集 · ";

export type PeriodBriefCollectorCandidate = Pick<
  Agent,
  "id" | "name" | "display_name" | "runtime_id" | "runtime_mode" | "runtime_status"
>;

/** True when this Agent is a provisioned Period Work collector. */
export function isPeriodBriefCollectorAgent(
  agent: Pick<Agent, "name"> | null | undefined,
): boolean {
  return Boolean(agent?.name?.startsWith(PERIOD_BRIEF_COLLECTOR_NAME_PREFIX));
}

/** Collectors only — synthesizer and specialty Agents are excluded. */
export function listPeriodBriefCollectorAgents<T extends Pick<Agent, "name">>(
  agents: readonly T[],
): T[] {
  return agents.filter((agent) => isPeriodBriefCollectorAgent(agent) && !isPeriodBriefAgent(agent));
}

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
 * Default collector set: online dedicated Period Work collectors.
 */
export function defaultPeriodBriefCollectorIds(
  agents: readonly PeriodBriefCollectorCandidate[],
  runtimes: readonly Pick<AgentRuntime, "id" | "status">[] = [],
): string[] {
  return listPeriodBriefCollectorAgents(agents)
    .filter((agent) => isPeriodBriefCollectorOnline(agent, runtimes))
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

/** Label shown in the collector picker (display name already includes 采集 ·). */
export function periodBriefCollectorLabel(
  agent: Pick<Agent, "display_name" | "name">,
): string {
  const display = agent.display_name?.trim();
  if (display) return display;
  return agent.name;
}
