/**
 * Period Brief Agent (「周报」) — default synthesizer for Period Work Briefs.
 * Permanent name is stable so Workspace resolve / Ensure can find it.
 */

import type { Agent } from "../types";

export const PERIOD_BRIEF_AGENT_NAME = "weekly-report";
export const PERIOD_BRIEF_AGENT_DISPLAY_NAME = "周报";
export const PERIOD_BRIEF_AGENT_TEMPLATE_SLUG = "weekly-report";

/** True when this Agent is the Workspace Period Brief Agent. */
export function isPeriodBriefAgent(agent: Pick<Agent, "name"> | null | undefined): boolean {
  return Boolean(agent?.name === PERIOD_BRIEF_AGENT_NAME);
}

/** Prefer the provisioned 周报 Agent; otherwise null (caller falls back). */
export function resolvePeriodBriefAgent<T extends Pick<Agent, "id" | "name">>(
  agents: readonly T[],
): T | null {
  return agents.find((agent) => isPeriodBriefAgent(agent)) ?? null;
}

/** Default synthesizer id: 周报 → preferred → first agent. */
export function resolvePeriodBriefSynthesizerId(
  agents: readonly Pick<Agent, "id" | "name">[],
  preferredAgentId?: string | null,
): string | null {
  const periodBrief = resolvePeriodBriefAgent(agents);
  if (periodBrief) return periodBrief.id;
  if (preferredAgentId && agents.some((agent) => agent.id === preferredAgentId)) {
    return preferredAgentId;
  }
  return agents[0]?.id ?? null;
}
