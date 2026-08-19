/**
 * Period Work collector selection helpers (ADR 0019).
 *
 * Collectors are dedicated Agents provisioned per local Computer (daemon_id),
 * not arbitrary specialty Agents. Collection is Computer-owner-only: a member
 * may only select collectors bound to Computers they own — never another
 * member's machine, even when that runtime is workspace-visible / public.
 */

import type { Agent, AgentRuntime } from "../types";
import { isPeriodBriefAgent } from "./period-brief-agent";

export const PERIOD_BRIEF_COLLECTOR_NAME_PREFIX = "period-collect-";
export const PERIOD_BRIEF_COLLECTOR_DISPLAY_LEAD = "采集 · ";

export type PeriodBriefCollectorCandidate = Pick<
  Agent,
  "id" | "name" | "display_name" | "runtime_id" | "runtime_mode" | "runtime_status" | "owner_id"
>;

export type PeriodBriefCollectorRuntime = Pick<AgentRuntime, "id" | "status" | "owner_id">;

/** True when this Agent is a provisioned Period Work collector. */
export function isPeriodBriefCollectorAgent(
  agent: Pick<Agent, "name"> | null | undefined,
): boolean {
  return Boolean(agent?.name?.startsWith(PERIOD_BRIEF_COLLECTOR_NAME_PREFIX));
}

/**
 * True when the collector's bound Computer is owned by `userId`.
 * Prefer runtime.owner_id; fall back to agent.owner_id when the runtime row is
 * missing from the local cache (still fail closed if neither matches).
 */
export function isPeriodBriefCollectorOwnedByUser(
  agent: PeriodBriefCollectorCandidate,
  runtimes: readonly PeriodBriefCollectorRuntime[],
  userId: string | null | undefined,
): boolean {
  const uid = userId?.trim();
  if (!uid) return false;
  const runtime = runtimes.find((item) => item.id === agent.runtime_id);
  if (runtime?.owner_id) {
    return runtime.owner_id === uid;
  }
  return agent.owner_id === uid;
}

/** Collectors only — synthesizer and specialty Agents are excluded. */
export function listPeriodBriefCollectorAgents<T extends Pick<Agent, "name">>(
  agents: readonly T[],
): T[] {
  return agents.filter((agent) => isPeriodBriefCollectorAgent(agent) && !isPeriodBriefAgent(agent));
}

/** Collectors the caller may dispatch — own Computers only. */
export function listOwnedPeriodBriefCollectorAgents(
  agents: readonly PeriodBriefCollectorCandidate[],
  runtimes: readonly PeriodBriefCollectorRuntime[],
  userId: string | null | undefined,
): PeriodBriefCollectorCandidate[] {
  return listPeriodBriefCollectorAgents(agents).filter((agent) =>
    isPeriodBriefCollectorOwnedByUser(agent, runtimes, userId),
  );
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
 * Default collector set: online dedicated Period Work collectors on Computers
 * owned by the current user.
 */
export function defaultPeriodBriefCollectorIds(
  agents: readonly PeriodBriefCollectorCandidate[],
  runtimes: readonly PeriodBriefCollectorRuntime[] = [],
  userId?: string | null,
): string[] {
  const owned =
    userId === undefined
      ? listPeriodBriefCollectorAgents(agents)
      : listOwnedPeriodBriefCollectorAgents(agents, runtimes, userId);
  return owned
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
