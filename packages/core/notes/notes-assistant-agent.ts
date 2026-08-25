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

/**
 * Empty-line send: proceed with the Editor job only when 笔记助手 exists.
 * Otherwise open the bubble sidebar so the human can configure it — same
 * first-open setup card as clicking the FAB. The in-note prompt still opens
 * on Space; this gate runs only when they send.
 */
export function requestInlineNotePageAI(input: {
  agents: readonly Pick<Agent, "id" | "name">[];
  openNotesBubble: () => void;
}): boolean {
  if (resolveNotesAssistantAgent(input.agents)) return true;
  input.openNotesBubble();
  return false;
}
