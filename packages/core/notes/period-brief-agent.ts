/**
 * Period Brief synthesizer identity is the Workspace Notes Assistant.
 * The dedicated 「周报」 / weekly-report Agent is retired.
 */

import type { Agent } from "../types";
import {
  NOTES_ASSISTANT_AGENT_DISPLAY_NAME,
  NOTES_ASSISTANT_AGENT_NAME,
  isNotesAssistantAgent,
  resolveNotesAssistantAgent,
} from "./notes-assistant-agent";

/** Retired permanent name — used only to find leftover 周报 rows to archive. */
export const RETIRED_PERIOD_BRIEF_AGENT_NAME = "weekly-report";

export const PERIOD_BRIEF_AGENT_NAME = NOTES_ASSISTANT_AGENT_NAME;
export const PERIOD_BRIEF_AGENT_DISPLAY_NAME = NOTES_ASSISTANT_AGENT_DISPLAY_NAME;

/** True when this Agent is the 写汇报 synthesizer (笔记助手). */
export function isPeriodBriefAgent(agent: Pick<Agent, "name"> | null | undefined): boolean {
  return isNotesAssistantAgent(agent);
}

/** Prefer the provisioned 笔记助手; otherwise null. */
export function resolvePeriodBriefAgent<T extends Pick<Agent, "id" | "name">>(
  agents: readonly T[],
): T | null {
  return resolveNotesAssistantAgent(agents);
}

/** Synthesizer is always 笔记助手; null until that agent is provisioned. */
export function resolvePeriodBriefSynthesizerId(
  agents: readonly Pick<Agent, "id" | "name">[],
): string | null {
  return resolvePeriodBriefAgent(agents)?.id ?? null;
}
