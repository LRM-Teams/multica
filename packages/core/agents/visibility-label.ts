import type { AgentVisibility } from "../types";

/**
 * Display labels for agent visibility. The DB stores `private` as the value
 * but the UI surface name is "Personal" — better matches what the field
 * actually means now that workspace admins can also assign private agents.
 *
 * `channel` (LRM-240) is locked to the Chinese product label「仅本群」in the
 * create/edit English-label row (Personal / 仅本群 / Workspace), matching
 * design-agent-channel-visibility-ab.html 方案 A.
 */
export const VISIBILITY_LABEL: Record<AgentVisibility, string> = {
  private: "Personal",
  channel: "仅本群",
  workspace: "Workspace",
};

/**
 * Descriptions for discover / invite / @mention (LRM-240 surfaces).
 * Not assignee-only copy — those three surfaces are what `visibility` gates.
 */
export const VISIBILITY_DESCRIPTION: Record<AgentVisibility, string> = {
  private: "Only you and admins can discover, invite, or @mention",
  channel: "Only in the bound group: discover, invite, and @mention",
  workspace: "All workspace members can discover, invite, or @mention",
};

/** Tooltip suitable for read-only badges on hover/list rows. */
export const VISIBILITY_TOOLTIP: Record<AgentVisibility, string> = {
  private: "Personal — only you and workspace admins can discover / invite / @",
  channel: "仅本群 — only in the bound group for discover / invite / @",
  workspace: "Workspace — all members can discover / invite / @",
};

/** Stable order for create/edit radio lists (方案 A). */
export const VISIBILITY_OPTIONS: readonly AgentVisibility[] = [
  "private",
  "channel",
  "workspace",
] as const;

export function visibilityLabel(v: AgentVisibility): string {
  return VISIBILITY_LABEL[v];
}
