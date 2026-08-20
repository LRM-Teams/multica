/**
 * Notes Assistant (「笔记助手」) — dedicated agent for the Notes page FAB bubble.
 * Permanent name is stable so Workspace Ensure / resolve can find it.
 */

import type { Agent } from "../types";

export const NOTES_ASSISTANT_AGENT_NAME = "notes-assistant";
export const NOTES_ASSISTANT_AGENT_DISPLAY_NAME = "笔记助手";
export const NOTES_ASSISTANT_AGENT_TEMPLATE_SLUG = "notes-assistant";

/** True when this Agent is the Workspace Notes Assistant. */
export function isNotesAssistantAgent(agent: Pick<Agent, "name"> | null | undefined): boolean {
  return Boolean(agent?.name === NOTES_ASSISTANT_AGENT_NAME);
}

/** Prefer the provisioned 笔记助手 Agent; otherwise null. */
export function resolveNotesAssistantAgent<T extends Pick<Agent, "id" | "name">>(
  agents: readonly T[],
): T | null {
  return agents.find((agent) => isNotesAssistantAgent(agent)) ?? null;
}

export function notesAssistantSetupDismissKey(workspaceId: string): string {
  return `multica:notes-assistant-setup-dismissed:${workspaceId}`;
}
